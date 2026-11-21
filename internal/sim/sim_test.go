package sim

import (
	"errors"
	"math"
	"strings"
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

// step runs the sim for n seconds at TickDT.
func step(s *Sim, seconds float64) {
	ticks := int(math.Round(seconds / TickDT))
	for i := 0; i < ticks; i++ {
		s.Step(TickDT)
	}
}

func TestStartState(t *testing.T) {
	s := newTestSim(t)
	if got := coinBalance(s, SideA); got != 100 {
		t.Fatalf("side A starts with %d coins, want 100", got)
	}
	if got := coinBalance(s, SideB); got != 100 {
		t.Fatalf("side B starts with %d coins, want 100", got)
	}
	if s.Finished || s.Draw || s.Winner != NoSide {
		t.Fatal("new sim should not be finished")
	}
}

func TestDeployDeductsCoins(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 2, Unit: Soldier}); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if got := coinBalance(s, SideA); got != 80 {
		t.Fatalf("coins = %d, want 80", got)
	}
	if len(s.units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(s.units))
	}
	u := s.units[0]
	if u.Side != SideA || u.Lane != 2 || u.X != 2 {
		t.Fatalf("unit placed wrong: %+v", u)
	}
}

func TestDeployInsufficientFundsDoesNotCharge(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 30)
	err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Siege})
	if !errors.Is(err, ErrNotEnoughCoins) {
		t.Fatalf("want ErrNotEnoughCoins, got %v", err)
	}
	if got := coinBalance(s, SideA); got != 30 {
		t.Fatalf("coins changed on failed deploy: %d", got)
	}
	if len(s.units) != 0 {
		t.Fatal("unit spawned despite insufficient funds")
	}
}

func TestDeployInvalidLane(t *testing.T) {
	s := newTestSim(t)
	for _, lane := range []int{-1, 5, 99} {
		if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: lane, Unit: Soldier}); !errors.Is(err, ErrInvalidLane) {
			t.Fatalf("lane %d: want ErrInvalidLane, got %v", lane, err)
		}
	}
	if got := coinBalance(s, SideA); got != 100 {
		t.Fatalf("coins changed on invalid lane: %d", got)
	}
}

func TestTurretPadOnlyOne(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 1}); err != nil {
		t.Fatalf("build turret: %v", err)
	}
	before := coinBalance(s, SideA)
	err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 1})
	if !errors.Is(err, ErrPadOccupied) {
		t.Fatalf("want ErrPadOccupied, got %v", err)
	}
	if got := coinBalance(s, SideA); got != before {
		t.Fatalf("coins changed on occupied pad: %d -> %d", before, got)
	}
	// Other lane still fine.
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 2}); err != nil {
		t.Fatalf("build second turret: %v", err)
	}
}

func TestTurretCannotAfford(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideB, 44)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideB, Lane: 0}); !errors.Is(err, ErrNotEnoughCoins) {
		t.Fatalf("want ErrNotEnoughCoins, got %v", err)
	}
	if s.turrets[SideB][0] != nil {
		t.Fatal("turret built without paying")
	}
}

func TestFactoryOnlyOne(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionBuildFactory, Side: SideA}); err != nil {
		t.Fatalf("build factory: %v", err)
	}
	before := coinBalance(s, SideA)
	if err := s.ApplyAction(Action{Kind: ActionBuildFactory, Side: SideA}); !errors.Is(err, ErrFactoryBuilt) {
		t.Fatalf("want ErrFactoryBuilt, got %v", err)
	}
	if coinBalance(s, SideA) != before {
		t.Fatal("coins changed on failed factory purchase")
	}
}

func TestIncomeTicksEveryFiveSeconds(t *testing.T) {
	s := newTestSim(t)
	step(s, 5.0)
	// 100 + 10 (base income); windfall may or may not have landed yet, so only
	// check the guaranteed portion via fresh sims at sub-windfall times.
	if got := coinBalance(s, SideA); got < 110 {
		t.Fatalf("side A has %d coins after 5s, want >= 110", got)
	}
}

func TestFactoryIncomeAddedToInterval(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	if err := s.ApplyAction(Action{Kind: ActionBuildFactory, Side: SideA}); err != nil {
		t.Fatal(err)
	}
	before := coinBalance(s, SideA)
	step(s, 5.0)
	got := coinBalance(s, SideA) - before
	// Base 10 + factory 5 = 15, plus possibly a windfall (>= 0).
	if got < 15 {
		t.Fatalf("income over 5s = %d, want at least 15", got)
	}
}

