/* Lane Siege — browser client. Talks to the Go server over WebSocket.
 * The server is authoritative; this file renders state and sends intentions. */
'use strict';

// ---------------------------------------------------------------------------
// Canvas setup
// ---------------------------------------------------------------------------
const FIELD = { x0: 52, x1: 1108, y0: 14, lanes: 5, laneH: 116, gap: 2.4 };
const W = 1160, H = 620;
const SCALE = 2;
const UNIT_TYPES = ['soldier', 'bowman', 'knight', 'ram', 'siege'];
const HOTKEYS = { '1': 'soldier', '2': 'bowman', '3': 'knight', '4': 'ram', '5': 'siege', '6': 'turret', '7': 'factory' };

const cv = document.getElementById('battlefield');
const ctx = cv.getContext('2d');
cv.width = W * SCALE;
cv.height = H * SCALE;
ctx.scale(SCALE, SCALE);

const laneY = (lane) => FIELD.y0 + lane * FIELD.laneH + FIELD.laneH / 2;
const fieldX = (x) => FIELD.x0 + (x / 100) * (FIELD.x1 - FIELD.x0);

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
const state = {
  screen: 'lobby',
  ws: null,
  seat: null,          // 0 = A (left, blue), 1 = B (right, red)
  code: null,
  balance: null,
  snapNow: null,
  snapPrev: null,
  snapNowAt: 0,
  snapPrevAt: 0,
  selection: null,     // {kind:'unit'|'turret', unit?}
  hoverLane: null,
  rematchPending: false,
  feed: [],
  over: false,
};

const $ = (id) => document.getElementById(id);
const els = {
  lobby: $('screen-lobby'), game: $('screen-game'),
  lobbyError: $('lobby-error'),
  roomCode: $('room-code'), waitingCode: $('waiting-code'),
  waiting: $('waiting-overlay'), gameover: $('gameover-overlay'),
  resultTitle: $('result-title'), resultDetail: $('result-detail'),
  rematchBtn: $('btn-rematch'), rematchStatus: $('rematch-status'),
  rematchBtn2: null,
  clock: $('clock'), phase: $('phase-pill'), turretPower: $('turret-power'),
  sideBadge: $('side-badge'),
  coinsYou: $('coins-you'), coinsFoe: $('coins-foe'),
  incomeYou: $('income-you'), incomeFoe: $('income-foe'),
  shop: $('shop'), unitUpgrades: $('unit-upgrades'), turretUpgrades: $('turret-upgrades'),
  feed: $('feed'), toast: $('toast'),
  disconnect: $('disconnect-banner'),
  selHint: $('selection-hint'),
};

// ---------------------------------------------------------------------------
// Session persistence (refresh-safe reconnect)
// ---------------------------------------------------------------------------
const SS = {
  save(code, token, seat) {
    sessionStorage.setItem('laneSiege', JSON.stringify({ code, token, seat }));
  },
  load() {
    try { return JSON.parse(sessionStorage.getItem('laneSiege') || 'null'); } catch { return null; }
  },
  clear() { sessionStorage.removeItem('laneSiege'); },
};
let sessionToken = null;

// ---------------------------------------------------------------------------
// Networking
// ---------------------------------------------------------------------------
async function api(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : '{}',
  });
  let data = null;
  try { data = await res.json(); } catch { /* ignore */ }
  if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status})`);
  return data;
}

function connectWS(code, token) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws?room=${encodeURIComponent(code)}&token=${encodeURIComponent(token)}`);
  state.ws = ws;

  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    handleServerMessage(msg);
  };
  ws.onclose = () => {
    if (state.screen === 'game' && !state.over) {
      showToast('Connection lost. Refresh the page to rejoin.', false);
      els.disconnect.classList.remove('hidden');
    }
  };
  ws.onerror = () => {};
}

