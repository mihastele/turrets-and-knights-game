# Lane Siege

A head-to-head lane siege for two players. Plants vs. Zombies meets a
headline-fight: five lanes, two commanders, one breach. Build an economy,
massed troops, or turtling turrets — whoever gets a living unit across the
enemy breach line first wins. No accounts, no matchmaking, no database.

Built with **Go** (server), **plain HTML/CSS/JS** (client), **Canvas 2D**
(battlefield), **WebSockets** (live match), plain **HTTP** (lobby + page).
No build step, no framework, no WebAssembly.

## Quick start

Requirements: Go 1.22+ (developed on Go 1.27).

```sh
go run ./cmd/lanesiege
```

Then open **http://localhost:8080** in two browser tabs:

1. Tab 1: click **Create Room** — you get a 4-letter code.
2. Tab 2: type the code, click **Join**.
3. The match starts automatically. Player 1 (Blue) pushes left→right,
   Player 2 (Red) pushes right→left.

Flags (optional):

```sh
go run ./cmd/lanesiege -addr :9000 -web web
```

- `-addr` — listen address (default `:8080`)
- `-web` — path to the client directory (default `web`, relative to repo root)

## How to play

- Click a shop item (or press **1–5** for troops, **6** for turret, **7** for
  factory), then click a lane to deploy / build. **Esc** or right-click cancels.
- You start with **100 coins**. You get **+10 coins every 5s**, plus a
  **shared windfall of 5–10 coins every 12–20s** — both players get the same
  amount at the same time (the server rolls it once).
- A **Coin Factory** (55c) adds **+5 coins every 5s**. It pays for itself in
  ~55 seconds. One per player.
- **Turrets** (45c) stop lone attackers but not an escorted ram. **Siege
  engines** outrange turrets but die fast if swarmed. **Knights** punch
  through open lanes but lose straight fights with equal soldiers.
- **Unit upgrades** apply to future spawns only. **Turret upgrades** apply in
  place, improve stats, and never heal. Both cap at two levels; the second
  costs more.
- At **6:00** overtime begins: no more turret construction or upgrades, and
  every 30s turrets lose 15% of their damage (floor 0%). Troops and income
  keep working.
- At **10:00** the match is a **draw**. A simultaneous double breach is also a
  draw. Either way, a rematch button appears.

The economy is fully public: you can see both players' coins and income at all
times. Deploy failures and upgrades never charge you on error.

## Project layout

```
cmd/lanesiege/     entry point (flags, wiring)
internal/sim/      the game simulation — pure Go, no I/O, no network
  balance.go       ALL tunable numbers (prices, HP, damage, ranges, timers)
  sim.go           tick loop, combat, economy, overtime, win detection
  snapshot.go      the wire snapshot sent to clients
internal/server/   HTTP + WebSocket rooms, lobby, match ownership
  server.go        room create/join/rejoin, static files, tick loop
  ws.go            socket lifecycle, message handling, broadcast
web/               the whole client (index.html, style.css, client.js)
```

### Architecture notes

- **Server-authoritative.** Clients send intentions (`deploy`, `turret`,
  `upgradeUnit`, …). The server owns coins, cooldowns, damage, positions and
  the win check. Clients render snapshots. Nothing the client says about
  money/damage is trusted.
- **Single owner per match.** Each room's `*sim.Sim` is only touched while
  holding the room mutex (tick loop and message handlers). The sim itself is
  deterministic given its seed (`sim.New(seed, balance)`), which is how the
  seeded windfall stays reproducible.
- **Fixed timestep.** The sim advances at 20 Hz. Snapshots go out at 10 Hz
  (`sim.SnapshotEvery = 2`); clients interpolate unit positions between
  snapshots for smooth motion.
- **Reconnects.** The browser stores its room code + token in
  `sessionStorage`; on refresh it rejoins its seat automatically.

## Tests

```sh
go test ./...          # everything, incl. the ~20s end-to-end match test
go test -short ./...   # skip the end-to-end match test
```

Coverage highlights (`internal/sim/sim_test.go`):

- spending, insufficient funds, invalid lanes, pad occupancy, factory limit
- shared seeded windfall (two sims with the same seed stay identical)
- unit upgrades affect future spawns only; turret upgrades never heal
- overtime lockout (build/upgrade) and the 15%/30s damage decay with a floor at 0
- simultaneous breach → draw; time limit at 10:00 → draw; normal breach → win + lane
- ram prioritizes turrets, soldiers ignore turrets, siege respects min-range,
  turret focuses the most advanced attacker

`internal/server/e2e_test.go` plays a real match over HTTP + WebSocket:
create → join → invalid action rejected → soldier marches → breach → rematch.

## Tuning the game

Everything lives in one place: **`internal/sim/balance.go`** (`DefaultBalance`).

- Prices, HP, damage, ranges, attack intervals, speeds: `Balance.Units`,
  `Balance.Turret`.
- Economy: `Balance.Economy` (start coins, interval, windfall bounds, factory).
- Match length and overtime: `Balance.Overtime` (`Start`, `Interval`, `Decay`,
  `DrawAt`).
- Spawn inset and start coins: `Balance.StartCoins`, `Balance.SpawnInset`.
- Ram turret damage uses `VsTurretDamage` on the unit entry; soldiers/bowmen/
  knights have no `VsTurretDamage` and cannot hurt turrets at all.
- Upgrade amounts use `UpgradeCosts`, `UpgradeHP`, `UpgradeDamage` per level.

Tune by comparing **equal coin investments** (e.g. 45c knight vs ~2 soldiers),
not unit-vs-unit in a vacuum. The sim is deterministic per seed, so you can
replay a specific line with `sim.New(seed, balance)`.

## What is deliberately not here (yet)

No accounts, rankings, matchmaking, database, or spectators. No client-side
prediction (snapshots interpolate; actions feel instant because the server
re-broadcasts a snapshot immediately after each valid action). If profiling a
real match shows a CPU bottleneck, revisit Canvas rendering or move the sim
off the room mutex — not WebAssembly yet.

## Verified

- `go vet ./...` clean; `go test ./...` green (sim unit tests + a full
  end-to-end match over real HTTP + WebSocket).
- Two headless Edge tabs driving the real UI: create room → join → deploy
  soldier / build turret / buy factory (coins deducted correctly), battle
  renders, soldier breaches, VICTORY/DEFEAT overlays show on both screens,
  rematch resets both clients. Zero console errors.
- Server logs confirm room lifecycle: create → join → start → finish → rematch.
- Not yet verified by a human: actual *fun*. See below.

## Known limits / needs human playtesting

- **Balance is unverified by humans.** Every number is a first-principles
  estimate. Expect to tune `balance.go` after real games, especially: ram
  vs. turret HP, knight vs. soldiers, and factory tempo vs. early rush.
- No latency compensation or client prediction (fine for this game's pacing;
  actions round-trip in milliseconds on LAN).
- No spectator mode, no disconnect forfeit (a disconnected player's army
  stands still; the other player can still win by breaching).
- Reconnecting works via `sessionStorage`, but two tabs of the *same* player
  will fight over their own seat (the newer one wins, the older is dropped).
- Canvas rendering is intentionally simple shapes + HP bars. If you want
  juice (particles, shake, sound), it's all in `web/client.js` (`draw*`).
