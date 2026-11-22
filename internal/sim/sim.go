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
	ActionFieldEffect
)

// Coupon field-effect identifiers (balance lives in Balance.Coupon).
const (
	EffectRockslide  = "rockslide"
	EffectDragonFire = "dragonfire"
)

// Action is a player intention. Lane is used for deploy/turret/field-effect
// actions, Unit for deploy/upgrade-unit actions. For ActionBuildTurret the
// turret takes the first free slot in the lane and Slot is ignored (slots are
// interchangeable). For ActionUpgradeTurret, Slot selects which turret to
// upgrade; a negative Slot means "the lowest-slot turret in the lane".
//
// Entitled is set by the server (never by the client) and marks the player as
// holding a valid coupon; it unlocks CouponOnly units and field effects.
type Action struct {
	Kind     ActionKind
	Side     int // SideA or SideB, filled in by the server from the connection
	Lane     int
	Unit     UnitType
	Slot     int
	Effect   string // field-effect id for ActionFieldEffect
	Entitled bool   // set by the server from the coupon store; never trusted from clients
}

var (
	ErrMatchOver      = errors.New("match is over")
	ErrInvalidLane    = errors.New("invalid lane")
	ErrNotEnoughCoins = errors.New("not enough coins")
	ErrUnknownUnit    = errors.New("unknown unit type")
	ErrPadOccupied    = errors.New("no free turret slot in that lane")
	ErrNoTurret       = errors.New("no turret in that lane")
	ErrInvalidSlot    = errors.New("invalid turret slot")
	ErrOvertime       = errors.New("locked during overtime")
	ErrMaxLevel       = errors.New("already at maximum level")
	ErrFactoryBuilt   = errors.New("factory already built")
	ErrCouponRequired = errors.New("requires an active coupon")
	ErrOnCooldown     = errors.New("effect is not ready yet")
	ErrUnknownEffect  = errors.New("unknown field effect")
)

// ---------------------------------------------------------------------------
// Entities
// ---------------------------------------------------------------------------

// Unit is a single fighting entity. All stats are fixed at spawn time;
// upgrades only affect future spawns.
type Unit struct {
	ID        int64    `json:"id"`
	Type      UnitType `json:"type"`
	Side      int      `json:"side"`
	Lane      int      `json:"lane"`
	X         float64  `json:"x"`
	YOff      float64  `json:"yOff"` // cosmetic lane-offset in lane-height fractions (-0.31..0.31)
	HP        float64  `json:"hp"`
	MaxHP     float64  `json:"maxHp"`
	Damage    float64  `json:"damage"`
	Cooldown  float64  `json:"-"` // seconds until next attack
	LastHitBy int      `json:"-"` // side that dealt the last damage, -1 if none
}

// Turret is a lane turret standing on a slot of a pad. It does not block
// movement. Slot identifies which of the lane's TurretSlots slots it fills.
type Turret struct {
	Side      int     `json:"side"`
	Lane      int     `json:"lane"`
	Slot      int     `json:"slot"`
	X         float64 `json:"x"`
	Level     int     `json:"level"`
	HP        float64 `json:"hp"`
	MaxHP     float64 `json:"maxHp"`
	Damage    float64 `json:"damage"` // includes upgrades, excludes overtime factor
	Cooldown  float64 `json:"-"`
	TargetID  int64   `json:"-"`
	LastHitBy int     `json:"-"`
}

// Cache is a supply crate dropped mid-field. First unit to touch it collects it.
type Cache struct {
	ID        int64   `json:"id"`
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Amount    int     `json:"amount"`
	ExpiresAt float64 `json:"expiresAt"`
}

// Burn is an active Dragon Fire field effect: a lane left ablaze, damaging
// every unit (of either side, except the caster's own) inside it over time.
type Burn struct {
	Lane       int
	Side       int     // caster side; burns everyone except the caster's own units
	Until      float64 // sim time at which the burn expires
	TickIn     float64 // seconds until the next damage tick
	Interval   float64 // seconds between damage ticks
	TickDamage float64
}