function send(obj) {
  if (state.ws && state.ws.readyState === WebSocket.OPEN) {
    state.ws.send(JSON.stringify(obj));
  }
}

function handleServerMessage(msg) {
  switch (msg.type) {
    case 'hello':
      state.seat = msg.seat;
      state.balance = msg.balance;
      state.code = msg.code;
      els.sideBadge.textContent = msg.seat === 0 ? 'YOU · BLUE' : 'YOU · RED';
      els.sideBadge.className = 'side-badge ' + (msg.seat === 0 ? 'a' : 'b');
      buildShop();
      buildUpgradePanels();
      break;
    case 'matchStart':
      els.waiting.classList.add('hidden');
      break;
    case 'snapshot':
      onSnapshot(msg);
      break;
    case 'matchReset':
      resetForRematch();
      break;
    case 'rematchStatus': {
      const mine = msg.seat === state.seat;
      els.rematchStatus.textContent = mine
        ? 'Rematch requested — waiting for opponent…'
        : 'Opponent wants a rematch!';
      break;
    }
    case 'opponentDisconnected':
      els.disconnect.classList.remove('hidden');
      break;
    case 'opponentConnected':
      els.disconnect.classList.add('hidden');
      break;
    case 'error':
      showToast(msg.message || 'Action rejected', false);
      break;
  }
}

// ---------------------------------------------------------------------------
// Snapshot handling + HUD
// ---------------------------------------------------------------------------
function onSnapshot(snap) {
  state.snapPrev = state.snapNow;
  state.snapPrevAt = state.snapNowAt;
  state.snapNow = snap;
  state.snapNowAt = performance.now();

  updateClock(snap);
  updateEconomyHUD(snap);
  updateUpgradeButtons(snap);
  updateTurretButtons(snap);
  const events = snap.events || [];
  events.forEach(addFeedItem);

  if (snap.finished && !state.over) {
    state.over = true;
    showGameOver(snap);
  }
}

function updateClock(snap) {
  const t = Math.max(0, snap.time);
  const mm = Math.floor(t / 60);
  const ss = Math.floor(t % 60).toString().padStart(2, '0');
  els.clock.textContent = `${mm}:${ss}`;
  els.clock.classList.toggle('overtime', !!snap.overtime);
  els.phase.textContent = snap.overtime ? 'OVERTIME' : (snap.time >= snap.overtimeStart - 30 ? 'SUDDEN DEATH SOON' : 'MATCH');
  els.phase.classList.toggle('overtime', !!snap.overtime);
  if (snap.overtime) {
    els.turretPower.classList.remove('hidden');
    els.turretPower.textContent = `Turrets at ${Math.round(snap.turretFactor * 100)}% damage`;
  } else {
    els.turretPower.classList.add('hidden');
  }
}

function otherSeat() { return state.seat === 0 ? 1 : 0; }

function updateEconomyHUD(snap) {
  const you = snap.players[state.seat];
  const foe = snap.players[otherSeat()];
  els.coinsYou.textContent = you.coins;
  els.coinsFoe.textContent = foe.coins;
  els.incomeYou.textContent = `+${you.income}/5s${you.factory ? ' 🏭' : ''}`;
  els.incomeFoe.textContent = `+${foe.income}/5s${foe.factory ? ' 🏭' : ''}`;
  // Refresh affordability styling without rebuilding the DOM.
  refreshAffordability(you.coins);
}

function addFeedItem(ev) {
  const li = document.createElement('li');
  li.className = ev.kind || '';
  li.textContent = ev.text;
  els.feed.prepend(li);
  while (els.feed.children.length > 7) els.feed.removeChild(els.feed.lastChild);
}

