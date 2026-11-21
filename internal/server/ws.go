package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"lanesiege/internal/sim"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // local game
}

// clientMsg is the envelope for everything a client may send.
type clientMsg struct {
	Type string `json:"type"`
	Lane int    `json:"lane"`
	Unit string `json:"unit"`
}

const writeWait = 5 * time.Second
const pongWait = 60 * time.Second
const pingPeriod = 30 * time.Second

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("room")))
	token := r.URL.Query().Get("token")

	s.mu.Lock()
	rm, ok := s.rooms[code]
	var pl *player
	if ok {
		for _, p := range rm.players {
			if p != nil && p.token == token {
				pl = p
			}
		}
	}
	s.mu.Unlock()
	if !ok || pl == nil {
		http.Error(w, "unknown room or token", http.StatusForbidden)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	conn := &wsConn{ws: ws, send: make(chan []byte, 64)}

	s.mu.Lock()
	old := pl.conn
	pl.conn = conn
	bothConnected := rm.players[0] != nil && rm.players[1] != nil &&
		rm.players[0].conn != nil && rm.players[1].conn != nil
	justStarted := bothConnected && rm.state == "waiting"
	if justStarted {
		rm.state = "playing"
	}
	s.mu.Unlock()

	if old != nil {
		// A refreshed tab replaces the stale connection. Closing the websocket
		// makes the old readPump exit; its defer closes old.send exactly once.
		_ = old.ws.Close()
	}
	// Notify the other player (if any) that we're here.
	s.mu.Lock()
	for _, p := range rm.players {
		if p != nil && p != pl && p.conn != nil {
			s.sendToLocked(p.conn, map[string]any{"type": "opponentConnected"})
		}
	}
	s.mu.Unlock()

	go s.writePump(conn)
	go s.readPump(r, code, token, pl, conn)

	// Tell this client its identity + the balance table (single source of truth).
	s.sendTo(conn, map[string]any{
		"type":    "hello",
		"seat":    pl.seat,
		"code":    code,
		"balance": s.balance,
	})
	if justStarted {
		log.Printf("room %s match started", code)
		s.broadcast(rm, map[string]any{"type": "matchStart"})
		s.broadcastSnapshot(rm)
	} else {
		// Reconnecting mid-match (or waiting): send the current state.
		s.mu.Lock()
		state := rm.state
		s.mu.Unlock()
		if state != "waiting" {
			s.sendTo(conn, map[string]any{"type": "matchStart"})
			s.broadcastSnapshot(rm)
		}
	}
}

func (s *Server) readPump(r *http.Request, code, token string, pl *player, conn *wsConn) {
	defer func() {
		s.mu.Lock()
		if cur, ok := s.rooms[code]; ok {
			for _, p := range cur.players {
				if p != nil && p.conn == conn {
					p.conn = nil
				}
			}
			s.sendRawLockedExcept(cur, conn, map[string]any{
				"type": "opponentDisconnected", "seat": pl.seat,
			})
		}
		s.mu.Unlock()
		close(conn.send)
		_ = conn.ws.Close()
	}()

	conn.ws.SetReadLimit(4096)
	_ = conn.ws.SetReadDeadline(time.Now().Add(pongWait))
	conn.ws.SetPongHandler(func(string) error {
		return conn.ws.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := conn.ws.ReadMessage()
		if err != nil {
			return
		}
		var msg clientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			s.sendTo(conn, map[string]any{"type": "error", "message": "bad message"})
			continue
		}
		s.handleClientMsg(code, pl, conn, msg)
	}
}

