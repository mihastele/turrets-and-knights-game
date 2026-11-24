package sim

import (
	"errors"
	"math"
	"testing"
)

func newTestSim(t *testing.T) *Sim {
	t.Helper()
	return New(42, DefaultBalance())
}

func giveCoins(s *Sim, side, amount int) {
	s.players[side].Coins = amount
}

func coinBalance(s *Sim, side int) int { return s.players[side].Coins }

func clearField(s *Sim) {
	s.units = nil
	s.anomalies = nil
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			s.towerShields[side][lane] = nil
			s.walls[side][lane] = nil
			for slot := 0; slot < TurretSlots; slot++ {
				s.turrets[side][lane][slot] = nil
			}
		}
	}
}

func step(s *Sim, seconds float64) {
	ticks := int(math.Round(seconds / TickDT))
	for i := 0; i < ticks; i++ {
		s.Step(TickDT)
	}
}

func drainedEvents(s *Sim) []Event { return s.DrainEvents() }

func TestStartStateEmptyField(t *testing.T) {
	s := newTestSim(t)
	if len(s.units) != 0 || len(s.anomalies) != 0 {
		t.Fatalf("field should start empty, got %d units %d anomalies", len(s.units), len(s.anomalies))
	}
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				if s.turrets[side][lane][slot] != nil {
					t.Fatalf("side %d lane %d slot %d should start empty", side, lane, slot)
				}
			}
			if s.walls[side][lane] != nil {
				t.Fatalf("side %d lane %d wall should start empty", side, lane)
			}
		}
	}
	if got := coinBalance(s, SideA); got != s.cfg.StartCoins {
		t.Fatalf("side A starts with %d coins, want %d", got, s.cfg.StartCoins)
	}
	if s.players[SideA].Lives != 1 || s.WinMode != WinModeLives {
		t.Fatalf("expected lives mode with 1 life, got %s lives=%d", s.WinMode, s.players[SideA].Lives)
	}
}

func TestDeploySpendsCoinsAndFightsImmediately(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if want := s.cfg.StartCoins - s.cfg.Units[Soldier].Cost; coinBalance(s, SideA) != want {
		t.Fatalf("coins = %d, want %d", coinBalance(s, SideA), want)
	}
	if len(s.units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(s.units))
	}
	x0 := s.units[0].X
	step(s, 1.0)
	if s.units[0].X <= x0 {
		t.Fatal("deployed unit should march immediately in real time")
	}
}

func TestDeployRejectsBadInput(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: -1, Unit: Soldier}); !errors.Is(err, ErrInvalidLane) {
		t.Fatalf("bad lane: want ErrInvalidLane, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: "peasant"}); !errors.Is(err, ErrUnknownUnit) {
		t.Fatalf("bad unit: want ErrUnknownUnit, got %v", err)
	}
	giveCoins(s, SideA, 5)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); !errors.Is(err, ErrNotEnoughCoins) {
		t.Fatalf("poor: want ErrNotEnoughCoins, got %v", err)
	}
	giveCoins(s, SideA, 1000)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Dragon}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("dragon without coupon: want ErrCouponRequired, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Valkyrie}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("valkyrie without coupon: want ErrCouponRequired, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Dragon, Entitled: true}); err != nil {
		t.Fatalf("dragon with coupon: %v", err)
	}
}

func TestIncomeFlowsOnInterval(t *testing.T) {
	s := newTestSim(t)
	step(s, s.cfg.Economy.Interval+0.1)
	// No windfall can fire before WindfallGapMin (12s), so this is pure income.
	want := s.cfg.StartCoins + s.cfg.Economy.BaseIncome
	if coinBalance(s, SideA) != want || coinBalance(s, SideB) != want {
		t.Fatalf("coins = %d/%d, want %d each", coinBalance(s, SideA), coinBalance(s, SideB), want)
	}
}

func TestSharedSeededWindfall(t *testing.T) {
	a, b := New(7, DefaultBalance()), New(7, DefaultBalance())
	step(a, 25)
	step(b, 25)
	if coinBalance(a, SideA) != coinBalance(b, SideA) {
		t.Fatalf("same-seed sims diverged: %d vs %d", coinBalance(a, SideA), coinBalance(b, SideA))
	}
	found := false
	for _, ev := range drainedEvents(a) {
		if ev.Kind == "windfall" {
			found = true
			if ev.Amount < a.cfg.Economy.WindfallMin || ev.Amount > a.cfg.Economy.WindfallMax {
				t.Fatalf("windfall amount %d out of bounds", ev.Amount)
			}
		}
	}
	if !found {
		t.Fatal("expected at least one windfall in 25s")
	}
}

