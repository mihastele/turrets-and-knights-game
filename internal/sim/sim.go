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

// WinMode determines how a match is won.
type WinMode string

const (
	WinModeLives    WinMode = "lives"
	WinModeBreaches WinMode = "breaches"
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
	ActionUpgradeFactory
	ActionBuildWall
	ActionUpgradeWall
	ActionScoutLane
)

// Coupon field-effect identifiers (balance lives in Balance.Coupon).
const (
	EffectRockslide  = "rockslide"
	EffectDragonFire = "dragonfire"
	EffectWarcry     = "warcry"
)

// Action is a player intention.
type Action struct {
	Kind     ActionKind
	Side     int // SideA or SideB, filled in by the server from the connection
	Lane     int
	Unit     UnitType
	Slot     int
	Track    UpgradeTrack // upgrade track for ActionUpgradeUnit / ActionUpgradeTurret
	Effect   string       // field-effect id for ActionFieldEffect
	Entitled bool         // set by the server from the coupon store; never trusted from clients
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
	ErrUnknownTrack   = errors.New("unknown upgrade track")
	ErrWallExists     = errors.New("reinforcement wall already stands in that lane")
	ErrNoWall         = errors.New("no reinforcement wall in that lane")
	ErrAlreadyScouted = errors.New("lane already scouted")
)

// ---------------------------------------------------------------------------
// Entities
// ---------------------------------------------------------------------------

// Unit is a single fighting entity.
type Unit struct {
	ID            int64                `json:"id"`
	Type          UnitType             `json:"type"`
	Side          int                  `json:"side"`
	Lane          int                  `json:"lane"`
	X             float64              `json:"x"`
	YOff          float64              `json:"yOff"` // cosmetic lane-offset in lane-height fractions (-0.31..0.31)
	HP            float64              `json:"hp"`
	MaxHP         float64              `json:"maxHp"`
	Shield        float64              `json:"shield"`
	MaxShield     float64              `json:"maxShield"`
	Damage        float64              `json:"damage"`
	Cooldown      float64              `json:"-"` // seconds until next attack
	LastHitBy     int                  `json:"-"` // side that dealt the last damage, -1 if none
	DistanceMoved float64              `json:"-"` // used for Knight Charge
	ChargeReady   bool                 `json:"-"`
	BurnUntil     float64              `json:"-"` // incendiary arrow DoT
	BurnTickIn    float64              `json:"-"`
	SpeedBuff     float64              `json:"-"` // fractional attack/move speed bonus for the round
	SpeedBuffEnd  float64              `json:"-"`
	TrackLevels   map[UpgradeTrack]int `json:"-"`
}

// Turret is a lane protective tower standing on a slot of a pad.
type Turret struct {
	Side        int                  `json:"side"`
	Lane        int                  `json:"lane"`
	Slot        int                  `json:"slot"`
	X           float64              `json:"x"`
	Level       int                  `json:"level"`
	HP          float64              `json:"hp"`
	MaxHP       float64              `json:"maxHp"`
	Shield      float64              `json:"shield"`
	MaxShield   float64              `json:"maxShield"`
	Damage      float64              `json:"damage"`
	Cooldown    float64              `json:"-"`
	TargetID    int64                `json:"-"`
	LastHitBy   int                  `json:"-"`
	TrackLevels map[UpgradeTrack]int `json:"trackLevels"`
}

// TowerShield is a weaker defensive barrier standing just in front of a lane's protective tower.
// Reserved for future use; real-time matches spawn none.
type TowerShield struct {
	Side      int     `json:"side"`
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	HP        float64 `json:"hp"`
	MaxHP     float64 `json:"maxHp"`
	Shield    float64 `json:"shield"`
	MaxShield float64 `json:"maxShield"`
	LastHitBy int     `json:"-"`
}

// Wall is a buildable, upgradeable, and rebuildable defensive barrier in a lane.
type Wall struct {
	Side      int     `json:"side"`
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Level     int     `json:"level"`
	HP        float64 `json:"hp"`
	MaxHP     float64 `json:"maxHp"`
	Shield    float64 `json:"shield"`
	MaxShield float64 `json:"maxShield"`
	LastHitBy int     `json:"-"`
}

// Anomaly is a random lane feature: loot, shrine, neutral monster, or hazard.
type Anomaly struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"` // "loot_coins" | "loot_buff" | "neutral_golem" | "hazard_fire" | "hazard_tar" | "hazard_mud" | "hazard_spikes"
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Radius    float64 `json:"radius"`
	Contested bool    `json:"contested"`
	HP        float64 `json:"hp"`
	MaxHP     float64 `json:"maxHp"`
	Damage    float64 `json:"-"`
	Cooldown  float64 `json:"-"`
	Value     int     `json:"value"` // coin bounty or amount
	LastHitBy int     `json:"-"`
	TickIn    float64 `json:"-"`
	ExpiresAt float64 `json:"expiresAt"`
}

// Burn is an active Dragon Fire field effect.
type Burn struct {
	Lane       int
	Side       int     // caster side
	Until      float64 // sim time at which the burn expires
	TickIn     float64 // seconds until the next damage tick
	Interval   float64 // seconds between damage ticks
	TickDamage float64
}

// Player holds per-player economy and upgrade state.
type Player struct {
	Coins             int                               `json:"coins"`
	Factory           bool                              `json:"factory"`
	FactoryLevel      int                               `json:"factoryLevel"`
	Lives             int                               `json:"lives"`
	UnitUpgrades      map[UnitType]int                  `json:"unitUpgrades"`
	UnitTrackUpgrades map[UnitType]map[UpgradeTrack]int `json:"unitTrackUpgrades"`
	Entitled          bool                              `json:"-"` // updated when actions arrive

	// Match statistics (for the end-of-match report).
	DamageDealt  float64 `json:"damageDealt"`
	Kills        int     `json:"kills"`
	UnitsLost    int     `json:"unitsLost"`
	TurretsDown  int     `json:"turretsDown"`
	BountyEarned int     `json:"bountyEarned"`
	ReconEarned  int     `json:"reconEarned"`
}

// IncomePerInterval returns the player's income per economy interval
// (base plus factory tiers).
func (p *Player) IncomePerInterval(cfg Balance) int {
	inc := cfg.Economy.BaseIncome
	if p.Factory {
		inc += cfg.Economy.FactoryIncome * (p.FactoryLevel + 1)
	}
	return inc
}

// ---------------------------------------------------------------------------
// Events (drained by the server and forwarded to clients)
// ---------------------------------------------------------------------------

type Event struct {
	Tick   int64  `json:"tick"`
	Side   int    `json:"side"` // SideA, SideB, or SideNone for global news
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Amount int    `json:"amount,omitempty"`
	Lane   int    `json:"lane,omitempty"`
}

// FxEvent is a purely cosmetic combat effect for clients to render.
type FxEvent struct {
	Kind  string  `json:"kind"` // "shot" | "melee" | "rockslide" | "dragonfire" | "warcry" | "burn" | "bounty"
	Unit  string  `json:"unit"`
	Side  int     `json:"side"`
	Lane  int     `json:"lane"`
	FromX float64 `json:"fromX"`
	ToX   float64 `json:"toX"`
	Slot  int     `json:"slot"` // turret slot index; -1 for non-turret FX
}

// DrainFx returns and clears pending FX events.
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

	units        []*Unit
	turrets      [2][NumLanes][TurretSlots]*Turret
	towerShields [2][NumLanes]*TowerShield
	walls        [2][NumLanes]*Wall
	players      [2]Player

	HideLuck       bool    `json:"hideLuck"`
	WinMode        WinMode `json:"winMode"`
	BreachesToWin  int     `json:"breachesToWin"`
	MaxBreachLoots int     `json:"maxBreachLoots"`
	breachedLanes  [2][NumLanes]bool
	laneBreachLoot [2][NumLanes]int
	scoutedUntil   [2][NumLanes]float64
	laneDominance  [NumLanes]int
	spotted        [2]map[int64]bool // per-viewer unit IDs already paying a recon bounty

	Overtime      bool    `json:"overtime"`
	TurretFactor  float64 `json:"turretFactor"`
	OvertimeSteps int     `json:"overtimeSteps"`

	Finished bool `json:"finished"`
	Draw     bool `json:"draw"`
	Winner   int  `json:"winner"`
	WinLane  int  `json:"winLane"`

	nextUnitID      int64
	events          []Event
	fx              []FxEvent
	firstBlood      bool
	anomalies       []*Anomaly
	streakCount     [2]int
	lastKillAt      [2]float64
	burns           []Burn
	abilityReady    [2]map[string]float64
	incomeIn        float64
	windfallIn      float64
	overtimeDecayIn float64
	anomalyIn       float64
}

