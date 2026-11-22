package coupons

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateRedeemFlow(t *testing.T) {
	s := New()
	c, err := s.Create("Alice — launch", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Code == "" || c.Label != "Alice — launch" {
		t.Fatalf("bad coupon: %+v", c)
	}
	if d := time.Until(c.ExpiresAt); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("default lifetime not ~30 days: %v", d)
	}
	// Redeem with messy casing/dashes/spacing.
	messy := " " + c.Code[:5] + " " + c.Code[5:] + " "
	got, err := s.Redeem(messy, "player-1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got.Code != c.Code {
		t.Fatalf("got %q, want %q", got.Code, c.Code)
	}
	// Re-redeeming with the same player is idempotent.
	if _, err := s.Redeem(c.Code, "player-1"); err != nil {
		t.Fatalf("re-redeem: %v", err)
	}
	list := s.List()
	if len(list) != 1 || len(list[0].Redemptions) != 1 {
		t.Fatalf("redemptions not recorded once: %+v", list)
	}
}

func TestMaxUses(t *testing.T) {
	s := New()
	c, err := s.Create("One-shot", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(c.Code, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(c.Code, "bob"); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second distinct redeem: want ErrExhausted, got %v", err)
	}
	// Alice can still re-validate her own coupon.
	if _, err := s.Redeem(c.Code, "alice"); err != nil {
		t.Fatalf("same-player re-redeem should succeed: %v", err)
	}
}

func TestExpiry(t *testing.T) {
	s := New()
	now := time.Now()
	s.now = func() time.Time { return now }
	c, err := s.Create("Short", time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(c.Code, "p"); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := s.Redeem(c.Code, "p2"); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	if s.AnyValid([]string{c.Code}) {
		t.Fatal("expired coupon reports valid")
	}
	if st := s.Status(c.Code); st.Valid || st.Reason != "expired" {
		t.Fatalf("status: %+v", st)
	}
}

func TestRevoked(t *testing.T) {
	s := New()
	c, err := s.Create("Rev", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(c.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(c.Code, "p"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if s.AnyValid([]string{c.Code}) {
		t.Fatal("revoked coupon reports valid")
	}
}

func TestUnknownCode(t *testing.T) {
	s := New()
	if _, err := s.Redeem("DRGN-AAAA-BBBB", "p"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("want ErrUnknown, got %v", err)
	}
	if s.AnyValid([]string{"DRGN-AAAA-BBBB"}) {
		t.Fatal("unknown code reports valid")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coupons.json")
	s1, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s1.Create("Persisted", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Redeem(c.Code, "player-9"); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.List()
	if len(list) != 1 {
		t.Fatalf("loaded %d coupons, want 1", len(list))
	}
	got := list[0]
	if got.Code != c.Code || got.Label != "Persisted" || len(got.Redemptions) != 1 || got.Redemptions[0].PlayerID != "player-9" {
		t.Fatalf("bad round trip: %+v", got)
	}
	if !s2.AnyValid([]string{c.Code}) {
		t.Fatal("reloaded store reports coupon invalid")
	}
}

func TestBestValidPicksLatestExpiry(t *testing.T) {
	s := New()
	now := time.Now()
	s.now = func() time.Time { return now }
	short, err := s.Create("short", time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	long, err := s.Create("long", 90*24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	best, ok := s.BestValid([]string{short.Code, long.Code})
	if !ok || best.Code != long.Code {
		t.Fatalf("best = %+v, want %s", best, long.Code)
	}
}