func TestFactoryLimitLevelsAndIncome(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionBuildFactory, Side: SideA}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildFactory, Side: SideA}); !errors.Is(err, ErrFactoryBuilt) {
		t.Fatalf("second factory: want ErrFactoryBuilt, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeFactory, Side: SideA}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeFactory, Side: SideA}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeFactory, Side: SideA}); !errors.Is(err, ErrMaxLevel) {
		t.Fatalf("third factory upgrade: want ErrMaxLevel, got %v", err)
	}
	want := s.cfg.Economy.BaseIncome + s.cfg.Economy.FactoryIncome*3
	if got := s.players[SideA].IncomePerInterval(s.cfg); got != want {
		t.Fatalf("income = %d, want %d", got, want)
	}
}

func TestFiveTrackUpgradesSpecialCouponLockAndFutureOnly(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackAttackSpeed}); err != nil {
		t.Fatalf("upgrade attackSpeed: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackShield}); err != nil {
		t.Fatalf("upgrade shield: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackSpecial}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("special track without coupon: want ErrCouponRequired, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackSpecial, Entitled: true}); err != nil {
		t.Fatalf("special track with coupon: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	u := s.units[0]
	if u.Shield <= 0 {
		t.Fatalf("expected upgraded soldier to spawn with shield, got %v", u.Shield)
	}

	// Future spawns only: buy damage, then compare an old and a new unit.
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 1, Unit: Bowman}); err != nil {
		t.Fatal(err)
	}
	oldBowman := s.units[len(s.units)-1]
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Bowman, Track: TrackDamage}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 1, Unit: Bowman}); err != nil {
		t.Fatal(err)
	}
	newBowman := s.units[len(s.units)-1]
	if oldBowman.Damage != s.cfg.Units[Bowman].Damage {
		t.Fatalf("existing unit changed after upgrade: %v", oldBowman.Damage)
	}
	want := s.cfg.Units[Bowman].Damage * (1 + s.cfg.Units[Bowman].UpgradeDamage)
	if math.Abs(newBowman.Damage-want) > 1e-9 {
		t.Fatalf("new spawn damage = %v, want %v", newBowman.Damage, want)
	}
}

func TestCounterDamageAndKillBounty(t *testing.T) {
	s := newTestSim(t)
	s.cfg.Fog.ReconBounty = 0 // isolate the kill bounty
	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 50, HP: 75, MaxHP: 75, Damage: 10}
	kni := &Unit{ID: 2, Type: Knight, Side: SideB, Lane: 0, X: 51, HP: 10, MaxHP: 100, Damage: 16}
	s.units = []*Unit{sol, kni}
	s.nextUnitID = 3

	coinsBefore := coinBalance(s, SideA)
	step(s, 0.2) // Soldier hits Knight for 10*1.45 = 14.5 dmg, killing it
	if kni.HP > 0 {
		t.Fatalf("expected soldier to kill 10-HP knight, remaining hp=%v", kni.HP)
	}
	gained := coinBalance(s, SideA) - coinsBefore
	wantBounty := int(math.Round(float64(s.cfg.Units[Knight].Cost) * 0.35))
	if gained != wantBounty {
		t.Fatalf("kill bounty = %d, want %d", gained, wantBounty)
	}
}

func TestSoldiersBeatKnightForEqualCoin(t *testing.T) {
	// The counter triangle in one fight: 2x Soldier (40c) must beat 1x Knight (45c).
	s := newTestSim(t)
	s.anomalyIn = 9999
	s.units = []*Unit{
		{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 46, HP: 75, MaxHP: 75, Damage: 10},
		{ID: 2, Type: Soldier, Side: SideA, Lane: 0, X: 47, HP: 75, MaxHP: 75, Damage: 10},
		{ID: 3, Type: Knight, Side: SideB, Lane: 0, X: 53, HP: 100, MaxHP: 100, Damage: 16},
	}
	s.nextUnitID = 4
	for i := 0; i < 200 && len(s.units) == 3; i++ {
		s.Step(TickDT)
	}
	knightAlive, soldiersAlive := false, 0
	for _, u := range s.units {
		if u.Type == Knight {
			knightAlive = true
		} else {
			soldiersAlive++
		}
	}
	if knightAlive {
		t.Fatal("expected the knight to die to 2 soldiers")
	}
	if soldiersAlive == 0 {
		t.Fatal("expected at least one soldier to survive")
	}
}

func TestBowmanShredsFlyers(t *testing.T) {
	s := newTestSim(t)
	bow := &Unit{ID: 1, Type: Bowman, Side: SideA, Lane: 0, X: 40, HP: 48, MaxHP: 48, Damage: 13}
	drg := &Unit{ID: 2, Type: Dragon, Side: SideB, Lane: 0, X: 50, HP: 240, MaxHP: 240, Shield: 50, MaxShield: 50, Damage: 26}
	s.units = []*Unit{bow, drg}
	s.nextUnitID = 3
	step(s, 0.2) // one bowman volley, no time for a second
	wantShield := 50 - 13*1.65
	if math.Abs(drg.Shield-wantShield) > 1e-6 {
		t.Fatalf("dragon shield = %v, want %v (13 * 1.65 anti-air)", drg.Shield, wantShield)
	}
	if drg.HP != 240 {
		t.Fatalf("shield should absorb first, hp = %v", drg.HP)
	}
}