// New creates a fresh real-time match. The field starts empty: no towers, no
// walls, no anomalies. The first anomaly drops shortly after the start.
func New(seed int64, cfg Balance) *Sim {
	s := &Sim{
		cfg:            cfg,
		rng:            rand.New(rand.NewSource(seed)),
		WinMode:        WinModeLives,
		BreachesToWin:  cfg.Breach.BreachesToWin,
		MaxBreachLoots: cfg.Breach.MaxBreachLoots,
		Winner:         NoSide,
		WinLane:        -1,
		nextUnitID:     1,
		TurretFactor:   1.0,
		incomeIn:       cfg.Economy.Interval,
	}
	if s.BreachesToWin < 1 {
		s.BreachesToWin = 3
	}
	if s.MaxBreachLoots < 1 {
		s.MaxBreachLoots = 5
	}
	s.windfallIn = s.rollWindfallGap()
	s.anomalyIn = 12 // first event drops early so scouting matters from the start
	for i := range s.players {
		s.players[i] = Player{
			Coins:             cfg.StartCoins,
			Lives:             1,
			UnitUpgrades:      map[UnitType]int{},
			UnitTrackUpgrades: map[UnitType]map[UpgradeTrack]int{},
		}
		s.lastKillAt[i] = -100
		s.abilityReady[i] = map[string]float64{}
		s.spotted[i] = map[int64]bool{}
	}
	for lane := 0; lane < NumLanes; lane++ {
		s.laneDominance[lane] = NoSide
	}
	return s
}

func (s *Sim) SetLives(lives int) {
	for i := range s.players {
		s.players[i].Lives = lives
	}
}

// SetWinMode configures the match win strategy ("lives" or "breaches") and the
// target number of distinct lanes to breach in a match when in "breaches" mode.
func (s *Sim) SetWinMode(mode WinMode, breachesToWin int) {
	if mode != WinModeBreaches {
		mode = WinModeLives
	}
	if breachesToWin < 1 {
		breachesToWin = 3
	} else if breachesToWin > NumLanes {
		breachesToWin = NumLanes
	}
	s.WinMode = mode
	s.BreachesToWin = breachesToWin
}

// SetMaxBreachLoots configures how many breaching units per lane per match can
// earn breach coins (1..15).
func (s *Sim) SetMaxBreachLoots(maxLoots int) {
	if maxLoots < 1 {
		maxLoots = 1
	} else if maxLoots > 15 {
		maxLoots = 15
	}
	s.MaxBreachLoots = maxLoots
}

func (s *Sim) countBreaches(side int) int {
	n := 0
	for lane := 0; lane < NumLanes; lane++ {
		if s.breachedLanes[side][lane] {
			n++
		}
	}
	return n
}

// SetHideLuck configures whether unrevealed field anomalies stay hidden under
// fog of war until a unit, tower, or scout flare reveals them.
func (s *Sim) SetHideLuck(hide bool) {
	s.HideLuck = hide
}

func (s *Sim) rollWindfallGap() float64 {
	e := s.cfg.Economy
	if e.WindfallGapMax <= e.WindfallGapMin {
		return e.WindfallGapMin
	}
	return e.WindfallGapMin + s.rng.Float64()*(e.WindfallGapMax-e.WindfallGapMin)
}

func (s *Sim) rollAnomalyGap() float64 {
	a := s.cfg.Anomaly
	if a.SpawnGapMax <= a.SpawnGapMin {
		return a.SpawnGapMin
	}
	return a.SpawnGapMin + s.rng.Float64()*(a.SpawnGapMax-a.SpawnGapMin)
}

// Balance returns the balance table the sim was built with.
func (s *Sim) Balance() Balance { return s.cfg }

func (s *Sim) addEvent(side int, kind, format string, args ...any) {
	s.events = append(s.events, Event{Tick: s.Tick, Side: side, Kind: kind, Text: fmt.Sprintf(format, args...)})
}

// DrainEvents returns and clears pending events.
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

// Resign forfeits the match for the given side.
func (s *Sim) Resign(side int) {
	if s.Finished || (side != SideA && side != SideB) {
		return
	}
	s.Finished = true
	s.Winner = 1 - side
	s.WinLane = -1
	s.addEvent(SideNone, "resign", "%s resigned", sideName(side))
}

