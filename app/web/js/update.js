// 检查更新:顶栏小提示 + 「…」菜单里的「检查更新」+ 更新面板 + 重启遮罩。
// 自成一个模块(index.html 单独引入),和 app.js 只通过 DOM 打交道:
//   · 静态文案用 data-i18n,app.js 切语言时会一起换;动态内容监听 <html data-lang> 自己重画;
//   · 后端接口见 internal/launcher/update_routes.go。
import { t, getLang } from './i18n.js';
import { getJSON } from './api.js';
import { fmtBytes, fmtDuration, escapeHTML } from './text.js';
import { pickNotes, miniMD, relTime } from './updatefmt.js';

const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const ACTIVE_MODEL = ['hashing', 'downloading', 'waiting', 'installing'];

const U = {
  st: null,
  open: false,
  timer: 0,
  manual: false, // 用户手动点了检查:检查完给个结果提示
  sawChecking: false,
  restarting: null, // { version, from, t0, down }
  resultHandled: '',
  opener: null,
  busyAct: '',
  applying: false, // 点了「重启并更新」:接下来连不上就是在重启
};

// ───────────────────────── 拉状态 ─────────────────────────
async function refresh() {
  clearTimeout(U.timer);
  if (U.restarting) return;
  try {
    U.st = await getJSON('/app/update', { timeout: 5000 });
    afterFetch();
    renderAll();
  } catch {
    // 启动器暂时连不上:平时由 app.js 显示断开;刚点过「重启并更新」则说明已经在重启
    if (U.applying && !U.restarting) { startRestart(U.applyingVersion || ''); return; }
  }
  U.timer = setTimeout(refresh, nextDelay());
}

function isActive(st) {
  if (!st) return false;
  return st.checking || st.busy || ['downloading', 'preparing', 'restarting'].includes(st.app.state) ||
    st.models.some((m) => ACTIVE_MODEL.includes(m.state));
}

// 刚因为更新重启过:新版本起来时旧进程还在收尾,结局(update-result.json)晚一两秒才写,这段时间勤快点查
let afterRestartUntil = 0;
try {
  const v = sessionStorage.getItem('hz.upd.restarted');
  if (v) { sessionStorage.removeItem('hz.upd.restarted'); afterRestartUntil = Date.now() + 20000; }
} catch { /* 无痕模式等 */ }

function nextDelay() {
  if (Date.now() < afterRestartUntil && !U.st?.result) return 1000;
  const active = isActive(U.st);
  if (U.open) return active ? 700 : 3000;
  if (active) return 2000;
  return document.hidden ? 300000 : 60000;
}

document.addEventListener('visibilitychange', () => { if (!document.hidden && !U.restarting) refresh(); });

async function post(action, body) {
  try {
    const r = await fetch('/app/update/' + action, {
      method: 'POST', headers: { 'X-Humanizer': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}),
    });
    let d = null;
    try { d = await r.json(); } catch { /* 空响应 */ }
    if (!r.ok) return { ok: false, code: d?.error?.code || 'failed', message: d?.error?.message || `HTTP ${r.status}` };
    return { ok: true };
  } catch (e) {
    return { ok: false, code: 'offline', message: e.message };
  }
}

function afterFetch() {
  const st = U.st;
  if (st.app.state === 'restarting' && !U.restarting) startRestart(st.app.latest?.version || '');
  if (U.applying && st.app.state !== 'preparing' && st.app.state !== 'restarting') U.applying = false; // 准备失败,留在原版本
  // 上一次「重启并更新」的结局:成功 → 轻提示一下;失败 → 打开面板说明(关面板时确认)
  const r = st.result;
  if (r && U.resultHandled !== r.at) {
    U.resultHandled = r.at;
    if (r.ok) { toast(t('upd.toast.done', { v: r.to })); post('ack'); }
    else openPanel(true);
  }
  if (st.checking) U.sawChecking = true;
  if (U.manual && U.sawChecking && !st.checking) {
    U.manual = false;
    U.sawChecking = false;
    if (!st.error && !st.app.available && !st.models.some((m) => m.state === 'available')) toast(t('upd.toast.latest'));
  }
}

// ───────────────────────── 提示点 / 菜单 ─────────────────────────
function availableModels(st) { return st.models.filter((m) => m.state === 'available' || m.state === 'paused'); }