func TestRamBreaksTowers(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideB, 500)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideB, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	tr := s.turrets[SideB][0][0]
	ram := &Unit{ID: 1, Type: Ram, Side: SideA, Lane: 0, X: tr.X - 1.5, HP: 180, MaxHP: 180, Shield: 30, MaxShield: 30, Damage: 9}
	s.units = []*Unit{ram}
	s.nextUnitID = 2
	before := tr.HP + tr.Shield
	step(s, 0.2)
	if tr.HP+tr.Shield >= before {
		t.Fatal("ram should smash the tower")
	}
	if ram.X > tr.X {
		t.Fatalf("ram walked past alive tower: ram=%v tower=%v", ram.X, tr.X)
	}
}

func TestShieldAbsorbsBeforeHPAndMageShredsShields(t *testing.T) {
	s := newTestSim(t)
	u := &Unit{ID: 1, Type: Shieldbearer, Side: SideA, Lane: 0, X: 10, HP: 130, MaxHP: 130, Shield: 45, MaxShield: 45, Damage: 11}
	s.dealDamage(SideB, u, 20, 1.0)
	if u.Shield != 25 || u.HP != 130 {
		t.Fatalf("shield should absorb first: shield=%v hp=%v", u.Shield, u.HP)
	}
	s.dealDamage(SideB, u, 20, 2.0) // mage-like 2x vs shields
	if u.Shield != 0 || u.HP >= 130 {
		t.Fatalf("2x shield damage should break through: shield=%v hp=%v", u.Shield, u.HP)
	}
}

func TestTowerSlotsUpgradesAndNoHeal(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0}); !errors.Is(err, ErrPadOccupied) {
		t.Fatalf("third tower in lane: want ErrPadOccupied, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 1}); !errors.Is(err, ErrNoTurret) {
		t.Fatalf("upgrade empty lane: want ErrNoTurret, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0, Slot: 5}); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("bad slot: want ErrInvalidSlot, got %v", err)
	}
	tr := s.turrets[SideA][0][1]
	tr.HP -= 20
	hpBefore := tr.HP
	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0, Slot: 1, Track: TrackHP}); err != nil {
		t.Fatal(err)
	}
	if tr.HP != hpBefore {
		t.Fatalf("turret upgrades must never heal: %v -> %v", hpBefore, tr.HP)
	}
	if tr.MaxHP <= s.cfg.Turret.MaxHP {
		t.Fatal("turret HP track should raise max HP")
	}
	if s.turrets[SideA][0][0].MaxHP != s.cfg.Turret.MaxHP {
		t.Fatal("slot-targeted upgrade leaked into the other slot")
	}
}

func TestTurretFocusesNearestAndFlakHitsFlyers(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	sol := &Unit{ID: 1, Type: Soldier, Side: SideB, Lane: 0, X: 20, HP: 75, MaxHP: 75, Damage: 10}
	kni := &Unit{ID: 2, Type: Knight, Side: SideB, Lane: 0, X: 22, HP: 100, MaxHP: 100, Damage: 16}
	drg := &Unit{ID: 3, Type: Dragon, Side: SideB, Lane: 0, X: 21, HP: 240, MaxHP: 240, Damage: 26}
	s.units = []*Unit{sol, kni, drg}
	s.nextUnitID = 4
	step(s, 0.3) // one tower volley
	if sol.HP >= 75 {
		t.Fatalf("tower should focus the nearest ground unit, soldier hp=%v", sol.HP)
	}
	if kni.HP != 100 || drg.HP != 240 || drg.Shield != 0 {
		t.Fatalf("tower should ignore others without flak: knight=%v dragon=%v/%v", kni.HP, drg.HP, drg.Shield)
	}

	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0, Slot: 0, Track: TrackSpecial}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("flak without coupon: want ErrCouponRequired, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0, Slot: 0, Track: TrackSpecial, Entitled: true}); err != nil {
		t.Fatalf("flak with coupon: %v", err)
	}
	sol.HP, kni.HP = 10000, 10000 // keep ground alive; dragon is the only target in range
	drg.X = 15
	sol.X, kni.X = 30, 32
	shieldBefore := drg.Shield
	step(s, 1.3)
	if drg.Shield >= shieldBefore && drg.HP >= 240 {
		t.Fatal("flak tower should hit flying units")
	}
}

func TestSiegeHoldsInsideMinRange(t *testing.T) {
	s := newTestSim(t)
	sg := &Unit{ID: 1, Type: Siege, Side: SideA, Lane: 0, X: 50, HP: 85, MaxHP: 85, Damage: 28}
	foe := &Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 0, X: 55, HP: 75, MaxHP: 75, Damage: 10}
	s.units = []*Unit{sg, foe}
	s.nextUnitID = 3
	step(s, 1.0)
	if sg.X != 50 {
		t.Fatalf("siege should hold position inside min range, x=%v", sg.X)
	}
	if foe.HP != 75 {
		t.Fatalf("siege should not fire inside min range, foe hp=%v", foe.HP)
	}
}

