// Package server runs the HTTP API, the lobby and the authoritative match
// loop. Layout:
//
//   - HTTP serves the client (static files) and room create/join endpoints.
//   - WebSocket carries intentions (client -> server) and snapshots
//     (server -> client).
//   - Each room owns its *sim.Sim; all access to sim state happens while
//     holding the room's mutex, which gives the simulation a single owner.
package server

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"lanesiege/internal/sim"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type player struct {
	token string
	seat  int
	conn  *wsConn
}

type wsConn struct {
	ws   *websocket.Conn
	send chan []byte
}

type room struct {
	code      string
	players   [2]*player
	game      *sim.Sim
	state     string // "waiting" | "playing" | "finished"
	rematch   [2]bool
	created   time.Time
	snapEvery int // tick counter for snapshot decimation
}

// Server owns all rooms.
type Server struct {
	webDir  string
	balance sim.Balance

	mu    sync.Mutex
	rooms map[string]*room

	httpServer *http.Server
	stop       chan struct{}
	wg         sync.WaitGroup
}

// New builds a Server. webDir is the directory holding index.html/style.css/
// client.js and is served at "/".
func New(webDir string) *Server {
	s := &Server{
		webDir:  webDir,
		balance: sim.DefaultBalance(),
		rooms:   map[string]*room{},
		stop:    make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rooms", s.handleCreateRoom)
	mux.HandleFunc("POST /api/rooms/{code}/join", s.handleJoinRoom)
	mux.HandleFunc("POST /api/rooms/{code}/rejoin", s.handleRejoin)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	s.httpServer = &http.Server{Handler: mux}

	s.wg.Add(1)
	go s.tickLoop()
	s.wg.Add(1)
	go s.janitor()
	return s
}

// Handler exposes the root handler (used by ListenAndServe).
func (s *Server) Handler() http.Handler { return s.httpServer.Handler }

// Serve serves on an existing listener (used by tests and main).
func (s *Server) Serve(l net.Listener) error {
	return s.httpServer.Serve(l)
}

// ListenAndServe serves until the server is closed.
func (s *Server) ListenAndServe(addr string) error {
	s.httpServer.Addr = addr
	return s.httpServer.ListenAndServe()
}

// Close stops background goroutines.
func (s *Server) Close() {
	close(s.stop)
	_ = s.httpServer.Close()
	s.wg.Wait()
}

// ---------------------------------------------------------------------------
// Room lifecycle (HTTP)
// ---------------------------------------------------------------------------

const roomCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func randomCode() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = roomCodeAlphabet[int(buf[i])%len(roomCodeAlphabet)]
	}
	return string(buf), nil
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func randomSeed() int64 {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return int64(binary.LittleEndian.Uint64(buf[:]))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var code string
	for i := 0; i < 20; i++ {
		c, err := randomCode()
		if err == nil {
			if _, exists := s.rooms[c]; !exists {
				code = c
				break
			}
		}
	}
	if code == "" {
		writeErr(w, http.StatusInternalServerError, "could not allocate room code")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not allocate token")
		return
	}
	s.rooms[code] = &room{
		code:    code,
		players: [2]*player{{token: token, seat: 0}, nil},
		game:    sim.New(randomSeed(), s.balance),
		state:   "waiting",
		created: time.Now(),
	}
	log.Printf("room %s created", code)
	writeJSON(w, http.StatusOK, map[string]any{
		"code": code, "token": token, "seat": 0, "side": "A",
	})
}

func (s *Server) handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	s.mu.Lock()
	defer s.mu.Unlock()

	rm, ok := s.rooms[code]
	if !ok {
		writeErr(w, http.StatusNotFound, "room not found")
		return
	}
	if rm.players[1] != nil {
		writeErr(w, http.StatusConflict, "room is full")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not allocate token")
		return
	}
	rm.players[1] = &player{token: token, seat: 1}
	log.Printf("room %s joined by player 2", code)
	writeJSON(w, http.StatusOK, map[string]any{
		"code": code, "token": token, "seat": 1, "side": "B",
	})
}

// handleRejoin lets a disconnected/refreshed client reclaim its seat with
// its token. Used by the browser on page load.
func (s *Server) handleRejoin(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.rooms[code]
	if !ok {
		writeErr(w, http.StatusNotFound, "room not found")
		return
	}
	for _, p := range rm.players {
		if p != nil && p.token == body.Token {
			writeJSON(w, http.StatusOK, map[string]any{
				"code": code, "token": p.token, "seat": p.seat,
				"side": map[int]string{0: "A", 1: "B"}[p.seat],
			})
			return
		}
	}
	writeErr(w, http.StatusForbidden, "bad token")
}
