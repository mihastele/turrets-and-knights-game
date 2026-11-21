# Lane Siege

A head-to-head lane siege for two players. Plants vs. Zombies meets a
headline-fight: five lanes, two commanders, one breach. Build an economy,
massed troops, or turtling turrets — whoever gets a living unit across the
enemy breach line first wins. No accounts, no database.

Built with **Go** (server), **plain HTML/CSS/JS** (client), **Canvas 2D**
(battlefield), **WebSockets** (live match), plain **HTTP** (lobby + page).
The client is embedded in the Go binary — one static binary serves everything.

## Run it locally (30 seconds)

Requires Go 1.22+ (developed on 1.27).

```sh
go run ./cmd/lanesiege
```

Open **http://localhost:8180** in **two browser tabs**. In tab 1 click
**Create Room**; in tab 2 type the 4-letter code and click **Join**. The
match starts automatically.

Prefer editing the client without recompiling? Serve it from disk instead:

```sh
go run ./cmd/lanesiege -web web
```

### Flags and environment

| Knob | Default | Meaning |
|---|---|---|
| `-addr` | `:8180`, or `:$PORT` if set | listen address |
| `-web` | unset = embedded assets | serve the client from this directory instead |
| `PORT` | — | used when `-addr` is not given (Fly/Render/Heroku style) |

`GET /healthz` returns `200 ok` (for load balancers / uptime checks).

## Host it so other people can join

Rooms live **in memory**. One process = one world of rooms. Consequences:
run exactly **one** instance (no replicas/autoscaling), and restarting drops
active matches — both fine for pickup games.

### Same LAN (this machine is the server)

```sh
go run ./cmd/lanesiege        # listens on :8180 on all interfaces
```

Find your LAN IP (Windows: `ipconfig`, look for IPv4 Address) and have the
other player open `http://<your-LAN-IP>:8180`. On Windows you may need to
allow inbound TCP 8180 once (run as admin):

```powershell
netsh advfirewall firewall add rule name="LaneSiege" dir=in action=allow protocol=TCP localport=8180
```

### Docker

```sh
docker build -t lanesiege .
docker run -d --name laneseige -p 8180:8180 --restart unless-stopped lanesiege
# or: docker compose up -d --build
```

Multi-stage build, static binary on distroless — the image holds no Go
toolchain and needs no env vars. `PORT` may be overridden (`-e PORT=9000`,
then publish that port instead). `docker-compose.yml` is included for
convenience.

### A cheap VPS / any Linux box

```sh
go build -trimpath -ldflags="-s -w" -o lanesiege ./cmd/lanesiege
./lanesiege                      # serves on :8180
```

or copy the `lanesiege` binary onto the box — the client is embedded, no
ports other than HTTP needed. Put Caddy or nginx in front for TLS:

```caddyfile
# /etc/caddy/Caddyfile — automatic HTTPS
siege.example.com {
    reverse_proxy 127.0.0.1:8180
}
```

```nginx
# nginx: note the WebSocket upgrade headers — required for the game socket
map $http_upgrade $connection_upgrade { default upgrade; '' close; }
server {
    listen 80;
    server_name siege.example.com;
    location / {
        proxy_pass http://127.0.0.1:8180;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
    }
}
```

Or a systemd unit:

```ini
[Unit]
Description=Lane Siege
After=network.target

[Service]
ExecStart=/usr/local/bin/lanesiege
Restart=always
Environment=PORT=8180

[Install]
WantedBy=multi-user.target
```

### Fly.io / Render / similar

Deploy as a **single-machine Docker web service**. Both set `PORT` for you;
the Dockerfile listens on `$PORT` (default 8180) and `/healthz` is wired for
health checks. Pin `min=1, max=1` instances — the server is stateful
(in-memory rooms), so horizontal scaling will split rooms across instances.

### Instant one-off public URL (no signup)

```sh
# Requires the binary from https://github.com/cloudflare/cloudflared
cloudflared tunnel --url http://localhost:8180
# prints https://<random>.trycloudflare.com  — share it, Ctrl+C to kill
```

or with any SSH daemon: `ssh -R 80:localhost:8180 ssh.localhost.run`.
Both expose the game to the public internet — anyone with the URL can open
the lobby. Rooms still need the 4-letter code, but treat the link as
semi-public and don't linger for weeks; it's a test tunnel, not hosting.

## How to play

- Click a shop item (or press **1–5** for troops, **6** for turret, **7** for
  factory), then **click a lane** to deploy / build. **Esc** or right-click
  cancels the selection.
- You start with **100 coins**. **+10 every 5s**, plus a **shared windfall of
  5–10 every 12–20s** rolled once for both players.
- A **Coin Factory** (55c) adds **+5 every 5s**; it pays back in ~55s. One
  per player.
- **Turrets** (45c) stop lone attackers but not an escorted ram. **Siege
  engines** outrange turrets but die to anything that reaches them.
  **Knights** punish open lanes but lose straight fights against equal coin
  in soldiers. **Bowmen** shoot over your frontline but fold up close.
