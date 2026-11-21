package sim

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
)

// Side indices.
const (
	SideNone = -1
)

// ---------------------------------------------------------------------------
// Actions (client intentions). The server validates everything; the client
// never reports money, damage, positions or victory.
// ---------------------------------------------------------------------------

type ActionKind int

const (
	ActionDeploy ActionKind = iota
	ActionBuildTurret
	ActionBuildFactory
	ActionUpgradeUnit
	ActionUpgradeTurret
)

// Action is a player intention. Lane is used for deploy/turret actions, Unit
// for deploy/upgrade-unit actions.
type Action struct {
	Kind ActionKind
	Side int // SideA or SideB, filled in by the server from the connection
	Lane int
	Unit UnitType
}

var (
	ErrMatchOver      = errors.New("match is over")
	ErrInvalidLane    = errors.New("invalid lane")
	ErrNotEnoughCoins = errors.New("not enough coins")
	ErrUnknownUnit    = errors.New("unknown unit type")
	ErrPadOccupied    = errors.New("lane already has a turret")
	ErrNoTurret       = errors.New("no turret in that lane")
	ErrOvertime       = errors.New("locked during overtime")
	ErrMaxLevel       = errors.New("already at maximum level")
	ErrFactoryBuilt   = errors.New("factory already built")
)

// ---------------------------------------------------------------------------
// Entities
// ---------------------------------------------------------------------------

// Unit is a single fighting entity. All stats are fixed at spawn time;
// upgrades only affect future spawns.
type Unit struct {
	ID       int64    `json:"id"`
	Type     UnitType `json:"type"`
	Side     int      `json:"side"`
	Lane     int      `json:"lane"`
	X        float64  `json:"x"`
	HP       float64  `json:"hp"`
	MaxHP    float64  `json:"maxHp"`
	Damage   float64  `json:"damage"`
	Cooldown float64  `json:"-"` // seconds until next attack
}

// Turret is a lane turret standing on a pad. It does not block movement.
type Turret struct {
	Side     int     `json:"side"`
	Lane     int     `json:"lane"`
	X        float64 `json:"x"`
	Level    int     `json:"level"`
	HP       float64 `json:"hp"`
	MaxHP    float64 `json:"maxHp"`
	Damage   float64 `json:"damage"` // includes upgrades, excludes overtime factor
	Cooldown float64 `json:"-"`
	TargetID int64   `json:"-"`
}

// Player holds per-player economy and upgrade state.
type Player struct {
	Coins        int              `json:"coins"`
	Factory      bool             `json:"factory"`
	UnitUpgrades map[UnitType]int `json:"unitUpgrades"`
}

// IncomePerInterval is the player's current income per economy tick.
func (p *Player) IncomePerInterval(cfg Balance) int {
	inc := cfg.Economy.BaseIncome
	if p.Factory {
		inc += cfg.Economy.FactoryIncome
	}
	return inc
}

// ---------------------------------------------------------------------------
// Events (drained by the server and forwarded to clients)
// ---------------------------------------------------------------------------

type Event struct {
	Tick int64  `json:"tick"`
	Side int    `json:"side"` // SideA, SideB, or SideNone for global news
	Kind string `json:"kind"` // "windfall", "overtime", "turretDestroyed", ...
	Text string `json:"text"`
}

// ---------------------------------------------------------------------------
// The simulation
// ---------------------------------------------------------------------------

type Sim struct {
	cfg Balance
	rng *rand.Rand

	Tick int64   `json:"tick"`
	Time float64 `json:"time"`

	units   []*Unit
	turrets [2][NumLanes]*Turret
	players [2]Player

	incomeIn   float64
	windfallIn float64

	Overtime        bool    `json:"overtime"`
	TurretFactor    float64 `json:"turretFactor"` // 1.0 -> 0.0 during overtime
	overtimeDecayIn float64

	Finished bool `json:"finished"`
	Draw     bool `json:"draw"`
	Winner   int  `json:"winner"`  // SideA/SideB, or NoSide when undecided/draw
	WinLane  int  `json:"winLane"` // 0-indexed lane that breached, -1 otherwise

	nextUnitID int64
	events     []Event
}