func TestKnightChargeBonusAfterMarching(t *testing.T) {
	s := newTestSim(t)
	s.cfg.Fog.ReconBounty = 0
	kni := &Unit{ID: 1, Type: Knight, Side: SideA, Lane: 0, X: 40, HP: 100, MaxHP: 100, Damage: 16, DistanceMoved: 12, ChargeReady: true}
	bow := &Unit{ID: 2, Type: Bowman, Side: SideB, Lane: 0, X: 42, HP: 48, MaxHP: 48, Damage: 13}
	s.units = []*Unit{kni, bow}
	s.nextUnitID = 3
	step(s, 0.2)
	// 16 * 1.5 (vs ranged) * 1.6 (charge) = 38.4
	want := 48 - 16*1.5*1.6
	if math.Abs(bow.HP-want) > 1e-6 {
		t.Fatalf("charged hit left bowman at %v hp, want %v", bow.HP, want)
	}
	if kni.ChargeReady {
		t.Fatal("charge should be spent after the first strike")
	}
}

func TestReinforcementWallBuildUpgradeBlockThornsAndRebuild(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideB, 500)

	if err := s.ApplyAction(Action{Kind: ActionBuildWall, Side: SideB, Lane: 0}); err != nil {
		t.Fatalf("build wall: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildWall, Side: SideB, Lane: 0}); !errors.Is(err, ErrWallExists) {
		t.Fatalf("second wall: want ErrWallExists, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeWall, Side: SideB, Lane: 1}); !errors.Is(err, ErrNoWall) {
		t.Fatalf("upgrade missing wall: want ErrNoWall, got %v", err)
	}
	w := s.walls[SideB][0]
	if w == nil || w.HP != s.cfg.Wall.MaxHP || w.Shield != s.cfg.Wall.BaseShield {
		t.Fatalf("unexpected wall state: %+v", w)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeWall, Side: SideB, Lane: 0}); err != nil {
		t.Fatalf("upgrade wall: %v", err)
	}
	if w.Level != 1 || w.MaxHP <= s.cfg.Wall.MaxHP || w.MaxShield <= s.cfg.Wall.BaseShield {
		t.Fatalf("wall upgrade did not boost stats: %+v", w)
	}

	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: w.X - 1.0, HP: 75, MaxHP: 75, Damage: 10}
	s.units = []*Unit{sol}
	s.nextUnitID = 2
	hpBefore := w.HP + w.Shield
	solHPBefore := sol.HP
	step(s, 1.5)
	if w.HP+w.Shield >= hpBefore {
		t.Fatal("soldier should damage enemy wall")
	}
	if sol.HP >= solHPBefore {
		t.Fatal("upgraded wall thorns should damage melee attacker")
	}
	if sol.X > w.X {
		t.Fatalf("ground soldier walked past alive enemy wall: sol.X=%v w.X=%v", sol.X, w.X)
	}

	// Destroyed walls clear immediately and can be rebuilt right away.
	w.Shield = 0
	w.HP = 1
	step(s, 1.5)
	if s.walls[SideB][0] != nil {
		t.Fatal("destroyed wall should be cleared")
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildWall, Side: SideB, Lane: 0}); err != nil {
		t.Fatalf("rebuild wall after destruction: %v", err)
	}
	if s.walls[SideB][0] == nil {
		t.Fatal("expected rebuilt wall in Lane 0")
	}
}

func TestNeutralGolemFightsAndAwardsBounty(t *testing.T) {
	s := newTestSim(t)
	s.anomalies = []*Anomaly{{
		ID: 500, Kind: "neutral_golem", Lane: 1, X: 50, HP: 15, MaxHP: 160,
		Damage: 15, Value: 50, LastHitBy: -1, ExpiresAt: 10000,
	}}
	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 1, X: 49, HP: 75, MaxHP: 75, Damage: 20}
	s.units = []*Unit{sol}
	s.nextUnitID = 2

	coinsBefore := coinBalance(s, SideA)
	step(s, 1.0)
	if len(s.anomalies) != 0 {
		t.Fatal("expected neutral golem to be slain and removed")
	}
	if got := coinBalance(s, SideA) - coinsBefore; got != 50 {
		t.Fatalf("golem bounty = %d, want 50", got)
	}
}

func TestBreachTakesLifeAndPaysBounty(t *testing.T) {
	s := newTestSim(t)
	s.SetLives(2)
	s.cfg.Fog.ReconBounty = 0
	s.units = []*Unit{{
		ID: 1, Type: Knight, Side: SideA, Lane: 2, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16,
	}}
	s.nextUnitID = 2
	coinsBefore := coinBalance(s, SideA)
	step(s, 0.5)
	if s.Finished {
		t.Fatal("match should continue with a life remaining")
	}
	if s.players[SideB].Lives != 1 {
		t.Fatalf("defender lives = %d, want 1", s.players[SideB].Lives)
	}
	if got := coinBalance(s, SideA) - coinsBefore; got != s.cfg.Breach.BreachBounty {
		t.Fatalf("breach bounty = %d, want %d", got, s.cfg.Breach.BreachBounty)
	}
	if s.players[SideB].Lives == 1 && coinBalance(s, SideB) != s.cfg.StartCoins+s.cfg.Breach.BreachComebackCoins {
		t.Fatalf("defender should get comeback coins, has %d", coinBalance(s, SideB))
	}
}

