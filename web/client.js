/* Lane Siege — browser client. Talks to the Go server over WebSocket.
 * The server is authoritative; this file renders state, plays juice, and
 * sends intentions. */
'use strict';

// ---------------------------------------------------------------------------
// Canvas setup
// ---------------------------------------------------------------------------
const FIELD = { x0: 52, x1: 1108, y0: 14, lanes: 5, laneH: 116 };
const W = 1160, H = 620;
const SCALE = 2;
const UNIT_TYPES = ['soldier', 'bowman', 'knight', 'ram', 'siege'];
const HOTKEYS = { '1': 'soldier', '2': 'bowman', '3': 'knight', '4': 'ram', '5': 'siege', '6': 'turret', '7': 'factory' };
const LANE_H = FIELD.laneH;

const cv = document.getElementById('battlefield');
const ctx = cv.getContext('2d');
cv.width = W * SCALE;
cv.height = H * SCALE;
ctx.scale(SCALE, SCALE);

const confettiCv = document.getElementById('confetti');
const fctx = confettiCv.getContext('2d');

const laneY = (lane) => FIELD.y0 + lane * LANE_H + LANE_H / 2;
const unitY = (u) => laneY(u.lane) + (u.yOff || 0) * LANE_H;
const fieldX = (x) => FIELD.x0 + (x / 100) * (FIELD.x1 - FIELD.x0);

// ---------------------------------------------------------------------------
// Audio (Web Audio, no assets, mutable)
// ---------------------------------------------------------------------------
const SFX = (() => {
  let ctxA = null;
  let master = null;
  let muted = false;
  try { muted = localStorage.getItem('laneSiegeMuted') === '1'; } catch {}

  function ensure() {
    if (!ctxA) {
      try {
        ctxA = new (window.AudioContext || window.webkitAudioContext)();
        master = ctxA.createGain();
        master.gain.value = 0.5;
        master.connect(ctxA.destination);
      } catch { return null; }
    }
    if (ctxA.state === 'suspended') ctxA.resume().catch(() => {});
    return ctxA;
  }
  function env(t0, a, peak, d) {
    const g = ctxA.createGain();
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.linearRampToValueAtTime(peak, t0 + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + a + d);
    return g;
  }
  function tone({ type = 'sine', f = 440, f1 = null, t = 0.15, vol = 0.2, delay = 0 }) {
    if (muted) return;
    const c = ensure(); if (!c) return;
    const t0 = c.currentTime + delay;
    const o = c.createOscillator();
    o.type = type;
    o.frequency.setValueAtTime(f, t0);
    if (f1) o.frequency.exponentialRampToValueAtTime(Math.max(1, f1), t0 + t);
    const g = env(t0, 0.005, vol, t);
    o.connect(g).connect(master);
    o.start(t0); o.stop(t0 + t + 0.05);
  }
  function noise({ t = 0.25, vol = 0.25, cutoff = 1200, delay = 0 }) {
    if (muted) return;
    const c = ensure(); if (!c) return;
    const t0 = c.currentTime + delay;
    const len = Math.max(1, Math.floor(c.sampleRate * t));
    const buf = c.createBuffer(1, len, c.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = c.createBufferSource();
    src.buffer = buf;
    const filter = c.createBiquadFilter();
    filter.type = 'lowpass';
    filter.frequency.value = cutoff;
    const g = env(t0, 0.004, vol, t);
    src.connect(filter).connect(g).connect(master);
    src.start(t0);
  }
  let lastHit = 0;
  return {
    unlock() { ensure(); },
    toggleMute() {
      muted = !muted;
      try { localStorage.setItem('laneSiegeMuted', muted ? '1' : '0'); } catch {}
      return muted;
    },
    isMuted: () => muted,
    click()  { tone({ type: 'square', f: 760, t: 0.04, vol: 0.05 }); },
    deploy() { tone({ type: 'triangle', f: 180, f1: 110, t: 0.14, vol: 0.28 }); tone({ type: 'sine', f: 520, t: 0.06, vol: 0.08, delay: 0.02 }); },
    build()  { tone({ type: 'square', f: 240, t: 0.07, vol: 0.16 }); tone({ type: 'square', f: 170, t: 0.09, vol: 0.16, delay: 0.07 }); },
    upgrade(){ tone({ type: 'triangle', f: 523, t: 0.07, vol: 0.18 }); tone({ type: 'triangle', f: 784, t: 0.09, vol: 0.18, delay: 0.06 }); },
    error()  { tone({ type: 'sawtooth', f: 140, f1: 90, t: 0.18, vol: 0.16 }); },
    arrow()  { tone({ type: 'sawtooth', f: 900 + Math.random() * 200, f1: 420, t: 0.05, vol: 0.07 }); },
    turretShot() { tone({ type: 'square', f: 500 + Math.random() * 120, t: 0.05, vol: 0.12 }); },
    shell()  { tone({ type: 'sine', f: 120, f1: 42, t: 0.45, vol: 0.5 }); noise({ t: 0.3, vol: 0.28, cutoff: 500 }); },
    hit() {
      const n = performance.now();
      if (n - lastHit < 70) return;
      lastHit = n;
      tone({ type: 'triangle', f: 320, f1: 170, t: 0.05, vol: 0.14 });
    },
    poof()   { tone({ type: 'sine', f: 430, f1: 120, t: 0.16, vol: 0.2 }); noise({ t: 0.08, vol: 0.1, cutoff: 900 }); },
    boom()   { noise({ t: 0.45, vol: 0.4, cutoff: 700 }); tone({ type: 'sine', f: 82, f1: 34, t: 0.5, vol: 0.5 }); },
    windfall() { [659, 784, 988].forEach((f, i) => tone({ type: 'triangle', f, t: 0.09, vol: 0.22, delay: i * 0.07 })); },
    firstBlood() { tone({ type: 'sawtooth', f: 196, f1: 147, t: 0.45, vol: 0.3 }); },
    breach() { [147, 147, 196].forEach((f, i) => tone({ type: 'sawtooth', f, t: 0.28, vol: 0.35, delay: i * 0.22 })); },
    win()    { [523, 659, 784, 1047].forEach((f, i) => tone({ type: 'triangle', f, t: 0.22, vol: 0.3, delay: i * 0.13 })); },
    lose()   { [392, 330, 262].forEach((f, i) => tone({ type: 'triangle', f, t: 0.3, vol: 0.3, delay: i * 0.18 })); },
  };
})();

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
const state = {
  screen: 'lobby',
  ws: null,
  seat: null,
  code: null,
  balance: null,
  snapNow: null,
  snapPrev: null,
  snapNowAt: 0,
  snapPrevAt: 0,
  selection: null,
  hoverLane: null,
  rematchPending: false,
  feed: [],
  over: false,
  hoveredEntity: null,
};

// FX pools (coordinates: x in field units, y in screen px offset from lane center)
const fx = {
  projectiles: [],
  particles: [],
  floaters: [],
  flashes: [],       // {x, y, r, until, color}
  rings: [],         // expanding shockwaves {x, y, r, maxR, life, maxLife}
  hpCache: new Map(),// key -> {hp, x, lane, yOff, side, kind}
  shake: 0,
  lastAlarmAt: 0,
};

const $ = (id) => document.getElementById(id);
const els = {
  lobby: $('screen-lobby'), game: $('screen-game'),
  lobbyError: $('lobby-error'),
  roomCode: $('room-code'), waitingCode: $('waiting-code'),
  waiting: $('waiting-overlay'), gameover: $('gameover-overlay'),
  resultTitle: $('result-title'), resultDetail: $('result-detail'),
  rematchBtn: $('btn-rematch'), rematchStatus: $('rematch-status'),
  resignBtn: $('btn-resign'),
  clock: $('clock'), phase: $('phase-pill'), turretPower: $('turret-power'),
  sideBadge: $('side-badge'),
  coinsYou: $('coins-you'), coinsFoe: $('coins-foe'),
  incomeYou: $('income-you'), incomeFoe: $('income-foe'),
  coinChip: $('coin-chip-you'),
  shop: $('shop'), unitUpgrades: $('unit-upgrades'), turretUpgrades: $('turret-upgrades'),
  feed: $('feed'), toast: $('toast'),
  disconnect: $('disconnect-banner'),
  selHint: $('selection-hint'),
  windfall: $('windfall-banner'),
  breachWarn: $('breach-warning'),
  muteBtn: $('btn-mute'),
};

// ---------------------------------------------------------------------------
// Session persistence
// ---------------------------------------------------------------------------
const SS = {
  save(code, token, seat) {
    try { sessionStorage.setItem('laneSiege', JSON.stringify({ code, token, seat })); } catch {}
  },
  load() {
    try { return JSON.parse(sessionStorage.getItem('laneSiege') || 'null'); } catch { return null; }
  },
  clear() { try { sessionStorage.removeItem('laneSiege'); } catch {} },
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
    return true;
  }
  showToast('Not connected — refresh to rejoin.', false);
  return false;
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
      SFX.error();
      showToast(msg.message || 'Action rejected', false);
      break;
  }
}