// Player holds per-player economy and upgrade state.
type Player struct {
	Coins        int              `json:"coins"`
	Factory      bool             `json:"factory"`
	UnitUpgrades map[UnitType]int `json:"unitUpgrades"`

	// Match statistics (for the end-of-match report).
	DamageDealt float64 `json:"damageDealt"`
	Kills       int     `json:"kills"`
	UnitsLost   int     `json:"unitsLost"`
	TurretsDown int     `json:"turretsDown"`
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
	Tick   int64  `json:"tick"`
	Side   int    `json:"side"` // SideA, SideB, or SideNone for global news
	Kind   string `json:"kind"` // "windfall", "overtime", "turretDestroyed", ...
	Text   string `json:"text"`
	Amount int    `json:"amount,omitempty"` // windfall/cache amount, streak size
	Lane   int    `json:"lane,omitempty"`   // lane for supply cache events (0 = lane 1 unless omitted)
}

// FxEvent is a purely cosmetic combat effect (projectile, melee spark) for
// clients to render. It carries no gameplay information.
type FxEvent struct {
	Kind  string  `json:"kind"` // "shot" | "melee"
	Unit  string  `json:"unit"` // shooter/attacker type ("turret" for turrets)
	Side  int     `json:"side"`
	Lane  int     `json:"lane"`
	FromX float64 `json:"fromX"`
	ToX   float64 `json:"toX"`
	Slot  int     `json:"slot"` // turret slot index; -1 for non-turret FX
}

// DrainFx returns and clears pending FX events (drained by Snapshot).
func (s *Sim) DrainFx() []FxEvent {
	if len(s.fx) == 0 {
		return nil
	}
	out := s.fx
	s.fx = nil
	return out
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
	turrets [2][NumLanes][TurretSlots]*Turret
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

	nextUnitID   int64
	events       []Event
	fx           []FxEvent
	firstBlood   bool
	caches       []*Cache
	nextCacheIdx int
	streakCount  [2]int
	lastKillAt   [2]float64
	burns        []Burn
	abilityReady [2]map[string]float64
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
		s.lastKillAt[i] = -100
		s.abilityReady[i] = map[string]float64{}
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

// Resign forfeits the match for the given side. The opponent wins; no lane is
// credited. Safe to call only while the match is running.
func (s *Sim) Resign(side int) {
	if s.Finished || (side != SideA && side != SideB) {
		return
	}
	s.Finished = true
	s.Winner = 1 - side
	s.WinLane = -1
	s.addEvent(SideNone, "resign", "%s resigned", sideName(side))
}

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
		if ub.CouponOnly && !a.Entitled {
			return ErrCouponRequired
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
			ID:        s.nextUnitID,
			Type:      a.Unit,
			Side:      a.Side,
			Lane:      a.Lane,
			X:         x,
			YOff:      (s.rng.Float64() - 0.5) * 0.62,
			HP:        ub.MaxHP * hpMul,
			MaxHP:     ub.MaxHP * hpMul,
			Damage:    ub.Damage * dmgMul,
			Cooldown:  0,
			LastHitBy: -1,
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
		slot := firstFreeTurretSlot(s.turrets[a.Side][a.Lane])
		if slot < 0 {
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
		s.turrets[a.Side][a.Lane][slot] = &Turret{
			Side:      a.Side,
			Lane:      a.Lane,
			Slot:      slot,
			X:         x,
			Level:     0,
			HP:        s.cfg.Turret.MaxHP,
			MaxHP:     s.cfg.Turret.MaxHP,
			Damage:    s.cfg.Turret.Damage,
			LastHitBy: -1,
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
		if ub.CouponOnly && !a.Entitled {
			return ErrCouponRequired
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
		slots := s.turrets[a.Side][a.Lane]
		idx := a.Slot
		if idx < 0 {
			idx = firstBuiltTurretSlot(slots)
			if idx < 0 {
				return ErrNoTurret
			}
		} else if idx >= TurretSlots {
			return ErrInvalidSlot
		}
		t := slots[idx]
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

	case ActionFieldEffect:
		return s.applyFieldEffect(a, p)
	}
	return errors.New("unknown action")
}

// effectConfig resolves a field-effect id to its balance row.
func (s *Sim) effectConfig(id string) (FieldEffectBalance, bool) {
	switch id {
	case EffectRockslide:
		return s.cfg.Coupon.Rockslide, true
	case EffectDragonFire:
		return s.cfg.Coupon.DragonFire, true
	default:
		return FieldEffectBalance{}, false
	}
}

// applyFieldEffect casts a coupon-gated, lane-targeted field effect.
// Casting requires an active coupon, a valid lane, a ready cooldown and
// enough coins; coins are only spent on a successful cast.
func (s *Sim) applyFieldEffect(a Action, p *Player) error {
	if !validLane(a.Lane) {
		return ErrInvalidLane
	}
	cfg, ok := s.effectConfig(a.Effect)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownEffect, a.Effect)
	}
	if !a.Entitled {
		return ErrCouponRequired
	}
	if readyAt := s.abilityReady[a.Side][a.Effect]; s.Time < readyAt {
		return fmt.Errorf("%w: %.0fs", ErrOnCooldown, readyAt-s.Time)
	}
	if p.Coins < cfg.Cost {
		return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cfg.Cost)
	}
	p.Coins -= cfg.Cost
	s.abilityReady[a.Side][a.Effect] = s.Time + cfg.Cooldown

	switch a.Effect {
	case EffectRockslide:
		damaged := 0
		for _, u := range s.units {
			if u.Side == a.Side || u.Lane != a.Lane || u.HP <= 0 {
				continue
			}
			// Rocks fall from the sky; flying dragons dodge them.
			if s.cfg.Units[u.Type].Flying {
				continue
			}
			s.dealDamage(a.Side, u, cfg.Damage)
			damaged++
		}
		s.addEvent(a.Side, "rockslide", "%s called a Rockslide down on lane %d! (%d enemies hit)", sideName(a.Side), a.Lane+1, damaged)
		s.fx = append(s.fx, FxEvent{Kind: "rockslide", Side: a.Side, Lane: a.Lane, Slot: -1})
	case EffectDragonFire:
		duration := cfg.Duration
		if duration <= 0 {
			duration = 6
		}
		interval := cfg.TickInterval
		if interval <= 0 {
			interval = 0.5
		}
		s.burns = append(s.burns, Burn{
			Lane:       a.Lane,
			Side:       a.Side,
			Until:      s.Time + duration,
			TickIn:     interval, // first tick lands after one interval
			Interval:   interval,
			TickDamage: cfg.Damage,
		})
		s.addEvent(a.Side, "dragonfire", "%s set lane %d ablaze with Dragon Fire!", sideName(a.Side), a.Lane+1)
		s.fx = append(s.fx, FxEvent{Kind: "dragonfire", Side: a.Side, Lane: a.Lane, Slot: -1})
	}
	return nil
}