// 「稍后提醒」记的是这一组更新的签名:出现新版本或新模型时提示会再出现
function updateSig(st) {
  const parts = [];
  if (st.app.available && st.app.latest) parts.push('app:' + st.app.latest.version);
  for (const m of availableModels(st)) parts.push(m.tier + ':' + (m.remote_sha256 || m.remote_size || '').toString().slice(0, 12));
  return parts.join(',');
}

function chipInfo(st) {
  const a = st.app;
  if (a.state === 'downloading' && a.progress) return { text: t('upd.chip.dl', { p: pct(a.progress.received, a.progress.total) }), busy: true, force: true };
  if (a.state === 'ready' && a.available && (a.mode === 'replace' || a.mode === 'installer') && !a.fallback) return { text: t('upd.chip.ready'), force: true };
  if (a.available && a.latest && !st.dev) return { text: t('upd.chip.app', { v: escapeHTML(a.latest.version) }) };
  if (st.models.some((m) => m.state === 'downloading')) return { text: t('upd.chip.model'), busy: true, force: true };
  if (availableModels(st).length) return { text: t('upd.chip.model') };
  return null;
}

function renderChip() {
  const st = U.st, chip = $('upd-chip');
  const info = st && chipInfo(st);
  const dismissed = info && !info.force && st.dismissed && st.dismissed === updateSig(st);
  const show = !!info && !dismissed;
  chip.hidden = !show;
  if (show) {
    $('upd-chip-text').textContent = info.text;
    chip.title = info.text;
    chip.setAttribute('aria-label', info.text);
    if (info.busy) chip.dataset.busy = '1'; else delete chip.dataset.busy;
  }
  $('btn-menu').classList.toggle('has-upd', show);
  const badge = $('pa-update-badge');
  const hasApp = st && st.app.available && st.app.latest && !st.dev;
  const hasModel = st && availableModels(st).length > 0;
  badge.hidden = !(hasApp || hasModel);
  badge.textContent = hasApp ? st.app.latest.version : (hasModel ? t('upd.newBadge') : '');
  $('pi-version').textContent = st ? st.current : '—';
}

// ───────────────────────── 面板 ─────────────────────────
function openPanel(open, opener) {
  if (open === U.open) { if (open) renderPanel(); return; }
  U.open = open;
  $('upd').hidden = !open;
  $('upd-scrim').hidden = !open;
  if (open) {
    U.opener = opener || document.activeElement;
    renderPanel();
    requestAnimationFrame(() => $('upd-close').focus({ preventScroll: true }));
    refresh();
  } else {
    // 关面板:确认掉已经看过的结局(失败说明、已完成的模型更新)
    if (U.st?.result && !U.st.result.ok) post('ack');
    for (const m of U.st?.models || []) if (m.state === 'done' || m.state === 'error') post('model-ack', { tier: m.tier });
    if (U.opener && document.contains(U.opener)) U.opener.focus({ preventScroll: true });
    refresh();
  }
}

function renderAll() {
  renderChip();
  if (U.open) renderPanel();
}

function renderPanel() {
  const st = U.st;
  if (!st) {
    $('upd-app').innerHTML = stateLine('busy', 'i-redo', t('upd.checking'), '');
    $('upd-models').innerHTML = '';
    return;
  }
  renderNotices(st);
  // 正在交互的按钮(比如刚点了下载)保持焦点:只在内容真变了时重画
  setHTML($('upd-app'), renderApp(st));
  setHTML($('upd-models'), renderModels(st));
  $('upd-auto').checked = !!st.auto;
  const checking = st.checking;
  const btn = $('upd-check');
  btn.disabled = checking || U.busyAct === 'check';
  btn.classList.toggle('spinning', checking);
  $('upd-check-text').textContent = checking ? t('upd.checking') : t('upd.checkNow');
  const rel = relTime(st.last_check, Date.now(), getLang());
  $('upd-last').textContent = ' · ' + (rel ? t('upd.lastCheck', { t: rel }) : t('upd.never'));
  scrollCues();
}

// 面板内容超出时的上下淡影
function scrollCues() {
  const b = $('upd-body'), u = $('upd');
  u.toggleAttribute('data-up', b.scrollTop > 2);
  u.toggleAttribute('data-more', b.scrollTop + b.clientHeight < b.scrollHeight - 2);
}