func TestBreachEndsMatchAtZeroLives(t *testing.T) {
	s := newTestSim(t)
	s.units = []*Unit{{
		ID: 1, Type: Knight, Side: SideA, Lane: 2, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16,
	}}
	step(s, 0.5)
	if !s.Finished || s.Winner != SideA || s.WinLane != 2 {
		t.Fatalf("expected SideA breach in lane 2, got finished=%v winner=%d lane=%d", s.Finished, s.Winner, s.WinLane)
	}
}

func TestSimultaneousBreachDraw(t *testing.T) {
	s := newTestSim(t)
	s.units = []*Unit{
		{ID: 1, Type: Knight, Side: SideA, Lane: 0, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16},
		{ID: 2, Type: Knight, Side: SideB, Lane: 1, X: 0.5, HP: 100, MaxHP: 100, Damage: 16},
	}
	step(s, 0.5)
	if !s.Finished || !s.Draw || s.Winner != NoSide {
		t.Fatalf("same-tick breach should draw, got finished=%v draw=%v winner=%d", s.Finished, s.Draw, s.Winner)
	}
}

func TestResignEndsMatch(t *testing.T) {
	s := newTestSim(t)
	s.Resign(SideB)
	if !s.Finished || s.Winner != SideA || s.Draw {
		t.Fatalf("resign failed: finished=%v winner=%d draw=%v", s.Finished, s.Winner, s.Draw)
	}
}

func TestBreachesWinModeAccumulatesDistinctLanes(t *testing.T) {
	s := newTestSim(t)
	s.SetWinMode(WinModeBreaches, 3)

	// Two distinct lanes (even with two units in lane 0): no win, no life loss.
	s.units = []*Unit{
		{ID: 1, Type: Knight, Side: SideA, Lane: 0, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16},
		{ID: 2, Type: Knight, Side: SideA, Lane: 0, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16},
		{ID: 3, Type: Knight, Side: SideA, Lane: 1, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16},
	}
	s.nextUnitID = 4
	step(s, 0.5)
	if s.Finished {
		t.Fatal("2 of 3 lanes should not win")
	}
	if s.countBreaches(SideA) != 2 {
		t.Fatalf("breaches = %d, want 2", s.countBreaches(SideA))
	}
	if s.players[SideB].Lives != 1 {
		t.Fatalf("breaches mode should not touch lives, got %d", s.players[SideB].Lives)
	}

	// Progress persists: a third lane any time later wins the match.
	step(s, 5)
	s.units = []*Unit{
		{ID: 10, Type: Knight, Side: SideA, Lane: 4, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16},
	}
	s.nextUnitID = 11
	step(s, 0.5)
	if !s.Finished || s.Winner != SideA || s.Draw {
		t.Fatalf("expected SideA victory on 3rd distinct lane, got finished=%v winner=%d draw=%v", s.Finished, s.Winner, s.Draw)
	}
	snap := s.Snapshot()
	if snap.Players[SideA].Breaches != 3 || !snap.Players[SideA].BreachedLanes[4] {
		t.Fatalf("breach snapshot wrong: %+v", snap.Players[SideA])
	}
}

func TestMaxBreachLootsPerLanePerMatch(t *testing.T) {
	s := newTestSim(t)
	s.SetWinMode(WinModeBreaches, 5)
	s.SetMaxBreachLoots(3)
	s.cfg.Fog.ReconBounty = 0

	coinsBefore := coinBalance(s, SideA)
	for i := 0; i < 6; i++ {
		s.units = append(s.units, &Unit{
			ID: int64(i + 1), Type: Knight, Side: SideA, Lane: 0, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16,
		})
	}
	for i := 0; i < 2; i++ {
		s.units = append(s.units, &Unit{
			ID: int64(10 + i), Type: Knight, Side: SideA, Lane: 1, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16,
		})
	}
	s.nextUnitID = 20
	step(s, 0.5)

	// Lane 0 capped at 3 loots, lane 1 pays 2: total 5x bounty.
	want := 5 * s.cfg.Breach.BreachBounty
	if got := coinBalance(s, SideA) - coinsBefore; got != want {
		t.Fatalf("breach loot = %d, want %d", got, want)
	}
	if s.Finished {
		t.Fatal("only 2 of 5 lanes breached: match must continue")
	}

	// The cap persists for the whole match (no per-round reset).
	step(s, 5)
	coinsLater := coinBalance(s, SideA)
	s.units = []*Unit{
		{ID: 30, Type: Knight, Side: SideA, Lane: 0, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16},
	}
	s.anomalyIn, s.windfallIn, s.incomeIn = 9999, 9999, 9999 // isolate the loot
	step(s, 0.5)
	if got := coinBalance(s, SideA) - coinsLater; got != 0 {
		t.Fatalf("capped lane paid again: +%d", got)
	}
}

