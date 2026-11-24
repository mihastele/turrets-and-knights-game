package sim

import (
	"errors"
	"strings"
	"testing"
)

func TestDragonRequiresCoupon(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Dragon})
	if !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("deploying dragon without coupon: want ErrCouponRequired, got %v", err)
	}
	if len(s.units) != 0 {
		t.Fatal("dragon spawned without coupon")
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Dragon, Entitled: true}); err != nil {
		t.Fatalf("deploying dragon with coupon: %v", err)
	}
	if coinBalance(s, SideA) != 500-140 {
		t.Fatalf("dragon cost not deducted: %d", coinBalance(s, SideA))
	}
	// Valkyrie is also coupon-locked.
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Valkyrie}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("deploying valkyrie without coupon: want ErrCouponRequired, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Valkyrie, Entitled: true}); err != nil {
		t.Fatalf("deploying valkyrie with coupon: %v", err)
	}
}

func TestDragonIsFlyingAndSplashDamages(t *testing.T) {
	s := newTestSim(t)
	s.anomalies = nil
	s.anomalyIn = 9999
	giveCoins(s, SideA, 500)
	giveCoins(s, SideB, 500)
	// B deploys two soldiers close together.
	for i := 0; i < 2; i++ {
		if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideB, Lane: 2, Unit: Soldier}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 2, Unit: Dragon, Entitled: true}); err != nil {
		t.Fatal(err)
	}
	// Simulate until the dragon is in range and breathes fire.
	for i := 0; i < 20*20 && s.units[0].HP == s.units[0].MaxHP; i++ {
		s.Step(TickDT)
	}
	// Find damage dealt to B soldiers.
	soldiers := 0
	soldierHP := 0.0
	for _, u := range s.units {
		if u.Type == Soldier && u.Side == SideB {
			soldiers++
			soldierHP += u.HP
		}
	}
	if soldiers == 0 {
		t.Fatal("no soldiers left to observe")
	}
	if soldierHP >= float64(soldiers)*75 {
		t.Fatal("expected dragon splash fire to wound multiple soldiers")
	}
}

func TestTurretCannotTargetDragon(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	giveCoins(s, SideB, 500)
	if err := s.ApplyAction(Action{Kind: ActionBuildTurret, Side: SideB, Lane: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Dragon, Entitled: true}); err != nil {
		t.Fatal(err)
	}
	tr := s.turrets[SideB][0][0]
	dragon := s.units[len(s.units)-1]
	dragon.X = tr.X - 10 // inside tower range, closing in
	// Base turret (without Flak special) must never lock onto the dragon.
	step(s, 3)
	if tr.TargetID == dragon.ID {
		t.Fatal("turret targeted a flying dragon")
	}
	if dragon.HP != dragon.MaxHP || dragon.Shield != dragon.MaxShield {
		t.Fatalf("dragon should be untouched by the tower, hp=%v shield=%v", dragon.HP, dragon.Shield)
	}
}

func TestRockslideDamagesGroundEnemiesOnly(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	giveCoins(s, SideB, 500)
	// Enemy ground unit and enemy dragon in lane 1.
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideB, Lane: 1, Unit: Knight}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideB, Lane: 1, Unit: Dragon, Entitled: true}); err != nil {
		t.Fatal(err)
	}
	// A own dragon in the blast radius must be spared too (own side), but we
	// care about enemies: rockslide hits ground enemies, not flyers.
	err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 1, Effect: EffectRockslide, Entitled: true})
	if err != nil {
		t.Fatalf("rockslide: %v", err)
	}
	var knight, dragon *Unit
	for _, u := range s.units {
		switch u.Type {
		case Knight:
			knight = u
		case Dragon:
			dragon = u
		}
	}
	if knight == nil || dragon == nil {
		t.Fatal("missing units")
	}
	if knight.HP >= knight.MaxHP {
		t.Fatalf("knight should have taken rockslide damage, hp=%v", knight.HP)
	}
	if dragon.HP != dragon.MaxHP {
		t.Fatalf("flying dragon should be unaffected by rockslide, hp=%v/%v", dragon.HP, dragon.MaxHP)
	}
	if got := coinBalance(s, SideA); got != 500-s.cfg.Coupon.Rockslide.Cost {
		t.Fatalf("rockslide cost not deducted: %d", got)
	}
}