// ApplyAction validates and applies a player intention.
func (s *Sim) ApplyAction(a Action) error {
	if s.Finished {
		return ErrMatchOver
	}
	if a.Side != SideA && a.Side != SideB {
		return errors.New("invalid side")
	}
	p := &s.players[a.Side]
	p.Entitled = a.Entitled

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
		p.Coins -= ub.Cost
		s.spawnUnit(a.Side, a.Lane, a.Unit)
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
			Side:        a.Side,
			Lane:        a.Lane,
			Slot:        slot,
			X:           x,
			Level:       0,
			HP:          s.cfg.Turret.MaxHP,
			MaxHP:       s.cfg.Turret.MaxHP,
			Shield:      s.cfg.Turret.BaseShield,
			MaxShield:   s.cfg.Turret.BaseShield,
			Damage:      s.cfg.Turret.Damage,
			LastHitBy:   -1,
			TrackLevels: map[UpgradeTrack]int{},
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
		s.addEvent(a.Side, "factory", "%s built a Coin Factory", sideName(a.Side))
		return nil

	case ActionUpgradeFactory:
		if !p.Factory {
			return ErrFactoryBuilt
		}
		if p.FactoryLevel >= 2 {
			return ErrMaxLevel
		}
		cost := [2]int{75, 125}[p.FactoryLevel]
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		p.FactoryLevel++
		s.addEvent(a.Side, "factory", "%s upgraded their Coin Factory (Lv%d)", sideName(a.Side), p.FactoryLevel+1)
		return nil

	case ActionUpgradeUnit:
		ub, ok := s.cfg.Units[a.Unit]
		if !ok {
			return ErrUnknownUnit
		}
		if ub.CouponOnly && !a.Entitled {
			return ErrCouponRequired
		}
		track := a.Track
		if track == "" {
			// Legacy / default track upgrades both HP & Damage via TrackDamage/TrackHP fallback
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
			s.setTrackLevel(p, a.Unit, TrackHP, level+1)
			s.setTrackLevel(p, a.Unit, TrackDamage, level+1)
			return nil
		}
		if !validTrack(track) {
			return ErrUnknownTrack
		}
		if track == TrackSpecial && !a.Entitled {
			return ErrCouponRequired
		}
		maxLvl := 2
		if track == TrackSpecial {
			maxLvl = 1
		}
		cur := s.getTrackLevel(p, a.Unit, track)
		if cur >= maxLvl {
			return ErrMaxLevel
		}
		costs := ub.TrackCosts[track]
		cost := costs[cur]
		if cost <= 0 {
			cost = ub.UpgradeCosts[cur]
		}
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		s.setTrackLevel(p, a.Unit, track, cur+1)
		if track == TrackHP || track == TrackDamage {
			// Keep aggregate legacy counter in sync for any display
			hpL := s.getTrackLevel(p, a.Unit, TrackHP)
			dmgL := s.getTrackLevel(p, a.Unit, TrackDamage)
			if hpL > dmgL {
				p.UnitUpgrades[a.Unit] = hpL
			} else {
				p.UnitUpgrades[a.Unit] = dmgL
			}
		}
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
		if t.TrackLevels == nil {
			t.TrackLevels = map[UpgradeTrack]int{}
		}
		track := a.Track
		if track == "" {
			if t.Level >= 2 {
				return ErrMaxLevel
			}
			cost := s.cfg.Turret.UpgradeCosts[t.Level]
			if p.Coins < cost {
				return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
			}
			p.Coins -= cost
			t.Level++
			t.MaxHP += s.cfg.Turret.MaxHP * s.cfg.Turret.UpgradeHPPerLevel
			t.Damage = s.cfg.Turret.Damage * (1 + float64(t.Level)*s.cfg.Turret.UpgradeDamagePerLevel)
			return nil
		}
		if !validTrack(track) {
			return ErrUnknownTrack
		}
		if track == TrackSpecial && !a.Entitled {
			return ErrCouponRequired
		}
		maxLvl := 2
		if track == TrackSpecial {
			maxLvl = 1
		}
		cur := t.TrackLevels[track]
		if cur >= maxLvl {
			return ErrMaxLevel
		}
		cost := s.cfg.Turret.TrackCosts[track][cur]
		if cost <= 0 {
			cost = s.cfg.Turret.UpgradeCosts[cur]
		}
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		t.TrackLevels[track] = cur + 1
		switch track {
		case TrackHP:
			t.MaxHP += s.cfg.Turret.MaxHP * s.cfg.Turret.UpgradeHPPerLevel
		case TrackDamage:
			t.Damage = s.cfg.Turret.Damage * (1 + float64(t.TrackLevels[TrackDamage])*s.cfg.Turret.UpgradeDamagePerLevel)
		case TrackShield:
			addShield := s.cfg.Turret.MaxHP * 0.25
			t.MaxShield += addShield
			t.Shield += addShield
		}
		t.Level = maxInt(t.TrackLevels[TrackHP], t.TrackLevels[TrackDamage])
		return nil

	case ActionFieldEffect:
		return s.applyFieldEffect(a, p)

	case ActionBuildWall:
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		if s.Overtime {
			return ErrOvertime
		}
		if s.walls[a.Side][a.Lane] != nil {
			return ErrWallExists
		}
		cost := s.cfg.Wall.Cost
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		x := s.cfg.Wall.PadX
		if a.Side == SideB {
			x = FieldLength - s.cfg.Wall.PadX
		}
		s.walls[a.Side][a.Lane] = &Wall{
			Side:      a.Side,
			Lane:      a.Lane,
			X:         x,
			Level:     0,
			HP:        s.cfg.Wall.MaxHP,
			MaxHP:     s.cfg.Wall.MaxHP,
			Shield:    s.cfg.Wall.BaseShield,
			MaxShield: s.cfg.Wall.BaseShield,
			LastHitBy: -1,
		}
		s.addEvent(a.Side, "wall", "%s fortified Lane %d with a Reinforcement Wall", sideName(a.Side), a.Lane+1)
		return nil

	case ActionUpgradeWall:
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		if s.Overtime {
			return ErrOvertime
		}
		w := s.walls[a.Side][a.Lane]
		if w == nil {
			return ErrNoWall
		}
		if w.Level >= 2 {
			return ErrMaxLevel
		}
		cost := s.cfg.Wall.UpgradeCosts[w.Level]
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		w.Level++
		hpGain := s.cfg.Wall.MaxHP * s.cfg.Wall.UpgradeHPPerLevel
		w.MaxHP += hpGain
		w.HP = math.Min(w.MaxHP, w.HP+hpGain)
		shGain := s.cfg.Wall.BaseShield * s.cfg.Wall.UpgradeShield
		w.MaxShield += shGain
		w.Shield = w.MaxShield
		s.addEvent(a.Side, "wall", "%s upgraded Lane %d Wall (Lv%d)", sideName(a.Side), a.Lane+1, w.Level+1)
		return nil

	case ActionScoutLane:
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		if s.scoutedUntil[a.Side][a.Lane] > s.Time {
			return ErrAlreadyScouted
		}
		cost := s.cfg.Fog.ScoutCost
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		s.scoutedUntil[a.Side][a.Lane] = s.Time + s.cfg.Fog.ScoutDuration
		s.addEvent(a.Side, "scout", "%s launched a Scout Flare in Lane %d!", sideName(a.Side), a.Lane+1)
		s.events[len(s.events)-1].Lane = a.Lane
		return nil
	}
	return errors.New("unknown action")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func validTrack(t UpgradeTrack) bool {
	switch t {
	case TrackDamage, TrackAttackSpeed, TrackHP, TrackShield, TrackSpecial:
		return true
	}
	return false
}

func (s *Sim) getTrackLevel(p *Player, u UnitType, t UpgradeTrack) int {
	if p.UnitTrackUpgrades == nil {
		return 0
	}
	if m := p.UnitTrackUpgrades[u]; m != nil {
		return m[t]
	}
	return 0
}

func (s *Sim) setTrackLevel(p *Player, u UnitType, t UpgradeTrack, lvl int) {
	if p.UnitTrackUpgrades == nil {
		p.UnitTrackUpgrades = map[UnitType]map[UpgradeTrack]int{}
	}
	if p.UnitTrackUpgrades[u] == nil {
		p.UnitTrackUpgrades[u] = map[UpgradeTrack]int{}
	}
	p.UnitTrackUpgrades[u][t] = lvl
}

// spawnUnit deploys a unit that marches and fights immediately. Unit upgrades
// apply to future spawns only: stats snapshot the owner's track levels.
func (s *Sim) spawnUnit(side, lane int, unitType UnitType) {
	ub := s.cfg.Units[unitType]
	p := &s.players[side]
	hpLvl := s.getTrackLevel(p, unitType, TrackHP)
	dmgLvl := s.getTrackLevel(p, unitType, TrackDamage)
	shdLvl := s.getTrackLevel(p, unitType, TrackShield)
	spdLvl := s.getTrackLevel(p, unitType, TrackAttackSpeed)
	spcLvl := s.getTrackLevel(p, unitType, TrackSpecial)

	hpMul := 1 + float64(hpLvl)*ub.UpgradeHP
	dmgMul := 1 + float64(dmgLvl)*ub.UpgradeDamage
	shield := ub.BaseShield + float64(shdLvl)*ub.MaxHP*ub.UpgradeShield

	if spcLvl > 0 {
		switch unitType {
		case Soldier:
			shield += 15
		case Ram:
			shield += 50
		case Mage:
			dmgMul += 0.20
		case Siege:
			dmgMul += 0.35
		case Valkyrie:
			hpMul += 0.20
			dmgMul += 0.20
			shield += 30
		case Dragon:
			hpMul += 0.20
			dmgMul += 0.20
			shield += 40
		}
	}

	x := s.cfg.SpawnInset + s.rng.Float64()*1.5
	if side == SideB {
		x = FieldLength - x
	}

	tracks := map[UpgradeTrack]int{
		TrackHP:          hpLvl,
		TrackDamage:      dmgLvl,
		TrackShield:      shdLvl,
		TrackAttackSpeed: spdLvl,
		TrackSpecial:     spcLvl,
	}

	s.units = append(s.units, &Unit{
		ID:            s.nextUnitID,
		Type:          unitType,
		Side:          side,
		Lane:          lane,
		X:             x,
		YOff:          (s.rng.Float64() - 0.5) * 0.60,
		HP:            ub.MaxHP * hpMul,
		MaxHP:         ub.MaxHP * hpMul,
		Shield:        shield,
		MaxShield:     shield,
		Damage:        ub.Damage * dmgMul,
		Cooldown:      0,
		LastHitBy:     -1,
		DistanceMoved: 0,
		ChargeReady:   false,
		TrackLevels:   tracks,
	})
	s.nextUnitID++
}

// effectConfig resolves a field-effect id to its balance row.
func (s *Sim) effectConfig(id string) (FieldEffectBalance, bool) {
	switch id {
	case EffectRockslide:
		return s.cfg.Coupon.Rockslide, true
	case EffectDragonFire:
		return s.cfg.Coupon.DragonFire, true
	case EffectWarcry:
		return s.cfg.Coupon.Warcry, true
	default:
		return FieldEffectBalance{}, false
	}
}

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
			if s.cfg.Units[u.Type].Flying {
				continue
			}
			s.dealDamage(a.Side, u, cfg.Damage, 1.0)
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
			TickIn:     interval,
			Interval:   interval,
			TickDamage: cfg.Damage,
		})
		s.addEvent(a.Side, "dragonfire", "%s set lane %d ablaze with Dragon Fire!", sideName(a.Side), a.Lane+1)
		s.fx = append(s.fx, FxEvent{Kind: "dragonfire", Side: a.Side, Lane: a.Lane, Slot: -1})

	case EffectWarcry:
		buffed := 0
		dur := cfg.Duration
		if dur <= 0 {
			dur = 8
		}
		for _, u := range s.units {
			if u.Side == a.Side && u.Lane == a.Lane && u.HP > 0 {
				u.Shield += cfg.Damage
				u.MaxShield = math.Max(u.MaxShield, u.Shield)
				u.SpeedBuff = math.Max(u.SpeedBuff, 0.35)
				u.SpeedBuffEnd = s.Time + dur
				buffed++
			}
		}
		s.addEvent(a.Side, "warcry", "%s sounded a Warcry in lane %d! (%d allies empowered)", sideName(a.Side), a.Lane+1, buffed)
		s.fx = append(s.fx, FxEvent{Kind: "warcry", Side: a.Side, Lane: a.Lane, Slot: -1})
	}
	return nil
}