// New creates a fresh match. seed makes the shared windfall sequence
// reproducible in tests.
func New(seed int64, cfg Balance) *Sim {
	s := &Sim{
		cfg:          cfg,
		rng:          rand.New(rand.NewSource(seed)),
		Winner:       NoSide,
		WinLane:      -1,
		incomeIn:     cfg.Economy.Interval,
		nextUnitID:   1,
		TurretFactor: 1.0,
	}
	for i := range s.players {
		s.players[i] = Player{
			Coins:        cfg.StartCoins,
			UnitUpgrades: map[UnitType]int{},
		}
	}
	s.windfallIn = s.rollWindfallGap()
	return s
}

// Balance returns the balance table the sim was built with.
func (s *Sim) Balance() Balance { return s.cfg }

func (s *Sim) rollWindfallGap() float64 {
	e := s.cfg.Economy
	return e.WindfallGapMin + s.rng.Float64()*(e.WindfallGapMax-e.WindfallGapMin)
}

func (s *Sim) addEvent(side int, kind, format string, args ...any) {
	s.events = append(s.events, Event{Tick: s.Tick, Side: side, Kind: kind, Text: fmt.Sprintf(format, args...)})
}

// DrainEvents returns and clears pending events. The server calls this after
// each tick batch and broadcasts them to clients.
func (s *Sim) DrainEvents() []Event {
	if len(s.events) == 0 {
		return nil
	}
	out := s.events
	s.events = nil
	return out
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// ApplyAction validates and applies a player intention. It returns a
// human-readable error when the action is illegal; coins are never deducted
// for illegal actions.
func (s *Sim) ApplyAction(a Action) error {
	if s.Finished {
		return ErrMatchOver
	}
	if a.Side != SideA && a.Side != SideB {
		return errors.New("invalid side")
	}
	p := &s.players[a.Side]
	switch a.Kind {
	case ActionDeploy:
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		ub, ok := s.cfg.Units[a.Unit]
		if !ok {
			return ErrUnknownUnit
		}
		if p.Coins < ub.Cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, ub.Cost)
		}
		level := p.UnitUpgrades[a.Unit]
		hpMul := 1 + float64(level)*ub.UpgradeHP
		dmgMul := 1 + float64(level)*ub.UpgradeDamage
		x := s.cfg.SpawnInset
		if a.Side == SideB {
			x = FieldLength - s.cfg.SpawnInset
		}
		p.Coins -= ub.Cost
		s.units = append(s.units, &Unit{
			ID:       s.nextUnitID,
			Type:     a.Unit,
			Side:     a.Side,
			Lane:     a.Lane,
			X:        x,
			HP:       ub.MaxHP * hpMul,
			MaxHP:    ub.MaxHP * hpMul,
			Damage:   ub.Damage * dmgMul,
			Cooldown: 0,
		})
		s.nextUnitID++
		return nil

	case ActionBuildTurret:
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		if s.Overtime {
			return ErrOvertime
		}
		if s.turrets[a.Side][a.Lane] != nil {
			return fmt.Errorf("%w: lane %d", ErrPadOccupied, a.Lane+1)
		}
		cost := s.cfg.Turret.Cost
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		x := s.cfg.Turret.PadX
		if a.Side == SideB {
			x = FieldLength - s.cfg.Turret.PadX
		}
		s.turrets[a.Side][a.Lane] = &Turret{
			Side:   a.Side,
			Lane:   a.Lane,
			X:      x,
			Level:  0,
			HP:     s.cfg.Turret.MaxHP,
			MaxHP:  s.cfg.Turret.MaxHP,
			Damage: s.cfg.Turret.Damage,
		}
		return nil

	case ActionBuildFactory:
		if p.Factory {
			return ErrFactoryBuilt
		}
		cost := s.cfg.Economy.FactoryCost
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		p.Factory = true
		s.addEvent(a.Side, "factory", "Side %s built a Coin Factory", sideName(a.Side))
		return nil

	case ActionUpgradeUnit:
		ub, ok := s.cfg.Units[a.Unit]
		if !ok {
			return ErrUnknownUnit
		}
		level := p.UnitUpgrades[a.Unit]
		if level >= 2 {
			return ErrMaxLevel
		}
		cost := ub.UpgradeCosts[level]
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		p.UnitUpgrades[a.Unit] = level + 1
		return nil

	case ActionUpgradeTurret:
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		if s.Overtime {
			return ErrOvertime
		}
		t := s.turrets[a.Side][a.Lane]
		if t == nil {
			return ErrNoTurret
		}
		if t.Level >= 2 {
			return ErrMaxLevel
		}
		cost := s.cfg.Turret.UpgradeCosts[t.Level]
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		t.Level++
		// Upgrade improves combat stats but never heals: current HP stays,
		// max HP grows by a slice of the *base* pool.
		t.MaxHP += s.cfg.Turret.MaxHP * s.cfg.Turret.UpgradeHPPerLevel
		t.Damage = s.cfg.Turret.Damage * (1 + float64(t.Level)*s.cfg.Turret.UpgradeDamagePerLevel)
		return nil
	}
	return errors.New("unknown action")
}