// ---------------------------------------------------------------------------
// Snapshots: HUD + FX ingestion
// ---------------------------------------------------------------------------
function onSnapshot(snap) {
  const prev = state.snapNow;
  state.snapPrev = prev;
  state.snapPrevAt = state.snapNowAt;
  state.snapNow = snap;
  state.snapNowAt = performance.now();

  ingestFx(prev, snap);
  updateClock(snap);
  updateEconomyHUD(snap);
  updateUpgradeButtons(snap);
  updateTurretButtons(snap);
  updateBreachWarning(snap);
  for (const ev of snap.events || []) handleEvent(ev);

  if (snap.finished && !state.over) {
    state.over = true;
    showGameOver(snap);
  }
}

function handleEvent(ev) {
  switch (ev.kind) {
    case 'windfall':
      showWindfallBanner(ev.amount || 0);
      SFX.windfall();
      els.coinChip && els.coinChip.classList.add('coin-pulse');
      setTimeout(() => els.coinChip && els.coinChip.classList.remove('coin-pulse'), 900);
      break;
    case 'firstBlood':
      SFX.firstBlood();
      break;
    case 'turretDestroyed':
      // Explosion already spawned from hp diff; just the horn-ish thud.
      break;
    case 'overtime':
      SFX.firstBlood(); // dramatic low horn signals the phase change
      break;
    case 'breach':
      SFX.breach();
      fx.shake = Math.max(fx.shake, 9);
      break;
    case 'draw':
      SFX.breach();
      break;
    case 'resign':
      break;
  }
  addFeedItem(ev);
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
  refreshAffordability(you.coins);
}

// Red pulsing warning when an enemy closing in on *your* breach line.
function updateBreachWarning(snap) {
  if (snap.finished) { els.breachWarn.classList.add('hidden'); return; }
  let best = null;
  for (const u of snap.units || []) {
    if (u.side === state.seat) continue;
    const dist = state.seat === 0 ? u.x : 100 - u.x;
    if (best === null || dist < best.dist) best = { dist, lane: u.lane };
  }
  const DANGER = 18;
  if (best && best.dist < DANGER) {
    els.breachWarn.textContent = `⚠ BREACH IMMINENT — LANE ${best.lane + 1}`;
    els.breachWarn.classList.remove('hidden');
    const now = performance.now();
    if (now - fx.lastAlarmAt > 2500) {
      fx.lastAlarmAt = now;
      SFX.firstBlood();
    }
  } else {
    els.breachWarn.classList.add('hidden');
  }
}

function addFeedItem(ev) {
  const li = document.createElement('li');
  li.className = ev.kind || '';
  li.textContent = ev.text;
  els.feed.prepend(li);
  while (els.feed.children.length > 8) els.feed.removeChild(els.feed.lastChild);
}