func validLane(l int) bool { return l >= 0 && l < NumLanes }

func firstFreeTurretSlot(slots [TurretSlots]*Turret) int {
	for i, t := range slots {
		if t == nil {
			return i
		}
	}
	return -1
}

func firstBuiltTurretSlot(slots [TurretSlots]*Turret) int {
	for i, t := range slots {
		if t != nil {
			return i
		}
	}
	return -1
}

const timerEpsilon = 1e-9

func sideName(side int) string {
	if side == SideA {
		return "Blue"
	}
	return "Red"
}

// ---------------------------------------------------------------------------
// Anomalies, dominance & fog of war
// ---------------------------------------------------------------------------

var anomalyKinds = []string{"loot_coins", "loot_buff", "neutral_golem", "hazard_fire", "hazard_tar", "hazard_mud", "hazard_spikes"}

// spawnAnomaly drops one random field event in a random lane near midfield.
// A rumor names the lane but not the kind, so scouting still matters.
func (s *Sim) spawnAnomaly() {
	ab := s.cfg.Anomaly
	lane := s.rng.Intn(NumLanes)
	kind := anomalyKinds[s.rng.Intn(len(anomalyKinds))]
	x := 38.0 + s.rng.Float64()*24.0
	anom := &Anomaly{
		ID:        s.nextUnitID,
		Kind:      kind,
		Lane:      lane,
		X:         x,
		Radius:    ab.PickupRadius,
		LastHitBy: -1,
		TickIn:    0.5,
		ExpiresAt: s.Time + ab.Lifetime,
	}
	switch kind {
	case "loot_coins":
		anom.Value = ab.CoinAmount
	case "loot_buff":
		anom.Value = int(ab.BuffShield)
	case "neutral_golem":
		anom.HP = ab.MonsterHP
		anom.MaxHP = ab.MonsterHP
		anom.Damage = ab.MonsterDamage
		anom.Value = ab.MonsterBounty
	case "hazard_mud":
		minR, maxR := ab.MudMinRadius, ab.MudMaxRadius
		if maxR < minR {
			maxR = minR
		}
		anom.Radius = minR + s.rng.Float64()*(maxR-minR)
	case "hazard_spikes":
		minR, maxR := ab.SpikeMinRadius, ab.SpikeMaxRadius
		if maxR < minR {
			maxR = minR
		}
		anom.Radius = minR + s.rng.Float64()*(maxR-minR)
		anom.TickIn = 0.4
	case "hazard_fire", "hazard_tar":
		// passive environmental hazard
	}
	s.nextUnitID++
	s.anomalies = append(s.anomalies, anom)
	s.addEvent(SideNone, "supply", "Rumor: something stirs in lane %d\u2026", lane+1)
	s.events[len(s.events)-1].Lane = lane
}

// updateDominance marks lanes held by exactly one side. Dominant armies march
// faster there, so pushing out always pays.
func (s *Sim) updateDominance() {
	var alive [2][NumLanes]int
	for _, u := range s.units {
		if u.HP > 0 && validLane(u.Lane) {
			alive[u.Side][u.Lane]++
		}
	}
	for lane := 0; lane < NumLanes; lane++ {
		switch {
		case alive[SideA][lane] > 0 && alive[SideB][lane] == 0:
			s.laneDominance[lane] = SideA
		case alive[SideB][lane] > 0 && alive[SideA][lane] == 0:
			s.laneDominance[lane] = SideB
		default:
			s.laneDominance[lane] = NoSide
		}
	}
}

// visionAt reports whether viewer can see position (lane, x) through the fog.
// Each side always sees its own half; past midfield, sight comes from units,
// towers, walls, and scout flares.
func (s *Sim) visionAt(viewer, lane int, x float64) bool {
	if !validLane(lane) || (viewer != SideA && viewer != SideB) {
		return false
	}
	if viewer == SideA && x <= FieldLength/2 {
		return true
	}
	if viewer == SideB && x >= FieldLength/2 {
		return true
	}
	if s.scoutedUntil[viewer][lane] > s.Time {
		return true
	}
	fb := s.cfg.Fog
	for _, u := range s.units {
		if u.Side != viewer || u.HP <= 0 {
			continue
		}
		r := fb.UnitVision
		if s.cfg.Units[u.Type].Flying {
			r = fb.FlyingVision
		}
		dl := u.Lane - lane
		if dl < 0 {
			dl = -dl
		}
		switch dl {
		case 0:
			if absDist(u.X, x) <= r {
				return true
			}
		case 1:
			if absDist(u.X, x) <= r*fb.AdjacentFactor {
				return true
			}
		}
	}
	for slot := 0; slot < TurretSlots; slot++ {
		if t := s.turrets[viewer][lane][slot]; t != nil && t.HP > 0 {
			if absDist(t.X, x) <= fb.StructureVision {
				return true
			}
		}
	}
	if w := s.walls[viewer][lane]; w != nil && w.HP > 0 {
		if absDist(w.X, x) <= fb.StructureVision {
			return true
		}
	}
	return false
}

// spotEnemies pays a recon bounty the first time each enemy unit enters the
// viewer's vision. Pushing units forward is the cheapest scout there is.
func (s *Sim) spotEnemies() {
	if s.cfg.Fog.ReconBounty <= 0 {
		return
	}
	for _, u := range s.units {
		if u.HP <= 0 {
			continue
		}
		viewer := 1 - u.Side
		if s.spotted[viewer][u.ID] {
			continue
		}
		if !s.visionAt(viewer, u.Lane, u.X) {
			continue
		}
		s.spotted[viewer][u.ID] = true
		s.players[viewer].Coins += s.cfg.Fog.ReconBounty
		s.players[viewer].ReconEarned += s.cfg.Fog.ReconBounty
		s.fx = append(s.fx, FxEvent{Kind: "bounty", Unit: fmt.Sprintf("%d", s.cfg.Fog.ReconBounty), Side: viewer, Lane: u.Lane, FromX: u.X, ToX: u.X, Slot: -1})
	}
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

	// --- Field events: one anomaly at a time, while the field has room ---
	s.anomalyIn -= dt
	if s.anomalyIn <= timerEpsilon {
		if len(s.anomalies) < s.cfg.Anomaly.MaxAlive {
			s.spawnAnomaly()
		}
		s.anomalyIn = s.rollAnomalyGap()
	}

	// --- Kill streak decay ---
	for side := 0; side < 2; side++ {
		if s.streakCount[side] > 0 && s.Time-s.lastKillAt[side] > 3.0 {
			s.streakCount[side] = 0
		}
	}

	// --- Overtime: turrets fade, attackers hit structures harder ---
	if !s.Overtime && s.Time >= s.cfg.Overtime.Start {
		s.Overtime = true
		s.overtimeDecayIn = s.cfg.Overtime.Interval
		s.addEvent(SideNone, "overtime", "OVERTIME — tower and wall construction disabled; towers weakening, breachers emboldened")
	}
	if s.Overtime {
		s.overtimeDecayIn -= dt
		// At most ten escalation steps; afterwards the draw clock decides.
		for s.overtimeDecayIn <= timerEpsilon && s.OvertimeSteps < 10 {
			if s.TurretFactor > 0 {
				s.TurretFactor = math.Max(0, s.TurretFactor-s.cfg.Overtime.Decay)
			}
			s.OvertimeSteps++
			s.overtimeDecayIn += s.cfg.Overtime.Interval
			s.addEvent(SideNone, "overtime", "Overtime: turret damage %d%%, breacher bonus +%d%%",
				int(math.Round(s.TurretFactor*100)), int(math.Round(float64(s.OvertimeSteps)*s.cfg.Overtime.StructureBonus*100)))
		}
	}

	// Dragon Fire burn zones & Incendiary Arrow DoTs
	s.stepBurns(dt)

	// Anomalies: loot pickups, neutral monster combat, environmental hazards
	s.stepAnomalies(dt)

	// Combat & movement
	s.stepUnits(dt)
	s.stepTurrets(dt)
	s.removeDead()

	// Intel & lane control
	s.updateDominance()
	s.spotEnemies()

	// Win / draw detection
	s.checkEnd()
}

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
					s.dealDamage(b.Side, u, b.TickDamage, 1.0)
				}
			}
			s.fx = append(s.fx, FxEvent{Kind: "burn", Side: b.Side, Lane: b.Lane, Slot: -1})
			b.TickIn += b.Interval
		}
		i++
	}
	// Unit-level incendiary burns
	for _, u := range s.units {
		if u.HP > 0 && u.BurnUntil > s.Time {
			u.BurnTickIn -= dt
			if u.BurnTickIn <= 0 {
				u.BurnTickIn += 0.5
				s.dealDamage(1-u.Side, u, 2.5, 1.0)
			}
		}
	}
}