func validLane(l int) bool { return l >= 0 && l < NumLanes }

// timerEpsilon absorbs float accumulation error in countdown timers.
const timerEpsilon = 1e-9

func sideName(side int) string {
	if side == SideA {
		return "Blue"
	}
	return "Red"
}

// ---------------------------------------------------------------------------
// Step
// ---------------------------------------------------------------------------

// Step advances the simulation by dt seconds. Callers should use TickDT.
func (s *Sim) Step(dt float64) {
	if s.Finished {
		return
	}
	s.Tick++
	s.Time += dt

	// --- Economy: fixed grants every interval ---
	s.incomeIn -= dt
	for s.incomeIn <= timerEpsilon {
		for side := range s.players {
			s.players[side].Coins += s.players[side].IncomePerInterval(s.cfg)
		}
		s.incomeIn += s.cfg.Economy.Interval
	}

	// --- Economy: shared windfall, same roll for both players ---
	s.windfallIn -= dt
	if s.windfallIn <= timerEpsilon {
		e := s.cfg.Economy
		amount := e.WindfallMin + s.rng.Intn(e.WindfallMax-e.WindfallMin+1)
		for side := range s.players {
			s.players[side].Coins += amount
		}
		s.addEvent(SideNone, "windfall", "Windfall! Both players gain %d coins", amount)
		s.windfallIn = s.rollWindfallGap()
	}

	// --- Overtime ---
	if !s.Overtime && s.Time >= s.cfg.Overtime.Start {
		s.Overtime = true
		s.overtimeDecayIn = s.cfg.Overtime.Interval
		s.addEvent(SideNone, "overtime", "OVERTIME — turret building and upgrades disabled, turrets weakening")
	}
	if s.Overtime {
		s.overtimeDecayIn -= dt
		for s.overtimeDecayIn <= timerEpsilon && s.TurretFactor > 0 {
			s.TurretFactor = math.Max(0, s.TurretFactor-s.cfg.Overtime.Decay)
			s.overtimeDecayIn += s.cfg.Overtime.Interval
			s.addEvent(SideNone, "overtime", "Overtime: turret damage reduced to %d%%", int(math.Round(s.TurretFactor*100)))
		}
	}

	// --- Combat & movement ---
	s.stepUnits(dt)
	s.stepTurrets(dt)
	s.removeDead()

	// --- Win / draw detection ---
	s.checkEnd()
}