function showGameOver(snap) {
  const iWon = !snap.draw && snap.winner === state.seat;
  // Reveal confetti canvas + launch if victorious
  if (iWon) launchConfetti();
  const title = els.resultTitle;
  if (snap.draw) {
    title.textContent = 'DRAW';
    title.className = 'draw';
    els.resultDetail.textContent = snap.time >= snap.timeLimit
      ? 'Time ran out — neither army broke through.'
      : 'Both armies breached on the same tick.';
    SFX.breach();
  } else if (iWon) {
    title.textContent = 'VICTORY';
    title.className = 'win';
    els.resultDetail.textContent = snap.winLane >= 0
      ? `Your forces breached the enemy line in lane ${snap.winLane + 1}!`
      : 'Your opponent resigned.';
    SFX.win();
  } else {
    title.textContent = 'DEFEAT';
    title.className = 'lose';
    els.resultDetail.textContent = snap.winLane >= 0
      ? `The enemy breached in lane ${snap.winLane + 1}.`
      : 'You resigned.';
    SFX.lose();
  }
  document.querySelectorAll('.shop-item, .upgrade-btn, .turret-cell button').forEach((b) => { b.disabled = true; });
  state.rematchPending = false;
  els.rematchBtn.disabled = false;
  els.rematchBtn.textContent = 'Rematch';
  els.rematchStatus.textContent = '';
  els.resignBtn.disabled = true;
  els.gameover.classList.remove('hidden');
}

function resetForRematch() {
  state.over = false;
  state.rematchPending = false;
  state.snapNow = null;
  state.snapPrev = null;
  fx.projectiles.length = 0;
  fx.particles.length = 0;
  fx.floaters.length = 0;
  fx.hpCache.clear();
  fx.shake = 0;
  els.gameover.classList.add('hidden');
  els.feed.innerHTML = '';
  els.shop.querySelectorAll('.shop-item').forEach((b) => { b.disabled = false; });
  els.turretUpgrades.querySelectorAll('button').forEach((b) => { b.disabled = false; });
  els.unitUpgrades.querySelectorAll('.upgrade-btn').forEach((b) => { b.disabled = false; });
  els.resignBtn.disabled = false;
  resetResignButton();
  els.rematchBtn.disabled = false;
  els.rematchBtn.textContent = 'Rematch';
  showToast('Rematch started!', true);
}

// ---------------------------------------------------------------------------
// Shop / upgrades + snapshot FX ingestion
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
    btn.innerHTML =
      '<span class="si-key">' + (i + 1) + '</span>' +
      '<div class="si-top"><span class="si-name">' + u.name + '</span><span class="si-cost">' + u.cost + 'c</span></div>' +
      '<div class="si-role">' + u.role + '</div>';
    btn.addEventListener('click', () => { SFX.click(); selectUnit(type); });
    els.shop.appendChild(btn);
  });
  const turretBtn = document.createElement('button');
  turretBtn.className = 'shop-item';
  turretBtn.dataset.cost = b.turret.cost;
  turretBtn.innerHTML =
    '<span class="si-key">6</span>' +
    '<div class="si-top"><span class="si-name">' + b.turret.name + '</span><span class="si-cost">' + b.turret.cost + 'c</span></div>' +
    '<div class="si-role">Reliable lane defense. Cannot cover neighboring lanes.</div>';
  turretBtn.addEventListener('click', () => { SFX.click(); selectTurret(); });
  els.shop.appendChild(turretBtn);
  const fac = b.economy;
  const facBtn = document.createElement('button');
  facBtn.className = 'shop-item factory-item';
  facBtn.dataset.cost = fac.factoryCost;
  facBtn.dataset.factory = '1';
  facBtn.innerHTML =
    '<span class="si-key">7</span>' +
    '<div class="si-top"><span class="si-name">Coin Factory</span><span class="si-cost">' + fac.factoryCost + 'c</span></div>' +
    '<div class="si-role">+' + fac.factoryIncome + ' coins every 5s (payback ≈ ' + fac.factoryPayback + 's). One per player.</div>';
  facBtn.addEventListener('click', () => { SFX.click(); send({ type: 'factory' }); });
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
    row.innerHTML =
      '<span class="u-name">' + u.name + '</span>' +
      '<span class="pips"><span class="pip" data-pip="0"></span><span class="pip" data-pip="1"></span></span>' +
      '<button class="upgrade-btn">Upgrade</button>';
    row.querySelector('.upgrade-btn').addEventListener('click', () => { SFX.click(); send({ type: 'upgradeUnit', unit: type }); });
    els.unitUpgrades.appendChild(row);
  });

  els.turretUpgrades.innerHTML = '';
  for (let lane = 0; lane < 5; lane++) {
    const cell = document.createElement('div');
    cell.className = 'turret-cell';
    cell.dataset.lane = String(lane);
    cell.innerHTML =
      '<div class="tc-lane">LANE ' + (lane + 1) + '</div>' +
      '<div class="tc-level">—</div>' +
      '<button>Build</button>';
    cell.querySelector('button').addEventListener('click', () => { SFX.click(); send({ type: 'upgradeTurret', lane }); });
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
      btn.textContent = 'Lv' + (level + 1) + ' · ' + cost + 'c';
      btn.disabled = you.coins < cost || state.over;
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
    if (state.over) { btn.disabled = true; return; }
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
      btn.textContent = 'Up ' + cost + 'c';
      btn.disabled = you.coins < cost || snap.overtime;
    }
  });
  els.shop.querySelectorAll('[data-factory]').forEach((btn) => {
    btn.disabled = state.over || you.factory || you.coins < b.economy.factoryCost;
    if (you.factory) btn.querySelector('.si-role').textContent = 'Built ✓ (+5 coins every 5s)';
  });
}

function selectUnit(type) { state.selection = { kind: 'unit', unit: type }; highlightSelection(); }
function selectTurret() { state.selection = { kind: 'turret' }; highlightSelection(); }
function clearSelection() { state.selection = null; highlightSelection(); }
function highlightSelection() {
  const sel = state.selection;
  els.shop.querySelectorAll('.shop-item').forEach((btn) => {
    const isUnit = sel && sel.kind === 'unit' && btn.dataset.unit === sel.unit;
    const isTurret = sel && sel.kind === 'turret' && !btn.dataset.unit && !btn.dataset.factory;
    btn.classList.toggle('selected', !!(isUnit || isTurret));
  });
  els.selHint.textContent = sel
    ? (sel.kind === 'turret' ? 'Click a lane to build the turret on your pad. Right-click / Esc to cancel.'
                             : 'Click a lane to deploy. Right-click / Esc to cancel.')
    : 'Select a unit, then click a lane. Right-click / Esc to cancel.';
}

