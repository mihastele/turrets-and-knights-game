package server

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startTestServer boots a server on an ephemeral port with the admin API
// enabled and an in-memory coupon store.
func startTestServer(t *testing.T) string {
	t.Helper()
	srv := NewWithOptions(os.DirFS("../../web"), Options{AdminToken: "test-admin-token"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Close)
	return ln.Addr().String()
}

func httpJSON(t *testing.T, method, url, bearer string, body any) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(b))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestAdminCouponLifecycleAndRoomEntitlement(t *testing.T) {
	base := "http://" + startTestServer(t)

	// Admin API is gated.
	if code, _ := httpJSON(t, "POST", base+"/api/admin/coupons", "", map[string]any{"label": "nope"}); code != http.StatusForbidden {
		t.Fatalf("admin without token: got %d, want 403", code)
	}
	if code, _ := httpJSON(t, "POST", base+"/api/admin/coupons", "wrong-token", map[string]any{"label": "nope"}); code != http.StatusForbidden {
		t.Fatalf("admin with wrong token: got %d, want 403", code)
	}

	// Create a coupon.
	code, created := httpJSON(t, "POST", base+"/api/admin/coupons", "test-admin-token", map[string]any{
		"label": "Test Dragon Sponsor", "validDays": 30, "maxUses": 0,
	})
	if code != http.StatusCreated {
		t.Fatalf("create coupon: got %d (%v)", code, created)
	}
	couponCode, _ := created["code"].(string)
	if !strings.HasPrefix(couponCode, "DRGN-") {
		t.Fatalf("unexpected coupon code %q", couponCode)
	}

	// Redeem it as a player.
	code, redeemed := httpJSON(t, "POST", base+"/api/coupons/redeem", "", map[string]any{
		"code": couponCode, "playerId": "player-abc-123",
	})
	if code != http.StatusOK {
		t.Fatalf("redeem: got %d (%v)", code, redeemed)
	}

	// Status endpoint reports it as valid.
	code, status := httpJSON(t, "POST", base+"/api/coupons/status", "", map[string]any{
		"codes": []string{couponCode},
	})
	if code != http.StatusOK || status["entitled"] != true {
		t.Fatalf("status: got %d (%v), want entitled=true", code, status)
	}

	// Creating a room with the coupon marks the seat entitled.
	code, room := httpJSON(t, "POST", base+"/api/rooms", "", map[string]any{
		"playerId": "player-abc-123", "coupons": []string{couponCode},
	})
	if code != http.StatusOK {
		t.Fatalf("create room: got %d (%v)", code, room)
	}
	if room["entitled"] != true {
		t.Fatalf("room create should report entitled=true, got %v", room)
	}
	roomCode, _ := room["code"].(string)
	token, _ := room["token"].(string)

	// Entitlement is assigned when both players start the match.
	ws := dialWS(t, strings.TrimPrefix(base, "http://"), roomCode, token)
	defer ws.Close()
	hello := readMsg(t, ws, 3*time.Second)
	if hello["type"] != "hello" {
		t.Fatalf("first message = %v, want hello", hello["type"])
	}
	coupon, _ := hello["coupon"].(map[string]any)
	if coupon == nil || coupon["entitled"] != false {
		t.Fatalf("hello coupon payload wrong: %v", hello["coupon"])
	}

	// Revoking kills the entitlement for future actions.
	if code, _ := httpJSON(t, "POST", base+"/api/admin/coupons/"+couponCode+"/revoke", "test-admin-token", nil); code != http.StatusOK {
		t.Fatalf("revoke: got %d", code)
	}
	code, status = httpJSON(t, "POST", base+"/api/coupons/status", "", map[string]any{
		"codes": []string{couponCode},
	})
	if code != http.StatusOK || status["entitled"] != false {
		t.Fatalf("status after revoke: got %d (%v), want entitled=false", code, status)
	}
}

func TestRedeemUnknownCoupon(t *testing.T) {
	base := "http://" + startTestServer(t)
	code, resp := httpJSON(t, "POST", base+"/api/coupons/redeem", "", map[string]any{
		"code": "DRGN-XXXX-YYYY", "playerId": "player-1",
	})
	if code != http.StatusNotFound {
		t.Fatalf("got %d (%v), want 404", code, resp)
	}
}

func TestFinalCouponUseAndRematch(t *testing.T) {
	base := "http://" + startTestServer(t)
	host := strings.TrimPrefix(base, "http://")
	_, created := httpJSON(t, "POST", base+"/api/admin/coupons", "test-admin-token", map[string]any{"label": "final use", "maxUses": 1})
	code := created["code"].(string)
	_, room := httpJSON(t, "POST", base+"/api/rooms", "", map[string]any{"coupons": []string{code}})
	roomCode := room["code"].(string)
	_, join := httpJSON(t, "POST", base+"/api/rooms/"+roomCode+"/join", "", map[string]any{})
	a := dialWS(t, host, roomCode, room["token"].(string))
	defer a.Close()
	b := dialWS(t, host, roomCode, join["token"].(string))
	defer b.Close()
	var update map[string]any
	for update == nil {
		m := readMsg(t, a, 3*time.Second)
		if m["type"] == "couponUpdate" {
			update = m
		}
	}
	if update["exhaustedCode"] != strings.ReplaceAll(code, "-", "") {
		t.Fatalf("missing exhausted code: %v", update)
	}
	if update["coupon"].(map[string]any)["entitled"] != true {
		t.Fatalf("final use lost entitlement: %v", update)
	}
	if err := a.WriteJSON(map[string]any{"type": "resign"}); err != nil {
		t.Fatal(err)
	}
	for {
		m := readMsg(t, a, 3*time.Second)
		if m["type"] == "snapshot" && m["finished"] == true {
			break
		}
	}
	_ = a.WriteJSON(map[string]any{"type": "rematch"})
	_ = b.WriteJSON(map[string]any{"type": "rematch"})
	for {
		m := readMsg(t, a, 3*time.Second)
		if m["type"] == "couponUpdate" {
			if m["coupon"].(map[string]any)["entitled"] != false {
				t.Fatalf("rematch reused coupon: %v", m)
			}
		}
		if m["type"] == "matchReset" {
			break
		}
	}
}