func validLane(l int) bool { return l >= 0 && l < NumLanes }

// firstFreeTurretSlot returns the index of the first free slot, or -1 if the lane is full.
func firstFreeTurretSlot(slots [TurretSlots]*Turret) int {
	for i, t := range slots {
		if t == nil {
			return i
		}
	}
	return -1
}

// firstBuiltTurretSlot returns the lowest-indexed occupied slot, or -1 if empty.
func firstBuiltTurretSlot(slots [TurretSlots]*Turret) int {
	for i, t := range slots {
		if t != nil {
			return i
		}
	}
	return -1
}

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
		s.events[len(s.events)-1].Amount = amount
		s.windfallIn = s.rollWindfallGap()
	}

	// --- Supply caches ---
	s.stepCaches()

	// --- Dragon Fire burn zones ---
	s.stepBurns(dt)

	// --- Kill streak decay ---
	for side := 0; side < 2; side++ {
		if s.streakCount[side] > 0 && s.Time-s.lastKillAt[side] > 3.0 {
			s.streakCount[side] = 0
		}
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

// stepBurns applies Dragon Fire damage over time and expires burns whose
// duration has elapsed.
func (s *Sim) stepBurns(dt float64) {
	for i := 0; i < len(s.burns); {
		b := &s.burns[i]
		if s.Time >= b.Until {
			s.burns = append(s.burns[:i], s.burns[i+1:]...)
			continue
		}
		b.TickIn -= dt
		for b.TickIn <= timerEpsilon {
			for _, u := range s.units {
				if u.Side != b.Side && u.Lane == b.Lane && u.HP > 0 {
					s.dealDamage(b.Side, u, b.TickDamage)
				}
			}
			s.fx = append(s.fx, FxEvent{Kind: "burn", Side: b.Side, Lane: b.Lane, Slot: -1})
			b.TickIn += b.Interval
		}
		i++
	}
}

// stepCaches spawns, ages and collects supply caches.
func (s *Sim) stepCaches() {
	cc := s.cfg.Cache
	for s.nextCacheIdx < len(cc.Times) && s.Time >= cc.Times[s.nextCacheIdx] {
		lane := s.rng.Intn(NumLanes)
		s.nextCacheIdx++
		s.caches = append(s.caches, &Cache{
			ID:        s.nextUnitID,
			Lane:      lane,
			X:         cc.X,
			Amount:    cc.Amount,
			ExpiresAt: s.Time + cc.Lifetime,
		})
		s.nextUnitID++
		s.addEvent(SideNone, "supply", "Supply cache dropped in lane %d! (+%d coins)", lane+1, cc.Amount)
		s.events[len(s.events)-1].Lane = lane
		s.events[len(s.events)-1].Amount = cc.Amount
	}
	for i := 0; i < len(s.caches); {
		c := s.caches[i]
		if s.Time >= c.ExpiresAt {
			s.addEvent(SideNone, "supplyGone", "The supply cache in lane %d vanished…", c.Lane+1)
			s.events[len(s.events)-1].Lane = c.Lane
			s.caches = append(s.caches[:i], s.caches[i+1:]...)
			continue
		}
		claimed := -1
		for _, u := range s.units {
			if u.Lane == c.Lane && absDist(u.X, c.X) <= cc.Radius {
				claimed = u.Side
				break
			}
		}
		if claimed >= 0 {
			s.players[claimed].Coins += c.Amount
			s.addEvent(SideNone, "supplyClaim", "%s claimed the supply cache in lane %d! (+%d coins)", sideName(claimed), c.Lane+1, c.Amount)
			s.events[len(s.events)-1].Lane = c.Lane
			s.events[len(s.events)-1].Amount = c.Amount
			s.caches = append(s.caches[:i], s.caches[i+1:]...)
			continue
		}
		i++
	}
}

// dealDamage applies damage, attributes it for stats, and remembers the
// attacker for kill credit.
func (s *Sim) dealDamage(attackerSide int, target any, dmg float64) {
	amount := func(hp float64) float64 {
		if dmg < hp {
			return dmg
		}
		return hp
	}
	switch t := target.(type) {
	case *Unit:
		s.players[attackerSide].DamageDealt += amount(t.HP)
		t.HP -= dmg
		t.LastHitBy = attackerSide
	case *Turret:
		s.players[attackerSide].DamageDealt += amount(t.HP)
		t.HP -= dmg
		t.LastHitBy = attackerSide
	}
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
				if ub.Splash > 0 {
					s.splashDamage(u.Side, u.Lane, t.X, ub.Splash, u.Damage)
				} else {
					s.dealDamage(u.Side, t, u.Damage)
				}
				u.Cooldown = s.cfg.Units[u.Type].AttackInterval
				s.emitAttackFx(u, t.X)
			}
		case *Turret:
			if u.Cooldown <= 0 {
				dmg := u.Damage
				if ub.VsTurretDamage > 0 {
					mul := 1 + float64(s.players[u.Side].UnitUpgrades[u.Type])*ub.UpgradeDamage
					dmg = ub.VsTurretDamage * mul
				}
				s.dealDamage(u.Side, t, dmg)
				u.Cooldown = s.cfg.Units[u.Type].AttackInterval
				s.emitAttackFx(u, t.X)
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
		// Structure-breaker: attacks turrets only, never troops.
		if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil {
			return tr
		}
		return nil
	case Siege:
		// Artillery: attacks turrets only, never troops.
		// Only targets inside [MinRange, Range]; hold position when too close.
		tr := s.nearestEnemyTurret(u, ub.Range)
		if tr == nil {
			return nil
		}
		if absDist(u.X, tr.X) < ub.MinRange {
			return nil // too close; handled by the hold-position rule
		}
		return tr
	default:
		// Soldiers, bowmen and knights ignore turrets entirely.
		if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil {
			return eu
		}
		return nil
	}
}

