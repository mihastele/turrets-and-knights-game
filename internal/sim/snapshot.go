package sim

// Snapshot is the state broadcast to clients. It contains everything both
// players are allowed to see (economy is fully public by design).
type Snapshot struct {
	Type          string            `json:"type"` // always "snapshot"
	Tick          int64             `json:"tick"`
	Time          float64           `json:"time"`
	Overtime      bool              `json:"overtime"`
	TurretFactor  float64           `json:"turretFactor"`
	Finished      bool              `json:"finished"`
	Draw          bool              `json:"draw"`
	Winner        int               `json:"winner"`  // NoSide when undecided or draw
	WinLane       int               `json:"winLane"` // -1 when undecided
	TimeLimit     float64           `json:"timeLimit"`
	OvertimeStart float64           `json:"overtimeStart"`
	Players       [2]PlayerSnapshot `json:"players"`
	Units         []UnitSnapshot    `json:"units"`
	Turrets       []TurretSnapshot  `json:"turrets"`
	Caches        []CacheSnapshot   `json:"caches"`
	Burns         []BurnSnapshot    `json:"burns"`
	Events        []Event           `json:"events"`
	Fx            []FxEvent         `json:"fx"`
}

type PlayerSnapshot struct {
	Coins        int                `json:"coins"`
	Income       int                `json:"income"` // per economy interval, factory included
	Factory      bool               `json:"factory"`
	FactoryLevel int                `json:"factoryLevel"`
	Lives        int                `json:"lives"`
	UnitUpgrades map[UnitType]int   `json:"unitUpgrades"`
	DamageDealt  int                `json:"damageDealt"`
	Kills        int                `json:"kills"`
	UnitsLost    int                `json:"unitsLost"`
	TurretsDown  int                `json:"turretsDown"`
	Abilities    map[string]float64 `json:"abilities"` // coupon field-effect id -> seconds until ready (0 = ready)
}

// CacheSnapshot describes a supply cache currently on the field.
type CacheSnapshot struct {
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Amount    int     `json:"amount"`
	ExpiresIn float64 `json:"expiresIn"` // seconds until it vanishes
}

// BurnSnapshot describes an active Dragon Fire field — a lane left ablaze.
type BurnSnapshot struct {
	Lane      int     `json:"lane"`
	Side      int     `json:"side"`      // who cast it
	Remaining float64 `json:"remaining"` // seconds left
}

type UnitSnapshot struct {
	ID    int64    `json:"id"`
	Type  UnitType `json:"type"`
	Side  int      `json:"side"`
	Lane  int      `json:"lane"`
	X     float64  `json:"x"`
	YOff  float64  `json:"yOff"`
	HP    float64  `json:"hp"`
	MaxHP float64  `json:"maxHp"`
}

type TurretSnapshot struct {
	Side   int     `json:"side"`
	Lane   int     `json:"lane"`
	Slot   int     `json:"slot"`
	Level  int     `json:"level"`
	HP     float64 `json:"hp"`
	MaxHP  float64 `json:"maxHp"`
	Damage float64 `json:"damage"` // includes upgrades, excludes overtime factor
}

// Snapshot builds the wire representation of the current state. Events are
// drained and included exactly once.
func (s *Sim) Snapshot() Snapshot {
	snap := Snapshot{
		Type:          "snapshot",
		Tick:          s.Tick,
		Time:          s.Time,
		Overtime:      s.Overtime,
		TurretFactor:  s.TurretFactor,
		Finished:      s.Finished,
		Draw:          s.Draw,
		Winner:        s.Winner,
		WinLane:       s.WinLane,
		TimeLimit:     s.cfg.Overtime.DrawAt,
		OvertimeStart: s.cfg.Overtime.Start,
		Events:        emptyIfNil(s.DrainEvents()),
		Fx:            emptyIfNil(s.DrainFx()),
	}
	for i := 0; i < 2; i++ {
		p := &s.players[i]
		snap.Players[i] = PlayerSnapshot{
			Coins:        p.Coins,
			Income:       p.IncomePerInterval(s.cfg),
			Factory:      p.Factory,
			FactoryLevel: p.FactoryLevel,
			Lives:        p.Lives,
			UnitUpgrades: copyUpgrades(p.UnitUpgrades),
			DamageDealt:  int(p.DamageDealt + 0.5),
			Kills:        p.Kills,
			UnitsLost:    p.UnitsLost,
			TurretsDown:  p.TurretsDown,
			Abilities:    abilityCooldowns(s.abilityReady[i], s.Time),
		}
	}
	snap.Units = make([]UnitSnapshot, 0, len(s.units))
	snap.Turrets = make([]TurretSnapshot, 0, 2*NumLanes*TurretSlots)
	snap.Caches = make([]CacheSnapshot, 0, len(s.caches))
	snap.Burns = make([]BurnSnapshot, 0, len(s.burns))
	for _, b := range s.burns {
		snap.Burns = append(snap.Burns, BurnSnapshot{
			Lane:      b.Lane,
			Side:      b.Side,
			Remaining: b.Until - s.Time,
		})
	}
	for _, c := range s.caches {
		snap.Caches = append(snap.Caches, CacheSnapshot{
			Lane:      c.Lane,
			X:         c.X,
			Amount:    c.Amount,
			ExpiresIn: c.ExpiresAt - s.Time,
		})
	}
	for _, u := range s.units {
		snap.Units = append(snap.Units, UnitSnapshot{
			ID: u.ID, Type: u.Type, Side: u.Side, Lane: u.Lane,
			X: u.X, YOff: u.YOff, HP: u.HP, MaxHP: u.MaxHP,
		})
	}
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				if t := s.turrets[side][lane][slot]; t != nil {
					snap.Turrets = append(snap.Turrets, TurretSnapshot{
						Side: t.Side, Lane: t.Lane, Slot: t.Slot, Level: t.Level,
						HP: t.HP, MaxHP: t.MaxHP, Damage: t.Damage,
					})
				}
			}
		}
	}
	return snap
}

func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// abilityCooldowns converts absolute ready-times into seconds remaining.
// Only effects that have been cast at least once appear; absent means ready.
func abilityCooldowns(readyAt map[string]float64, now float64) map[string]float64 {
	out := make(map[string]float64, len(readyAt))
	for id, t := range readyAt {
		rem := t - now
		if rem < 0 {
			rem = 0
		}
		out[id] = rem
	}
	return out
}

func copyUpgrades(in map[UnitType]int) map[UnitType]int {
	out := make(map[UnitType]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