func TestWindfallIsSharedAndSeeded(t *testing.T) {
	// Two sims with the same seed must produce identical windfall sequences.
	a, b := New(123, DefaultBalance()), New(123, DefaultBalance())
	for i := 0; i < 60*20; i++ { // 60 simulated seconds
		a.Step(TickDT)
		b.Step(TickDT)
	}
	ea, eb := a.DrainEvents(), b.DrainEvents()
	findWindfall := func(evs []Event) []string {
		var out []string
		for _, e := range evs {
			if e.Kind == "windfall" {
				out = append(out, e.Text)
			}
		}
		return out
	}
	wa, wb := findWindfall(ea), findWindfall(eb)
	if len(wa) == 0 {
		t.Fatal("expected at least one windfall in 60s of simulated time")
	}
	if strings.Join(wa, "|") != strings.Join(wb, "|") {
		t.Fatalf("seeded sims diverged:\nA: %v\nB: %v", wa, wb)
	}
	// Windfalls are shared: after 60s both players must have equal totals
	// (identical income, identical windfalls, no purchases).
	if coinBalance(a, SideA) != coinBalance(a, SideB) {
		t.Fatalf("windfall not shared: A=%d B=%d", coinBalance(a, SideA), coinBalance(a, SideB))
	}
}

func TestUnitUpgradeAffectsFutureSpawnsOnly(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	before := s.units[0]
	baseHP, baseDmg := before.MaxHP, before.Damage
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	// Existing unit untouched.
	if before.MaxHP != baseHP || before.Damage != baseDmg {
		t.Fatal("upgrade affected an already-spawned unit")
	}
	// Next spawn is upgraded.
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 1, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	after := s.units[1]
	if after.MaxHP <= baseHP || after.Damage <= baseDmg {
		t.Fatalf("upgraded soldier not stronger: hp %v->%v dmg %v->%v", baseHP, after.MaxHP, baseDmg, after.Damage)
	}
}

func TestUnitUpgradeCapAndCostEscalation(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	cfg := DefaultBalance().Units[Soldier]
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if got := coinBalance(s, SideA); got != 1000-cfg.UpgradeCosts[0] {
		t.Fatalf("level 1 cost mismatch, balance %d", got)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if got := coinBalance(s, SideA); got != 1000-cfg.UpgradeCosts[0]-cfg.UpgradeCosts[1] {
		t.Fatalf("level 2 cost mismatch, balance %d", got)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier}); !errors.Is(err, ErrMaxLevel) {
		t.Fatalf("want ErrMaxLevel, got %v", err)
	}
}

func TestTurretUpgradeDoesNotHeal(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 2}); err != nil {
		t.Fatal(err)
	}
	tr := s.turrets[SideA][2]
	tr.HP = 40 // simulate damage
	baseDamage := tr.Damage
	baseMaxHP := tr.MaxHP
	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 2}); err != nil {
		t.Fatal(err)
	}
	if tr.HP != 40 {
		t.Fatalf("turret healed by upgrade: hp=%v", tr.HP)
	}
	if tr.MaxHP <= baseMaxHP {
		t.Fatal("turret max HP did not grow")
	}
	if tr.Damage <= baseDamage {
		t.Fatal("turret damage did not grow")
	}
}

func TestTurretUpgradeCap(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	_ = s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0})
	_ = s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0})
	_ = s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0})
	if err := s.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0}); !errors.Is(err, ErrMaxLevel) {
		t.Fatalf("want ErrMaxLevel, got %v", err)
	}
}

func TestOvertimeLocksTurretBuildAndUpgrade(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	s.Time = s.cfg.Overtime.Start + 0.01
	step(s, TickDT) // cross into overtime
	if !s.Overtime {
		t.Fatal("expected overtime after 360s")
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0}); !errors.Is(err, ErrOvertime) {
		t.Fatalf("build in overtime: want ErrOvertime, got %v", err)
	}
	// Pre-build a turret before overtime to test upgrades are locked too.
	pre := newTestSim(t)
	giveCoins(pre, SideA, 1000)
	_ = pre.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0})
	pre.Time = pre.cfg.Overtime.Start + 0.01
	step(pre, TickDT)
	if err := pre.ApplyAction(Action{Kind: ActionUpgradeTurret, Side: SideA, Lane: 0}); !errors.Is(err, ErrOvertime) {
		t.Fatalf("upgrade in overtime: want ErrOvertime, got %v", err)
	}
}

