// Package coupons implements the coupon ("coupon user") unlock system.
//
// Coupons are bearer codes created by the admin (see the /api/admin/coupons
// endpoints). A coupon has a label (who/what it is for), an expiry (default
// 30 days), an optional redemption cap, and records which player IDs redeemed
// it. Redeeming a coupon entitles that browser (player ID) to the coupon
// unlocks: the Dragon unit and the coupon field effects.
//
// Entitlement is always re-checked against the live store, so revoking or
// letting a coupon expire takes effect immediately (even mid-match); units
// already deployed stay on the field.
//
// The store keeps an in-memory map and persists every change to a JSON file
// (atomic write via temp file + rename). Pass "" as the path for an
// in-memory only store (used by tests).
package coupons

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultLifetime is how long a new coupon lives unless the admin overrides it.
const DefaultLifetime = 30 * 24 * time.Hour

var (
	ErrUnknown   = errors.New("unknown coupon code")
	ErrExpired   = errors.New("coupon has expired")
	ErrRevoked   = errors.New("coupon has been revoked")
	ErrExhausted = errors.New("coupon has already been redeemed the maximum number of times")
)

// Redemption records that a given player ID redeemed the coupon, and when.
type Redemption struct {
	PlayerID string    `json:"playerId"`
	At       time.Time `json:"at"`
}

// Coupon is a single redeemable unlock code.
type Coupon struct {
	Code        string       `json:"code"`
	Label       string       `json:"label"`
	CreatedAt   time.Time    `json:"createdAt"`
	ExpiresAt   time.Time    `json:"expiresAt"`
	RevokedAt   *time.Time   `json:"revokedAt,omitempty"`
	MaxUses     int          `json:"maxUses"` // 0 = unlimited redemptions
	Redemptions []Redemption `json:"redemptions"`
}

// Status describes a coupon as seen by a player trying to use it.
type Status struct {
	Code      string    `json:"code"`
	Label     string    `json:"label,omitempty"`
	Valid     bool      `json:"valid"`
	Reason    string    `json:"reason,omitempty"` // "expired" | "revoked" | "unknown" when !Valid
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// storeFile is the on-disk schema.
type storeFile struct {
	Coupons []*Coupon `json:"coupons"`
}

// alphabet avoids lookalike characters (no 0/O, 1/I/L).
const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// Store guards the coupon table. Safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	path    string // "" = in-memory only
	coupons map[string]*Coupon
	now     func() time.Time // injectable for tests
}

// New returns an in-memory store (nothing is persisted).
func New() *Store {
	return &Store{coupons: map[string]*Coupon{}, now: time.Now}
}

// Load opens (or creates) a file-backed store. An empty path returns an
// in-memory store. A missing file starts empty; a corrupt file is an error.
func Load(path string) (*Store, error) {
	s := &Store{path: path, coupons: map[string]*Coupon{}, now: time.Now}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read coupon store: %w", err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse coupon store %s: %w", path, err)
	}
	for _, c := range f.Coupons {
		if c == nil || c.Code == "" {
			continue
		}
		cp := *c
		s.coupons[cp.Code] = &cp
	}
	return s, nil
}

// Normalize canonicalizes a user-typed code (case, spaces, dashes).
func Normalize(code string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
}

// NormalizeWithDashes re-inserts the dashes of the canonical DRGN-XXXX-XXXX shape.
func normalizeDashes(code string) string {
	c := Normalize(code)
	if strings.HasPrefix(c, "DRGN") {
		c = strings.TrimPrefix(c, "DRGN")
		c = "DRGN" + c
	}
	if len(c) == 12 {
		return c[:4] + "-" + c[4:8] + "-" + c[8:]
	}
	return c
}

// canon finds a coupon by user input that may omit dashes/case.
func (s *Store) canon(code string) (string, bool) {
	n := Normalize(code)
	if _, ok := s.coupons[n]; ok {
		return n, true
	}
	dashed := normalizeDashes(n)
	if _, ok := s.coupons[dashed]; ok {
		return dashed, true
	}
	return "", false
}