// ---- FX ingestion (called from onSnapshot) ----

function entityKey(kind, ref) { return kind + ':' + ref; }

function ingestFx(prev, snap) {
  const seen = new Set();
  const now = performance.now();

  const track = (key, fxX, lane, yOff, hp, side, kind) => {
    seen.add(key);
    const before = fx.hpCache.get(key);
    if (before) {
      const lost = before.hp - hp;
      if (lost > 0.5) {
        const dispY = laneY(lane) + (yOff || 0) * LANE_H - 16;
        fx.floaters.push({
          x: px(fxX), y: dispY, vy: -52, life: 0.85, maxLife: 0.85,
          text: '-' + Math.round(lost),
          color: side === state.seat ? '#fda4af' : '#fde68a',
        });
        fx.flashes.push({ x: px(fxX), y: laneY(lane) + (yOff || 0) * LANE_H, r: 16, until: now + 110, color: 'rgba(255,255,255,0.55)' });
        SFX.hit();
      }
    }
    fx.hpCache.set(key, { hp, x: fxX, lane, yOff: yOff || 0, side, kind });
  };

  for (const u of snap.units || []) track(entityKey('u', u.id), u.x, u.lane, u.yOff, u.hp, u.side, 'unit');
  for (const t of snap.turrets || []) track(entityKey('t', t.side + ':' + t.lane), turretX(t.side), t.lane, 0, t.hp, t.side, 'turret');

  // Entities present before but gone now → death poof / turret explosion.
  for (const [key, info] of [...fx.hpCache]) {
    if (seen.has(key)) continue;
    fx.hpCache.delete(key);
    const dy = laneY(info.lane) + info.yOff * LANE_H;
    if (info.kind === 'turret') {
      boomAt(px(info.x), laneY(info.lane), 1.4);
      fx.shake = Math.max(fx.shake, 7);
      SFX.boom();
    } else {
      poofAt(px(info.x), dy, 7);
      SFX.poof();
    }
  }

  // Server-authored shots / melee → projectiles, muzzle flashes, sparks.
  for (const f of snap.fx || []) {
    if (f.kind === 'shot') {
      spawnProjectile(f);
      const isSiege = f.unit === 'siege';
      const isTurret = f.unit === 'turret';
      fx.flashes.push({
        x: px(f.fromX), y: laneY(f.lane) - 4,
        r: isSiege ? 16 : isTurret ? 9 : 7,
        until: now + (isSiege ? 150 : 90),
        color: isSiege ? 'rgba(253,224,71,0.9)' : 'rgba(254,240,138,0.85)',
      });
      if (isSiege) SFX.shell(); else if (isTurret) SFX.turretShot(); else SFX.arrow();
    } else if (f.kind === 'melee') {
      meleeSparks(f.toX, f.lane, 4);
    }
  }
}

// ---------------------------------------------------------------------------
// FX: projectiles, particles, floaters, flashes, shake
// All FX positions are PIXEL coordinates on the canvas (unless noted).
// ---------------------------------------------------------------------------
function turretX(side) {
  return side === 0 ? 10 : 90;
}
const px = (fieldX) => (fieldX / 100) * (FIELD.x1 - FIELD.x0) + FIELD.x0;

function spawnProjectile(f) {
  const y = laneY(f.lane) - 4;
  const x0 = px(f.fromX), x1 = px(f.toX);
  const dist = Math.abs(x1 - x0);
  let kind = 'arrow', dur = Math.min(0.5, Math.max(0.16, dist / 620)), arc = 14;
  if (f.unit === 'siege') { kind = 'shell'; dur = Math.min(1.15, Math.max(0.45, dist / 430)); arc = 78; }
  else if (f.unit === 'turret') { kind = 'bullet'; dur = Math.max(0.07, dist / 980); arc = 0; }
  fx.projectiles.push({
    kind, x0, x1, y0: y, y1: y, y, t: 0, dur, arc,
    dir: Math.sign(x1 - x0) || 1,
  });
}

function meleeSparks(fieldXPos, lane, n) {
  const x = px(fieldXPos), y = laneY(lane);
  n = n || 4;
  for (let i = 0; i < n; i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = 40 + Math.random() * 60;
    fx.particles.push({
      x, y: y + (Math.random() - 0.5) * 10,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 30,
      grav: 320, life: 0.32, maxLife: 0.32,
      size: 1.4 + Math.random() * 1.5, color: '#fde68a',
    });
  }
}

function poofAt(x, y, n) {
  for (let i = 0; i < (n || 7); i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = 16 + Math.random() * 46;
    fx.particles.push({
      x, y,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 24,
      grav: 55, life: 0.55, maxLife: 0.55,
      size: 2 + Math.random() * 3, color: 'rgba(203,213,225,0.85)',
    });
  }
}

function boomAt(x, y, scale) {
  scale = scale || 1;
  for (let i = 0; i < Math.floor(16 * scale); i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = (45 + Math.random() * 130) * scale;
    fx.particles.push({
      x, y,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 40,
      grav: 230, life: 0.6 + Math.random() * 0.3, maxLife: 0.9,
      size: (2 + Math.random() * 3.4) * scale,
      color: ['#fde68a', '#fb923c', '#f87171', '#e2e8f0'][Math.floor(Math.random() * 4)],
    });
  }
  fx.rings.push({ x, y, r: 6, maxR: 46 * scale, life: 0.45, maxLife: 0.45 });
}

