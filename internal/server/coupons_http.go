package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"lanesiege/internal/coupons"
)

// maxPlayerIDLen caps the client-generated player id we are willing to store.
const maxPlayerIDLen = 64

func readJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return false
	}
	return true
}

// normalizePlayerID validates the client-generated player id. We only accept a
// conservative character set so stored values stay tidy.
func normalizePlayerID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) == 0 || len(id) > maxPlayerIDLen {
		return ""
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return ""
		}
	}
	return id
}

func validCouponList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		c = coupons.Normalize(c)
		if c == "" || len(c) > 32 || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

type couponView struct {
	Code        string                 `json:"code"`
	Label       string                 `json:"label"`
	CreatedAt   time.Time              `json:"createdAt"`
	ExpiresAt   time.Time              `json:"expiresAt"`
	Revoked     bool                   `json:"revoked"`
	MaxUses     int                    `json:"maxUses"`
	Uses        int                    `json:"uses"`
	Redemptions []couponRedemptionView `json:"redemptions,omitempty"`
}

type couponRedemptionView struct {
	PlayerID string    `json:"playerId"`
	At       time.Time `json:"at"`
}

func couponToView(c coupons.Coupon, withRedemptions bool) couponView {
	v := couponView{
		Code: c.Code, Label: c.Label, CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt,
		Revoked: c.RevokedAt != nil, MaxUses: c.MaxUses, Uses: len(c.Redemptions),
	}
	if withRedemptions {
		v.Redemptions = make([]couponRedemptionView, 0, len(c.Redemptions))
		for _, r := range c.Redemptions {
			v.Redemptions = append(v.Redemptions, couponRedemptionView{PlayerID: r.PlayerID, At: r.At})
		}
	}
	return v
}

// requireAdmin gates the admin API on a bearer token. Returns false (and
// writes the response) when the admin API is disabled or the token mismatches.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.adminToken == "" {
		writeErr(w, http.StatusForbidden, "admin API is disabled (set -admin-token or LANESIEGE_ADMIN_TOKEN)")
		return false
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	tok := strings.TrimPrefix(h, prefix)
	if tok == h { // no Bearer prefix — also accept X-Admin-Token for curl convenience
		tok = r.Header.Get("X-Admin-Token")
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.adminToken)) != 1 {
		writeErr(w, http.StatusForbidden, "invalid admin token")
		return false
	}
	return true
}

// POST /api/admin/coupons — create a coupon.
// Body: {"label": "...", "validDays": 30, "maxUses": 0}
func (s *Server) handleAdminCreateCoupon(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body struct {
		Label     string `json:"label"`
		ValidDays int    `json:"validDays"`
		MaxUses   int    `json:"maxUses"`
	}
	if !readJSONBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Label) == "" {
		writeErr(w, http.StatusBadRequest, "label is required (who/what this coupon is for)")
		return
	}
	if body.ValidDays < 0 || body.ValidDays > 3650 {
		writeErr(w, http.StatusBadRequest, "validDays must be between 0 (default 30) and 3650")
		return
	}
	if body.MaxUses < 0 {
		writeErr(w, http.StatusBadRequest, "maxUses must be >= 0 (0 = unlimited)")
		return
	}
	validFor := time.Duration(body.ValidDays) * 24 * time.Hour // 0 → library default (30d)
	c, err := s.coupons.Create(strings.TrimSpace(body.Label), validFor, body.MaxUses)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, couponToView(*c, true))
}

// GET /api/admin/coupons — list every coupon with redemption info.
func (s *Server) handleAdminListCoupons(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	list := s.coupons.List()
	out := make([]couponView, 0, len(list))
	for _, c := range list {
		out = append(out, couponToView(c, true))
	}
	writeJSON(w, http.StatusOK, map[string]any{"coupons": out})
}

// POST /api/admin/coupons/{code}/revoke — disable a coupon immediately.
func (s *Server) handleAdminRevokeCoupon(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	code := r.PathValue("code")
	if err := s.coupons.Revoke(code); err != nil {
		if errors.Is(err, coupons.ErrUnknown) {
			writeErr(w, http.StatusNotFound, "unknown coupon code")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// couponStatusView is what the lobby renders for each stored code.
func couponStatusJSON(st coupons.Status) map[string]any {
	v := map[string]any{
		"code":  st.Code,
		"valid": st.Valid,
	}
	if st.Label != "" {
		v["label"] = st.Label
	}
	if !st.ExpiresAt.IsZero() {
		v["expiresAt"] = st.ExpiresAt
	}
	if st.Reason != "" {
		v["reason"] = st.Reason
	}
	return v
}

// POST /api/coupons/redeem — a player registers a coupon code.
// Body: {"code": "DRGN-…", "playerId": "<uuid>"}
func (s *Server) handleRedeemCoupon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code     string `json:"code"`
		PlayerID string `json:"playerId"`
	}
	if !readJSONBody(w, r, &body) {
		return
	}
	playerID := normalizePlayerID(body.PlayerID)
	if playerID == "" {
		writeErr(w, http.StatusBadRequest, "playerId is required")
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		writeErr(w, http.StatusBadRequest, "code is required")
		return
	}
	c, err := s.coupons.Redeem(code, playerID)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, coupons.ErrUnknown):
			status = http.StatusNotFound
		case errors.Is(err, coupons.ErrExpired), errors.Is(err, coupons.ErrRevoked), errors.Is(err, coupons.ErrExhausted):
			status = http.StatusGone
		}
		writeErr(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"coupon": couponStatusJSON(coupons.Status{Code: c.Code, Label: c.Label, Valid: true, ExpiresAt: c.ExpiresAt}),
	})
}

// POST /api/coupons/status — batch-check codes the browser holds.
// Body: {"codes": ["DRGN-…"]}
func (s *Server) handleCouponStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Codes []string `json:"codes"`
	}
	if !readJSONBody(w, r, &body) {
		return
	}
	codes := validCouponList(body.Codes)
	results := make([]map[string]any, 0, len(codes))
	for _, code := range codes {
		results = append(results, couponStatusJSON(s.coupons.Status(code)))
	}
	best, entitled := s.coupons.BestValid(codes)
	out := map[string]any{
		"entitled": entitled,
		"coupons":  results,
	}
	if entitled {
		out["active"] = map[string]any{
			"code":      best.Code,
			"label":     best.Label,
			"expiresAt": best.ExpiresAt,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// entitledCoupons filters a client-supplied code list down to currently valid
// ones. Used when a room is created/joined.
func (s *Server) entitledCoupons(codes []string) []string {
	valid := make([]string, 0, len(codes))
	for _, c := range validCouponList(codes) {
		if s.coupons.AnyValid([]string{c}) {
			valid = append(valid, c)
		}
	}
	return valid
}

// playerEntitled reports whether any code the player holds is currently valid.
// Checked live (not cached) so revocation/expiry applies mid-session.
func (s *Server) playerEntitled(p *player) bool {
	return p != nil && len(p.coupons) > 0 && s.coupons.AnyValid(p.coupons)
}

// couponHello builds the coupon block of the hello message so the client can
// render the badge/unlock UI without an extra round trip.
func (s *Server) couponHello(p *player) map[string]any {
	if p == nil {
		return map[string]any{"entitled": false}
	}
	best, ok := s.coupons.BestValid(p.coupons)
	if !ok {
		return map[string]any{"entitled": false}
	}
	return map[string]any{
		"entitled":  true,
		"code":      best.Code,
		"label":     best.Label,
		"expiresAt": best.ExpiresAt,
	}
}