func (s *Sim) stepAnomalies(dt float64) {
	ab := s.cfg.Anomaly
	for i := 0; i < len(s.anomalies); {
		an := s.anomalies[i]
		if s.Time >= an.ExpiresAt {
			s.anomalies = append(s.anomalies[:i], s.anomalies[i+1:]...)
			continue
		}
		switch an.Kind {
		case "loot_coins":
			// First army to touch the loot takes it — unless the enemy is
			// fighting in the same lane, in which case the loot is contested
			// and waits for a winner.
			nearA, nearB := false, false
			for _, u := range s.units {
				if u.Lane != an.Lane || u.HP <= 0 {
					continue
				}
				if absDist(u.X, an.X) > ab.PickupRadius*2 {
					continue
				}
				if u.Side == SideA {
					nearA = true
				} else {
					nearB = true
				}
			}
			an.Contested = nearA && nearB
			claimed := -1
			if !an.Contested {
				for _, u := range s.units {
					if u.Lane == an.Lane && u.HP > 0 && absDist(u.X, an.X) <= ab.PickupRadius {
						claimed = u.Side
						break
					}
				}
			}
			if claimed >= 0 {
				s.players[claimed].Coins += an.Value
				s.players[claimed].BountyEarned += an.Value
				s.addEvent(claimed, "supplyClaim", "%s won +%dc gold loot in lane %d!", sideName(claimed), an.Value, an.Lane+1)
				s.events[len(s.events)-1].Lane = an.Lane
				s.events[len(s.events)-1].Amount = an.Value
				s.anomalies = append(s.anomalies[:i], s.anomalies[i+1:]...)
				continue
			}

		case "loot_buff":
			claimed := -1
			for _, u := range s.units {
				if u.Lane == an.Lane && u.HP > 0 && absDist(u.X, an.X) <= ab.PickupRadius {
					claimed = u.Side
					break
				}
			}
			if claimed >= 0 {
				for _, u := range s.units {
					if u.Side == claimed && u.Lane == an.Lane && u.HP > 0 {
						u.Shield += float64(an.Value)
						u.MaxShield = math.Max(u.MaxShield, u.Shield)
						u.SpeedBuff = math.Max(u.SpeedBuff, ab.BuffSpeedBonus)
						u.SpeedBuffEnd = s.Time + 15.0
					}
				}
				s.addEvent(claimed, "warcry", "%s claimed the Shrine in lane %d! (+Speed & +%d Shield)", sideName(claimed), an.Lane+1, an.Value)
				s.anomalies = append(s.anomalies[:i], s.anomalies[i+1:]...)
				continue
			}

		case "neutral_golem":
			if an.HP <= 0 {
				if killer := an.LastHitBy; killer == SideA || killer == SideB {
					s.players[killer].Coins += an.Value
					s.players[killer].BountyEarned += an.Value
					s.addEvent(killer, "bounty", "%s slew the Lane %d Guardian! (+%dc bounty)", sideName(killer), an.Lane+1, an.Value)
					s.events[len(s.events)-1].Lane = an.Lane
					s.events[len(s.events)-1].Amount = an.Value
				}
				s.anomalies = append(s.anomalies[:i], s.anomalies[i+1:]...)
				continue
			}
			an.Cooldown -= dt
			if an.Cooldown <= 0 {
				var target *Unit
				bestD := math.Inf(1)
				for _, u := range s.units {
					if u.Lane == an.Lane && u.HP > 0 {
						if d := absDist(u.X, an.X); d <= ab.MonsterRange && d < bestD {
							bestD = d
							target = u
						}
					}
				}
				if target != nil {
					s.dealDamage(SideNone, target, an.Damage, 1.0)
					an.Cooldown = 1.2
					s.fx = append(s.fx, FxEvent{Kind: "melee", Unit: "golem", Side: SideNone, Lane: an.Lane, FromX: an.X, ToX: target.X, Slot: -1})
				}
			}

		case "hazard_fire":
			an.TickIn -= dt
			if an.TickIn <= 0 {
				an.TickIn += 0.5
				for _, u := range s.units {
					if u.Lane == an.Lane && u.HP > 0 && !s.cfg.Units[u.Type].Flying && absDist(u.X, an.X) <= ab.PickupRadius*1.4 {
						s.dealDamage(SideNone, u, ab.HazardDPS*0.5, 1.0)
					}
				}
			}

		case "hazard_spikes":
			an.TickIn -= dt
			if an.TickIn <= 0 {
				an.TickIn += 0.4
				r := an.Radius
				if r <= 0 {
					r = ab.SpikeMinRadius
				}
				for _, u := range s.units {
					if u.Lane == an.Lane && u.HP > 0 && !s.cfg.Units[u.Type].Flying && absDist(u.X, an.X) <= r {
						s.dealDamage(SideNone, u, ab.SpikeDPS*0.4, 1.0)
					}
				}
			}
		}
		i++
	}
}

func (s *Sim) dealDamage(attackerSide int, target any, dmg float64, shieldMult float64) {
	if dmg <= 0 {
		return
	}
	if shieldMult <= 0 {
		shieldMult = 1.0
	}
	switch t := target.(type) {
	case *Unit:
		// Flat armor reduction from Shield track
		armor := float64(t.TrackLevels[TrackShield]) * 1.5
		effDmg := math.Max(1.0, dmg-armor)
		dealt := 0.0
		if t.Shield > 0 {
			shieldDmg := effDmg * shieldMult
			if shieldDmg >= t.Shield {
				usedRatio := t.Shield / shieldDmg
				dealt += t.Shield
				t.Shield = 0
				remDmg := effDmg * (1.0 - usedRatio)
				hpDmg := math.Min(t.HP, remDmg)
				t.HP -= hpDmg
				dealt += hpDmg
			} else {
				t.Shield -= shieldDmg
				dealt += shieldDmg
			}
		} else {
			hpDmg := math.Min(t.HP, effDmg)
			t.HP -= hpDmg
			dealt += hpDmg
		}
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += dealt
			t.LastHitBy = attackerSide
		}
	case *Turret:
		armor := float64(t.TrackLevels[TrackShield]) * 1.5
		dealt := applyShieldedDamage(&t.HP, &t.Shield, dmg, shieldMult, armor)
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += dealt
			t.LastHitBy = attackerSide
		}
	case *TowerShield:
		dealt := applyShieldedDamage(&t.HP, &t.Shield, dmg, shieldMult, 0)
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += dealt
			t.LastHitBy = attackerSide
		}
	case *Wall:
		armor := float64(t.Level) * 2.0
		dealt := applyShieldedDamage(&t.HP, &t.Shield, dmg, shieldMult, armor)
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += dealt
			t.LastHitBy = attackerSide
		}
	case *Anomaly:
		hpDmg := math.Min(t.HP, dmg)
		t.HP -= hpDmg
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += hpDmg
			t.LastHitBy = attackerSide
		}
	}
}