function stepFx(dt) {
  for (let i = fx.projectiles.length - 1; i >= 0; i--) {
    const p = fx.projectiles[i];
    p.t += dt / p.dur;
    if (p.kind === 'shell' && Math.random() < 0.55) {
      const pos = projPos(p, Math.min(1, p.t));
      fx.particles.push({
        x: pos.x, y: pos.y - 2,
        vx: (Math.random() - 0.5) * 24, vy: -8 - Math.random() * 12,
        grav: 0, life: 0.38, maxLife: 0.38, size: 1.5 + Math.random(),
        color: 'rgba(148,163,184,0.65)',
      });
    }
    if (p.t >= 1) {
      if (p.kind === 'shell') {
        boomAt(p.x1, p.y, 1.15);
        fx.shake = Math.max(fx.shake, 5);
        SFX.boom();
      } else {
        // small impact puff for arrows/bullets
        for (let k = 0; k < 3; k++) {
          const a = Math.random() * Math.PI * 2;
          fx.particles.push({
            x: p.x1, y: p.y, vx: Math.cos(a) * 45, vy: Math.sin(a) * 45 - 15,
            grav: 260, life: 0.22, maxLife: 0.22, size: 1.3, color: '#fef9c3',
          });
        }
      }
      fx.projectiles.splice(i, 1);
    }
  }
  for (let i = fx.particles.length - 1; i >= 0; i--) {
    const p = fx.particles[i];
    p.life -= dt;
    if (p.life <= 0) { fx.particles.splice(i, 1); continue; }
    p.vy += (p.grav || 0) * dt;
    p.x += p.vx * dt;
    p.y += p.vy * dt;
  }
  for (let i = fx.rings.length - 1; i >= 0; i--) {
    const r = fx.rings[i];
    r.life -= dt;
    r.r += (r.maxR * 2.2) * dt;
    if (r.life <= 0) fx.rings.splice(i, 1);
  }
  for (let i = fx.floaters.length - 1; i >= 0; i--) {
    const f = fx.floaters[i];
    f.life -= dt;
    f.y += f.vy * dt;
    f.vy *= 0.94;
    if (f.life <= 0) fx.floaters.splice(i, 1);
  }
  const now = performance.now();
  fx.flashes = fx.flashes.filter((f) => f.until > now);
  fx.shake *= Math.pow(0.0015, dt);
  if (fx.shake < 0.25) fx.shake = 0;
}

function projPos(p, t) {
  const tt = Math.min(Math.max(t, 0), 1);
  return {
    x: p.x0 + (p.x1 - p.x0) * tt,
    y: p.y - Math.sin(Math.PI * tt) * p.arc,
  };
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------
function unitRenderX(u) {
  const prev = state.snapPrev && state.snapPrev.units.find((p) => p.id === u.id);
  if (!prev) return u.x;
  const t0 = state.snapPrevAt, t1 = state.snapNowAt;
  const span = Math.max(1, t1 - t0);
  const gamma = Math.min(1.25, Math.max(0, (performance.now() - t0) / span));
  return prev.x + (u.x - prev.x) * gamma;
}

const P = () => performance.now();
let lastFrame = 0;

function draw() {
  requestAnimationFrame(draw);
  const now = performance.now();
  const dt = lastFrame ? Math.min(0.05, (now - lastFrame) / 1000) : 0.016;
  lastFrame = now;
  stepFx(dt);

  ctx.clearRect(0, 0, W, H);
  ctx.save();
  if (fx.shake > 0) {
    ctx.translate((Math.random() - 0.5) * fx.shake, (Math.random() - 0.5) * fx.shake);
  }
  drawField(now);
  const snap = state.snapNow;
  if (snap) {
    drawPads(snap);
    drawTurrets(snap, now);
    drawUnits(snap, now);
    drawProjectiles();
    drawParticles();
    drawRings();
    drawFlashes(now);
    drawFloaters();
    if (snap.overtime) drawOvertimeTint();
  } else {
    drawIdleHint();
  }
  ctx.restore();
}

function drawField(now) {
  const bg = ctx.createLinearGradient(0, 0, 0, H);
  bg.addColorStop(0, '#111838');
  bg.addColorStop(0.5, '#0e1430');
  bg.addColorStop(1, '#0b1024');
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, W, H);

  for (let lane = 0; lane < 5; lane++) {
    const y = FIELD.y0 + lane * LANE_H;
    ctx.fillStyle = lane % 2 === 0 ? 'rgba(148,163,216,0.055)' : 'rgba(148,163,216,0.028)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, LANE_H);
    // lane separator
    ctx.fillStyle = 'rgba(148,163,216,0.10)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, 1);
    ctx.fillStyle = '#66729b';
    ctx.font = '700 12px "Segoe UI", system-ui, sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(String(lane + 1), FIELD.x0 - 30, y + LANE_H / 2 + 4);
    ctx.fillText(String(lane + 1), FIELD.x1 + 30, y + LANE_H / 2 + 4);
  }

  // Pulsing breach glow strips
  const pulse = 0.55 + 0.3 * Math.sin(now / 480);
  drawBreachStrip(FIELD.x0 - 26, '#38bdf8', 'BREACH', pulse);
  drawBreachStrip(FIELD.x1 + 2, '#fb7185', 'BREACH', pulse);

  // Center line
  ctx.strokeStyle = 'rgba(148,163,216,0.12)';
  ctx.setLineDash([4, 12]);
  ctx.beginPath();
  ctx.moveTo((FIELD.x0 + FIELD.x1) / 2, FIELD.y0);
  ctx.lineTo((FIELD.x0 + FIELD.x1) / 2, H - 14);
  ctx.stroke();
  ctx.setLineDash([]);

  // Hover-lane highlight (deployment aid)
  if (state.hoverLane != null && state.selection) {
    const y = FIELD.y0 + state.hoverLane * LANE_H;
    ctx.fillStyle = state.seat === 0 ? 'rgba(56,189,248,0.10)' : 'rgba(251,113,133,0.10)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, LANE_H);
    ctx.strokeStyle = state.seat === 0 ? 'rgba(56,189,248,0.55)' : 'rgba(251,113,133,0.55)';
    ctx.lineWidth = 1.5;
    ctx.strokeRect(FIELD.x0 + 0.5, y + 0.5, FIELD.x1 - FIELD.x0 - 1, LANE_H - 1);
  }
}

function drawBreachStrip(x, color, label, alpha) {
  const g = ctx.createLinearGradient(x, 0, x + 24, 0);
  g.addColorStop(0, color + '00');
  g.addColorStop(0.5, color + Math.round(alpha * 255).toString(16).padStart(2, '0'));
  g.addColorStop(1, color + '00');
  ctx.fillStyle = g;
  ctx.fillRect(x, 0, 24, H);
  ctx.save();
  ctx.translate(x + 12, H / 2);
  ctx.rotate(-Math.PI / 2);
  ctx.fillStyle = color;
  ctx.globalAlpha = Math.min(1, alpha + 0.25);
  ctx.font = '800 13px "Segoe UI", system-ui, sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText(label.split('').join(' '), 0, 4);
  ctx.restore();
  ctx.globalAlpha = 1;
}