function showGameOver(snap) {
  state.rematchPending = false;
  // Lock out all purchase controls; the server rejects them anyway.
  document.querySelectorAll('.shop-item, .upgrade-btn, .turret-cell button').forEach((b) => { b.disabled = true; });
  const title = els.resultTitle;
  if (snap.draw) {
    title.textContent = 'DRAW';
    title.className = 'draw';
    els.resultDetail.textContent = snap.time >= snap.timeLimit
      ? 'Time ran out — neither army broke through.'
      : 'Both armies breached on the same tick.';
  } else if (snap.winner === state.seat) {
    title.textContent = 'VICTORY';
    title.className = 'win';
    els.resultDetail.textContent = `Your forces breached the enemy line in lane ${snap.winLane + 1}!`;
  } else {
    title.textContent = 'DEFEAT';
    title.className = 'lose';
    els.resultDetail.textContent = `The enemy breached in lane ${snap.winLane + 1}.`;
  }
  state.rematchPending = false;
  els.rematchBtn.disabled = false;
  els.rematchBtn.textContent = 'Rematch';
  els.rematchStatus.textContent = '';
  els.gameover.classList.remove('hidden');
}

function resetForRematch() {
  state.over = false;
  state.rematchPending = false;
  state.snapNow = null;
  state.snapPrev = null;
  els.gameover.classList.add('hidden');
  els.feed.innerHTML = '';
  els.shop.querySelectorAll('.shop-item').forEach((b) => { b.disabled = false; });
  els.turretUpgrades.querySelectorAll('button').forEach((b) => { b.disabled = false; });
  els.unitUpgrades.querySelectorAll('.upgrade-btn').forEach((b) => { b.disabled = false; });
  els.rematchBtn.disabled = false;
  els.rematchBtn.textContent = 'Rematch';
  showToast('Rematch started!', true);
}

// ---------------------------------------------------------------------------
// Shop / upgrades
// ---------------------------------------------------------------------------
function buildShop() {
  const b = state.balance;
  if (!b) return;
  els.shop.innerHTML = '';
  UNIT_TYPES.forEach((type, i) => {
    const u = b.units[type];
    const btn = document.createElement('button');
    btn.className = 'shop-item';
    btn.dataset.unit = type;
    btn.dataset.cost = u.cost;
    btn.innerHTML = `
      <span class="si-key">${i + 1}</span>
      <div class="si-top"><span class="si-name">${u.name}</span><span class="si-cost">${u.cost}c</span></div>
      <div class="si-role">${u.role}</div>`;
    btn.addEventListener('click', () => selectUnit(type));
    els.shop.appendChild(btn);
  });
  // Turret card
  const turretBtn = document.createElement('button');
  turretBtn.className = 'shop-item';
  turretBtn.dataset.cost = b.turret.cost;
  turretBtn.innerHTML = `
    <span class="si-key">6</span>
    <div class="si-top"><span class="si-name">${b.turret.name}</span><span class="si-cost">${b.turret.cost}c</span></div>
    <div class="si-role">Reliable lane defense. Cannot cover neighboring lanes.</div>`;
  turretBtn.addEventListener('click', () => selectTurret());
  els.shop.appendChild(turretBtn);
  // Factory card (spans full width, acts immediately)
  const fac = b.economy;
  const facBtn = document.createElement('button');
  facBtn.className = 'shop-item factory-item';
  facBtn.dataset.cost = fac.factoryCost;
  facBtn.dataset.factory = '1';
  facBtn.innerHTML = `
    <span class="si-key">7</span>
    <div class="si-top"><span class="si-name">Coin Factory</span><span class="si-cost">${fac.factoryCost}c</span></div>
    <div class="si-role">+${fac.factoryIncome} coins every 5s (payback ≈ ${fac.factoryPayback}s). One per player, no upgrades.</div>`;
  facBtn.addEventListener('click', () => send({ type: 'factory' }));
  els.shop.appendChild(facBtn);
}