// direction returns +1 for side A (moving right) and -1 for side B.
func direction(side int) float64 {
	if side == SideA {
		return 1
	}
	return -1
}

// turretX is unused helper kept for symmetry; turret positions are stored on
// the Turret struct itself.
func absDist(a, b float64) float64 { return math.Abs(a - b) }

func (s *Sim) stepUnits(dt float64) {
	for _, u := range s.units {
		u.Cooldown -= dt
		ub := s.cfg.Units[u.Type]

		// Siege engines hold position when an enemy is inside their minimum
		// range — advancing would only make the problem worse.
		if u.Type == Siege {
			if d, ok := s.nearestEnemyDist(u); ok && d < ub.MinRange {
				continue
			}
		}

		target := s.findTarget(u)
		switch t := target.(type) {
		case *Unit:
			if u.Cooldown <= 0 {
				t.HP -= u.Damage
				u.Cooldown = s.cfg.Units[u.Type].AttackInterval
			}
		case *Turret:
			if u.Cooldown <= 0 {
				dmg := u.Damage
				if ub.VsTurretDamage > 0 {
					mul := 1 + float64(s.players[u.Side].UnitUpgrades[u.Type])*ub.UpgradeDamage
					dmg = ub.VsTurretDamage * mul
				}
				t.HP -= dmg
				u.Cooldown = s.cfg.Units[u.Type].AttackInterval
			}
		default:
			u.X += direction(u.Side) * ub.Speed * dt
		}
	}
}

// findTarget picks what a unit will attack this tick, or nil if nothing is in
// range (in which case the unit walks).
func (s *Sim) findTarget(u *Unit) any {
	ub := s.cfg.Units[u.Type]

	switch u.Type {
	case Ram:
		// Priority 1: an enemy turret in range. Priority 2: nearest enemy unit.
		if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil {
			return tr
		}
		if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil {
			return eu
		}
		return nil
	case Siege:
		// Only targets inside [MinRange, Range]; nearest first, turrets included.
		bestUnit := s.nearestEnemyUnit(u, ub.Range)
		bestTurret := s.nearestEnemyTurret(u, ub.Range)
		var best any
		var bestD float64 = math.Inf(1)
		if bestUnit != nil {
			best, bestD = bestUnit, absDist(u.X, bestUnit.X)
		}
		if bestTurret != nil {
			d := absDist(u.X, bestTurret.X)
			if d < bestD {
				best, bestD = bestTurret, d
			}
		}
		if best == nil {
			return nil
		}
		if bestD < ub.MinRange {
			return nil // too close; handled by the hold-position rule
		}
		return best
	default:
		// Soldiers, bowmen and knights ignore turrets entirely.
		if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil {
			return eu
		}
		return nil
	}
}

func (s *Sim) nearestEnemyUnit(u *Unit, maxRange float64) *Unit {
	var best *Unit
	var bestD float64 = math.Inf(1)
	for _, e := range s.units {
		if e.Side == u.Side || e.Lane != u.Lane || e.HP <= 0 {
			continue
		}
		d := absDist(u.X, e.X)
		if d <= maxRange && d < bestD {
			best, bestD = e, d
		}
	}
	return best
}

func (s *Sim) nearestEnemyTurret(u *Unit, maxRange float64) *Turret {
	enemy := 1 - u.Side
	var best *Turret
	var bestD float64 = math.Inf(1)
	for lane := 0; lane < NumLanes; lane++ {
		if lane != u.Lane {
			continue
		}
		t := s.turrets[enemy][lane]
		if t == nil {
			continue
		}
		d := absDist(u.X, t.X)
		if d <= maxRange && d < bestD {
			best, bestD = t, d
		}
	}
	return best
}