function setHTML(el, html) {
  if (el.dataset.html === html) return;
  const focusKey = document.activeElement?.closest?.('#upd') ? document.activeElement.dataset?.act + '|' + (document.activeElement.dataset?.tier || '') : '';
  el.innerHTML = html;
  el.dataset.html = html;
  if (focusKey) {
    const [act, tier] = focusKey.split('|');
    const again = el.querySelector(`[data-act="${act}"]${tier ? `[data-tier="${tier}"]` : ''}`);
    if (again) again.focus({ preventScroll: true });
  }
}

function renderNotices(st) {
  const out = [];
  const r = st.result;
  if (r && !r.ok) {
    out.push(['err', t('upd.result.fail', { to: escapeHTML(r.to), from: escapeHTML(r.from) }), r.error]);
  }
  if (st.app.fallback) out.push(['warn', t('upd.manual.fallback'), '']);
  if (st.error && !st.checking) {
    const k = st.error.startsWith('http_') ? 'upd.err.http' : 'upd.err.' + st.error;
    const msg = t(k, { code: escapeHTML(st.error.replace('http_', 'HTTP ')) });
    if (msg !== k) out.push(['warn', msg, '']);
  }
  const box = $('upd-notice');
  // 启动器给的原始说明(中文)收进「技术细节」,和出错页的做法一样
  const html = out.map(([kind, msg, detail]) => `<div class="upd-notice ${kind}">${msg}` +
    (detail ? `<details class="upd-detail"><summary>${t('err.detail')}</summary><pre>${escapeHTML(detail)}</pre></details>` : '') + '</div>').join('');
  box.hidden = !html;
  // 多条提示:外层容器只是个占位,里面每条自带样式
  box.className = '';
  setHTML(box, html);
}

function stateLine(kind, icon, title, sub) {
  return `<div class="upd-state ${kind}"><span class="ico"><svg><use href="#${icon}"/></svg></span>` +
    `<div class="upd-state-text"><b>${title}</b>${sub ? `<span>${sub}</span>` : ''}</div></div>`;
}

function secHead(idx, title, meta) {
  return `<div class="upd-sec-head"><span class="idx">${idx}</span><h3>${title}</h3>${meta ? `<span class="meta mono">${meta}</span>` : ''}</div>`;
}

function pct(a, b) { return b > 0 ? Math.min(100, Math.floor((a / b) * 100)) : 0; }

function progHTML(p, { indet = false, live = true } = {}) {
  if (!p) return `<div class="upd-prog"><div class="upd-bar indet"><i></i></div></div>`;
  const total = p.total || 0;
  let left, right = '', w;
  if (p.stage === 'verify' || p.stage === 'hash') {
    w = pct(p.verify_done, total);
    left = p.stage === 'verify' ? t('upd.verifying', { p: w }) : t('upd.model.hashing', { p: w });
  } else if (p.stage === 'probe' && live) {
    w = 0; indet = true;
    left = t('upd.connecting');
  } else {
    w = pct(p.received, total);
    left = live && p.speed > 0
      ? t('upd.dlStats', { got: fmtBytes(p.received), total: fmtBytes(total), speed: fmtBytes(p.speed), eta: fmtDuration(p.eta, getLang()) })
      : t('upd.dlStatsNoSpeed', { got: fmtBytes(p.received), total: fmtBytes(total) });
    right = `<b>${w}%</b>`;
  }
  return `<div class="upd-prog"><div class="upd-bar${indet ? ' indet' : ''}"><i style="width:${indet ? '' : w + '%'}"></i></div>` +
    `<div class="upd-prog-meta"><span>${left}</span><span>${right}</span></div></div>`;
}

function errText(e) {
  if (!e) return '';
  const k = 'upd.errc.' + e.code;
  return t(k) !== k ? t(k) : escapeHTML(e.message || e.code);
}

function modeText(a) {
  if (a.mode === 'replace') return t(a.kind === 'win_portable' ? 'upd.mode.portable' : 'upd.mode.replace');
  if (a.mode === 'installer') return t('upd.mode.installer');
  if (a.mode === 'manual') return t(a.fallback ? 'upd.manual.fallback' : 'upd.manual.' + (a.reason || 'dev'));
  return t(a.reason === 'no_package' ? 'upd.none.no_package' : 'upd.none.unsupported');
}

function fmtDate(s) {
  const d = Date.parse(s || '');
  if (!d) return '';
  return new Date(d).toLocaleDateString(getLang() === 'zh' ? 'zh-CN' : 'en-US', { year: 'numeric', month: 'short', day: 'numeric' });
}