func TestOvertimeLocksBuildsDecaysTowersAndBoostsBreachers(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	s.Time = s.cfg.Overtime.Start
	step(s, 0.2)
	if !s.Overtime {
		t.Fatal("expected overtime to begin")
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 1}); !errors.Is(err, ErrOvertime) {
		t.Fatalf("overtime build: want ErrOvertime, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0, Slot: 0, Track: TrackDamage}); !errors.Is(err, ErrOvertime) {
		t.Fatalf("overtime upgrade: want ErrOvertime, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildWall, Side: SideA, Lane: 1}); !errors.Is(err, ErrOvertime) {
		t.Fatalf("overtime wall: want ErrOvertime, got %v", err)
	}
	// Troops and unit upgrades keep working in overtime.
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatalf("overtime deploy: %v", err)
	}

	s.overtimeDecayIn = 0.01
	step(s, 0.1)
	if s.TurretFactor >= 1.0 {
		t.Fatalf("turret factor should decay, got %v", s.TurretFactor)
	}
	if s.OvertimeSteps != 1 {
		t.Fatalf("overtime steps = %d, want 1", s.OvertimeSteps)
	}
	mult := s.counterMultiplier(Soldier, ClassStructure, map[UpgradeTrack]int{})
	want := 0.65 * (1 + s.cfg.Overtime.StructureBonus)
	if math.Abs(mult-want) > 1e-9 {
		t.Fatalf("overtime structure multiplier = %v, want %v", mult, want)
	}
}

func TestTimeLimitDraw(t *testing.T) {
	s := newTestSim(t)
	s.Time = s.cfg.Overtime.DrawAt - 0.1
	step(s, 0.2)
	if !s.Finished || !s.Draw {
		t.Fatalf("time limit should draw, got finished=%v draw=%v", s.Finished, s.Draw)
	}
}

func TestFogHidesFarEnemiesAndReconPaysOnce(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideB, Lane: 0, Unit: Knight}); err != nil {
		t.Fatal(err)
	}
	snapA := s.SnapshotFor(SideA)
	if len(snapA.Units) != 1 || snapA.Units[0].Side != SideA {
		t.Fatalf("SideA should see only its own unit, got %+v", snapA.Units)
	}

	// March the soldier next to the knight: it appears and pays recon once.
	s.units[0].X = 85
	coinsBefore := coinBalance(s, SideA)
	step(s, 0.05)
	snapA = s.SnapshotFor(SideA)
	if len(snapA.Units) != 2 {
		t.Fatalf("knight should be visible near the soldier, got %d units", len(snapA.Units))
	}
	if got := coinBalance(s, SideA) - coinsBefore; got != s.cfg.Fog.ReconBounty {
		t.Fatalf("recon bounty = %d, want %d", got, s.cfg.Fog.ReconBounty)
	}
	if s.players[SideA].ReconEarned != s.cfg.Fog.ReconBounty {
		t.Fatalf("ReconEarned = %d", s.players[SideA].ReconEarned)
	}
	step(s, 0.5)
	if coinBalance(s, SideA) != coinsBefore+s.cfg.Fog.ReconBounty {
		t.Fatal("recon bounty must pay exactly once per enemy unit")
	}
}