func applyShieldedDamage(hp, shield *float64, dmg, shieldMult, armor float64) float64 {
	effDmg := math.Max(1.0, dmg-armor)
	dealt := 0.0
	if *shield > 0 {
		shieldDmg := effDmg * shieldMult
		if shieldDmg >= *shield {
			usedRatio := *shield / shieldDmg
			dealt += *shield
			*shield = 0
			remDmg := effDmg * (1.0 - usedRatio)
			hpDmg := math.Min(*hp, remDmg)
			*hp -= hpDmg
			dealt += hpDmg
		} else {
			*shield -= shieldDmg
			dealt += shieldDmg
		}
	} else {
		hpDmg := math.Min(*hp, effDmg)
		*hp -= hpDmg
		dealt += hpDmg
	}
	return dealt
}

func direction(side int) float64 {
	if side == SideA {
		return 1
	}
	return -1
}

func absDist(a, b float64) float64 { return math.Abs(a - b) }

func (s *Sim) counterMultiplier(attacker UnitType, targetClass UnitClass, attackerTracks map[UpgradeTrack]int) float64 {
	ub := s.cfg.Units[attacker]
	mult := 1.0
	if ub.CounterMultipliers != nil {
		if m, ok := ub.CounterMultipliers[targetClass]; ok {
			mult = m
		}
	}
	if attackerTracks[TrackSpecial] > 0 {
		switch {
		case attacker == Soldier && (targetClass == ClassCavalry || targetClass == ClassHeavy):
			mult += 0.25
		case attacker == Bowman && targetClass == ClassStructure:
			mult += 0.25
		case attacker == Ram && targetClass == ClassStructure:
			mult *= 1.40
		}
	}
	if targetClass == ClassStructure && s.OvertimeSteps > 0 {
		mult *= 1.0 + float64(s.OvertimeSteps)*s.cfg.Overtime.StructureBonus
	}
	return mult
}