const btn = (act, label, cls = '', extra = '') => `<button class="btn ${cls}" type="button" data-act="${act}" ${extra}>${label}</button>`;
const ARROW = '<svg class="arrow" aria-hidden="true"><use href="#i-arrow"/></svg>';

function renderApp(st) {
  const a = st.app, L = a.latest;
  const head = secHead('01', t('upd.appTitle'), escapeHTML(t('upd.current', { v: st.current })));
  if (st.dev) {
    return head + stateLine('', 'i-hash', t('upd.dev', { v: escapeHTML(st.current) }), L ? escapeHTML('GitHub: ' + L.version) : '');
  }
  if (!L) {
    if (st.checking) return head + stateLine('busy', 'i-redo', t('upd.checking'), '');
    return head + stateLine('', 'i-update', t('upd.never'), t('upd.neverChecked'));
  }
  if (!a.available) {
    return head + stateLine('ok', 'i-check', t('upd.upToDate'), t('upd.upToDateSub', { v: escapeHTML(L.version) }));
  }

  let h = head;
  const date = fmtDate(L.published_at);
  h += `<div class="upd-ver"><span class="from">${escapeHTML(st.current)}</span>${ARROW}` +
    `<span class="to"><span class="ins">${escapeHTML(L.version)}</span></span>` +
    `<span class="badge">${t('upd.newBadge')}</span>${date ? `<span class="upd-date">${t('upd.published', { d: escapeHTML(date) })}</span>` : ''}</div>`;
  const notes = pickNotes(L.notes, getLang());
  if (notes) h += `<div class="upd-notes">${miniMD(notes)}</div>`;

  const link = L.url ? `<a class="upd-link" href="${escapeHTML(L.url)}" target="_blank" rel="noopener noreferrer">${t('upd.fullNotes')}<svg aria-hidden="true"><use href="#i-arrow"/></svg></a>` : '';
  const size = L.size ? fmtBytes(L.size) : '';
  const manual = a.mode === 'manual' || a.fallback;
  switch (a.state) {
    case 'downloading':
      h += progHTML(a.progress) + `<div class="upd-actions">${btn('app-pause', t('upd.pause'))}${btn('app-cancel', t('upd.cancel'), 'ghost')}${link}</div>`;
      break;
    case 'paused':
      h += progHTML(a.progress, { live: false }) + `<div class="upd-actions">${btn('app-download', t('upd.resume'), 'primary')}${btn('app-cancel', t('upd.cancel'), 'ghost')}${link}</div>`;
      break;
    case 'ready':
      if (a.error && !a.fallback) h += `<p class="upd-err">${errText(a.error)}</p>`;
      h += stateLine('ok', 'i-check', t('upd.ready', { v: escapeHTML(L.version) }), '');
      h += `<div class="upd-actions" style="margin-top:14px">` +
        (manual ? btn('app-open', `<svg><use href="#i-folder"/></svg>${t('upd.openPkg')}`, 'primary')
          : btn('app-apply', `<svg><use href="#i-redo"/></svg>${t('upd.restart')}`, 'primary')) +
        btn('app-cancel', t('upd.cancel'), 'ghost') + link + `</div>`;
      break;
    case 'preparing':
    case 'restarting':
      h += progHTML(null) + `<p class="upd-fine" style="margin-top:0">${t(a.state === 'preparing' ? 'upd.preparing' : 'upd.restarting')}</p>`;
      break;
    default: // idle / error
      if (a.state === 'error') h += `<p class="upd-err">${errText(a.error)}</p>`;
      if (a.mode === 'none') {
        h += `<div class="upd-actions">${L.url ? `<a class="btn primary" href="${escapeHTML(L.url)}" target="_blank" rel="noopener noreferrer">GitHub</a>` : ''}${btn('later', t('upd.later'), 'ghost')}</div>`;
      } else {
        const label = a.state === 'error' ? t('upd.retry') : t(manual ? 'upd.downloadManual' : 'upd.download', { size });
        h += `<div class="upd-actions">${btn('app-download', `<svg><use href="#i-update"/></svg>${label}`, 'primary')}${btn('later', t('upd.later'), 'ghost')}${link}</div>`;
      }
  }
  if (!['preparing', 'restarting'].includes(a.state)) h += `<p class="upd-fine">${modeText(a)}</p>`;
  return h;
}