function buildUpgradePanels() {
  const b = state.balance;
  if (!b) return;

  els.unitUpgrades.innerHTML = '';
  UNIT_TYPES.forEach((type) => {
    const u = b.units[type];
    const row = document.createElement('div');
    row.className = 'upgrade-row';
    row.dataset.unit = type;
    row.innerHTML = `
      <span class="u-name">${u.name}</span>
      <span class="pips"><span class="pip" data-pip="0"></span><span class="pip" data-pip="1"></span></span>
      <button class="upgrade-btn">Upgrade</button>`;
    row.querySelector('.upgrade-btn').addEventListener('click', () => {
      send({ type: 'upgradeUnit', unit: type });
    });
    els.unitUpgrades.appendChild(row);
  });

  els.turretUpgrades.innerHTML = '';
  for (let lane = 0; lane < 5; lane++) {
    const cell = document.createElement('div');
    cell.className = 'turret-cell';
    cell.dataset.lane = String(lane);
    cell.innerHTML = `
      <div class="tc-lane">LANE ${lane + 1}</div>
      <div class="tc-level">—</div>
      <button>Build</button>`;
    cell.querySelector('button').addEventListener('click', () => {
      send({ type: 'upgradeTurret', lane });
    });
    els.turretUpgrades.appendChild(cell);
  }
}

function refreshAffordability(coins) {
  els.shop.querySelectorAll('.shop-item').forEach((btn) => {
    if (state.over) { btn.disabled = true; return; }
    const cost = Number(btn.dataset.cost || '0');
    btn.classList.toggle('cant-afford', coins < cost);
    btn.disabled = coins < cost;
  });
}

function updateUpgradeButtons(snap) {
  const you = snap.players[state.seat];
  const b = state.balance;
  if (!b) return;
  els.unitUpgrades.querySelectorAll('.upgrade-row').forEach((row) => {
    const type = row.dataset.unit;
    const level = (you.unitUpgrades && you.unitUpgrades[type]) || 0;
    row.querySelectorAll('.pip').forEach((pip, i) => pip.classList.toggle('on', i < level));
    const btn = row.querySelector('.upgrade-btn');
    if (level >= 2) {
      btn.textContent = 'MAX';
      btn.disabled = true;
    } else {
      const cost = b.units[type].upgradeCosts[level];
      btn.textContent = `Lv${level + 1} · ${cost}c`;
      btn.disabled = you.coins < cost;
    }
  });
}

function updateTurretButtons(snap) {
  const b = state.balance;
  if (!b) return;
  const you = snap.players[state.seat];
  const turrets = snap.turrets || [];
  const myTurrets = {};
  turrets.forEach((t) => { if (t.side === state.seat) myTurrets[t.lane] = t; });
  els.turretUpgrades.querySelectorAll('.turret-cell').forEach((cell) => {
    const lane = Number(cell.dataset.lane);
    const t = myTurrets[lane];
    const lvlEl = cell.querySelector('.tc-level');
    const btn = cell.querySelector('button');
    if (!t) {
      lvlEl.textContent = '—';
      btn.textContent = 'No turret';
      btn.disabled = true;
      return;
    }
    lvlEl.textContent = '●'.repeat(t.level) + '○'.repeat(2 - t.level);
    if (t.level >= 2) {
      btn.textContent = 'MAX';
      btn.disabled = true;
    } else {
      const cost = b.turret.upgradeCosts[t.level];
      btn.textContent = `Up ${cost}c`;
      btn.disabled = you.coins < cost || snap.overtime;
    }
  });
  // Factory button state
  els.shop.querySelectorAll('[data-factory]').forEach((btn) => {
    btn.disabled = you.factory || you.coins < b.economy.factoryCost;
    if (you.factory) btn.querySelector('.si-role').textContent = 'Built ✓ (+5 coins every 5s)';
  });
}