func TestRockslideRequiresEntitlementAndCooldown(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectRockslide}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("want ErrCouponRequired, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectRockslide, Entitled: true}); err != nil {
		t.Fatalf("first cast: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectRockslide, Entitled: true}); !errors.Is(err, ErrOnCooldown) {
		t.Fatalf("want ErrOnCooldown, got %v", err)
	}
	// Unknown effect id is rejected without charging coins.
	coins := coinBalance(s, SideA)
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: "meteor", Entitled: true}); !errors.Is(err, ErrUnknownEffect) {
		t.Fatalf("want ErrUnknownEffect, got %v", err)
	}
	if coinBalance(s, SideA) != coins {
		t.Fatal("coins charged for unknown effect")
	}
}

func TestDragonFireBurnsLaneOverTime(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	giveCoins(s, SideB, 500)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideB, Lane: 3, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	var soldier *Unit
	for _, u := range s.units {
		if u.Type == Soldier {
			soldier = u
		}
	}
	hpBefore := soldier.HP
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 3, Effect: EffectDragonFire, Entitled: true}); err != nil {
		t.Fatalf("dragon fire: %v", err)
	}
	if len(s.burns) != 1 {
		t.Fatalf("expected 1 active burn, got %d", len(s.burns))
	}
	step(s, 2.0)
	if soldier.HP >= hpBefore {
		t.Fatalf("soldier should burn: hp %v -> %v", hpBefore, soldier.HP)
	}
	step(s, 10.0) // outlast the 6s burn
	if len(s.burns) != 0 {
		t.Fatalf("burn should have expired, got %d", len(s.burns))
	}
}

func TestBurnsSnapshotAndAbilityCooldowns(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectDragonFire, Entitled: true}); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Burns) != 1 {
		t.Fatalf("snapshot burns = %d, want 1", len(snap.Burns))
	}
	if snap.Burns[0].Lane != 0 || snap.Burns[0].Remaining <= 0 {
		t.Fatalf("bad burn snapshot: %+v", snap.Burns[0])
	}
	rem, ok := snap.Players[0].Abilities["dragonfire"]
	if !ok || rem <= 0 {
		t.Fatalf("expected dragonfire cooldown in snapshot, got %v", snap.Players[0].Abilities)
	}
	if got := snap.Players[1].Abilities; len(got) != 0 {
		t.Fatalf("side B never cast; abilities should be empty, got %v", got)
	}
}

func TestWarcryBuffsOwnLaneAndRequiresCoupon(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 1, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectWarcry}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("warcry without coupon: want ErrCouponRequired, got %v", err)
	}
	inLane, otherLane := s.units[0], s.units[1]
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectWarcry, Entitled: true}); err != nil {
		t.Fatalf("warcry: %v", err)
	}
	if inLane.Shield != s.cfg.Coupon.Warcry.Damage || inLane.SpeedBuff <= 0 {
		t.Fatalf("warcry should grant shield + speed, got shield=%v buff=%v", inLane.Shield, inLane.SpeedBuff)
	}
	if otherLane.Shield != 0 || otherLane.SpeedBuff != 0 {
		t.Fatal("warcry must only affect the targeted lane")
	}
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectWarcry, Entitled: true}); !errors.Is(err, ErrOnCooldown) {
		t.Fatalf("warcry recast: want ErrOnCooldown, got %v", err)
	}
}

func TestFieldEffectEventText(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 500)
	if err := s.ApplyAction(Action{Kind: ActionFieldEffect, Side: SideA, Lane: 0, Effect: EffectRockslide, Entitled: true}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range s.DrainEvents() {
		if ev.Kind == "rockslide" && strings.Contains(ev.Text, "lane 1") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a rockslide event mentioning lane 1")
	}
}