func TestScoutRevealsLaneThenExpires(t *testing.T) {
	s := newTestSim(t)
	s.SetHideLuck(true)
	s.anomalies = []*Anomaly{{
		ID: 1, Kind: "loot_buff", Lane: 3, X: 60, Radius: 3.2, Value: 35, ExpiresAt: 10000,
	}}
	sg := &Unit{ID: 2, Type: Siege, Side: SideB, Lane: 3, X: 90, HP: 85, MaxHP: 85, Damage: 28}
	s.units = []*Unit{sg}
	s.nextUnitID = 3

	snapA := s.SnapshotFor(SideA)
	if len(snapA.Anomalies) != 0 || len(snapA.Units) != 0 {
		t.Fatalf("far lane should start hidden, got %+v %+v", snapA.Anomalies, snapA.Units)
	}
	if err := s.ApplyAction(Action{Kind: ActionScoutLane, Side: SideA, Lane: 3}); err != nil {
		t.Fatalf("scout: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionScoutLane, Side: SideA, Lane: 3}); !errors.Is(err, ErrAlreadyScouted) {
		t.Fatalf("double scout: want ErrAlreadyScouted, got %v", err)
	}
	snapA = s.SnapshotFor(SideA)
	if !snapA.ScoutedLanes[3] {
		t.Fatal("lane 3 should read as scouted")
	}
	if len(snapA.Anomalies) != 1 || len(snapA.Units) != 1 {
		t.Fatalf("scout should reveal lane 3, got %+v %+v", snapA.Anomalies, snapA.Units)
	}
	step(s, s.cfg.Fog.ScoutDuration+0.2)
	snapA = s.SnapshotFor(SideA)
	if snapA.ScoutedLanes[3] {
		t.Fatal("scout flare should expire")
	}
	if len(snapA.Anomalies) != 0 || len(snapA.Units) != 0 {
		t.Fatalf("lane 3 should be hidden again, got %+v %+v", snapA.Anomalies, snapA.Units)
	}
	if err := s.ApplyAction(Action{Kind: ActionScoutLane, Side: SideA, Lane: 3}); err != nil {
		t.Fatalf("re-scout after expiry: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionScoutLane, Side: SideA, Lane: 9}); !errors.Is(err, ErrInvalidLane) {
		t.Fatalf("bad lane: want ErrInvalidLane, got %v", err)
	}
}

func TestHideLuckConcealsUnrevealedAnomalies(t *testing.T) {
	s := newTestSim(t)
	s.SetHideLuck(true)
	s.anomalies = []*Anomaly{{
		ID: 1, Kind: "loot_coins", Lane: 1, X: 60, Radius: 3.2, Value: 35, ExpiresAt: 10000,
	}}
	snap := s.SnapshotFor(SideA)
	if !snap.HideLuck {
		t.Fatal("expected snap.HideLuck = true")
	}
	if len(snap.Anomalies) != 0 || snap.HiddenLuckCount != 1 {
		t.Fatalf("expected 1 hidden anomaly, got %+v count=%d", snap.Anomalies, snap.HiddenLuckCount)
	}
	s.SetHideLuck(false)
	snap = s.SnapshotFor(SideA)
	if len(snap.Anomalies) != 1 || snap.HiddenLuckCount != 0 {
		t.Fatalf("expected anomaly visible with HideLuck off, got %+v", snap.Anomalies)
	}
}

func TestGoldLootUncontestedClaimAndContestedWait(t *testing.T) {
	// Uncontested: first army to touch the loot takes it.
	s1 := newTestSim(t)
	s1.anomalies = []*Anomaly{{ID: 100, Kind: "loot_coins", Lane: 1, X: 50, Radius: 3.2, Value: 35, ExpiresAt: 10000}}
	s1.units = []*Unit{{ID: 1, Type: Soldier, Side: SideA, Lane: 1, X: 49, HP: 75, MaxHP: 75, Damage: 10}}
	s1.nextUnitID = 2
	coinsA := coinBalance(s1, SideA)
	step(s1, 0.2)
	if len(s1.anomalies) != 0 {
		t.Fatal("expected uncontested loot to be claimed on touch")
	}
	if coinBalance(s1, SideA) != coinsA+35 {
		t.Fatalf("loot claim paid %d, want 35", coinBalance(s1, SideA)-coinsA)
	}

	// Contested: both armies near the loot -> it waits for a winner.
	s2 := newTestSim(t)
	s2.anomalies = []*Anomaly{{ID: 200, Kind: "loot_coins", Lane: 2, X: 50, Radius: 3.2, Value: 40, ExpiresAt: 10000}}
	solA := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 2, X: 49, HP: 200, MaxHP: 200, Damage: 25}
	solB := &Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 2, X: 51, HP: 30, MaxHP: 30, Damage: 5}
	s2.units = []*Unit{solA, solB}
	s2.nextUnitID = 3
	coinsBeforeA := coinBalance(s2, SideA)
	step(s2, 0.1)
	if len(s2.anomalies) != 1 || !s2.anomalies[0].Contested {
		t.Fatal("expected loot to be contested while both armies are near")
	}
	step(s2, 1.2) // SideA kills SideB's soldier, then claims
	if len(s2.anomalies) != 0 {
		t.Fatal("expected contested loot to be claimed once SideA won the fight")
	}
	if coinBalance(s2, SideA) < coinsBeforeA+40 {
		t.Fatalf("expected SideA to receive +40c loot, got +%d", coinBalance(s2, SideA)-coinsBeforeA)
	}
}

func TestMudSlowsAndSpikesBurnGroundOnly(t *testing.T) {
	s := newTestSim(t)
	s.anomalies = []*Anomaly{
		{ID: 1, Kind: "hazard_mud", Lane: 0, X: 50, Radius: 8.0, ExpiresAt: 10000},
		{ID: 2, Kind: "hazard_spikes", Lane: 1, X: 50, Radius: 6.0, TickIn: 0.1, ExpiresAt: 10000},
	}
	if !s.inMudHazard(0, 43.0) || s.inMudHazard(0, 41.0) {
		t.Fatal("inMudHazard should respect variable radius 8.0 around X=50")
	}
	ground := &Unit{ID: 10, Type: Soldier, Side: SideA, Lane: 1, X: 48, HP: 75, MaxHP: 75, Damage: 10}
	flyer := &Unit{ID: 11, Type: Dragon, Side: SideA, Lane: 1, X: 48, HP: 240, MaxHP: 240, Damage: 26}
	s.units = []*Unit{ground, flyer}
	s.nextUnitID = 12
	step(s, 0.5)
	if ground.HP >= 75 {
		t.Fatalf("spike field should damage ground unit over time, hp=%v", ground.HP)
	}
	if flyer.HP != 240 {
		t.Fatalf("spike field should not damage flying unit, hp=%v", flyer.HP)
	}
}