function selectUnit(type) {
  state.selection = { kind: 'unit', unit: type };
  highlightSelection();
}
function selectTurret() {
  state.selection = { kind: 'turret' };
  highlightSelection();
}
function clearSelection() {
  state.selection = null;
  highlightSelection();
}
function highlightSelection() {
  const sel = state.selection;
  els.shop.querySelectorAll('.shop-item').forEach((btn) => {
    const isUnit = sel && sel.kind === 'unit' && btn.dataset.unit === sel.unit;
    const isTurret = sel && sel.kind === 'turret' && btn.querySelector('.si-name')?.textContent === 'Turret';
    btn.classList.toggle('selected', !!(isUnit || isTurret));
  });
  els.selHint.textContent = sel
    ? (sel.kind === 'turret'
        ? 'Click a lane to build the turret on your pad. Right-click / Esc to cancel.'
        : 'Click a lane to deploy. Right-click / Esc to cancel.')
    : 'Select a unit, then click a lane. Right-click / Esc to cancel.';
}

// ---------------------------------------------------------------------------
// Canvas rendering
// ---------------------------------------------------------------------------
function lerp(a, b, t) { return a + (b - a) * t; }

function unitRenderX(u) {
  const prev = state.snapPrev && state.snapPrev.units.find((p) => p.id === u.id);
  if (!prev) return u.x;
  const t0 = state.snapPrevAt, t1 = state.snapNowAt;
  const span = Math.max(1, t1 - t0);
  const gamma = Math.min(1.25, Math.max(0, (performance.now() - t0) / span));
  return lerp(prev.x, u.x, gamma);
}

function draw() {
  requestAnimationFrame(draw);
  ctx.clearRect(0, 0, W, H);
  drawField();
  const snap = state.snapNow;
  if (snap) {
    drawPads(snap);
    drawTurrets(snap);
    drawUnits(snap);
    if (snap.overtime) drawOvertimeTint();
  } else {
    drawIdleHint();
  }
}

function drawField() {
  // Background
  const bg = ctx.createLinearGradient(0, 0, 0, H);
  bg.addColorStop(0, '#101737');
  bg.addColorStop(1, '#0b1024');
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, W, H);

  // Lanes
  for (let lane = 0; lane < 5; lane++) {
    const y = FIELD.y0 + lane * FIELD.laneH;
    ctx.fillStyle = lane % 2 === 0 ? 'rgba(148,163,216,0.055)' : 'rgba(148,163,216,0.028)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, FIELD.laneH);
    // lane number
    ctx.fillStyle = '#5b6890';
    ctx.font = '700 12px "Segoe UI", sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(String(lane + 1), FIELD.x0 - 30, y + FIELD.laneH / 2 + 4);
    ctx.fillText(String(lane + 1), FIELD.x1 + 30, y + FIELD.laneH / 2 + 4);
  }

  // Breach zones (glow strips)
  drawBreachStrip(FIELD.x0 - 26, '#38bdf8', 'BREACH');
  drawBreachStrip(FIELD.x1 + 2, '#fb7185', 'BREACH');

  // Field divider dots (center line)
  ctx.strokeStyle = 'rgba(148,163,216,0.12)';
  ctx.setLineDash([4, 10]);
  ctx.beginPath();
  ctx.moveTo((FIELD.x0 + FIELD.x1) / 2, FIELD.y0);
  ctx.lineTo((FIELD.x0 + FIELD.x1) / 2, H - 14);
  ctx.stroke();
  ctx.setLineDash([]);

  // Hover lane highlight
  if (state.hoverLane != null && state.selection) {
    const y = FIELD.y0 + state.hoverLane * FIELD.laneH;
    ctx.fillStyle = state.seat === 0 ? 'rgba(56,189,248,0.10)' : 'rgba(251,113,133,0.10)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, FIELD.laneH - 1);
    ctx.strokeStyle = state.seat === 0 ? 'rgba(56,189,248,0.5)' : 'rgba(251,113,133,0.5)';
    ctx.strokeRect(FIELD.x0 + 0.5, y + 0.5, FIELD.x1 - FIELD.x0 - 1, FIELD.laneH - 1);
  }
}