func TestOvertimeDamageDecayAndFloor(t *testing.T) {
	s := newTestSim(t)
	s.Time = s.cfg.Overtime.Start
	step(s, TickDT) // enter overtime
	if s.TurretFactor != 1.0 {
		t.Fatalf("factor right at overtime start = %v, want 1.0", s.TurretFactor)
	}
	step(s, 30)
	if math.Abs(s.TurretFactor-0.85) > 1e-9 {
		t.Fatalf("factor after 30s of overtime = %v, want 0.85", s.TurretFactor)
	}
	// Run far enough that the factor must hit the floor of 0.
	s.Time = s.cfg.Overtime.Start
	s.TurretFactor = 1.0
	s.overtimeDecayIn = s.cfg.Overtime.Interval
	step(s, 30*8)
	if s.TurretFactor != 0.0 {
		t.Fatalf("factor should floor at 0, got %v", s.TurretFactor)
	}
}

func TestResignEndsMatch(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 100)
	s.Resign(SideB) // Red forfeits
	if !s.Finished {
		t.Fatal("resign should finish the match")
	}
	if s.Draw || s.Winner != SideA {
		t.Fatalf("winner = %d draw=%v, want SideA", s.Winner, s.Draw)
	}
	if s.WinLane != -1 {
		t.Fatalf("resign win should not credit a lane, got %d", s.WinLane)
	}
	// Resigning after the match ended is a no-op.
	s.Resign(SideA)
	if s.Winner != SideA {
		t.Fatal("resign changed the winner of a finished match")
	}
}

func TestSoldierMarchesAndBreachWins(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 10000)
	// Spam soldiers down lane 3; no defenders exist.
	for i := 0; i < 3; i++ {
		if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 3, Unit: Soldier}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60*20 && !s.Finished; i++ {
		s.Step(TickDT)
	}
	if !s.Finished {
		t.Fatal("match did not finish")
	}
	if s.Draw || s.Winner != SideA {
		t.Fatalf("winner = %d draw=%v, want side A", s.Winner, s.Draw)
	}
	if s.WinLane != 3 {
		t.Fatalf("win lane = %d, want 3", s.WinLane)
	}
}

func TestSimultaneousBreachIsDraw(t *testing.T) {
	s := newTestSim(t)
	// Place a nearly-breaching soldier for each side so both cross in one tick.
	s.units = append(s.units,
		&Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 2, X: FieldLength - 0.1, HP: 50, MaxHP: 50, Damage: 9},
		&Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 2, X: 0.1, HP: 50, MaxHP: 50, Damage: 9},
	)
	s.nextUnitID = 3
	s.Step(TickDT)
	if !s.Finished {
		t.Fatal("expected match to end")
	}
	if !s.Draw {
		t.Fatalf("expected draw, got winner=%d", s.Winner)
	}
}

func TestTimeLimitDraw(t *testing.T) {
	s := newTestSim(t)
	s.Time = s.cfg.Overtime.DrawAt - 0.001
	s.Step(TickDT)
	if !s.Finished || !s.Draw {
		t.Fatalf("expected draw at time limit, finished=%v draw=%v", s.Finished, s.Draw)
	}
}

func TestRamPrioritizesTurretOverUnit(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	// Enemy turret in lane 0 at x=90; enemy soldier nearby at x=88.
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideB, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	enemy := &Unit{ID: 900, Type: Soldier, Side: SideB, Lane: 0, X: 88, HP: 70, MaxHP: 70, Damage: 9}
	s.units = append(s.units, enemy)
	// Our ram in striking distance.
	ram := &Unit{ID: 901, Type: Ram, Side: SideA, Lane: 0, X: 88.6, HP: 170, MaxHP: 170, Damage: 4}
	s.units = append(s.units, ram)
	s.nextUnitID = 902
	ram.Cooldown = 0
	s.stepUnits(TickDT)
	if enemy.HP != 70 {
		t.Fatalf("ram chewed on the soldier (hp=%v); should prioritize the turret", enemy.HP)
	}
	if s.turrets[SideB][0].HP >= s.cfg.Turret.MaxHP {
		t.Fatal("ram did not damage the turret")
	}
}

func TestSoldierIgnoresTurret(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	// Move side A's turret hostile: build for side A, command a side B soldier next to it.
	_ = s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideB, Lane: 1})
	soldier := &Unit{ID: 950, Type: Soldier, Side: SideA, Lane: 1, X: FieldLength - s.cfg.Turret.PadX - 1, HP: 70, MaxHP: 70, Damage: 9}
	s.units = append(s.units, soldier)
	s.nextUnitID = 951
	hpBefore := s.turrets[SideB][1].HP
	step(s, 1.0)
	if s.turrets[SideB][1].HP != hpBefore {
		t.Fatal("soldier damaged a turret; soldiers must ignore turrets")
	}
}