func TestLaneDominanceIsLiveAndSpeedsHolders(t *testing.T) {
	s := newTestSim(t)
	dom := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 10, HP: 75, MaxHP: 75, Damage: 10}
	conA := &Unit{ID: 2, Type: Soldier, Side: SideA, Lane: 1, X: 10, HP: 75, MaxHP: 75, Damage: 10}
	conB := &Unit{ID: 3, Type: Soldier, Side: SideB, Lane: 1, X: 90, HP: 75, MaxHP: 75, Damage: 10}
	s.units = []*Unit{dom, conA, conB}
	s.nextUnitID = 4
	step(s, 1.0)
	if s.laneDominance[0] != SideA {
		t.Fatalf("lane 0 dominance = %d, want SideA", s.laneDominance[0])
	}
	if s.laneDominance[1] != NoSide {
		t.Fatalf("lane 1 dominance = %d, want NoSide", s.laneDominance[1])
	}
	// Dominant army marches 10% faster: 5.2*1.1 vs 5.2 over 1s.
	if math.Abs((dom.X-10)-5.2*1.1) > 0.3 {
		t.Fatalf("dominant soldier moved %v, want ~5.72", dom.X-10)
	}
	if math.Abs((conA.X-10)-5.2) > 0.3 {
		t.Fatalf("contested soldier moved %v, want ~5.2", conA.X-10)
	}
}

func TestSoakRandomPlayEndsCleanly(t *testing.T) {
	// Seeded chaos match: both sides spam every action. The sim must never
	// panic, never mint negative coins, and always finish by the time limit.
	s := New(1234, DefaultBalance())
	rng := s.rng
	stock := []UnitType{Soldier, Bowman, Knight, Shieldbearer, Mage, Ram, Siege}
	tracks := []UpgradeTrack{TrackDamage, TrackAttackSpeed, TrackHP, TrackShield}
	for tick := 0; tick < 13000 && !s.Finished; tick++ {
		if tick%20 == 0 {
			for side := 0; side < 2; side++ {
				switch rng.Intn(8) {
				case 0, 1, 2:
					_ = s.ApplyAction(Action{Kind: ActionDeploy, Side: side, Lane: rng.Intn(NumLanes), Unit: stock[rng.Intn(len(stock))]})
				case 3:
					_ = s.ApplyAction(Action{Kind: ActionBuildTurret, Side: side, Lane: rng.Intn(NumLanes)})
				case 4:
					_ = s.ApplyAction(Action{Kind: ActionBuildWall, Side: side, Lane: rng.Intn(NumLanes)})
				case 5:
					_ = s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: side, Unit: stock[rng.Intn(len(stock))], Track: tracks[rng.Intn(len(tracks))]})
				case 6:
					_ = s.ApplyAction(Action{Kind: ActionScoutLane, Side: side, Lane: rng.Intn(NumLanes)})
				case 7:
					_ = s.ApplyAction(Action{Kind: ActionBuildFactory, Side: side})
				}
			}
		}
		s.Step(TickDT)
		for side := 0; side < 2; side++ {
			if coinBalance(s, side) < 0 {
				t.Fatalf("negative coins for side %d at tick %d", side, tick)
			}
		}
	}
	if !s.Finished {
		t.Fatal("chaos match should finish by the time limit")
	}
	_ = s.SnapshotPair() // fog snapshots must build on a finished match too
}

func TestAnomalySpawnerRumorCapAndExpiry(t *testing.T) {
	s := newTestSim(t)
	if len(s.anomalies) != 0 {
		t.Fatal("no anomalies should exist at match start")
	}
	step(s, 12.1)
	if len(s.anomalies) != 1 {
		t.Fatalf("expected first anomaly at 12s, got %d", len(s.anomalies))
	}
	rumor := false
	for _, ev := range drainedEvents(s) {
		if ev.Kind == "supply" {
			rumor = true
		}
	}
	if !rumor {
		t.Fatal("expected a rumor event for the anomaly")
	}
	s.anomalies[0].ExpiresAt = s.Time - 1
	step(s, 0.1)
	if len(s.anomalies) != 0 {
		t.Fatal("expired anomaly should be removed")
	}
	for i := 0; i < 200; i++ {
		step(s, 1.0)
		if len(s.anomalies) > s.cfg.Anomaly.MaxAlive {
			t.Fatalf("alive anomalies %d exceed cap %d", len(s.anomalies), s.cfg.Anomaly.MaxAlive)
		}
		if s.Finished {
			break
		}
	}
}