function drawBreachStrip(x, color, label) {
  const g = ctx.createLinearGradient(x, 0, x + 24, 0);
  g.addColorStop(0, color + '00');
  g.addColorStop(0.5, color + '55');
  g.addColorStop(1, color + '00');
  ctx.fillStyle = g;
  ctx.fillRect(x, 0, 24, H);
  ctx.save();
  ctx.translate(x + 12, H / 2);
  ctx.rotate(-Math.PI / 2);
  ctx.fillStyle = color;
  ctx.globalAlpha = 0.75;
  ctx.font = '800 13px "Segoe UI", sans-serif';
  ctx.textAlign = 'center';
  ctx.letterSpacing = '6px';
  ctx.fillText(label, 0, 4);
  ctx.restore();
  ctx.globalAlpha = 1;
}

function drawPads(snap) {
  for (let lane = 0; lane < 5; lane++) {
    const y = laneY(lane);
    for (const side of [0, 1]) {
      const occupied = (snap.turrets || []).some((t) => t.side === side && t.lane === lane);
      if (occupied) continue;
      const unitX = side === 0 ? state.balance.turret.padX : 100 - state.balance.turret.padX;
      const px = fieldX(unitX);
      ctx.save();
      ctx.setLineDash([5, 5]);
      ctx.strokeStyle = side === state.seat ? 'rgba(125,211,252,0.5)' : 'rgba(148,163,184,0.25)';
      ctx.lineWidth = 1.6;
      roundRect(ctx, px - 17, y - 17, 34, 34, 7);
      ctx.stroke();
      ctx.restore();
    }
  }
}

function drawTurrets(snap) {
  for (const t of (snap.turrets || [])) {
    const px = fieldX(t.side === 0 ? state.balance.turret.padX : 100 - state.balance.turret.padX);
    const py = laneY(t.lane);
    const mine = t.side === state.seat;
    const body = t.side === 0 ? '#1d4ed8' : '#9f1239';
    const trim = t.side === 0 ? '#7dd3fc' : '#fda4af';

    // base
    ctx.fillStyle = '#0f172a';
    roundRect(ctx, px - 17, py - 13, 34, 30, 6);
    ctx.fill();
    ctx.fillStyle = body;
    roundRect(ctx, px - 15, py - 15, 30, 30, 6);
    ctx.fill();
    ctx.strokeStyle = trim;
    ctx.lineWidth = 2;
    roundRect(ctx, px - 15, py - 15, 30, 30, 6);
    ctx.stroke();
    // barrel
    const dir = t.side === 0 ? 1 : -1;
    ctx.strokeStyle = trim;
    ctx.lineWidth = 5;
    ctx.beginPath();
    ctx.moveTo(px, py);
    ctx.lineTo(px + dir * 17, py);
    ctx.stroke();
    // level pips
    for (let i = 0; i < t.level; i++) {
      ctx.fillStyle = '#fde047';
      ctx.beginPath();
      ctx.arc(px - 8 + i * 8, py + 24, 2.5, 0, Math.PI * 2);
      ctx.fill();
    }
    drawHealthBar(px - 20, py - 26, 40, 5, t.hp / t.maxHp, mine);
  }
}