func (s *Server) handleClientMsg(code string, pl *player, conn *wsConn, msg clientMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.rooms[code]
	if !ok {
		return
	}

	if msg.Type == "rematch" {
		if rm.state != "finished" {
			return
		}
		rm.rematch[pl.seat] = true
		s.broadcastLocked(rm, map[string]any{"type": "rematchStatus", "seat": pl.seat})
		if rm.rematch[0] && rm.rematch[1] {
			rm.game = sim.New(randomSeed(), s.balance)
			rm.state = "playing"
			rm.rematch = [2]bool{}
			rm.snapEvery = 0
			log.Printf("room %s rematch started", code)
			s.broadcastLocked(rm, map[string]any{"type": "matchReset"})
			s.broadcastSnapshotLocked(rm)
		}
		return
	}

	if msg.Type == "resign" {
		if rm.state != "playing" {
			return
		}
		rm.game.Resign(pl.seat)
		rm.state = "finished"
		log.Printf("room %s: side %d resigned", code, pl.seat)
		s.broadcastSnapshotLocked(rm)
		return
	}

	if rm.state != "playing" {
		s.sendToLocked(conn, map[string]any{"type": "error", "message": "match is not running"})
		return
	}

	var action sim.Action
	action.Side = pl.seat
	switch msg.Type {
	case "deploy":
		action.Kind = sim.ActionDeploy
		action.Lane = msg.Lane
		action.Unit = sim.UnitType(msg.Unit)
	case "turret":
		action.Kind = sim.ActionBuildTurret
		action.Lane = msg.Lane
	case "factory":
		action.Kind = sim.ActionBuildFactory
	case "upgradeUnit":
		action.Kind = sim.ActionUpgradeUnit
		action.Unit = sim.UnitType(msg.Unit)
	case "upgradeTurret":
		action.Kind = sim.ActionUpgradeTurret
		action.Lane = msg.Lane
	default:
		s.sendToLocked(conn, map[string]any{"type": "error", "message": "unknown action"})
		return
	}

	if err := rm.game.ApplyAction(action); err != nil {
		s.sendToLocked(conn, map[string]any{"type": "error", "message": err.Error()})
		return
	}
	// Echo an immediate snapshot so the actor sees the result without
	// waiting for the next 10 Hz broadcast.
	s.broadcastSnapshotLocked(rm)
}

// ---------------------------------------------------------------------------
// Game loop
// ---------------------------------------------------------------------------

func (s *Server) tickLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second / sim.TickRate)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.tickAll()
		}
	}
}

func (s *Server) tickAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rm := range s.rooms {
		if rm.state != "playing" || rm.game == nil {
			continue
		}
		rm.game.Step(sim.TickDT)
		rm.snapEvery++
		if rm.game.Finished {
			rm.state = "finished"
			log.Printf("room %s finished (winner=%d draw=%v lane=%d)", rm.code, rm.game.Winner, rm.game.Draw, rm.game.WinLane)
		}
		if rm.snapEvery >= sim.SnapshotEvery || rm.game.Finished {
			rm.snapEvery = 0
			s.broadcastSnapshotLocked(rm)
		}
	}
}

func (s *Server) janitor() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			for code, rm := range s.rooms {
				connected := false
				for _, p := range rm.players {
					if p != nil && p.conn != nil {
						connected = true
					}
				}
				if !connected && time.Since(rm.created) > 15*time.Minute {
					log.Printf("room %s expired", code)
					delete(s.rooms, code)
				}
			}
			s.mu.Unlock()
		}
	}
}

// ---------------------------------------------------------------------------
// Broadcast helpers
// ---------------------------------------------------------------------------

func (s *Server) sendTo(c *wsConn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default: // slow consumer: drop rather than block the match
	}
}

// sendToLocked is sendTo for call sites that already hold s.mu.
func (s *Server) sendToLocked(c *wsConn, v any) { s.sendTo(c, v) }

func (s *Server) broadcastLocked(rm *room, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	for _, p := range rm.players {
		if p != nil && p.conn != nil {
			select {
			case p.conn.send <- data:
			default:
			}
		}
	}
}

func (s *Server) sendRawLockedExcept(rm *room, except *wsConn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	for _, p := range rm.players {
		if p != nil && p.conn != nil && p.conn != except {
			select {
			case p.conn.send <- data:
			default:
			}
		}
	}
}

func (s *Server) broadcast(rm *room, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastLocked(rm, v)
}

func (s *Server) broadcastSnapshot(rm *room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastSnapshotLocked(rm)
}

func (s *Server) broadcastSnapshotLocked(rm *room) {
	if rm.game == nil {
		return
	}
	snap := rm.game.Snapshot()
	s.broadcastLocked(rm, snap)
}

func (s *Server) writePump(c *wsConn) {
	defer func() { _ = c.ws.Close() }()
	ping := time.NewTicker(pingPeriod)
	defer ping.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