func (s *Sim) stepUnits(dt float64) {
	// Precompute Valkyrie aura per side/lane
	valkAura := [2][NumLanes]bool{}
	for _, u := range s.units {
		if u.HP > 0 && u.Type == Valkyrie {
			valkAura[u.Side][u.Lane] = true
		}
	}

	for _, u := range s.units {
		if u.HP <= 0 {
			continue
		}
		if u.SpeedBuff > 0 && s.Time >= u.SpeedBuffEnd {
			u.SpeedBuff = 0
		}
		ub := s.cfg.Units[u.Type]
		atkRateMult := 1.0 + float64(u.TrackLevels[TrackAttackSpeed])*ub.UpgradeSpeed + u.SpeedBuff
		if valkAura[u.Side][u.Lane] && u.Type != Valkyrie {
			atkRateMult += 0.15
		}
		u.Cooldown -= dt * atkRateMult

		// Siege engines hold position when an enemy is inside minimum range
		if u.Type == Siege {
			if d, ok := s.nearestEnemyDist(u); ok && d < ub.MinRange {
				continue
			}
		}

		target := s.findTarget(u)
		shieldMult := ub.ShieldMultiplier
		if shieldMult <= 0 {
			shieldMult = 1.0
		}

		switch t := target.(type) {
		case *Unit:
			if u.Cooldown <= 0 {
				tClass := s.cfg.Units[t.Type].Class
				dmg := u.Damage * s.counterMultiplier(u.Type, tClass, u.TrackLevels)
				if ub.ChargeBonus > 1.0 && u.ChargeReady {
					bonus := ub.ChargeBonus
					if u.TrackLevels[TrackSpecial] > 0 {
						bonus += 0.20
					}
					dmg *= bonus
					u.ChargeReady = false
				}
				splash := ub.Splash
				if u.Type == Mage && u.TrackLevels[TrackSpecial] > 0 {
					splash *= 1.5
				} else if u.Type == Siege && u.TrackLevels[TrackSpecial] > 0 {
					splash *= 1.35
				}
				if splash > 0 {
					s.splashDamage(u.Side, u.Lane, t.X, splash, dmg, shieldMult, ub.CanHitFlying)
				} else {
					s.dealDamage(u.Side, t, dmg, shieldMult)
					// Reflective Bulwark (Shieldbearer special)
					if t.Type == Shieldbearer && t.TrackLevels[TrackSpecial] > 0 && !ub.Flying {
						s.dealDamage(t.Side, u, dmg*0.25, 1.0)
					}
					// Bowman Incendiary Arrows
					if u.Type == Bowman && u.TrackLevels[TrackSpecial] > 0 {
						t.BurnUntil = s.Time + 3.0
						t.BurnTickIn = 0.5
					}
				}
				if u.Type == Knight && u.TrackLevels[TrackSpecial] > 0 && t.HP <= 0 {
					u.ChargeReady = true
				}
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		case *Anomaly:
			if u.Cooldown <= 0 {
				dmg := u.Damage * s.counterMultiplier(u.Type, ClassHeavy, u.TrackLevels)
				s.dealDamage(u.Side, t, dmg, shieldMult)
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		case *Turret:
			if u.Cooldown <= 0 {
				dmg := s.structureAttackDamage(u)
				s.dealDamage(u.Side, t, dmg, shieldMult)
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		case *TowerShield:
			if u.Cooldown <= 0 {
				dmg := s.structureAttackDamage(u)
				s.dealDamage(u.Side, t, dmg, shieldMult)
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		case *Wall:
			if u.Cooldown <= 0 {
				dmg := s.structureAttackDamage(u)
				s.dealDamage(u.Side, t, dmg, shieldMult)
				if t.Level > 0 && !ub.Flying && ub.Range <= 4.0 && s.cfg.Wall.ThornDamage > 0 {
					s.dealDamage(t.Side, u, s.cfg.Wall.ThornDamage*float64(t.Level), 1.0)
				}
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		default:
			moveSpeed := ub.Speed * (1.0 + float64(u.TrackLevels[TrackAttackSpeed])*0.07 + u.SpeedBuff*0.5)
			if validLane(u.Lane) && s.laneDominance[u.Lane] == u.Side {
				moveSpeed *= 1.10
			}
			if s.OvertimeSteps > 0 {
				moveSpeed *= 1.0 + float64(s.OvertimeSteps)*0.06
			}
			if !ub.Flying && s.inTarHazard(u.Lane, u.X) {
				moveSpeed *= (1.0 - s.cfg.Anomaly.HazardSlow)
			}
			if !ub.Flying && s.inMudHazard(u.Lane, u.X) {
				moveSpeed *= (1.0 - s.cfg.Anomaly.MudSlow)
			}
			stepDist := moveSpeed * dt
			nextX := u.X + direction(u.Side)*stepDist

			// Ground units cannot walk past an alive enemy wall, tower shield, or protective tower in their lane
			if !ub.Flying {
				if blockX, blocked := s.enemyWallBlockX(u.Side, u.Lane); blocked {
					if u.Side == SideA && nextX > blockX-1.5 {
						nextX = math.Max(u.X, blockX-1.5)
					} else if u.Side == SideB && nextX < blockX+1.5 {
						nextX = math.Min(u.X, blockX+1.5)
					}
				}
				if blockX, blocked := s.enemyTowerShieldBlockX(u.Side, u.Lane); blocked {
					if u.Side == SideA && nextX > blockX-1.5 {
						nextX = math.Max(u.X, blockX-1.5)
					} else if u.Side == SideB && nextX < blockX+1.5 {
						nextX = math.Min(u.X, blockX+1.5)
					}
				}
				if blockX, blocked := s.enemyTurretBlockX(u.Side, u.Lane); blocked {
					if u.Side == SideA && nextX > blockX-1.5 {
						nextX = math.Max(u.X, blockX-1.5)
					} else if u.Side == SideB && nextX < blockX+1.5 {
						nextX = math.Min(u.X, blockX+1.5)
					}
				}
			}

			moved := math.Abs(nextX - u.X)
			u.X = nextX
			u.DistanceMoved += moved
			if ub.ChargeBonus > 1.0 && u.DistanceMoved >= 12.0 {
				u.ChargeReady = true
			}
		}
	}
}

func (s *Sim) inTarHazard(lane int, x float64) bool {
	for _, an := range s.anomalies {
		if an.Kind == "hazard_tar" && an.Lane == lane && absDist(x, an.X) <= s.cfg.Anomaly.PickupRadius*1.5 {
			return true
		}
	}
	return false
}

func (s *Sim) inMudHazard(lane int, x float64) bool {
	for _, an := range s.anomalies {
		if an.Kind == "hazard_mud" && an.Lane == lane {
			r := an.Radius
			if r <= 0 {
				r = s.cfg.Anomaly.MudMinRadius
			}
			if absDist(x, an.X) <= r {
				return true
			}
		}
	}
	return false
}

func (s *Sim) structureAttackDamage(u *Unit) float64 {
	ub := s.cfg.Units[u.Type]
	dmg := u.Damage * s.counterMultiplier(u.Type, ClassStructure, u.TrackLevels)
	if ub.VsTurretDamage > 0 {
		dmgLvl := u.TrackLevels[TrackDamage]
		mul := 1 + float64(dmgLvl)*ub.UpgradeDamage
		if u.Type == Ram && u.TrackLevels[TrackSpecial] > 0 {
			mul *= 1.40
		} else if u.Type == Siege && u.TrackLevels[TrackSpecial] > 0 {
			mul *= 1.35
		}
		if s.OvertimeSteps > 0 {
			mul *= 1.0 + float64(s.OvertimeSteps)*s.cfg.Overtime.StructureBonus
		}
		dmg = ub.VsTurretDamage * mul
	}
	return dmg
}

func (s *Sim) enemyWallBlockX(side, lane int) (float64, bool) {
	enemy := 1 - side
	if validLane(lane) {
		if w := s.walls[enemy][lane]; w != nil && w.HP > 0 {
			return w.X, true
		}
	}
	return 0, false
}

func (s *Sim) enemyTowerShieldBlockX(side, lane int) (float64, bool) {
	enemy := 1 - side
	if validLane(lane) {
		if ts := s.towerShields[enemy][lane]; ts != nil && ts.HP > 0 {
			return ts.X, true
		}
	}
	return 0, false
}

func (s *Sim) enemyTurretBlockX(side, lane int) (float64, bool) {
	enemy := 1 - side
	for slot := 0; slot < TurretSlots; slot++ {
		if t := s.turrets[enemy][lane][slot]; t != nil && t.HP > 0 {
			return t.X, true
		}
	}
	return 0, false
}

func (s *Sim) findTarget(u *Unit) any {
	ub := s.cfg.Units[u.Type]

	// Rams prioritize enemy walls, tower shields & turrets when in range
	if u.Type == Ram {
		if w := s.nearestEnemyWall(u, ub.Range); w != nil {
			return w
		}
		if ts := s.nearestEnemyTowerShield(u, ub.Range); ts != nil {
			return ts
		}
		if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil {
			return tr
		}
		if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil {
			return eu
		}
		return nil
	}

	if u.Type == Siege {
		if w := s.nearestEnemyWall(u, ub.Range); w != nil && absDist(u.X, w.X) >= ub.MinRange {
			return w
		}
		if ts := s.nearestEnemyTowerShield(u, ub.Range); ts != nil && absDist(u.X, ts.X) >= ub.MinRange {
			return ts
		}
		if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil && absDist(u.X, tr.X) >= ub.MinRange {
			return tr
		}
		if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil && absDist(u.X, eu.X) >= ub.MinRange {
			return eu
		}
		return nil
	}

	// Standard troops: fight enemy units -> neutral golems -> enemy walls -> enemy tower shields -> enemy towers
	if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil {
		return eu
	}
	if an := s.nearestNeutralMonster(u, ub.Range); an != nil {
		return an
	}
	if w := s.nearestEnemyWall(u, ub.Range); w != nil {
		return w
	}
	if ts := s.nearestEnemyTowerShield(u, ub.Range); ts != nil {
		return ts
	}
	if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil {
		return tr
	}
	return nil
}

func (s *Sim) splashDamage(side, lane int, centerX, radius, dmg, shieldMult float64, hitFlying bool) {
	for _, u := range s.units {
		if u.Side == side || u.Lane != lane || u.HP <= 0 {
			continue
		}
		if s.cfg.Units[u.Type].Flying && !hitFlying {
			continue
		}
		if absDist(u.X, centerX) <= radius {
			s.dealDamage(side, u, dmg, shieldMult)
		}
	}
}

func (s *Sim) nearestEnemyUnit(u *Unit, maxRange float64) *Unit {
	ub := s.cfg.Units[u.Type]
	var best *Unit
	bestD := math.Inf(1)
	for _, e := range s.units {
		if e.Side == u.Side || e.Lane != u.Lane || e.HP <= 0 {
			continue
		}
		if s.cfg.Units[e.Type].Flying && !ub.CanHitFlying && !ub.Flying {
			continue
		}
		d := absDist(u.X, e.X)
		if d <= maxRange && d < bestD {
			best, bestD = e, d
		}
	}
	return best
}

func (s *Sim) nearestNeutralMonster(u *Unit, maxRange float64) *Anomaly {
	var best *Anomaly
	bestD := math.Inf(1)
	for _, an := range s.anomalies {
		if an.Kind != "neutral_golem" || an.Lane != u.Lane || an.HP <= 0 {
			continue
		}
		d := absDist(u.X, an.X)
		if d <= maxRange && d < bestD {
			best, bestD = an, d
		}
	}
	return best
}

func (s *Sim) nearestEnemyWall(u *Unit, maxRange float64) *Wall {
	if !validLane(u.Lane) {
		return nil
	}
	w := s.walls[1-u.Side][u.Lane]
	if w != nil && w.HP > 0 && absDist(u.X, w.X) <= maxRange {
		return w
	}
	return nil
}

func (s *Sim) nearestEnemyTowerShield(u *Unit, maxRange float64) *TowerShield {
	if !validLane(u.Lane) {
		return nil
	}
	ts := s.towerShields[1-u.Side][u.Lane]
	if ts != nil && ts.HP > 0 && absDist(u.X, ts.X) <= maxRange {
		return ts
	}
	return nil
}

func (s *Sim) nearestEnemyTurret(u *Unit, maxRange float64) *Turret {
	enemy := 1 - u.Side
	var best *Turret
	bestD := math.Inf(1)
	if u.Lane >= 0 && u.Lane < NumLanes {
		for slot := 0; slot < TurretSlots; slot++ {
			t := s.turrets[enemy][u.Lane][slot]
			if t == nil || t.HP <= 0 {
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
		if e.Side == u.Side || e.Lane != u.Lane || e.HP <= 0 || s.cfg.Units[e.Type].Flying {
			continue
		}
		if dd := absDist(u.X, e.X); dd < d {
			d, found = dd, true
		}
	}
	for slot := 0; slot < TurretSlots; slot++ {
		if t := s.turrets[1-u.Side][u.Lane][slot]; t != nil && t.HP > 0 {
			if dd := absDist(u.X, t.X); dd < d {
				d, found = dd, true
			}
		}
	}
	if w := s.walls[1-u.Side][u.Lane]; w != nil && w.HP > 0 {
		if dd := absDist(u.X, w.X); dd < d {
			d, found = dd, true
		}
	}
	return d, found
}

func (s *Sim) emitAttackFx(u *Unit, targetX float64) {
	kind := "melee"
	if u.Type == Bowman || u.Type == Mage || u.Type == Siege || u.Type == Dragon {
		kind = "shot"
	}
	s.fx = append(s.fx, FxEvent{Kind: kind, Unit: string(u.Type), Side: u.Side, Lane: u.Lane, FromX: u.X, ToX: targetX, Slot: -1})
}

func (s *Sim) stepTurrets(dt float64) {
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				t := s.turrets[side][lane][slot]
				if t == nil || t.HP <= 0 {
					continue
				}
				atkRate := 1.0 + float64(t.TrackLevels[TrackAttackSpeed])*0.20
				if t.TrackLevels[TrackSpecial] > 0 {
					atkRate += 0.25
				}
				t.Cooldown -= dt * atkRate

				canHitFlying := t.TrackLevels[TrackSpecial] > 0
				target := s.unitByID(t.TargetID)
				if target != nil && (target.Side == side || target.Lane != lane || absDist(target.X, t.X) > s.cfg.Turret.Range || target.HP <= 0 || (s.cfg.Units[target.Type].Flying && !canHitFlying)) {
					target = nil
					t.TargetID = 0
				}

				if target == nil {
					target = s.pickTurretTarget(t, side, lane, canHitFlying)
					if target != nil {
						t.TargetID = target.ID
					}
				}

				if target != nil && t.Cooldown <= 0 {
					dmg := t.Damage * s.TurretFactor
					if target.Type == Shieldbearer {
						dmg *= 0.65
					}
					s.dealDamage(side, target, dmg, 1.0)
					t.Cooldown = s.cfg.Turret.AttackInterval
					s.fx = append(s.fx, FxEvent{Kind: "shot", Unit: "turret", Side: side, Lane: lane, FromX: t.X, ToX: target.X, Slot: slot})
				}
			}
		}
	}
}

func (s *Sim) pickTurretTarget(t *Turret, side, lane int, canHitFlying bool) *Unit {
	var best *Unit
	for _, u := range s.units {
		if u.Side == side || u.Lane != lane || u.HP <= 0 {
			continue
		}
		if s.cfg.Units[u.Type].Flying && !canHitFlying {
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
			better = u.X < best.X
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

func (s *Sim) unitBounty(u *Unit) int {
	ub := s.cfg.Units[u.Type]
	trackSum := 0
	for _, lvl := range u.TrackLevels {
		trackSum += lvl
	}
	base := float64(ub.Cost)*0.35 + float64(trackSum)*4.0
	bounty := int(math.Round(base))
	if bounty < 5 {
		bounty = 5
	}
	return bounty
}

func (s *Sim) removeDead() {
	kept := s.units[:0]
	for _, u := range s.units {
		if u.HP > 0 {
			kept = append(kept, u)
			continue
		}
		s.players[u.Side].UnitsLost++
		delete(s.spotted[SideA], u.ID)
		delete(s.spotted[SideB], u.ID)
		if !s.firstBlood {
			s.firstBlood = true
			s.addEvent(SideNone, "firstBlood", "First blood in lane %d!", u.Lane+1)
		}
		if killer := u.LastHitBy; killer == SideA || killer == SideB {
			s.players[killer].Kills++
			if s.Time-s.lastKillAt[killer] <= 3.0 {
				s.streakCount[killer]++
			} else {
				s.streakCount[killer] = 1
			}
			s.lastKillAt[killer] = s.Time

			bounty := s.unitBounty(u)
			streakBonus := math.Min(0.60, float64(s.streakCount[killer]-1)*0.20)
			if streakBonus > 0 {
				bounty = int(math.Round(float64(bounty) * (1.0 + streakBonus)))
			}
			s.players[killer].Coins += bounty
			s.players[killer].BountyEarned += bounty
			s.fx = append(s.fx, FxEvent{Kind: "bounty", Unit: fmt.Sprintf("%d", bounty), Side: killer, Lane: u.Lane, FromX: u.X, ToX: u.X, Slot: -1})

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
					if killer := t.LastHitBy; killer == SideA || killer == SideB {
						s.players[killer].TurretsDown++
						bounty := s.cfg.Breach.TowerKillBounty
						if bounty > 0 {
							s.players[killer].Coins += bounty
							s.players[killer].BountyEarned += bounty
						}
					}
					s.turrets[side][lane][slot] = nil
					s.addEvent(side, "turretDestroyed", "%s tower in lane %d was destroyed! (+%dc)", sideName(side), lane+1, s.cfg.Breach.TowerKillBounty)
				}
			}
			if ts := s.towerShields[side][lane]; ts != nil && ts.HP <= 0 {
				if killer := ts.LastHitBy; killer == SideA || killer == SideB {
					s.players[killer].Coins += 15
					s.players[killer].BountyEarned += 15
				}
				s.towerShields[side][lane] = nil
				s.addEvent(side, "wallDestroyed", "%s Tower Shield in lane %d was shattered! (+15c)", sideName(side), lane+1)
			}
			if w := s.walls[side][lane]; w != nil && w.HP <= 0 {
				if killer := w.LastHitBy; killer == SideA || killer == SideB {
					s.players[killer].Coins += s.cfg.Breach.WallKillBounty
					s.players[killer].BountyEarned += s.cfg.Breach.WallKillBounty
				}
				s.walls[side][lane] = nil
				s.addEvent(side, "wallDestroyed", "%s Reinforcement Wall in lane %d was breached! (+%dc)", sideName(side), lane+1, s.cfg.Breach.WallKillBounty)
			}
		}
	}
}

func (s *Sim) checkEnd() {
	var breachLane = [2]int{-1, -1}
	remaining := s.units[:0]
	maxLoots := s.MaxBreachLoots
	if maxLoots <= 0 {
		maxLoots = 5
	}
	for _, u := range s.units {
		if u.HP <= 0 {
			continue
		}
		crossed := (u.Side == SideA && u.X >= FieldLength) || (u.Side == SideB && u.X <= 0)
		if !crossed {
			remaining = append(remaining, u)
			continue
		}
		attacker := u.Side
		defender := 1 - attacker
		breachLane[attacker] = u.Lane
		delete(s.spotted[SideA], u.ID)
		delete(s.spotted[SideB], u.ID)
		if validLane(u.Lane) {
			s.breachedLanes[attacker][u.Lane] = true
		}

		if s.WinMode != WinModeBreaches {
			s.players[defender].Lives--
			if s.players[defender].Lives > 0 && s.cfg.Breach.BreachComebackCoins > 0 {
				s.players[defender].Coins += s.cfg.Breach.BreachComebackCoins
			}
		}

		if bounty := s.cfg.Breach.BreachBounty; bounty > 0 && validLane(u.Lane) && s.laneBreachLoot[attacker][u.Lane] < maxLoots {
			s.laneBreachLoot[attacker][u.Lane]++
			s.players[attacker].Coins += bounty
			s.players[attacker].BountyEarned += bounty
			fxX := FieldLength
			if attacker == SideB {
				fxX = 0
			}
			s.fx = append(s.fx, FxEvent{Kind: "bounty", Unit: fmt.Sprintf("%d", bounty), Side: attacker, Lane: u.Lane, FromX: fxX, ToX: fxX, Slot: -1})
			if s.WinMode == WinModeBreaches || s.players[defender].Lives > 0 {
				s.addEvent(attacker, "bounty", "%s unit crossed the line in lane %d! (+%dc)", sideName(attacker), u.Lane+1, bounty)
			}
		}
	}
	s.units = remaining
	if s.players[SideA].Lives < 0 {
		s.players[SideA].Lives = 0
	}
	if s.players[SideB].Lives < 0 {
		s.players[SideB].Lives = 0
	}

	if s.WinMode == WinModeBreaches {
		target := s.BreachesToWin
		if target <= 0 {
			target = 3
		}
		breachesA := s.countBreaches(SideA)
		breachesB := s.countBreaches(SideB)
		switch {
		case breachesA >= target && breachesB >= target:
			s.Finished = true
			s.Draw = true
			s.Winner = NoSide
			s.addEvent(SideNone, "draw", "Both armies breached %d lanes on the same tick — draw!", target)
		case breachesA >= target:
			s.Finished = true
			s.Winner = SideA
			s.WinLane = breachLane[SideA]
			s.addEvent(SideNone, "breach", "Blue breached %d lanes!", target)
		case breachesB >= target:
			s.Finished = true
			s.Winner = SideB
			s.WinLane = breachLane[SideB]
			s.addEvent(SideNone, "breach", "Red breached %d lanes!", target)
		case s.Time >= s.cfg.Overtime.DrawAt:
			s.Finished = true
			s.Draw = true
			s.Winner = NoSide
			s.addEvent(SideNone, "draw", "Time limit reached — draw!")
		}
		return
	}

	switch {
	case s.players[SideA].Lives == 0 && s.players[SideB].Lives == 0:
		s.Finished = true
		s.Draw = true
		s.Winner = NoSide
		s.addEvent(SideNone, "draw", "Both armies breached on the same tick — draw!")
	case s.players[SideB].Lives == 0:
		s.Finished = true
		s.Winner = SideA
		s.WinLane = breachLane[SideA]
		s.addEvent(SideNone, "breach", "Blue breached in lane %d!", breachLane[SideA]+1)
	case s.players[SideA].Lives == 0:
		s.Finished = true
		s.Winner = SideB
		s.WinLane = breachLane[SideB]
		s.addEvent(SideNone, "breach", "Red breached in lane %d!", breachLane[SideB]+1)
	case s.Time >= s.cfg.Overtime.DrawAt:
		s.Finished = true
		s.Draw = true
		s.Winner = NoSide
		s.addEvent(SideNone, "draw", "Time limit reached — draw!")
	}
}