func TestSiegeMinRangeHolds(t *testing.T) {
	s := newTestSim(t)
	// Enemy right in front of the siege engine, inside MinRange.
	siege := &Unit{ID: 1, Type: Siege, Side: SideA, Lane: 0, X: 50, HP: 85, MaxHP: 85, Damage: 30}
	enemy := &Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 0, X: 55, HP: 70, MaxHP: 70, Damage: 9}
	s.units = append(s.units, siege, enemy)
	s.nextUnitID = 3
	step(s, 1.0)
	if enemy.HP != 70 {
		t.Fatalf("siege fired inside minimum range (enemy hp=%v)", enemy.HP)
	}
	if siege.X != 50 {
		t.Fatalf("siege moved while too close to enemy: x=%v", siege.X)
	}
	// Pull the enemy back into the kill window and confirm it fires.
	enemy.X = 70
	step(s, 2.5)
	if enemy.HP >= 70 {
		t.Fatal("siege did not fire at a target inside its range band")
	}
}

func TestSiegeOutrangesTurret(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	giveCoins(s, SideB, 1000)
	_ = s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideB, Lane: 0})
	siege := &Unit{ID: 1, Type: Siege, Side: SideA, Lane: 0, X: 90 - s.cfg.Turret.PadX - 20, HP: 85, MaxHP: 85, Damage: 30}
	s.units = append(s.units, siege)
	s.nextUnitID = 2
	step(s, 6.0)
	if s.turrets[SideB][0] == nil {
		t.Fatal("turret destroyed too fast or missing")
	}
	if s.turrets[SideB][0].HP >= s.cfg.Turret.MaxHP {
		t.Fatal("siege dealt no damage to turret it outranges")
	}
	if siege.HP < siege.MaxHP {
		// Turret range 13; siege at 20+ distance should not have been hit.
		t.Fatalf("turret shot a siege engine that was out of range (hp=%v)", siege.HP)
	}
}

func TestShotFxEmittedForRangedAndTurrets(t *testing.T) {
	s := newTestSim(t)
	// Bowman vs enemy in lane 0.
	bowman := &Unit{ID: 1, Type: Bowman, Side: SideA, Lane: 0, X: 50, HP: 45, MaxHP: 45, Damage: 12}
	enemy := &Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 0, X: 60, HP: 70, MaxHP: 70, Damage: 9}
	s.units = append(s.units, bowman, enemy)
	s.nextUnitID = 3
	s.stepUnits(TickDT)
	fx := s.DrainFx()
	found := false
	for _, f := range fx {
		if f.Kind == "shot" && f.Unit == "bowman" && f.FromX == 50 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a bowman shot fx, got %+v", fx)
	}

	// Soldier (melee) attacking: expect "melee", not "shot".
	s2 := newTestSim(t)
	sa := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 1, X: 50, HP: 70, MaxHP: 70, Damage: 9}
	sb := &Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 1, X: 51, HP: 70, MaxHP: 70, Damage: 9}
	s2.units = append(s2.units, sa, sb)
	s2.nextUnitID = 3
	s2.stepUnits(TickDT)
	for _, f := range s2.DrainFx() {
		if f.Unit == "soldier" && f.Kind != "melee" {
			t.Fatalf("soldier attack should be melee fx, got %+v", f)
		}
	}
}

func TestFirstBloodEventOnce(t *testing.T) {
	s := newTestSim(t)
	s.units = append(s.units,
		&Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 50, HP: 1, MaxHP: 70, Damage: 0},
		&Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 1, X: 60, HP: 1, MaxHP: 70, Damage: 0},
	)
	s.units[0].HP = 0 // dies now
	s.units[1].HP = 0
	s.nextUnitID = 3
	s.removeDead()
	events := s.DrainEvents()
	count := 0
	for _, e := range events {
		if e.Kind == "firstBlood" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one firstBlood event, got %d (%+v)", count, events)
	}
}

func TestTurretFocusesMostAdvancedTarget(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	_ = s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideA, Lane: 0})
	// Side A's turret sits at x = PadX and defends x=0; the dangerous B unit
	// is the one with the lowest x (closest to A's breach line).
	farFromBase := &Unit{ID: 1, Type: Soldier, Side: SideB, Lane: 0, X: s.cfg.Turret.PadX + 5, HP: 200, MaxHP: 200, Damage: 1}
	nearBase := &Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 0, X: s.cfg.Turret.PadX + 2, HP: 200, MaxHP: 200, Damage: 1}
	s.units = append(s.units, farFromBase, nearBase)
	s.nextUnitID = 3
	step(s, 1.2)
	if nearBase.HP >= 200 {
		t.Fatal("turret did not fire on the enemy nearest to its breach line")
	}
	if farFromBase.HP != 200 {
		t.Fatal("turret failed to focus the most advanced enemy first")
	}
}