// Create mints a new coupon. validFor <= 0 means DefaultLifetime. maxUses 0
// means unlimited redemptions.
func (s *Store) Create(label string, validFor time.Duration, maxUses int) (*Coupon, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, errors.New("label is required (e.g. who the coupon is for)")
	}
	if validFor <= 0 {
		validFor = DefaultLifetime
	}
	if maxUses < 0 {
		maxUses = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var code string
	for i := 0; i < 32; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		part := func(off int) string {
			var sb strings.Builder
			for j := 0; j < 4; j++ {
				sb.WriteByte(alphabet[int(b[off+j])%len(alphabet)])
			}
			return sb.String()
		}
		// 8 random alphabet chars -> DRGN-XXXX-XXXX
		code = "DRGN-" + part(0) + "-" + part(4)
		if _, exists := s.coupons[code]; !exists {
			break
		}
		code = ""
	}
	if code == "" {
		return nil, errors.New("could not allocate a unique coupon code")
	}
	now := s.now()
	c := &Coupon{
		Code:      code,
		Label:     label,
		CreatedAt: now,
		ExpiresAt: now.Add(validFor),
		MaxUses:   maxUses,
	}
	s.coupons[code] = c
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	cp := *c
	return &cp, nil
}

// checkLocked validates a coupon for use right now. Assumes s.mu is held and
// the coupon has been resolved via canon().
func (s *Store) checkLocked(c *Coupon) error {
	switch {
	case c.RevokedAt != nil:
		return ErrRevoked
	case !s.now().Before(c.ExpiresAt):
		return ErrExpired
	default:
		return nil
	}
}

// Redeem records playerID as a user of the coupon. Re-redeeming with the same
// player ID is idempotent. The coupon must pass validation (exists, not
// revoked, not expired, redemption cap not exceeded by *other* players).
func (s *Store) Redeem(code, playerID string) (*Coupon, error) {
	playerID = strings.TrimSpace(playerID)
	if playerID == "" {
		return nil, errors.New("playerId is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.canon(code)
	if !ok {
		return nil, ErrUnknown
	}
	c := s.coupons[key]
	if err := s.checkLocked(c); err != nil {
		return nil, err
	}
	for _, r := range c.Redemptions {
		if r.PlayerID == playerID {
			cp := *c
			return &cp, nil // already registered, treat as success
		}
	}
	if c.MaxUses > 0 && len(c.Redemptions) >= c.MaxUses {
		return nil, ErrExhausted
	}
	c.Redemptions = append(c.Redemptions, Redemption{PlayerID: playerID, At: s.now()})
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	cp := *c
	return &cp, nil
}

// Status reports the state of a code for display purposes. Never mutates.
func (s *Store) Status(code string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.canon(code)
	if !ok {
		return Status{Code: Normalize(code), Reason: "unknown"}
	}
	c := s.coupons[key]
	st := Status{Code: c.Code, Label: c.Label, ExpiresAt: c.ExpiresAt}
	switch {
	case c.RevokedAt != nil:
		st.Reason = "revoked"
	case !s.now().Before(c.ExpiresAt):
		st.Reason = "expired"
	default:
		st.Valid = true
	}
	return st
}

// AnyValid reports whether at least one of the given codes is currently
// usable. Used to decide entitlements at match time.
func (s *Store) AnyValid(codes []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, code := range codes {
		key, ok := s.canon(code)
		if !ok {
			continue
		}
		if err := s.checkLocked(s.coupons[key]); err == nil {
			return true
		}
	}
	return false
}

// BestValid returns the valid coupon with the latest expiry, for display in
// the client (badge, "valid until …").
func (s *Store) BestValid(codes []string) (Coupon, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Coupon
	for _, code := range codes {
		key, ok := s.canon(code)
		if !ok {
			continue
		}
		c := s.coupons[key]
		if err := s.checkLocked(c); err != nil {
			continue
		}
		if best == nil || c.ExpiresAt.After(best.ExpiresAt) {
			best = c
		}
	}
	if best == nil {
		return Coupon{}, false
	}
	return *best, true
}

// Revoke disables a coupon immediately. Redemptions are kept for the record.
func (s *Store) Revoke(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.canon(code)
	if !ok {
		return ErrUnknown
	}
	if s.coupons[key].RevokedAt == nil {
		now := s.now()
		s.coupons[key].RevokedAt = &now
		return s.saveLocked()
	}
	return nil
}

// List returns all coupons, newest first. Callers must not mutate.
func (s *Store) List() []Coupon {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Coupon, 0, len(s.coupons))
	for _, c := range s.coupons {
		out = append(out, *c)
	}
	// sort newest first (insertion sort; admin lists are tiny)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CreatedAt.After(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	f := storeFile{}
	for _, c := range s.coupons {
		f.Coupons = append(f.Coupons, c)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// Write-then-rename; on Windows rename-over-existing can fail, so remove first.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if err2 := os.Rename(tmp, s.path); err2 != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("persist coupons: %w", err2)
		}
	}
	// Best-effort directory sync so the rename survives a crash.
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