function short(h) { return h ? h.slice(0, 8) : '—'; }

function renderModels(st) {
  let h = secHead('02', t('upd.modelsTitle'), st.models.length ? 'Hugging Face' : '');
  if (!st.models.length) return h + `<p class="um-empty">${t('upd.model.none')}</p>`;
  h += '<div class="um-list">';
  let note = false;
  for (const m of st.models) {
    let side = '', below = '', cls = '';
    const size = fmtBytes(m.remote_size || m.local_size);
    switch (m.state) {
      case 'current':
        side = `<span class="um-state ok"><svg><use href="#i-check"/></svg>${t('upd.model.current')}</span>`;
        break;
      case 'available':
        cls = 'is-avail'; note = true;
        side = `<span class="badge">${t('upd.model.available')}</span>` +
          `<button class="btn sm primary" type="button" data-act="model-download" data-tier="${escapeHTML(m.tier)}"><svg><use href="#i-update"/></svg>${t('upd.model.download', { size })}</button>`;
        break;
      case 'hashing':
        side = `<span class="um-state spin"><svg><use href="#i-redo"/></svg>${t('upd.model.hashing', { p: pct(m.progress?.verify_done, m.progress?.total) })}</span>`;
        break;
      case 'downloading':
        cls = 'is-avail'; note = true;
        side = `<button class="btn sm" type="button" data-act="model-pause" data-tier="${escapeHTML(m.tier)}">${t('upd.pause')}</button>`;
        below = progHTML(m.progress);
        break;
      case 'paused':
        cls = 'is-avail'; note = true;
        side = `<button class="btn sm primary" type="button" data-act="model-download" data-tier="${escapeHTML(m.tier)}">${t('upd.resume')}</button>` +
          `<button class="btn sm ghost" type="button" data-act="model-cancel" data-tier="${escapeHTML(m.tier)}">${t('upd.cancel')}</button>`;
        below = progHTML(m.progress, { live: false });
        break;
      case 'waiting':
      case 'installing':
        cls = 'is-avail';
        side = `<span class="um-state spin"><svg><use href="#i-redo"/></svg>${t(m.state === 'waiting' ? 'upd.model.waiting' : 'upd.model.installing')}</span>`;
        below = m.state === 'installing' ? progHTML(null) : '';
        break;
      case 'done':
        side = `<span class="um-state ok"><svg><use href="#i-check"/></svg>${t('upd.model.done')}</span>`;
        break;
      case 'error':
        side = `<button class="btn sm" type="button" data-act="model-download" data-tier="${escapeHTML(m.tier)}">${t('upd.retry')}</button>` +
          `<button class="btn sm ghost" type="button" data-act="model-cancel" data-tier="${escapeHTML(m.tier)}">${t('upd.cancel')}</button>`;
        below = `<p class="um-err">${errText(m.error)}</p>`;
        break;
      default:
        side = `<span class="um-state dim">${t(m.checked && !String(m.checked).startsWith('0001') ? 'upd.model.unreachable' : 'upd.model.unknown')}</span>`;
    }
    const fp = m.state === 'available' && m.local_sha256 && m.remote_sha256
      ? `<div class="um-fp">${escapeHTML(t('upd.model.fp', { a: short(m.local_sha256), b: short(m.remote_sha256) }))}</div>` : '';
    const active = m.active ? `<span class="badge soft">${t('upd.model.active')}</span>` : '';
    h += `<div class="um ${cls}"><div class="um-name">${escapeHTML(m.label)}${active}</div>` +
      `<div class="um-file" title="${escapeHTML(m.file)}">${escapeHTML(m.file)}</div>${fp}` +
      `<div class="um-side">${side}</div>${below ? `<div class="um-prog">${below}</div>` : ''}</div>`;
  }
  h += '</div>';
  if (note) h += `<p class="um-note">${t('upd.model.note')}</p>`;
  return h;
}

// ───────────────────────── 操作 ─────────────────────────
async function act(action, tier) {
  if (action === 'later') {
    await post('dismiss', { version: updateSig(U.st) });
    openPanel(false);
    return;
  }
  U.busyAct = action;
  if (action === 'app-apply') { U.applying = true; U.applyingVersion = U.st?.app?.latest?.version; }
  const res = await post(action, tier ? { tier } : {});
  U.busyAct = '';
  if (!res.ok && action === 'app-apply') U.applying = false;
  $('upd-app').dataset.html = ''; // 强制重画:失败时按钮要重新可点
  $('upd-models').dataset.html = '';
  if (!res.ok) {
    const k = 'upd.errc.' + res.code;
    toast(t(k) !== k ? t(k) : res.message);
  }
  if (action === 'app-open' && res.ok) toast(t('toast.revealed'));
  refresh();
}

