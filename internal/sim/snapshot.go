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
	Events        []Event           `json:"events"`
	Fx            []FxEvent         `json:"fx"`
}

type PlayerSnapshot struct {
	Coins        int              `json:"coins"`
	Income       int              `json:"income"` // per economy interval, factory included
	Factory      bool             `json:"factory"`
	UnitUpgrades map[UnitType]int `json:"unitUpgrades"`
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
			UnitUpgrades: copyUpgrades(p.UnitUpgrades),
		}
	}
	snap.Units = make([]UnitSnapshot, 0, len(s.units))
	snap.Turrets = make([]TurretSnapshot, 0, 4)
	for _, u := range s.units {
		snap.Units = append(snap.Units, UnitSnapshot{
			ID: u.ID, Type: u.Type, Side: u.Side, Lane: u.Lane,
			X: u.X, YOff: u.YOff, HP: u.HP, MaxHP: u.MaxHP,
		})
	}
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			if t := s.turrets[side][lane]; t != nil {
				snap.Turrets = append(snap.Turrets, TurretSnapshot{
					Side: t.Side, Lane: t.Lane, Level: t.Level,
					HP: t.HP, MaxHP: t.MaxHP, Damage: t.Damage,
				})
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

func copyUpgrades(in map[UnitType]int) map[UnitType]int {
	out := make(map[UnitType]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