func (s *Sim) nearestEnemyDist(u *Unit) (float64, bool) {
	d := math.Inf(1)
	found := false
	for _, e := range s.units {
		if e.Side == u.Side || e.Lane != u.Lane || e.HP <= 0 {
			continue
		}
		if dd := absDist(u.X, e.X); dd < d {
			d, found = dd, true
		}
	}
	for lane := 0; lane < NumLanes; lane++ {
		if t := s.turrets[1-u.Side][lane]; lane == u.Lane && t != nil {
			if dd := absDist(u.X, t.X); dd < d {
				d, found = dd, true
			}
		}
	}
	return d, found
}

func (s *Sim) stepTurrets(dt float64) {
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			t := s.turrets[side][lane]
			if t == nil {
				continue
			}
			t.Cooldown -= dt

			// Revalidate the focused target: must be alive, in lane, in range.
			target := s.unitByID(t.TargetID)
			if target != nil && (target.Side == side || target.Lane != lane || absDist(target.X, t.X) > s.cfg.Turret.Range || target.HP <= 0) {
				target = nil
				t.TargetID = 0
			}

			if target == nil {
				target = s.pickTurretTarget(t, side, lane)
				if target != nil {
					t.TargetID = target.ID
				}
			}

			if target != nil && t.Cooldown <= 0 {
				target.HP -= t.Damage * s.TurretFactor
				t.Cooldown = s.cfg.Turret.AttackInterval
			}
		}
	}
}

// pickTurretTarget focuses the enemy unit closest to the owner's breach line
// (the most advanced attacker). Focus is kept until the target dies or
// leaves range.
func (s *Sim) pickTurretTarget(t *Turret, side, lane int) *Unit {
	var best *Unit
	for _, u := range s.units {
		if u.Side == side || u.Lane != lane || u.HP <= 0 {
			continue
		}
		if absDist(u.X, t.X) > s.cfg.Turret.Range {
			continue
		}
		if best == nil {
			best = u
			continue
		}
		better := false
		if side == SideA {
			better = u.X < best.X // A's breach line is low x for B units
		} else {
			better = u.X > best.X
		}
		if better {
			best = u
		}
	}
	return best
}

func (s *Sim) unitByID(id int64) *Unit {
	if id == 0 {
		return nil
	}
	for _, u := range s.units {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *Sim) removeDead() {
	kept := s.units[:0]
	for _, u := range s.units {
		if u.HP > 0 {
			kept = append(kept, u)
		}
	}
	for i := len(kept); i < len(s.units); i++ {
		s.units[i] = nil
	}
	s.units = kept

	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			if t := s.turrets[side][lane]; t != nil && t.HP <= 0 {
				s.turrets[side][lane] = nil
				s.addEvent(side, "turretDestroyed", "%s turret in lane %d was destroyed!", sideName(side), lane+1)
			}
		}
	}
}

func (s *Sim) checkEnd() {
	breachA := -1
	breachB := -1
	for _, u := range s.units {
		if u.HP <= 0 {
			continue
		}
		if u.Side == SideA && u.X >= FieldLength && breachA == -1 {
			breachA = u.Lane
		}
		if u.Side == SideB && u.X <= 0 && breachB == -1 {
			breachB = u.Lane
		}
	}

	switch {
	case breachA != -1 && breachB != -1:
		s.Finished = true
		s.Draw = true
		s.Winner = NoSide
		s.addEvent(SideNone, "draw", "Both armies breached on the same tick — draw!")
	case breachA != -1:
		s.Finished = true
		s.Winner = SideA
		s.WinLane = breachA
		s.addEvent(SideNone, "breach", "Blue breached in lane %d!", breachA+1)
	case breachB != -1:
		s.Finished = true
		s.Winner = SideB
		s.WinLane = breachB
		s.addEvent(SideNone, "breach", "Red breached in lane %d!", breachB+1)
	case s.Time >= s.cfg.Overtime.DrawAt:
		s.Finished = true
		s.Draw = true
		s.Winner = NoSide
		s.addEvent(SideNone, "draw", "Time limit reached — draw!")
	}
}