// splashDamage hits every enemy unit in the lane within radius of centerX.
// Used by fire-breathing dragons; the primary target is included.
func (s *Sim) splashDamage(side, lane int, centerX, radius, dmg float64) {
	for _, u := range s.units {
		if u.Side == side || u.Lane != lane || u.HP <= 0 {
			continue
		}
		if absDist(u.X, centerX) <= radius {
			s.dealDamage(side, u, dmg)
		}
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
	if u.Lane >= 0 && u.Lane < NumLanes {
		for slot := 0; slot < TurretSlots; slot++ {
			t := s.turrets[enemy][u.Lane][slot]
			if t == nil {
				continue
			}
			d := absDist(u.X, t.X)
			if d <= maxRange && d < bestD {
				best, bestD = t, d
			}
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
		if s.cfg.Units[e.Type].Flying {
			continue // flyers don't force siege engines to hold position
		}
		if dd := absDist(u.X, e.X); dd < d {
			d, found = dd, true
		}
	}
	for lane := 0; lane < NumLanes; lane++ {
		if lane != u.Lane {
			continue
		}
		for slot := 0; slot < TurretSlots; slot++ {
			if t := s.turrets[1-u.Side][lane][slot]; t != nil {
				if dd := absDist(u.X, t.X); dd < d {
					d, found = dd, true
				}
			}
		}
	}
	return d, found
}

// emitAttackFx records a cosmetic shot/melee event for clients to render.
// Bowman, dragon and siege units fire projectiles; everything else is a melee swing.
func (s *Sim) emitAttackFx(u *Unit, targetX float64) {
	kind := "melee"
	if u.Type == Bowman || u.Type == Siege || u.Type == Dragon {
		kind = "shot"
	}
	s.fx = append(s.fx, FxEvent{Kind: kind, Unit: string(u.Type), Side: u.Side, Lane: u.Lane, FromX: u.X, ToX: targetX, Slot: -1})
}

func (s *Sim) stepTurrets(dt float64) {
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				t := s.turrets[side][lane][slot]
				if t == nil {
					continue
				}
				t.Cooldown -= dt

				// Revalidate the focused target: must be alive, in lane, in range,
				// and not flying (turrets cannot target flyers).
				target := s.unitByID(t.TargetID)
				if target != nil && (target.Side == side || target.Lane != lane || absDist(target.X, t.X) > s.cfg.Turret.Range || target.HP <= 0 || s.cfg.Units[target.Type].Flying) {
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
					s.dealDamage(side, target, t.Damage*s.TurretFactor)
					t.Cooldown = s.cfg.Turret.AttackInterval
					s.fx = append(s.fx, FxEvent{Kind: "shot", Unit: "turret", Side: side, Lane: lane, FromX: t.X, ToX: target.X, Slot: slot})
				}
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
		if s.cfg.Units[u.Type].Flying {
			continue // turrets cannot hit flyers
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
			continue
		}
		s.players[u.Side].UnitsLost++
		if !s.firstBlood {
			s.firstBlood = true
			s.addEvent(SideNone, "firstBlood", "First blood in lane %d!", u.Lane+1)
		}
		if killer := u.LastHitBy; killer >= 0 {
			s.players[killer].Kills++
			if s.Time-s.lastKillAt[killer] <= 3.0 {
				s.streakCount[killer]++
			} else {
				s.streakCount[killer] = 1
			}
			s.lastKillAt[killer] = s.Time
			if n := s.streakCount[killer]; n >= 2 {
				var label string
				switch n {
				case 2:
					label = "DOUBLE KILL"
				case 3:
					label = "TRIPLE KILL"
				default:
					label = fmt.Sprintf("RAMPAGE x%d", n)
				}
				s.addEvent(SideNone, "streak", "%s — %s!", sideName(killer), label)
				s.events[len(s.events)-1].Amount = n
			}
		}
	}
	for i := len(kept); i < len(s.units); i++ {
		s.units[i] = nil
	}
	s.units = kept

	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				if t := s.turrets[side][lane][slot]; t != nil && t.HP <= 0 {
					if killer := t.LastHitBy; killer >= 0 {
						s.players[killer].TurretsDown++
					}
					s.turrets[side][lane][slot] = nil
					s.addEvent(side, "turretDestroyed", "%s turret in lane %d was destroyed!", sideName(side), lane+1)
				}
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