async function checkNow() {
  U.manual = true;
  U.sawChecking = false;
  U.busyAct = 'check';
  renderPanel();
  await post('check');
  U.busyAct = '';
  setTimeout(refresh, 250);
}

// ───────────────────────── 重启遮罩 ─────────────────────────
function startRestart(version) {
  U.restarting = { version, from: U.st?.current, t0: Date.now(), down: false };
  openPanel(false);
  $('upd-ov-title').textContent = t('upd.ov.title', { v: version });
  $('upd-ov-msg').textContent = t('upd.ov.msg');
  delete $('upd-overlay').dataset.slow;
  $('upd-overlay').hidden = false;
  setTimeout(pollPing, 1000);
}

// 旧进程先关(ping 失败)→ 新版本(或回滚后的旧版本)起来 → 刷新页面,带上新的网页文件
async function pollPing() {
  const R = U.restarting;
  try {
    const p = await getJSON('/app/ping', { timeout: 1500 });
    if (p.version !== R.from || R.down) {
      try { sessionStorage.setItem('hz.upd.restarted', '1'); } catch { /* 忽略 */ }
      location.reload();
      return;
    }
  } catch {
    R.down = true;
  }
  if (Date.now() - R.t0 > 180000 && !$('upd-overlay').dataset.slow) {
    $('upd-overlay').dataset.slow = '1';
    $('upd-ov-title').textContent = t('upd.ov.slowTitle');
    $('upd-ov-msg').textContent = t('upd.ov.slowMsg');
  }
  setTimeout(pollPing, 1000);
}

// ───────────────────────── 杂项 ─────────────────────────
let toastTimer = 0;
function toast(msg) {
  const el = $('toast');
  el.textContent = msg;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 2400);
}

function closePop() {
  const pop = $('pop');
  if (pop && !pop.hidden) { pop.hidden = true; $('status').setAttribute('aria-expanded', 'false'); }
}

function bind() {
  $('upd-chip').addEventListener('click', (e) => { e.stopPropagation(); closePop(); openPanel(true, e.currentTarget); });
  $('pa-update').addEventListener('click', (e) => {
    e.stopPropagation();
    closePop();
    openPanel(true, $('btn-menu'));
    // 从菜单进来 = 想要现在查一下(一分钟内刚查过就不重复查)
    const last = Date.parse(U.st?.last_check || '');
    if (!U.st?.checking && !(last > 86400000 && Date.now() - last < 60000)) checkNow();
  });
  $('upd-close').addEventListener('click', () => openPanel(false));
  $('upd-scrim').addEventListener('click', () => openPanel(false));
  $('upd-check').addEventListener('click', checkNow);
  $('upd-body').addEventListener('scroll', scrollCues, { passive: true });
  addEventListener('resize', () => { if (U.open) scrollCues(); });
  $('upd-auto').addEventListener('change', async (e) => {
    const res = await post('auto', { auto: e.target.checked });
    if (!res.ok) { e.target.checked = !e.target.checked; toast(res.message); }
    refresh();
  });
  $('upd').addEventListener('click', (e) => {
    e.stopPropagation();
    const b = e.target.closest('[data-act]');
    if (b && !b.disabled) { b.disabled = true; act(b.dataset.act, b.dataset.tier); }
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && U.open) { e.stopPropagation(); openPanel(false); }
    if (e.key === 'Tab' && U.open) trapFocus(e);
  }, true);
  // 切语言:app.js 改 <html data-lang>,这里跟着重画动态内容
  new MutationObserver(() => renderAll()).observe(document.documentElement, { attributes: true, attributeFilter: ['data-lang'] });
}

function trapFocus(e) {
  const els = [...$('upd').querySelectorAll('button:not([disabled]), a[href], input')].filter((x) => x.offsetParent !== null);
  if (!els.length) return;
  const first = els[0], last = els[els.length - 1];
  if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
  else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
}

bind();
setTimeout(refresh, 400);
if (params.get('panel') === 'update') setTimeout(() => openPanel(true), 200);