- **Unit upgrades** apply to future spawns only; **turret upgrades** apply in
  place, improve stats and never heal. Both cap at 2 levels, second costs
  more. **Resign** forfeits the match (two-click confirm, top-left).
- At **6:00** overtime begins: turret construction/upgrades are locked and
  turrets lose 15% of their damage every 30s until it floors at 0. Troops and
  income keep flowing. At **10:00** the match is a draw. A simultaneous
  double breach is also a draw.
- Both players' coins and income are public — watch the enemy's war fund.
- If you disconnect, refresh the page: you rejoin your seat (rooms are kept
  for 15 minutes).

## Project layout

```
cmd/lanesiege/     entry point (flags, PORT env, graceful shutdown)
embed.go           //go:embed web — the client inside the binary
internal/sim/      game simulation — pure Go, no I/O, no network
  balance.go       ALL tunable numbers (prices, HP, damage, ranges, timers)
  sim.go           tick loop, combat, economy, overtime, win detection
  snapshot.go      the wire snapshot sent to clients
internal/server/   HTTP + WebSocket rooms, lobby, match ownership
  server.go        create/join/rejoin, health, static files, room lifecycle
  ws.go            socket lifecycle, message handling, broadcast
web/               the client (index.html, style.css, client.js)
Dockerfile         static binary on distroless (docker build -t lanesiege .)
docker-compose.yml convenience wrapper
```

### Architecture notes

- **Server-authoritative.** Clients send intentions; the server owns coins,
  cooldowns, damage, positions and the win check. Clients render snapshots.
- **Single owner per match.** All `sim.Sim` access happens under the room
  mutex. The sim is deterministic given its seed (`sim.New(seed, balance)`).
- **Fixed timestep.** 20 Hz sim; snapshots at 10 Hz; the browser
  interpolates between them.
- **Reconnects.** `sessionStorage` keeps your room code + token; refreshing
  rejoins the seat.

## Tests

```sh
go test ./...          # everything, incl. ~20s end-to-end match over real HTTP+WS
go test -short ./...   # skip the end-to-end test
```

Coverage includes spending, insufficient funds, invalid lanes, pad occupancy,
factory limit, shared seeded windfall (identical across same-seed sims),
future-only unit upgrades, non-healing turret upgrades, overtime lockout and
damage decay to zero, simultaneous-breach draw, time-limit draw, ram vs.
turret prioritization, soldier-ignores-turret, siege min-range, turret target
focus, resign flow, rematch flow.

## Tuning the game

Everything lives in **`internal/sim/balance.go`** (`DefaultBalance()`).
Compare equal coin investments, not unit-vs-unit in a vacuum. The sim is
deterministic per seed, so you can replay a specific line with
`sim.New(seed, balance)`.

## What's in the game

- **Five lanes, two war economies** — shared windfalls, a coin factory you
  must pay down before it beats an early enemy rush, public ledgers.
- **Five units, a turret, upgrades twice deep** — counters matted in matching
  paper-rock-scissors: soldiers > knights, bowmen > fragile damage, rams >
  turrets, siege engines > static defenses.
- **Match structure** — 100-coin start, win by beaching a unit; 6:00
  overtime (turrets disarmed); 10:00 draw; simultaneous breach is a draw;
  resign with two clicks.
- **Game feel** — projectiles with arcs (arrows, tracer rounds, plunging
  shells with explosions), damage numbers, unit death poofs, muzzle flashes,
  hit flashes, screen shake on turret kills, a breach-warning siren when an
  enemy gets close to your line, windfall fanfare, confetti on victory, and a
  full synthesized soundscape (no assets) with a mute toggle.
- **Readable chaos** — team-colored units with shadows and walk bob, per-lane
  HP bars, dashed pad outlines, and an event feed for everything that matters.

## Verified

- `go vet`, `gofmt` clean; all tests pass, including the HTTP+WS end-to-end
  tests (full match, resign forfeit, room guards).
- Two-browser (headless Edge) matches: create → join → deploy → turret →
  factory → breach → victory/defeat overlays → rematch; resign flow; windfall
  banner and first-blood feed observed in live play; zero console errors.
- Docker image rebuilt and re-verified after the FX pass (serves embedded
  client, `/healthz`, full match playable).
- One-command public tunnel (`cloudflared`) verified end-to-end: page, API,
  WebSocket upgrade.
- Not yet verified by a human: actual *fun* — balance may need a pass.

## Known limits

- Balance is playtested only by simulation, not by people. Expect to tune
  `balance.go`.
- No client prediction (fine at LAN/low-latency; high latency adds a few
  frames of felt lag).
- One instance only — rooms are in-memory; no persistence, no accounts,
  no ranking, no spectators, no reconnect-after-forfeit.
- A disconnected player's army just stops; the opponent can still win.

