// Package sim contains the Lane Siege game simulation.
//
// The simulation is deliberately free of any network or browser code so it can
// be unit-tested headlessly. The server owns one *Sim per match and is the
// only authority for rules, money, damage and victory.
package sim

// TurretSlots is how many turrets each player may build per lane.
const TurretSlots = 2

// UnitType identifies one of the five deployable units.
type UnitType string

const (
	Soldier UnitType = "soldier"
	Bowman  UnitType = "bowman"
	Knight  UnitType = "knight"
	Ram     UnitType = "ram"
	Siege   UnitType = "siege"
)

// UnitTypes lists every unit in stable order (handy for UI and tests).
var UnitTypes = []UnitType{Soldier, Bowman, Knight, Ram, Siege}

const (
	// NumLanes is the number of lanes on the field.
	NumLanes = 5
	// FieldLength is the length of a lane in abstract units.
	FieldLength = 100.0

	SideA  = 0
	SideB  = 1
	NoSide = -1

	// TickRate is the fixed simulation frequency in Hz.
	TickRate = 20
	// TickDT is the time advanced per tick.
	TickDT = 1.0 / TickRate
	// SnapshotEvery is how many ticks pass between snapshots (2 => 10 Hz).
	SnapshotEvery = 2
)

// UnitBalance holds every tunable number for a unit type.
// Range/MinRange are measured in field units. AttackInterval is in seconds.
type UnitBalance struct {
	Name           string  `json:"name"`
	Role           string  `json:"role"`
	Cost           int     `json:"cost"`
	MaxHP          float64 `json:"maxHp"`
	Damage         float64 `json:"damage"`
	Range          float64 `json:"range"`
	MinRange       float64 `json:"minRange"`
	AttackInterval float64 `json:"attackInterval"`
	Speed          float64 `json:"speed"`
	UpgradeCosts   [2]int  `json:"upgradeCosts"`
	UpgradeHP      float64 `json:"upgradeHp"`      // fractional max-HP gain per level
	UpgradeDamage  float64 `json:"upgradeDamage"`  // fractional damage gain per level
	VsTurretDamage float64 `json:"vsTurretDamage"` // damage vs turrets per hit; 0 = cannot target turrets
}

// TurretBalance holds every tunable number for turrets.
type TurretBalance struct {
	Name                  string  `json:"name"`
	Cost                  int     `json:"cost"`
	MaxHP                 float64 `json:"maxHp"`
	Damage                float64 `json:"damage"`
	Range                 float64 `json:"range"`
	AttackInterval        float64 `json:"attackInterval"`
	PadX                  float64 `json:"padX"` // distance of each pad from its owner's edge
	Slots                 int     `json:"slots"`
	UpgradeCosts          [2]int  `json:"upgradeCosts"`
	UpgradeHPPerLevel     float64 `json:"upgradeHpPerLevel"`     // added to max HP, never heals
	UpgradeDamagePerLevel float64 `json:"upgradeDamagePerLevel"` // fractional damage gain per level
}

// CacheBalance configures the mid-field supply caches that drop on a timer.
type CacheBalance struct {
	Times    []float64 `json:"times"`    // seconds into the match when a cache drops
	Amount   int       `json:"amount"`   // coins awarded to the collector
	Lifetime float64   `json:"lifetime"` // seconds a cache stays on the field before fading
	X        float64   `json:"x"`        // drop position (center line)
	Radius   float64   `json:"radius"`   // pickup radius
}

// EconomyBalance holds income-related knobs.
type EconomyBalance struct {
	BaseIncome     int     `json:"baseIncome"`  // coins per interval for both players
	Interval       float64 `json:"interval"`    // seconds between base income grants
	WindfallMin    int     `json:"windfallMin"` // shared random payout bounds (inclusive)
	WindfallMax    int     `json:"windfallMax"`
	WindfallGapMin float64 `json:"windfallGapMin"` // seconds between windfalls (rolled per event)
	WindfallGapMax float64 `json:"windfallGapMax"`
	FactoryCost    int     `json:"factoryCost"`
	FactoryIncome  int     `json:"factoryIncome"`  // extra coins per interval, factory owner only
	FactoryPayback float64 `json:"factoryPayback"` // informational, seconds to break even
}

// OvertimeBalance holds the sudden-death knobs.
type OvertimeBalance struct {
	Start    float64 `json:"start"`    // seconds; turret construction/upgrades disabled
	Interval float64 `json:"interval"` // seconds between damage decay steps
	Decay    float64 `json:"decay"`    // fraction of original damage removed per step
	DrawAt   float64 `json:"drawAt"`   // seconds; hard time limit, match is a draw
}