func TestWithoutCouponClearsRoomList(t *testing.T) {
	s := NewWithOptions(os.DirFS("../../web"), Options{})
	defer s.Close()
	p := &player{seat: 0, coupons: []string{"DRGN-AAAA-BBBB"}}
	rm := &room{code: "TEST", state: "waiting", players: [2]*player{p, nil}}
	s.mu.Lock()
	s.rooms[rm.code] = rm
	s.mu.Unlock()
	s.handleClientMsg(rm.code, p, nil, clientMsg{Type: "withoutCoupon"})
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(p.coupons) != 0 || !p.withoutCoupon {
		t.Fatalf("coupon list was not cleared: %+v", p)
	}
}

// TestWebSocketDragonEntitlement plays over a real WS connection: the coupon
// holder's dragon deploy passes the entitlement gate (fails only on coins),
// while the non-holder is rejected with the coupon error.
func TestWebSocketDragonEntitlement(t *testing.T) {
	base := "http://" + startTestServer(t)
	host := strings.TrimPrefix(base, "http://")

	_, created := httpJSON(t, "POST", base+"/api/admin/coupons", "test-admin-token", map[string]any{"label": "WS test", "validDays": 30})
	couponCode, _ := created["code"].(string)
	if couponCode == "" {
		t.Fatalf("no coupon code in %v", created)
	}

	_, roomA := httpJSON(t, "POST", base+"/api/rooms", "", map[string]any{
		"playerId": "ws-player-a", "coupons": []string{couponCode},
	})
	code, _ := roomA["code"].(string)
	tokenA, _ := roomA["token"].(string)
	_, roomB := httpJSON(t, "POST", base+"/api/rooms/"+code+"/join", "", map[string]any{"playerId": "ws-player-b"})
	tokenB, _ := roomB["token"].(string)
	if code == "" || tokenA == "" || tokenB == "" {
		t.Fatalf("bad room setup: a=%v b=%v", roomA, roomB)
	}

	wsA := dialWS(t, host, code, tokenA)
	defer wsA.Close()
	wsB := dialWS(t, host, code, tokenB)
	defer wsB.Close()

	for _, ws := range []*websocket.Conn{wsA, wsB} {
		deadline := time.Now().Add(5 * time.Second)
		got := map[string]bool{}
		for time.Now().Before(deadline) && !(got["hello"] && got["matchStart"]) {
			var m map[string]any
			_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
			if err := ws.ReadJSON(&m); err != nil {
				break
			}
			if t, ok := m["type"].(string); ok {
				got[t] = true
			}
		}
		if !got["hello"] || !got["matchStart"] {
			t.Fatalf("missing hello/matchStart, got %v", got)
		}
	}

	// Non-coupon player tries to deploy a dragon: must be rejected mentioning the coupon.
	if err := wsB.WriteJSON(map[string]any{"type": "deploy", "unit": "dragon", "lane": 0}); err != nil {
		t.Fatal(err)
	}
	sawCouponErr := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !sawCouponErr {
		var m map[string]any
		_ = wsB.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := wsB.ReadJSON(&m); err != nil {
			break
		}
		if m["type"] == "error" {
			msg, _ := m["message"].(string)
			if strings.Contains(msg, "coupon") {
				sawCouponErr = true
			}
		}
	}
	if !sawCouponErr {
		t.Fatal("expected the non-entitled player to be rejected with a coupon error")
	}

	// Coupon holder deploys a dragon: 100 coins is not enough for 140c, but the
	// failure must NOT be the coupon gate.
	if err := wsA.WriteJSON(map[string]any{"type": "deploy", "unit": "dragon", "lane": 2}); err != nil {
		t.Fatal(err)
	}
	sawNonCouponOutcome := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !sawNonCouponOutcome {
		var m map[string]any
		_ = wsA.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := wsA.ReadJSON(&m); err != nil {
			break
		}
		switch m["type"] {
		case "error":
			msg, _ := m["message"].(string)
			if strings.Contains(msg, "coupon") {
				t.Fatalf("entitled player was rejected by the coupon gate: %v", msg)
			}
			sawNonCouponOutcome = true // e.g. not enough coins — entitlement passed
		case "snapshot":
			sawNonCouponOutcome = true
		}
	}
	if !sawNonCouponOutcome {
		t.Fatal("no response to entitled dragon deploy")
	}
}