function drawPads(snap) {
  const padX = [10, 90]; // field units per side
  for (let lane = 0; lane < 5; lane++) {
    for (const side of [0, 1]) {
      const occupied = (snap.turrets || []).some((t) => t.side === side && t.lane === lane);
      if (occupied) continue;
      const p = px(padX[side]);
      const y = laneY(lane);
      ctx.save();
      ctx.setLineDash([5, 5]);
      ctx.strokeStyle = side === state.seat ? 'rgba(125,211,252,0.45)' : 'rgba(148,163,184,0.22)';
      ctx.lineWidth = 1.5;
      roundRect(ctx, p - 16, y - 16, 32, 32, 7);
      ctx.stroke();
      ctx.restore();
    }
  }
}

function drawTurrets(snap, now) {
  for (const t of snap.turrets || []) {
    const p = px(t.side === 0 ? 10 : 90);
    const py = laneY(t.lane);
    const trim = t.side === 0 ? '#7dd3fc' : '#fda4af';
    const body = t.side === 0 ? '#1e4fd8' : '#b91c3c';
    const bodyDark = t.side === 0 ? '#152f6e' : '#6f1526';

    // recoil: look for a fresh muzzle flash for this turret
    let recoil = 0;
    for (const f of fx.flashes) {
      if (Math.abs(f.x - p) < 2 && Math.abs(f.y - (py - 4)) < 2) {
        recoil = Math.max(0, (f.until - now) / 90) * 4;
      }
    }
    const dir = t.side === 0 ? 1 : -1;

    // shadow
    ctx.fillStyle = 'rgba(0,0,0,0.35)';
    ctx.beginPath();
    ctx.ellipse(p, py + 14, 20, 6, 0, 0, Math.PI * 2);
    ctx.fill();

    // base + body
    ctx.fillStyle = '#0f172a';
    roundRect(ctx, p - 17 - dir * recoil, py - 14, 34, 30, 6); ctx.fill();
    ctx.fillStyle = body;
    roundRect(ctx, p - 15 - dir * recoil, py - 16, 30, 30, 6); ctx.fill();
    ctx.fillStyle = bodyDark;
    roundRect(ctx, p - 15 - dir * recoil, py + 2, 30, 12, 6); ctx.fill();
    ctx.strokeStyle = trim;
    ctx.lineWidth = 2;
    roundRect(ctx, p - 15 - dir * recoil, py - 16, 30, 30, 6); ctx.stroke();

    // barrel
    ctx.strokeStyle = trim;
    ctx.lineWidth = 5;
    ctx.beginPath();
    ctx.moveTo(p - dir * recoil, py - 2);
    ctx.lineTo(p + dir * (18 - recoil), py - 2);
    ctx.stroke();

    // level pips
    for (let i = 0; i < t.level; i++) {
      ctx.fillStyle = '#fde047';
      ctx.beginPath();
      ctx.arc(p - 8 + i * 8, py + 25, 2.4, 0, Math.PI * 2);
      ctx.fill();
    }
    drawHealthBar(p - 21, py - 28, 42, 5, t.hp / t.maxHp);
  }
}