function drawUnits(snap) {
  for (const u of (snap.units || [])) {
    const px = fieldX(unitRenderX(u));
    const py = laneY(u.lane);
    const mine = u.side === state.seat;
    const body = u.side === 0 ? '#38bdf8' : '#fb7185';
    const dark = u.side === 0 ? '#075985' : '#7f1d1d';
    const dir = u.side === 0 ? 1 : -1;

    ctx.save();
    ctx.translate(px, py);
    ctx.scale(dir, 1); // face the enemy

    ctx.lineWidth = 2;
    ctx.strokeStyle = dark;
    ctx.fillStyle = body;
    switch (u.type) {
      case 'soldier':
        ctx.beginPath(); ctx.arc(0, 0, 10, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(2, -2, 4.5, Math.PI, 0); ctx.fill(); // helmet
        break;
      case 'bowman':
        ctx.beginPath(); ctx.arc(0, 0, 9, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.strokeStyle = '#fbbf24';
        ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(6, 0, 8, -Math.PI / 2.4, Math.PI / 2.4); ctx.stroke(); // bow
        break;
      case 'knight':
        ctx.beginPath();
        ctx.moveTo(0, -12); ctx.lineTo(11, 0); ctx.lineTo(0, 12); ctx.lineTo(-11, 0);
        ctx.closePath(); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(0, 0, 4, 0, Math.PI * 2); ctx.fill();
        break;
      case 'ram':
        ctx.fillRect(-16, -9, 26, 18);
        ctx.strokeRect(-16, -9, 26, 18);
        ctx.beginPath(); // ram wedge
        ctx.moveTo(10, -11); ctx.lineTo(22, 0); ctx.lineTo(10, 11); ctx.closePath();
        ctx.fillStyle = '#b45309'; ctx.fill(); ctx.stroke();
        break;
      case 'siege':
        ctx.beginPath();
        ctx.moveTo(-14, 8); ctx.lineTo(-9, -8); ctx.lineTo(9, -8); ctx.lineTo(14, 8);
        ctx.closePath(); ctx.fill(); ctx.stroke();
        ctx.strokeStyle = '#e2e8f0'; ctx.lineWidth = 3;
        ctx.beginPath(); ctx.moveTo(0, -4); ctx.lineTo(17, -10); ctx.stroke(); // barrel
        ctx.fillStyle = '#0f172a';
        ctx.beginPath(); ctx.arc(-8, 9, 3.4, 0, Math.PI * 2); ctx.arc(8, 9, 3.4, 0, Math.PI * 2); ctx.fill();
        break;
    }
    ctx.restore();

    drawHealthBar(px - 14, py - 19, 28, 4, u.hp / u.maxHp, mine);
  }
}

function drawHealthBar(x, y, w, h, frac, mine) {
  frac = Math.max(0, Math.min(1, frac));
  ctx.fillStyle = 'rgba(2,6,23,0.8)';
  ctx.fillRect(x - 1, y - 1, w + 2, h + 2);
  ctx.fillStyle = frac > 0.55 ? '#34d399' : frac > 0.25 ? '#facc15' : '#f87171';
  ctx.fillRect(x, y, w * frac, h);
}

function drawOvertimeTint() {
  ctx.fillStyle = 'rgba(220, 38, 38, 0.05)';
  ctx.fillRect(0, 0, W, H);
  ctx.strokeStyle = 'rgba(248, 113, 113, 0.55)';
  ctx.lineWidth = 4;
  ctx.strokeRect(2, 2, W - 4, H - 4);
}

function drawIdleHint() {
  ctx.fillStyle = 'rgba(226,232,240,0.5)';
  ctx.font = '600 18px "Segoe UI", sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText('Waiting for the battle to begin…', W / 2, H / 2);
}

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

// lanes from canvas Y
function laneFromEvent(ev) {
  const rect = cv.getBoundingClientRect();
  const y = (ev.clientY - rect.top) * (H / rect.height);
  const lane = Math.floor((y - FIELD.y0) / FIELD.laneH);
  return lane >= 0 && lane < 5 ? lane : null;
}

// ---------------------------------------------------------------------------
// Toast
// ---------------------------------------------------------------------------
let toastTimer = null;
function showToast(msg, ok) {
  els.toast.textContent = msg;
  els.toast.classList.toggle('ok', !!ok);
  els.toast.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => els.toast.classList.add('hidden'), 2600);
}

// ---------------------------------------------------------------------------
// Lobby flow
// ---------------------------------------------------------------------------
function showScreen(name) {
  state.screen = name;
  els.lobby.classList.toggle('hidden', name !== 'lobby');
  els.game.classList.toggle('hidden', name !== 'game');
}

async function enterGame(code, token, seat) {
  state.code = code;
  state.seat = seat;
  sessionToken = token;
  SS.save(code, token, seat);
  els.roomCode.textContent = code;
  els.waitingCode.textContent = code;
  els.waiting.classList.remove('hidden');
  els.gameover.classList.add('hidden');
  showScreen('game');
  connectWS(code, token);
}

async function onCreate() {
  els.lobbyError.textContent = '';
  try {
    const res = await api('/api/rooms', {});
    await enterGame(res.code, res.token, res.seat);
  } catch (e) {
    els.lobbyError.textContent = e.message;
  }
}

async function onJoin(ev) {
  ev.preventDefault();
  els.lobbyError.textContent = '';
  const code = $('join-code').value.trim().toUpperCase();
  if (code.length !== 4) {
    els.lobbyError.textContent = 'Room codes are 4 letters.';
    return;
  }
  try {
    const res = await api(`/api/rooms/${encodeURIComponent(code)}/join`, {});
    await enterGame(res.code, res.token, res.seat);
  } catch (e) {
    els.lobbyError.textContent = e.message;
  }
}

async function tryResume() {
  const saved = SS.load();
  if (!saved) return;
  try {
    const res = await api(`/api/rooms/${encodeURIComponent(saved.code)}/rejoin`, { token: saved.token });
    await enterGame(res.code, res.token, res.seat);
  } catch {
    SS.clear();
  }
}

function backToMenu() {
  SS.clear();
  if (state.ws) { try { state.ws.close(); } catch {} }
  state.ws = null;
  state.snapNow = null;
  state.snapPrev = null;
  state.over = false;
  state.feed = [];
  els.feed.innerHTML = '';
  els.disconnect.classList.add('hidden');
  showScreen('lobby');
}

// ---------------------------------------------------------------------------
// Wire up events
// ---------------------------------------------------------------------------
$('btn-create').addEventListener('click', onCreate);
$('join-form').addEventListener('submit', onJoin);
$('btn-quit').addEventListener('click', backToMenu);
$('btn-back-menu').addEventListener('click', backToMenu);
$('btn-rematch').addEventListener('click', () => {
  if (state.rematchPending) return;
  state.rematchPending = true;
  els.rematchBtn.disabled = true;
  els.rematchBtn.textContent = 'Waiting…';
  els.rematchStatus.textContent = 'Rematch requested — waiting for opponent…';
  send({ type: 'rematch' });
});

$('btn-copy').addEventListener('click', () => copyCode());
$('btn-copy-2').addEventListener('click', () => copyCode());
function copyCode() {
  if (state.code) {
    navigator.clipboard?.writeText(state.code);
    showToast('Room code copied', true);
  }
}

cv.addEventListener('click', (ev) => {
  const lane = laneFromEvent(ev);
  if (lane === null || !state.selection) return;
  if (state.selection.kind === 'unit') {
    send({ type: 'deploy', unit: state.selection.unit, lane });
  } else if (state.selection.kind === 'turret') {
    send({ type: 'turret', lane });
  }
});
cv.addEventListener('mousemove', (ev) => { state.hoverLane = laneFromEvent(ev); });
cv.addEventListener('mouseleave', () => { state.hoverLane = null; });
cv.addEventListener('contextmenu', (ev) => { ev.preventDefault(); clearSelection(); });

window.addEventListener('keydown', (ev) => {
  if (state.screen !== 'game') return;
  if (ev.key === 'Escape') { clearSelection(); return; }
  const unit = HOTKEYS[ev.key];
  if (!unit) return;
  if (unit === 'factory') { send({ type: 'factory' }); return; }
  if (unit === 'turret') { selectTurret(); return; }
  selectUnit(unit);
});

// boot
requestAnimationFrame(draw);
tryResume();