// Balance is the single server-side source of truth for tuning.
type Balance struct {
	StartCoins int                      `json:"startCoins"`
	SpawnInset float64                  `json:"spawnInset"` // distance from own edge where units spawn
	Units      map[UnitType]UnitBalance `json:"units"`
	Turret     TurretBalance            `json:"turret"`
	Economy    EconomyBalance           `json:"economy"`
	Overtime   OvertimeBalance          `json:"overtime"`
	Cache      CacheBalance             `json:"cache"`
}

// DefaultBalance returns the launch tuning. Tune the game here; everything
// else in the codebase reads from this table.
//
// Damage-vs-turret rules: Ram uses VsTurretDamage (its troops damage is a
// limp 4), Siege damages turrets with its normal Damage. Soldier, Bowman and
// Knight have VsTurretDamage == 0 and cannot hurt turrets at all — turrets
// don't block movement, they just shoot. The Ram's anti-turret attack reuses
// AttackInterval.
//
// Targeting is split troops vs structures: Soldier, Bowman and Knight attack
// enemy units only; Ram and Siege attack turrets only and cannot damage
// enemy units.
//
// Reference matchups (roughly equal coin investments):
//   - 1 Knight (45) vs 2 Soldiers (40): soldiers win the straight fight,
//     but the knight arrives much faster and is stronger vs single targets.
//   - Lone Soldier (20) vs Turret (70): turret wins if soldier is alone;
//     an escorted Ram (55 + escort) grinds the turret down.
//   - Siege (75) outranges the Turret (13) and forces a response, but
//     dies to anything that reaches it unsupported.
//   - Each lane holds two turret slots per side, so a lane can be locked
//     down hard — turrets are priced accordingly.
func DefaultBalance() Balance {
	return Balance{
		StartCoins: 100,
		SpawnInset: 2,
		Units: map[UnitType]UnitBalance{
			Soldier: {Name: "Soldier", Role: "Cheap frontline. Contests a lane, protects support.", Cost: 20, MaxHP: 70, Damage: 9, Range: 2.4, AttackInterval: 1.0, Speed: 5.0, UpgradeCosts: [2]int{15, 25}, UpgradeHP: 0.15, UpgradeDamage: 0.15},
			Bowman:  {Name: "Bowman", Role: "Ranged support. Strong behind a frontline; fragile up close.", Cost: 30, MaxHP: 45, Damage: 12, Range: 16, AttackInterval: 1.2, Speed: 4.6, UpgradeCosts: [2]int{20, 35}, UpgradeHP: 0.15, UpgradeDamage: 0.15},
			Knight:  {Name: "Knight", Role: "Fast attacker. Punishes open lanes and lone bowmen.", Cost: 45, MaxHP: 95, Damage: 15, Range: 2.4, AttackInterval: 0.9, Speed: 8.5, UpgradeCosts: [2]int{25, 45}, UpgradeHP: 0.15, UpgradeDamage: 0.15},
			Ram:     {Name: "Ram", Role: "Building breaker. Smashes turrets, cannot attack troops. Needs an escort.", Cost: 55, MaxHP: 170, Damage: 4, Range: 2.6, AttackInterval: 1.2, Speed: 3.1, UpgradeCosts: [2]int{30, 50}, UpgradeHP: 0.20, UpgradeDamage: 0.15, VsTurretDamage: 38},
			Siege:   {Name: "Siege Engine", Role: "Long-range pressure. Outranges turrets; targets structures only. Cannot fire close up.", Cost: 75, MaxHP: 85, Damage: 30, Range: 30, MinRange: 14, AttackInterval: 2.4, Speed: 2.3, UpgradeCosts: [2]int{40, 70}, UpgradeHP: 0.15, UpgradeDamage: 0.15, VsTurretDamage: 30},
		},
		Turret: TurretBalance{
			Name: "Turret", Cost: 70, MaxHP: 130, Damage: 14, Range: 13, AttackInterval: 1.1,
			PadX:         10,
			Slots:        TurretSlots,
			UpgradeCosts: [2]int{25, 45}, UpgradeHPPerLevel: 0.25, UpgradeDamagePerLevel: 0.25,
		},
		Economy: EconomyBalance{
			BaseIncome: 10, Interval: 5,
			WindfallMin: 5, WindfallMax: 10, WindfallGapMin: 12, WindfallGapMax: 20,
			FactoryCost: 55, FactoryIncome: 5, FactoryPayback: 55,
		},
		Overtime: OvertimeBalance{
			Start: 360, Interval: 30, Decay: 0.15, DrawAt: 600,
		},
		Cache: CacheBalance{
			Times:    []float64{45, 150, 260},
			Amount:   30,
			Lifetime: 25,
			X:        FieldLength / 2, // center of the field
			Radius:   3.5,
		},
	}
}