function drawUnits(snap, now) {
  for (const u of snap.units || []) {
    const ux = px(unitRenderX(u));
    const uy = laneY(u.lane) + (u.yOff || 0) * LANE_H;
    const body = u.side === 0 ? '#38bdf8' : '#fb7185';
    const dark = u.side === 0 ? '#0c4a6e' : '#881337';
    const dir = u.side === 0 ? 1 : -1;

    // walk bob
    const prev = state.snapPrev && state.snapPrev.units.find((p) => p.id === u.id);
    const moving = prev ? Math.abs(u.x - prev.x) > 0.005 : true;
    const bob = moving ? Math.sin(now * 0.012 + u.id * 1.7) * 1.8 : 0;

    // shadow
    ctx.fillStyle = 'rgba(0,0,0,0.32)';
    ctx.beginPath();
    ctx.ellipse(ux, uy + 12, 13, 4, 0, 0, Math.PI * 2);
    ctx.fill();

    ctx.save();
    ctx.translate(ux, uy + bob);
    ctx.scale(dir, 1);
    ctx.lineWidth = 2;
    ctx.strokeStyle = dark;
    ctx.fillStyle = body;

    switch (u.type) {
      case 'soldier':
        ctx.beginPath(); ctx.arc(0, 0, 9.5, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(1.5, -2, 5, Math.PI, 0); ctx.fill();
        // spear
        ctx.strokeStyle = '#e2e8f0'; ctx.lineWidth = 1.6;
        ctx.beginPath(); ctx.moveTo(4, 3); ctx.lineTo(11, -7); ctx.stroke();
        break;
      case 'bowman':
        ctx.beginPath(); ctx.arc(0, 0, 9, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(0, -3, 4.4, Math.PI, 0); ctx.fill();
        // bow
        ctx.strokeStyle = '#fbbf24'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(7, 0, 8, -Math.PI / 2.3, Math.PI / 2.3); ctx.stroke();
        ctx.strokeStyle = '#e7e5e4'; ctx.lineWidth = 1;
        ctx.beginPath(); ctx.moveTo(9.2, -6.4); ctx.lineTo(9.2, 6.4); ctx.stroke();
        break;
      case 'knight':
        ctx.beginPath();
        ctx.moveTo(0, -12); ctx.lineTo(11, 0); ctx.lineTo(0, 12); ctx.lineTo(-11, 0);
        ctx.closePath(); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(0, 0, 4.2, 0, Math.PI * 2); ctx.fill();
        // lance
        ctx.strokeStyle = '#e2e8f0'; ctx.lineWidth = 1.6;
        ctx.beginPath(); ctx.moveTo(6, 2); ctx.lineTo(15, -6); ctx.stroke();
        break;
      case 'ram':
        ctx.fillStyle = '#b45309';
        roundRect(ctx, -17, -9, 24, 18, 3); ctx.fill();
        ctx.strokeStyle = '#451a03';
        ctx.lineWidth = 2;
        roundRect(ctx, -17, -9, 24, 18, 3); ctx.stroke();
        // ram head
        ctx.fillStyle = '#d6d3d1';
        ctx.beginPath();
        ctx.moveTo(7, -8); ctx.lineTo(20, 0); ctx.lineTo(7, 8); ctx.closePath(); ctx.fill(); ctx.stroke();
        // wheels
        ctx.fillStyle = '#1c1917';
        ctx.beginPath(); ctx.arc(-9, 9, 3.6, 0, Math.PI * 2); ctx.arc(1, 9, 3.6, 0, Math.PI * 2); ctx.fill();
        break;
      case 'siege':
        ctx.beginPath();
        ctx.moveTo(-15, 8); ctx.lineTo(-10, -8); ctx.lineTo(10, -8); ctx.lineTo(15, 8);
        ctx.closePath(); ctx.fill(); ctx.stroke();
        // barrel
        ctx.strokeStyle = '#e2e8f0'; ctx.lineWidth = 3.4;
        ctx.beginPath(); ctx.moveTo(2, -3); ctx.lineTo(20, -9); ctx.stroke();
        // wheels
        ctx.fillStyle = '#0f172a';
        ctx.beginPath(); ctx.arc(-9, 8.5, 4, 0, Math.PI * 2); ctx.arc(9, 8.5, 4, 0, Math.PI * 2); ctx.fill();
        ctx.fillStyle = '#64748b';
        ctx.beginPath(); ctx.arc(-9, 8.5, 1.6, 0, Math.PI * 2); ctx.arc(9, 8.5, 1.6, 0, Math.PI * 2); ctx.fill();
        break;
    }
    ctx.restore();

    drawHealthBar(ux - 13, uy - 18, 26, 3.6, u.hp / u.maxHp);
  }
}

function drawProjectiles() {
  for (const p of fx.projectiles) {
    const pos = projPos(p, p.t);
    if (p.kind === 'bullet') {
      ctx.save();
      ctx.shadowColor = '#fef08a';
      ctx.shadowBlur = 6;
      ctx.fillStyle = '#fef9c3';
      ctx.beginPath();
      ctx.arc(pos.x, pos.y, 2.4, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    } else if (p.kind === 'arrow') {
      const angle = Math.atan2(
        (projPos(p, Math.min(1, p.t + 0.02)).y) - pos.y,
        (projPos(p, Math.min(1, p.t + 0.02)).x) - pos.x
      );
      ctx.save();
      ctx.translate(pos.x, pos.y);
      ctx.rotate(angle);
      ctx.strokeStyle = '#e7c576';
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(-7, 0); ctx.lineTo(6, 0); ctx.stroke();
      ctx.beginPath(); ctx.moveTo(6, 0); ctx.lineTo(2, -3); ctx.moveTo(6, 0); ctx.lineTo(2, 3); ctx.stroke();
      ctx.restore();
    } else if (p.kind === 'shell') {
      ctx.save();
      ctx.shadowColor = '#fb923c';
      ctx.shadowBlur = 8;
      ctx.fillStyle = '#f8fafc';
      ctx.beginPath();
      ctx.arc(pos.x, pos.y, 4.5, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    }
  }
}

function drawParticles() {
  for (const p of fx.particles) {
    ctx.globalAlpha = Math.max(0, p.life / p.maxLife);
    ctx.fillStyle = p.color;
    ctx.beginPath();
    ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.globalAlpha = 1;
}

function drawRings() {
  for (const r of fx.rings) {
    ctx.globalAlpha = Math.max(0, r.life / r.maxLife) * 0.9;
    ctx.strokeStyle = '#fdba74';
    ctx.lineWidth = 3;
    ctx.beginPath();
    ctx.arc(r.x, r.y, r.r, 0, Math.PI * 2);
    ctx.stroke();
  }
  ctx.globalAlpha = 1;
}

function drawFlashes(now) {
  for (const f of fx.flashes) {
    const a = Math.max(0, (f.until - now) / 110);
    const g = ctx.createRadialGradient(f.x, f.y, 0, f.x, f.y, f.r);
    g.addColorStop(0, f.color);
    g.addColorStop(1, 'rgba(255,255,255,0)');
    ctx.globalAlpha = a;
    ctx.fillStyle = g;
    ctx.beginPath();
    ctx.arc(f.x, f.y, f.r, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.globalAlpha = 1;
}

function drawFloaters() {
  ctx.textAlign = 'center';
  ctx.font = '800 12px "Segoe UI", system-ui, sans-serif';
  for (const f of fx.floaters) {
    ctx.globalAlpha = Math.max(0, f.life / f.maxLife);
    ctx.lineWidth = 3;
    ctx.strokeStyle = 'rgba(2,6,23,0.8)';
    ctx.strokeText(f.text, f.x, f.y);
    ctx.fillStyle = f.color;
    ctx.fillText(f.text, f.x, f.y);
  }
  ctx.globalAlpha = 1;
}

function drawHealthBar(x, y, w, h, frac) {
  frac = Math.max(0, Math.min(1, frac));
  ctx.fillStyle = 'rgba(2,6,23,0.75)';
  ctx.fillRect(x - 1, y - 1, w + 2, h + 2);
  ctx.fillStyle = frac > 0.55 ? '#34d399' : frac > 0.25 ? '#facc15' : '#f87171';
  ctx.fillRect(x, y, w * frac, h);
}

function drawOvertimeTint() {
  ctx.fillStyle = 'rgba(220, 38, 38, 0.06)';
  ctx.fillRect(0, 0, W, H);
  ctx.strokeStyle = 'rgba(248, 113, 113, 0.6)';
  ctx.lineWidth = 4;
  ctx.strokeRect(2, 2, W - 4, H - 4);
}

function drawIdleHint() {
  ctx.fillStyle = 'rgba(226,232,240,0.5)';
  ctx.font = '600 18px "Segoe UI", system-ui, sans-serif';
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

// ---------------------------------------------------------------------------
// Windfall banner + confetti + toast
// ---------------------------------------------------------------------------
let windfallTimer = null;
function showWindfallBanner(amount) {
  els.windfall.textContent = `💰 Windfall! Both players +${amount} coins`;
  els.windfall.classList.remove('hidden');
  els.windfall.classList.remove('pop');
  void els.windfall.offsetWidth; // restart animation
  els.windfall.classList.add('pop');
  clearTimeout(windfallTimer);
  windfallTimer = setTimeout(() => els.windfall.classList.add('hidden'), 2400);
}

function showToast(msg, ok) {
  els.toast.textContent = msg;
  els.toast.classList.toggle('ok', !!ok);
  els.toast.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => els.toast.classList.add('hidden'), 2800);
}
let toastTimer = null;

const confetti = { running: false, parts: [] };
const CONFETTI_COLORS = ['#38bdf8', '#f43f5e', '#fbbf24', '#34d399', '#e879f9', '#f8fafc', '#fda4af'];

function launchConfetti() {
  const c = confettiCv;
  const dpr = window.devicePixelRatio || 1;
  c.width = window.innerWidth * dpr;
  c.height = window.innerHeight * dpr;
  c.style.width = window.innerWidth + 'px';
  c.style.height = window.innerHeight + 'px';
  fctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  c.classList.remove('hidden');
  const Wpx = window.innerWidth;
  for (let i = 0; i < 170; i++) {
    confetti.parts.push({
      x: Math.random() * Wpx,
      y: -20 - Math.random() * window.innerHeight * 0.55,
      vx: (Math.random() - 0.5) * 110,
      vy: 80 + Math.random() * 150,
      w: 6 + Math.random() * 8,
      h: 8 + Math.random() * 12,
      rot: Math.random() * Math.PI,
      vr: (Math.random() - 0.5) * 6,
      sway: Math.random() * Math.PI * 2,
      color: CONFETTI_COLORS[Math.floor(Math.random() * CONFETTI_COLORS.length)],
    });
  }
  if (!confetti.running) {
    confetti.running = true;
    let last = performance.now();
    const tick = (now) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      fctx.clearRect(0, 0, window.innerWidth, window.innerHeight);
      let alive = 0;
      for (const p of confetti.parts) {
        p.sway += dt * 2.2;
        p.x += (p.vx + Math.sin(p.sway) * 35) * dt;
        p.y += p.vy * dt;
        p.rot += p.vr * dt;
        if (p.y > window.innerHeight + 30) continue;
        alive++;
        fctx.save();
        fctx.translate(p.x, p.y);
        fctx.rotate(p.rot);
        fctx.fillStyle = p.color;
        fctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
        fctx.restore();
      }
      if (alive > 0) {
        requestAnimationFrame(tick);
      } else {
        confetti.running = false;
        confetti.parts = [];
        fctx.clearRect(0, 0, window.innerWidth, window.innerHeight);
        c.classList.add('hidden');
      }
    };
    requestAnimationFrame(tick);
  }
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
  SFX.click();
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
  SFX.click();
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
  fx.hpCache.clear();
  fx.projectiles.length = 0;
  fx.particles.length = 0;
  fx.floaters.length = 0;
  fx.rings.length = 0;
  fx.flashes.length = 0;
  fx.shake = 0;
  els.feed.innerHTML = '';
  els.disconnect.classList.add('hidden');
  resetResignButton();
  els.resignBtn.disabled = false;
  SFX.click();
  showScreen('lobby');
}

// ---------------------------------------------------------------------------
// Input wiring
// ---------------------------------------------------------------------------
function laneFromEvent(ev) {
  const rect = cv.getBoundingClientRect();
  const y = (ev.clientY - rect.top) * (H / rect.height);
  const lane = Math.floor((y - FIELD.y0) / LANE_H);
  return lane >= 0 && lane < 5 ? lane : null;
}

$('btn-create').addEventListener('click', onCreate);
$('join-form').addEventListener('submit', onJoin);
$('btn-quit').addEventListener('click', backToMenu);
$('btn-back-menu').addEventListener('click', backToMenu);
$('btn-rematch').addEventListener('click', () => {
  if (state.rematchPending) return;
  SFX.click();
  state.rematchPending = true;
  els.rematchBtn.disabled = true;
  els.rematchBtn.textContent = 'Waiting…';
  els.rematchStatus.textContent = 'Rematch requested — waiting for opponent…';
  send({ type: 'rematch' });
});

// Resign: two-step confirm so nobody throws a match by accident.
let resignArmed = false;
let resignTimer = null;
function resetResignButton() {
  resignArmed = false;
  clearTimeout(resignTimer);
  els.resignBtn.textContent = 'Resign';
  els.resignBtn.classList.remove('confirm');
}
els.resignBtn.addEventListener('click', () => {
  SFX.click();
  if (!resignArmed) {
    resignArmed = true;
    els.resignBtn.textContent = 'Confirm resign?';
    els.resignBtn.classList.add('confirm');
    clearTimeout(resignTimer);
    resignTimer = setTimeout(resetResignButton, 3000);
    return;
  }
  resetResignButton();
  send({ type: 'resign' });
});

$('btn-copy').addEventListener('click', () => copyCode());
$('btn-copy-2').addEventListener('click', () => copyCode());
function copyCode() {
  if (state.code) {
    navigator.clipboard?.writeText(state.code);
    SFX.click();
    showToast('Room code copied', true);
  }
}

els.muteBtn.addEventListener('click', () => {
  const muted = SFX.toggleMute();
  els.muteBtn.textContent = muted ? '🔇' : '🔊';
  if (!muted) SFX.click();
});

cv.addEventListener('click', (ev) => {
  const lane = laneFromEvent(ev);
  if (lane === null || !state.selection) return;
  SFX.unlock();
  if (state.selection.kind === 'unit') {
    SFX.deploy();
    send({ type: 'deploy', unit: state.selection.unit, lane });
  } else if (state.selection.kind === 'turret') {
    SFX.build();
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
  SFX.unlock();
  if (unit === 'factory') { send({ type: 'factory' }); return; }
  if (unit === 'turret') { selectTurret(); return; }
  SFX.click();
  selectUnit(unit);
});

// Browsers require a user gesture before audio can start.
document.addEventListener('pointerdown', () => SFX.unlock(), { once: true });

// boot
els.muteBtn.textContent = SFX.isMuted() ? '🔇' : '🔊';
requestAnimationFrame(draw);
tryResume();
