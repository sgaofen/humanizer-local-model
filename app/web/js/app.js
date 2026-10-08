// Humanizer 网页主控:状态轮询 → 视图切换(首次下载 / 编辑器)→ 改写流式输出 → 差异与历史。
import { t, apply, setLang, getLang } from './i18n.js';
import { getJSON, postJSON, countTokens, streamCompletion } from './api.js';
import { buildPrompt, selfCheck, nPredictFor } from './prompt.js';
import { withLangGuard, langDrift } from './guard.js';
import { tokenize, diffTokens, renderMarked, flagNumbers } from './diff.js';
import { countText, isMostlyCJK, isLongDraft, numberCheck, fmtBytes, fmtDuration, fmtNum, escapeHTML, pyStrip } from './text.js';
import { loadHistory, addHistory, removeHistory, clearHistory, store } from './history.js';
import { SAMPLES } from './samples.js';
import { activeFacts, protectFacts, restoreFacts, streamView, withFactGuard, FACT_TRIES } from './facts.js';

const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const IS_MAC = document.documentElement.classList.contains('mac');
const KEY_LABEL = IS_MAC ? '⌘ ↵' : 'Ctrl ↵';
const BACKEND = { metal: 'Metal', cuda: 'CUDA', vulkan: 'Vulkan', cpu: 'CPU', custom: 'custom' };

const S = {
  protect: null, // 本次改写的占位符信息(Create fact),流式显示时用来换回原文
  retryable: false, // 上一次因为保留的文字没带过来而作废:允许直接点「重新生成」
  status: null,
  cfg: null,
  promptOK: false,
  view: null,
  forceSetup: false,
  pickTier: null,
  pickEndpoint: null,
  running: false,
  abort: null,
  out: '',
  facts: [],
  result: null,
  showDiff: store.get('diff', '1') === '1',
  editing: true,
  fails: 0,
  quit: false,
  autoRun: params.get('run') === '1',
  tierSig: '',
  pollTimer: 0,
};

// ───────────────────────── 启动 ─────────────────────────
function init() {
  apply();
  markLang();
  $('go-kbd').textContent = KEY_LABEL;
  renderSamples();
  const ex = SAMPLES.find((s) => s.id === params.get('example'));
  $('draft').value = ex ? ex.draft : store.get('draft', '');
  try { S.facts = ex ? [] : JSON.parse(store.get('facts', '[]')); } catch { S.facts = []; }
  onDraftInput(false);
  setDiffToggle(S.showDiff);
  bind();
  loadConfig();
  poll();
  if (params.get('panel') === 'history') openDrawer(true);
}

async function loadConfig() {
  for (let i = 0; i < 20 && !S.cfg; i++) {
    try {
      S.cfg = await getJSON('/app/config');
      S.promptOK = selfCheck(S.cfg);
      if (!S.promptOK) showNotice('err', t('out.promptMismatch'));
    } catch {
      await sleep(1000);
    }
  }
  updateGo();
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ───────────────────────── 轮询状态 ─────────────────────────
async function poll() {
  clearTimeout(S.pollTimer);
  if (S.quit) return;
  let next = 4000;
  try {
    const st = await getJSON('/app/status', { timeout: 4000 });
    S.status = st;
    S.fails = 0;
    $('overlay').hidden = true;
    render();
    if (['downloading', 'starting', 'paused'].includes(st.phase)) next = 700;
    else if (st.phase === 'setup' || st.phase === 'error') next = 2500;
    maybeAutoRun();
  } catch {
    S.fails++;
    if (S.fails >= 3 && !S.running) showOverlay(t('overlay.offTitle'), t('overlay.offMsg'), 'offline');
    next = 3000;
  }
  clearTimeout(S.pollTimer); // 可能有两次轮询同时在路上(切回标签页时),只留一个定时器
  S.pollTimer = setTimeout(poll, document.hidden ? Math.max(next, 15000) : next);
}

document.addEventListener('visibilitychange', () => { if (!document.hidden) poll(); });

function render() {
  const st = S.status;
  if (!st) return;
  const ph = st.phase;
  let view = 'editor';
  if (S.forceSetup || ['setup', 'downloading', 'paused', 'error'].includes(ph)) view = 'setup';
  if (ph === 'starting' && S.view === 'setup' && !S.forceSetup) view = 'setup'; // 首次下载完接着在卡片里显示装载
  if (ph === 'ready' && S.view === 'setup' && !S.forceSetup) view = 'editor';
  if (view !== S.view) {
    S.view = view;
    $('view-setup').hidden = view !== 'setup';
    $('view-editor').hidden = view !== 'editor';
    if (view === 'editor') requestAnimationFrame(() => $('draft').focus({ preventScroll: true }));
  }
  renderStatus();
  renderBrand();
  if (!$('pop').hidden) renderPop();
  if (view === 'setup') renderSetup();
  updateGo();
}

function tierOf(id) {
  return S.status?.tiers?.find((x) => x.id === id);
}

function renderBrand() {
  const tier = tierOf(S.status?.tier);
  const name = '12B';
  $('brand-tag').textContent = t('brand.tag', { tier: name });
}

function renderStatus() {
  const st = S.status, el = $('status');
  let state = 'idle', text = '';
  const tier = tierOf(st.tier);
  switch (st.phase) {
    case 'ready': {
      state = 'ready';
      const be = BACKEND[st.engine?.backend] || st.engine?.backend || '';
      text = S.running ? t('status.writing') : [t('status.ready'), tier?.label, be].filter(Boolean).join(' · ');
      break;
    }
    case 'starting': state = 'busy'; text = [t('status.loading'), tier?.label].filter(Boolean).join(' · '); break;
    case 'downloading': {
      state = 'busy';
      const d = st.download;
      text = d?.stage === 'verify' ? t('status.verify') : t('status.download', { p: pct(d?.received, d?.total) });
      break;
    }
    case 'paused': text = t('status.paused'); break;
    case 'setup': text = t('status.setup'); break;
    case 'error': state = 'error'; text = t('status.error'); break;
    default: text = st.phase;
  }
  el.dataset.state = state;
  el.classList.toggle('working', S.running);
  $('status-text').textContent = text;
}

function pct(a, b) {
  if (!(b > 0)) return 0;
  return Math.min(100, Math.floor((a / b) * 100));
}

// ───────────────────────── 首次运行 / 下载 ─────────────────────────
function renderSetup() {
  const st = S.status;
  const ph = st.phase;
  const hasModel = st.tiers.some((x) => x.downloaded);
  const title = S.forceSetup && hasModel ? t('setup.titleSwitch') : t('setup.title');
  $('setup-title').innerHTML = title.replace(/\{([^}]+)\}/g, '<span class="ins">$1</span>');

  let pane = 'choose';
  if (!S.forceSetup) {
    if (ph === 'error') pane = 'error';
    else if (ph === 'downloading' || ph === 'paused' || ph === 'starting') pane = 'progress';
  } else if (ph === 'downloading') {
    pane = 'progress';
  }
  for (const el of document.querySelectorAll('#setup-card .pane')) el.hidden = el.dataset.pane !== pane;
  if (pane === 'choose') renderChoose();
  else if (pane === 'progress') renderProgress();
  else renderError();
}

function renderChoose() {
  const st = S.status;
  // 以前选的 lite 档已下线:默认选这台机器的推荐档(旧文件不删)
  if (!S.pickTier || !tierOf(S.pickTier)) S.pickTier = st.notice === 'lite_retired' ? st.recommended : (st.tier || st.recommended);
  if (!S.pickEndpoint) S.pickEndpoint = st.endpoint;
  const cpu = st.sys.cpu ? escapeHTML(st.sys.cpu.replace(/\(R\)|\(TM\)|CPU|@.*$/g, '').trim()) : st.sys.os;
  $('sysline').innerHTML = t('setup.sys', { cpu, ram: Math.round(st.sys.ram_gb) });
  const noteEl = $('setup-notice');
  const noFit = st.tiers.length > 0 && st.tiers.every((x) => !x.fits);
  const msgs = [];
  if (st.notice === 'lite_retired') msgs.push(t('setup.liteRetired'));
  if (noFit) msgs.push(t('setup.lowRam', { ram: Math.round(st.sys.ram_gb) }));
  noteEl.hidden = msgs.length === 0;
  noteEl.textContent = msgs.join(' ');

  const sig = getLang() + '|' + S.pickTier + '|' + st.recommended + '|' + st.tiers.map((x) => `${x.id}:${x.downloaded}:${x.partial || 0}:${x.fits}`).join(',');
  if (sig !== S.tierSig) {
    S.tierSig = sig;
    const box = $('tiers');
    box.replaceChildren();
    for (const tr of st.tiers) {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'tier' + (tr.fits ? '' : ' nofit');
      b.setAttribute('role', 'radio');
      b.setAttribute('aria-checked', String(tr.id === S.pickTier));
      const badges = [];
      if (tr.id === st.recommended) badges.push(`<span class="badge">${t('setup.recommended')}</span>`);
      if (tr.downloaded) badges.push(`<span class="badge soft">${t('setup.downloaded')}</span>`);
      else if (tr.partial) badges.push(`<span class="badge soft">${t('setup.partial', { got: fmtBytes(tr.partial) })}</span>`);
      if (!tr.fits) badges.push(`<span class="badge warn">${t('setup.nofit')}</span>`);
      const note = tr.note ? escapeHTML(tr.note[getLang()] || tr.note.en || '') : '';
      b.innerHTML = `<span class="radio"></span>
        <span><span class="tier-name">${escapeHTML(tr.label)} ${badges.join('')}</span><span class="tier-note">${note}</span></span>
        <span class="tier-size">${t('setup.approx', { n: tr.approx_gb })}<small>${t('setup.needRam', { n: tr.min_ram_gb })}</small></span>`;
      b.addEventListener('click', () => { S.pickTier = tr.id; S.tierSig = ''; renderChoose(); });
      box.append(b);
    }
    const sel = $('endpoint');
    sel.replaceChildren(...st.endpoints.map((e) => new Option(e.label, e.id, false, e.id === S.pickEndpoint)));
  }
  const tr = tierOf(S.pickTier);
  let cta;
  if (tr.downloaded) cta = t('setup.ctaUse', { tier: tr.label });
  else if (tr.partial) cta = t('setup.ctaResume', { tier: tr.label });
  else cta = t('setup.cta', { tier: tr.label, size: tr.approx_gb });
  $('cta-text').textContent = cta;
  $('endpoint').closest('.field').hidden = !!tr.downloaded;
  $('btn-back').hidden = !(S.forceSetup && ['ready', 'starting'].includes(st.phase));
}

function renderProgress() {
  const st = S.status;
  const d = st.download;
  const tier = tierOf(d?.tier || st.tier);
  const label = tier?.label || '';
  const sq = $('squiggle');
  let p = 0, stage, stats = '\u00a0', note = '';
  if (st.phase === 'starting') {
    stage = t('prog.starting', { tier: label });
    const at = st.engine?.attempts?.at(-1);
    const lk = 'layers.' + (at?.layers ?? '');
    const lt = t(lk) === lk ? '-ngl ' + at?.layers : t(lk);
    const be = at ? `${BACKEND[at.backend] || at.backend} · ${lt}` : '…';
    stats = escapeHTML(be);
    note = t('prog.noteStarting', { backend: escapeHTML(be) });
    sq.classList.add('indet');
    $('prog-pct').hidden = true;
    $('btn-pause').hidden = true;
    $('btn-rechoose').hidden = true;
  } else {
    sq.classList.remove('indet');
    $('prog-pct').hidden = false;
    $('btn-pause').hidden = false;
    $('btn-rechoose').hidden = false;
    const total = d?.total > 0 ? d.total : (tier ? tier.approx_gb * 1e9 : 0);
    if (st.phase === 'paused') {
      stage = t('prog.paused', { tier: label });
      p = pct(d?.received, total);
      stats = t('prog.statsNoSpeed', { got: fmtBytes(d?.received || 0), total: fmtBytes(total) });
      $('btn-pause').textContent = t('prog.resume');
      note = t('prog.noteDownload');
    } else if (d?.stage === 'verify') {
      stage = t('prog.verify', { tier: label });
      p = pct(d.verify_done, total);
      stats = t('prog.statsNoSpeed', { got: fmtBytes(d.verify_done), total: fmtBytes(total) });
      note = t('prog.noteVerify');
      $('btn-pause').hidden = true;
    } else {
      stage = t(d?.stage === 'probe' ? 'prog.probe' : 'prog.download', { tier: label });
      p = pct(d?.received, total);
      stats = d?.speed > 0
        ? t('prog.stats', { got: fmtBytes(d.received), total: fmtBytes(total), speed: fmtBytes(d.speed), eta: fmtDuration(d.eta, getLang()) })
        : t('prog.statsNoSpeed', { got: fmtBytes(d?.received || 0), total: fmtBytes(total) });
      if (d?.attempt > 1) stats += ' ' + t('prog.retrying', { n: d.attempt - 1 });
      $('btn-pause').textContent = t('prog.pause');
      note = t('prog.noteDownload');
    }
  }
  $('prog-stage').textContent = stage;
  $('prog-file').textContent = tier?.file || '';
  $('prog-pct').innerHTML = `${p}<small>%</small>`;
  $('prog-stats').innerHTML = stats;
  $('prog-note').innerHTML = note;
  drawSquiggle(st.phase === 'starting' ? null : p);
}

function drawSquiggle(p) {
  const svg = $('squiggle');
  const w = Math.max(40, svg.clientWidth || 400), h = 30, mid = h / 2;
  if (svg.dataset.w !== String(w)) {
    svg.dataset.w = String(w);
    // 手写感:主波 + 一点不规则的二次谐波,振幅从左往右略增
    let d = `M0 ${mid}`;
    for (let x = 2; x <= w; x += 2) {
      const amp = 5 + 2.2 * (x / w);
      const y = mid + amp * Math.sin((x / 23) * Math.PI * 2) + 1.3 * Math.sin((x / 61) * Math.PI * 2 + 1.1);
      d += ` L${x} ${y.toFixed(2)}`;
    }
    const path = $('sq-fill');
    path.setAttribute('d', d);
    path.setAttribute('pathLength', '100');
  }
  const path = $('sq-fill');
  if (p === null) {
    path.style.strokeDasharray = '16 84';
    path.style.strokeDashoffset = '';
  } else {
    path.style.strokeDasharray = '100 100';
    path.style.strokeDashoffset = String(100 - Math.max(p, 0.6));
  }
}

function renderError() {
  const e = S.status.error || {};
  let title = e.kind === 'download' ? t('err.download') : t('err.engine');
  if (e.code === 'disk_full') title = t('err.disk');
  if (e.code === 'not_found') title = t('err.notfound');
  // 启动器给的是中文原文;有对应错误码的换成当前语言的说明,原文放进「技术细节」
  const key = 'errc.' + e.code;
  const friendly = t(key) !== key ? t(key) : '';
  const detail = [friendly ? e.message : '', e.detail].filter(Boolean).join('\n\n');
  $('err-title').textContent = title;
  $('err-msg').textContent = friendly || e.message || '';
  $('err-detail').textContent = detail;
  $('err-detail-wrap').hidden = !detail;
}

// ───────────────────────── 弹出面板 ─────────────────────────
function renderPop() {
  const st = S.status;
  if (!st) return;
  const tier = tierOf(st.tier);
  const e = st.engine || {};
  $('pi-model').textContent = tier ? `${tier.label} · ${tier.file}` : '—';
  const eng = [BACKEND[e.backend] || e.backend, e.offload ? t('menu.layers', { n: e.offload }) : '', e.build].filter(Boolean).join(' · ');
  $('pi-engine').textContent = eng || (st.backends?.length ? st.backends.map((b) => BACKEND[b] || b).join(' / ') : '—');
  $('pi-device').textContent = e.device || st.sys.cpu || '—';
  $('pi-ram').textContent = `${Math.round(st.sys.ram_gb)} GB`;
  $('pi-ctx').textContent = `${fmtNum(st.ctx_size, getLang())} tokens`;
  $('pi-idle').textContent = st.idle_exit_minutes > 0 ? t('menu.idle', { n: st.idle_exit_minutes }) : '';
  $('pa-restart').hidden = !['ready', 'starting', 'error'].includes(st.phase);
}

function togglePop(force) {
  const pop = $('pop');
  const open = force ?? pop.hidden;
  pop.hidden = !open;
  $('status').setAttribute('aria-expanded', String(open));
  if (open) renderPop();
}

// ───────────────────────── 编辑器 ─────────────────────────
function renderSamples() {
  const box = $('sample-btns');
  box.replaceChildren(...SAMPLES.map((s) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'sample-btn';
    b.textContent = s.label[getLang()] || s.label.en;
    b.addEventListener('click', () => { $('draft').value = s.draft; S.facts = []; onDraftInput(); $('draft').focus(); });
    return b;
  }));
}

let saveTimer = 0;
let importing = false;
function onDraftInput(save = true) {
  const v = $('draft').value;
  S.facts = activeFacts(S.facts, v);
  store.set('facts', JSON.stringify(S.facts));
  renderFacts();
  updateFactSelection();
  const c = countText(v);
  $('draft-count').innerHTML = v ? `${t('count.zh', { n: fmtNum(c.units, getLang()) })} · ${t('count.chars', { n: fmtNum(c.chars, getLang()) })}` : '';
  $('samples').hidden = v.trim() !== '';
  $('draft-long').hidden = !isLongDraft(v);
  $('draft').classList.toggle('cjk', isMostlyCJK(v));
  updateGo();
  if (save) {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(() => store.set('draft', v), 400);
  }
}

function updateGo() {
  const ready = S.status?.phase === 'ready' && S.promptOK;
  const has = pyStrip($('draft').value) !== '';
  const go = $('btn-go');
  go.disabled = !S.running && !(ready && has);
  go.classList.toggle('running', S.running);
  let label = t('go.label');
  if (S.running) label = t('go.stop');
  else if (S.status && S.status.phase !== 'ready') label = t('go.loading');
  $('go-label').textContent = label;
  go.setAttribute('aria-label', label);
  if (!importing) $('btn-upload').disabled = S.running;
  updateFactSelection();
  for (const button of $('facts-list').querySelectorAll('button')) button.disabled = S.running;
  $('btn-regen').disabled = S.running || !ready || !(S.result || S.retryable);
  $('btn-copy').disabled = S.running || !S.result;
  $('out-empty-sub').innerHTML = t('out.emptySub', { key: KEY_LABEL });
}

function showNotice(kind, html) {
  const n = $('out-notice');
  n.className = 'notice ' + kind;
  n.innerHTML = html;
  n.hidden = false;
}

function setEditing(on) {
  S.editing = on;
  $('draft').hidden = !on;
  $('draft-view').hidden = on;
  $('btn-edit').hidden = on;
  $('legend-del').hidden = on;
  updateFactSelection();
  if (on) requestAnimationFrame(() => $('draft').focus({ preventScroll: true }));
}

function maybeAutoRun() {
  if (S.autoRun && S.status?.phase === 'ready' && S.promptOK && pyStrip($('draft').value)) {
    S.autoRun = false;
    run();
  }
}

// 流式输出按帧合并渲染。注意:改写结束后可能还有一帧没跑(后台标签页里 rAF 会被推迟),
// 它不能再把带高亮的结果覆盖成纯文本 —— 所以结束时取消,回调里也再查一次。
let renderRaf = 0;
function scheduleStreamRender() {
  if (renderRaf) return;
  renderRaf = requestAnimationFrame(() => {
    renderRaf = 0;
    if (!S.running) return;
    const out = $('output');
    const nearBottom = out.scrollHeight - out.scrollTop - out.clientHeight < 80;
    const caret = document.createElement('span');
    caret.className = 'caret';
    const shown = S.protect ? streamView(S.out, S.protect) : S.out;
    out.replaceChildren(shown.replace(/^\s+/, ''), caret);
    out.classList.toggle('cjk', isMostlyCJK(shown));
    $('out-empty').hidden = true;
    if (nearBottom) out.scrollTop = out.scrollHeight;
    const c = countText(shown);
    $('out-count').innerHTML = t('count.zh', { n: fmtNum(c.units, getLang()) });
  });
}

async function run() {
  if (S.running) { stop(); return; }
  const draft = $('draft').value;
  if (!pyStrip(draft)) return;
  if (!S.promptOK || !S.cfg) { showNotice('err', t('out.promptMismatch')); return; }
  if (S.status?.phase !== 'ready') { toast(t('out.notReady')); return; }

  const protectedDraft = protectFacts(draft, S.facts);
  togglePop(false);
  closeNumbers();
  setEditing(true);
  S.running = true;
  S.abort = new AbortController();
  S.out = '';
  S.result = null;
  $('out-notice').hidden = true;
  $('chip-numbers').hidden = true;
  $('out-stats').textContent = '';
  $('out-count').textContent = '';
  $('out-empty').hidden = true;
  $('output').classList.remove('cjk');
  $('output').innerHTML = `<span class="reading">${t('out.reading', { p: 0 })}</span>`;
  updateGo();
  renderStatus();

  const t0 = performance.now();
  const runSignal = S.abort.signal;
  let final = null, error = null, aborted = false, factsLost = false, factTries = 0;
  try {
    let nTok;
    try { nTok = await countTokens(pyStrip(draft), { signal: runSignal }); } catch (e) {
      if (runSignal.aborted || e.name === 'AbortError' || e.name === 'TimeoutError') throw e;
      nTok = Math.ceil(countText(draft).chars / 3);
    }
    const nPred = nPredictFor(S.cfg, nTok);
    const r = S.cfg.n_predict;
    let promptTokens;
    const prompt = buildPrompt(S.cfg, protectedDraft.text);
    try { promptTokens = await countTokens(prompt, { signal: runSignal }); } catch (e) {
      if (runSignal.aborted || e.name === 'AbortError' || e.name === 'TimeoutError') throw e;
      promptTokens = Math.ceil(prompt.length / 3);
    }
    if (runSignal.aborted) throw runSignal.reason;
    if (promptTokens + nPred > S.cfg.ctx_size) throw Object.assign(new Error(t('out.tooLong', { n: fmtNum(nTok, getLang()) })), { notice: true });
    if (nTok * r.factor > r.max) showNotice('warn', t('out.longDraft', { n: fmtNum(nTok, getLang()) }));
    const body = {
      ...S.cfg.sampling,
      prompt,
      n_predict: nPred,
      stream: true,
      cache_prompt: true,
      return_progress: true,
    };
    // 语言保险(guard.js):英文草稿写着写着成了中文,当场掐掉、同样参数静默重采,最多 3 次;最后一次照常交出。
    // Create fact(facts.js):写完核对占位符,丢了/改了/重复了就整篇重写,最多 FACT_TRIES 次;都不行就作废。
    const locked = protectedDraft.locks.length > 0;
    S.protect = locked ? protectedDraft : null;
    let note = '';
    const reading = (p) => { $('output').innerHTML = `<span class="reading">${note ? escapeHTML(note) : t('out.reading', { p })}</span>`; };
    const attempt = async (i, isLast) => {
      if (runSignal.aborted) throw new DOMException('stopped', 'AbortError');
      const ctl = new AbortController();
      const onStop = () => ctl.abort();
      runSignal.addEventListener('abort', onStop);
      let fin = null, drifted = false;
      S.out = '';
      if (i > 0) reading(0);
      try {
        for await (const ev of streamCompletion(body, ctl.signal)) {
          if (ev.prompt_progress && !S.out) {
            const pp = ev.prompt_progress;
            reading(pp.total ? Math.round((100 * pp.processed) / pp.total) : 0);
          }
          if (ev.content) {
            S.out += ev.content;
            if (!isLast && langDrift(draft, locked ? restoreFacts(S.out, protectedDraft, false) : S.out, false)) { drifted = true; ctl.abort(); break; }
            scheduleStreamRender();
          }
          if (ev.stop) { fin = ev; break; }
        }
      } catch (e) {
        if (!(drifted && e.name === 'AbortError' && !runSignal.aborted)) throw e;
      } finally {
        runSignal.removeEventListener('abort', onStop);
      }
      if (drifted) {
        if (renderRaf) { cancelAnimationFrame(renderRaf); renderRaf = 0; }
        reading(0);
      }
      return { text: locked ? restoreFacts(S.out, protectedDraft, false) : S.out, final: fin, drifted };
    };
    const fg = await withFactGuard(protectedDraft, async () => {
      const res = await withLangGuard(draft, attempt);
      return { raw: S.out, final: res.final };
    }, FACT_TRIES, (n, m) => {
      if (renderRaf) { cancelAnimationFrame(renderRaf); renderRaf = 0; }
      note = t('facts.retrying', { n, m });
      reading(0);
    });
    final = fg.final;
    if (locked) {
      if (fg.ok) { S.out = fg.text; factTries = fg.tries; } else { factsLost = true; S.out = ''; }
    }
  } catch (e) {
    if (e.name === 'AbortError') aborted = true;
    else if (e.name === 'TimeoutError') error = Object.assign(new Error(t('out.requestTimeout')), { notice: true });
    else error = e;
    if (S.protect) S.out = ''; // 有保护段时,没核对过的半截结果不显示(可能缺原文)
  }
  S.running = false;
  S.abort = null;
  S.protect = null;
  S.retryable = factsLost;
  if (renderRaf) { cancelAnimationFrame(renderRaf); renderRaf = 0; }
  const secs = (performance.now() - t0) / 1000;
  const text = S.out.trim();

  if (error && !text) {
    $('output').replaceChildren();
    $('out-empty').hidden = false;
    showNotice('err', error.notice ? escapeHTML(error.message) : t('out.failed', { msg: escapeHTML(error.message) }));
  } else if (!text) {
    $('output').replaceChildren();
    $('out-empty').hidden = false;
    $('out-count').textContent = '';
    if (aborted) showNotice('', t('out.stopped'));
    else if (factsLost) showNotice('warn', t('facts.failed', { n: FACT_TRIES }));
  } else {
    S.result = makeResult(draft, text);
    if (final) {
      const tok = final.tokens_predicted || 0;
      const tps = final.timings?.predicted_per_second;
      $('out-stats').textContent = t('out.stats', { tok: fmtNum(tok, getLang()), tps: tps ? tps.toFixed(1) : '—', sec: secs.toFixed(1) });
      if (final.stop_type === 'limit') showNotice('warn', t('out.truncated', { n: final.tokens_predicted }));
      else if (factTries > 1) showNotice('', t('facts.retried', { n: factTries }));
      addHistory({
        id: Date.now().toString(36) + Math.random().toString(36).slice(2, 6),
        t: Date.now(), draft, facts: protectedDraft.facts, out: text, tier: S.status?.tier, tierLabel: tierOf(S.status?.tier)?.label, tok, tps,
        ratio: S.result.diff ? S.result.diff.ratio : null, stop: final.stop_type,
      });
    } else if (aborted) {
      showNotice('', t('out.stopped'));
    } else if (error) {
      showNotice('err', t('out.failed', { msg: escapeHTML(error.message) }));
    }
    renderResult();
  }
  updateGo();
  renderStatus();
}

function stop() {
  if (S.abort) S.abort.abort();
}

function makeResult(draft, out) {
  const aTok = tokenize(draft), bTok = tokenize(out);
  const diff = diffTokens(aTok, bTok);
  return { draft, out, aTok, bTok, diff, nums: numberCheck(draft, out) };
}

function renderResult() {
  const r = S.result;
  if (!r) return;
  const out = $('output');
  out.classList.toggle('cjk', isMostlyCJK(r.out));
  $('out-empty').hidden = true;
  const useDiff = S.showDiff && r.diff;
  if (useDiff) renderMarked(out, r.bTok, r.diff.b, 'ins');
  else out.textContent = r.out;

  // 草稿侧:显示被改掉的部分(只在草稿没被改过时)
  if (useDiff && $('draft').value === r.draft) {
    const v = $('draft-view');
    v.classList.toggle('cjk', isMostlyCJK(r.draft));
    renderMarked(v, r.aTok, r.diff.a, 'del');
    flagNumbers(v, r.nums.missing);
    setEditing(false);
  } else if (!S.editing) {
    setEditing(true);
  }
  if (S.showDiff && !r.diff) showNotice('', t('diff.tooBig'));

  const c = countText(r.out);
  let meta = t('count.zh', { n: fmtNum(c.units, getLang()) });
  if (r.diff) meta += ' · ' + t('out.changed', { p: Math.round(r.diff.ratio * 100) });
  $('out-count').innerHTML = meta;

  const chip = $('chip-numbers');
  if (r.nums.total) {
    const ok = r.nums.missing.length === 0;
    chip.hidden = false;
    chip.className = 'chip ' + (ok ? 'ok' : 'warn');
    chip.innerHTML = `<svg><use href="#${ok ? 'i-check' : 'i-hash'}"/></svg>` +
      (ok ? t('num.ok', { n: r.nums.total }) : t('num.warn', { k: r.nums.missing.length }));
  } else {
    chip.hidden = true;
  }
  updateGo();
}

function setDiffToggle(on) {
  S.showDiff = on;
  store.set('diff', on ? '1' : '0');
  $('btn-diff').setAttribute('aria-pressed', String(on));
}

function openNumbers() {
  const r = S.result;
  if (!r || !r.nums.missing.length) return;
  const pop = $('numbers-pop');
  pop.innerHTML = `<div>${t('num.popTitle')}</div><div class="nums">${r.nums.missing.map((n) => `<span>${escapeHTML(n)}</span>`).join('')}</div><div>${t('num.popBody')}</div>`;
  pop.hidden = false;
  const rc = $('chip-numbers').getBoundingClientRect();
  pop.style.left = Math.max(12, Math.min(rc.left, innerWidth - pop.offsetWidth - 12)) + 'px';
  pop.style.top = Math.max(12, rc.top - pop.offsetHeight - 10) + 'px';
}

function closeNumbers() { $('numbers-pop').hidden = true; }

async function copyOut() {
  const r = S.result;
  if (!r) return;
  try {
    await navigator.clipboard.writeText(r.out);
  } catch {
    const ta = document.createElement('textarea');
    ta.value = r.out;
    document.body.append(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
  }
  const b = $('btn-copy');
  b.classList.add('done');
  b.querySelector('span').textContent = t('out.copied');
  b.querySelector('use').setAttribute('href', '#i-check');
  setTimeout(() => {
    b.classList.remove('done');
    b.querySelector('span').textContent = t('out.copy');
    b.querySelector('use').setAttribute('href', '#i-copy');
  }, 1600);
}

// ───────────────────────── 历史 ─────────────────────────
function renderHistory() {
  const list = loadHistory();
  $('history-count').textContent = list.length ? String(list.length) : '';
  $('hist-empty').hidden = list.length > 0;
  $('btn-history-clear').hidden = list.length === 0;
  const ol = $('hist');
  ol.replaceChildren(...list.map((h) => {
    const li = document.createElement('li');
    const when = new Date(h.t).toLocaleString(getLang() === 'zh' ? 'zh-CN' : 'en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
    const tier = h.tierLabel || tierOf(h.tier)?.label || h.tier || '';
    const words = countText(h.out).units;
    const ratio = typeof h.ratio === 'number' ? ` · ${Math.round(h.ratio * 100)}%` : '';
    li.innerHTML = `<div class="hist-meta"><span>${escapeHTML(when)}</span><span>·</span><span>${escapeHTML(tier)}</span><span>·</span><span>${t('count.zh', { n: words }).replace(/<\/?b>/g, '')}${ratio}</span></div>
      <div class="hist-text"></div>
      <button class="hist-del" type="button" aria-label="delete"><svg><use href="#i-close"/></svg></button>`;
    li.querySelector('.hist-text').textContent = h.out.slice(0, 220);
    li.addEventListener('click', () => restore(h));
    li.querySelector('.hist-del').addEventListener('click', (ev) => { ev.stopPropagation(); removeHistory(h.id); renderHistory(); });
    return li;
  }));
}

function restore(h) {
  if (S.running) return;
  $('draft').value = h.draft;
  S.facts = activeFacts(h.facts, h.draft);
  onDraftInput();
  $('out-notice').hidden = true;
  $('out-stats').textContent = h.tok ? `${fmtNum(h.tok, getLang())} tokens` + (h.tps ? ` · ${h.tps.toFixed(1)} tok/s` : '') : '';
  S.result = makeResult(h.draft, h.out);
  renderResult();
  openDrawer(false);
  toast(t('history.restored'));
}

function openDrawer(open) {
  const d = $('drawer');
  if (open) renderHistory();
  d.classList.toggle('open', open);
  d.setAttribute('aria-hidden', String(!open));
  $('scrim').hidden = !open;
}

// ───────────────────────── 杂项 ─────────────────────────
let toastTimer = 0;
/** kind:'' 普通(1.8 秒)、'ok' 成功(3 秒)、'err' 出错(带红点,5 秒,长句也读得完) */
function toast(msg, kind = '') {
  const el = $('toast');
  el.textContent = msg;
  el.dataset.kind = kind;
  el.setAttribute('role', kind === 'err' ? 'alert' : 'status');
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), kind === 'err' ? 5000 : kind === 'ok' ? 3000 : 1800);
}

function showOverlay(title, msg, kind) {
  $('overlay-title').textContent = title;
  $('overlay-msg').textContent = msg;
  $('overlay').hidden = false;
  $('overlay').dataset.kind = kind;
}

function markLang() {
  for (const b of document.querySelectorAll('[data-lang]')) b.setAttribute('aria-pressed', String(b.dataset.lang === getLang()));
}

function effectiveTheme() {
  const a = document.documentElement.getAttribute('data-theme');
  if (a) return a;
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

async function setup(tier, endpoint) {
  try {
    await postJSON('/app/setup', { tier, endpoint });
    S.forceSetup = false;
    S.tierSig = '';
  } catch (e) {
    toast(e.message);
  }
  poll();
}

// ───────────────────────── 导入 .docx / .pdf ─────────────────────────
// 文件交给启动器(/app/document)在本机提取文字,不存盘;提取完草稿若已被改动就不覆盖。
const IMPORT_ERRORS = ['uploadInvalid', 'uploadLarge', 'uploadEmpty', 'uploadUnreadable', 'uploadGarbled'];

function setImporting(on) {
  importing = on;
  const b = $('btn-upload');
  b.disabled = on || S.running;
  b.classList.toggle('busy', on);
  b.setAttribute('aria-busy', String(on));
  b.querySelector('use').setAttribute('href', on ? '#i-spin' : '#i-import');
  b.querySelector('span').textContent = t(on ? 'draft.uploadLoading' : 'draft.upload');
}

async function importDocument(file) {
  if (importing || S.running) return;
  if (!/\.(docx|pdf)$/i.test(file.name)) { toast(t('draft.uploadInvalid'), 'err'); return; }
  if (file.size > 20 * 1024 * 1024) { toast(t('draft.uploadLarge'), 'err'); return; }
  if ($('draft').value.trim() && !confirm(t('draft.uploadReplace', { name: file.name }))) return;
  const original = $('draft').value;
  setImporting(true);
  try {
    const body = new FormData();
    body.append('file', file);
    const response = await fetch('/app/document', { method: 'POST', headers: { 'X-Humanizer': '1' }, body });
    let result = {};
    try { result = await response.json(); } catch { /* 不是 JSON */ }
    if (!response.ok) throw new Error(result.error || 'uploadUnreadable');
    // 提取期间用户改了草稿,或者开始改写了:不覆盖
    if (S.running || $('draft').value !== original) { toast(t('draft.uploadChanged'), 'err'); return; }
    $('draft').value = result.text;
    S.facts = [];
    setEditing(true);
    onDraftInput();
    store.set('draft', result.text);
    $('draft').scrollTop = 0;
    $('draft').setSelectionRange(0, 0);
    toast(t('draft.uploadDone', { name: file.name, n: fmtNum(countText(result.text).units, getLang()) }), 'ok');
  } catch (error) {
    toast(t('draft.' + (IMPORT_ERRORS.includes(error.message) ? error.message : 'uploadUnreadable')), 'err');
  } finally {
    setImporting(false);
  }
}

// 把文件拖到草稿上导入;拖到页面别处松手也不能让浏览器直接打开文件(那会离开 App)。
function bindDrop() {
  const sheet = $('sheet-draft'), zone = $('dropzone');
  const hasFiles = (e) => Array.from(e.dataTransfer?.types || []).includes('Files');
  let depth = 0;
  const hide = () => { depth = 0; zone.hidden = true; };
  sheet.addEventListener('dragenter', (e) => {
    if (!hasFiles(e) || S.running || importing) return;
    e.preventDefault();
    depth++;
    zone.hidden = false;
  });
  sheet.addEventListener('dragover', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = S.running || importing ? 'none' : 'copy';
  });
  sheet.addEventListener('dragleave', (e) => { if (hasFiles(e) && --depth <= 0) hide(); });
  sheet.addEventListener('drop', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    hide();
    const file = e.dataTransfer.files[0];
    if (file) importDocument(file);
  });
  window.addEventListener('dragover', (e) => { if (hasFiles(e)) e.preventDefault(); });
  window.addEventListener('drop', (e) => { if (hasFiles(e)) { e.preventDefault(); hide(); } });
  window.addEventListener('dragend', hide);
}

// ───────────────────────── Create fact:选中的文字改写时一字不改 ─────────────────────────
function selectedFact() {
  const draft = $('draft');
  return S.editing && !S.running ? draft.value.slice(draft.selectionStart, draft.selectionEnd).trim() : '';
}

// 选区在草稿框里的坐标:用一个同样排版的隐藏镜像算(textarea 不给选区坐标)
const MIRROR = ['boxSizing', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft', 'fontFamily', 'fontSize', 'fontWeight', 'fontStyle',
  'fontVariationSettings', 'letterSpacing', 'lineHeight', 'textTransform', 'wordSpacing', 'textIndent', 'tabSize'];
function caretXY(ta, pos) {
  const cs = getComputedStyle(ta);
  const m = document.createElement('div');
  for (const p of MIRROR) m.style[p] = cs[p];
  Object.assign(m.style, { position: 'absolute', visibility: 'hidden', top: '0', left: '-9999px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', width: ta.clientWidth + 'px', border: '0' });
  m.textContent = ta.value.slice(0, pos);
  const mark = document.createElement('span');
  mark.textContent = '\u200b';
  m.append(mark);
  document.body.append(m);
  const r = { x: mark.offsetLeft, y: mark.offsetTop - ta.scrollTop, h: parseFloat(cs.lineHeight) || 26 };
  m.remove();
  return r;
}

function placeFactPop() {
  const ta = $('draft'), pop = $('btn-create-fact');
  const w = pop.offsetWidth, h = pop.offsetHeight, W = ta.clientWidth, H = ta.clientHeight;
  let x = W - w - 16, y = H - h - 12; // 草稿特别长时不算坐标,放右下角
  if (ta.value.length < 60000) {
    const a = caretXY(ta, ta.selectionStart), b = caretXY(ta, ta.selectionEnd);
    x = (a.y === b.y ? (a.x + b.x) / 2 : a.x + 60) - w / 2;
    y = a.y - h - 6; // 选区第一行上方;太靠上就放到最后一行下方
    if (y < 6) y = b.y + b.h + 4;
  }
  pop.style.left = Math.round(Math.min(Math.max(x, 10), W - w - 10)) + 'px';
  pop.style.top = Math.round(Math.min(Math.max(y, 6), H - h - 6)) + 'px';
}

function updateFactSelection() {
  const pop = $('btn-create-fact');
  const selected = selectedFact();
  const focused = document.activeElement === $('draft') || document.activeElement === pop;
  const show = !!selected && focused;
  pop.hidden = !show;
  if (!show) return;
  const kept = S.facts.includes(selected);
  pop.disabled = S.running || kept;
  pop.querySelector('span').textContent = t(kept ? 'facts.already' : 'facts.create');
  placeFactPop();
}

function renderFacts(fresh = '') {
  $('facts-panel').hidden = !S.facts.length;
  $('facts-count').textContent = S.facts.length > 1 ? String(S.facts.length) : '';
  $('facts-list').replaceChildren(...S.facts.map((fact) => {
    const li = document.createElement('li');
    li.className = 'fact-chip' + (fact === fresh ? ' new' : '');
    li.title = fact;
    const text = document.createElement('span');
    text.className = 'fact-text';
    text.textContent = fact.replace(/\s+/g, ' ');
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'fact-x';
    remove.innerHTML = '<svg aria-hidden="true"><use href="#i-close"/></svg>';
    remove.setAttribute('aria-label', t('facts.removeLabel', { fact }));
    remove.title = t('facts.remove');
    remove.disabled = S.running;
    remove.addEventListener('click', () => {
      S.facts = S.facts.filter((f) => f !== fact);
      store.set('facts', JSON.stringify(S.facts));
      renderFacts();
      updateFactSelection();
    });
    li.append(text, remove);
    return li;
  }));
}

function bind() {
  $('btn-upload').addEventListener('click', () => { if (!S.running && !importing) $('document-file').click(); });
  $('document-file').addEventListener('change', () => {
    const file = $('document-file').files[0];
    $('document-file').value = '';
    if (file) importDocument(file);
  });
  bindDrop();
  for (const event of ['select', 'keyup', 'mouseup', 'focus']) $('draft').addEventListener(event, updateFactSelection);
  document.addEventListener('selectionchange', updateFactSelection);
  $('draft').addEventListener('scroll', () => { if (!$('btn-create-fact').hidden) placeFactPop(); });
  $('draft').addEventListener('blur', (e) => { if (e.relatedTarget !== $('btn-create-fact')) $('btn-create-fact').hidden = true; });
  window.addEventListener('resize', () => { if (!$('btn-create-fact').hidden) placeFactPop(); });
  // 点按钮时别让草稿框先丢掉选区
  $('btn-create-fact').addEventListener('mousedown', (event) => event.preventDefault());
  $('btn-create-fact').addEventListener('click', () => {
    const fact = selectedFact();
    if (!fact || S.facts.includes(fact)) return;
    if (S.facts.length >= 50) { toast(t('facts.limit'), 'err'); return; }
    S.facts.push(fact);
    store.set('draft', $('draft').value);
    store.set('facts', JSON.stringify(S.facts));
    renderFacts(fact);
    const ta = $('draft');
    ta.focus({ preventScroll: true });
    ta.setSelectionRange(ta.selectionEnd, ta.selectionEnd);
    updateFactSelection();
    toast(t('facts.created'), 'ok');
  });
  $('draft').addEventListener('input', () => onDraftInput());
  $('draft-view').addEventListener('click', () => setEditing(true));
  $('btn-edit').addEventListener('click', () => setEditing(true));
  $('btn-go').addEventListener('click', run);
  $('btn-regen').addEventListener('click', run);
  $('btn-copy').addEventListener('click', copyOut);
  $('btn-diff').addEventListener('click', () => { setDiffToggle(!S.showDiff); renderResult(); });
  $('chip-numbers').addEventListener('click', (e) => { e.stopPropagation(); $('numbers-pop').hidden ? openNumbers() : closeNumbers(); });
  $('btn-clear').addEventListener('click', () => {
    $('draft').value = '';
    setEditing(true);
    onDraftInput();
  });
  $('btn-paste').addEventListener('click', async () => {
    try {
      const txt = await navigator.clipboard.readText();
      if (txt) { $('draft').value = txt; S.facts = []; setEditing(true); onDraftInput(); }
    } catch {
      setEditing(true);
      toast(IS_MAC ? '⌘ V' : 'Ctrl V');
    }
  });

  // 首次运行
  $('btn-download').addEventListener('click', () => setup(S.pickTier, $('endpoint').value));
  $('endpoint').addEventListener('change', (e) => { S.pickEndpoint = e.target.value; });
  $('btn-back').addEventListener('click', () => { S.forceSetup = false; S.tierSig = ''; render(); });
  $('btn-pause').addEventListener('click', async () => {
    const st = S.status;
    if (st.phase === 'paused') await setup(st.download?.tier || st.tier, st.endpoint);
    else { await postJSON('/app/download/pause').catch(() => {}); poll(); }
  });
  $('btn-rechoose').addEventListener('click', () => { S.forceSetup = true; S.tierSig = ''; render(); });
  $('btn-err-choose').addEventListener('click', () => { S.forceSetup = true; S.tierSig = ''; render(); });
  $('btn-retry').addEventListener('click', async () => {
    const st = S.status;
    if (st.error?.kind === 'engine') { await postJSON('/app/engine/restart').catch((e) => toast(e.message)); poll(); }
    else await setup(st.download?.tier || st.tier, st.endpoint);
  });
  $('btn-err-logs').addEventListener('click', () => postJSON('/app/reveal', { what: 'logs' }).then(() => toast(t('toast.revealed'))).catch((e) => toast(e.message)));

  // 顶栏
  $('status').addEventListener('click', (e) => { e.stopPropagation(); togglePop(); });
  $('btn-menu').addEventListener('click', (e) => { e.stopPropagation(); togglePop(); });
  $('pop').addEventListener('click', (e) => e.stopPropagation());
  document.addEventListener('click', () => { togglePop(false); closeNumbers(); });
  $('btn-history').addEventListener('click', () => openDrawer(!$('drawer').classList.contains('open')));
  $('btn-drawer-close').addEventListener('click', () => openDrawer(false));
  $('scrim').addEventListener('click', () => openDrawer(false));
  $('btn-history-clear').addEventListener('click', () => {
    if (confirm(t('history.confirmClear'))) { clearHistory(); renderHistory(); toast(t('history.cleared')); }
  });
  $('btn-theme').addEventListener('click', () => {
    const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    store.set('theme', next);
  });
  for (const b of document.querySelectorAll('[data-lang]')) {
    b.addEventListener('click', () => {
      setLang(b.dataset.lang);
      store.set('lang', b.dataset.lang);
      markLang();
      renderSamples();
      onDraftInput(false);
      S.tierSig = '';
      render();
      if (S.result) renderResult();
      if ($('drawer').classList.contains('open')) renderHistory();
    });
  }
  $('pa-tier').addEventListener('click', () => { togglePop(false); S.forceSetup = true; S.pickTier = S.status?.tier; S.tierSig = ''; render(); });
  $('pa-reveal').addEventListener('click', () => postJSON('/app/reveal', { what: 'models' }).then(() => toast(t('toast.revealed'))).catch((e) => toast(e.message)));
  $('pa-restart').addEventListener('click', async () => { togglePop(false); toast(t('toast.restarting')); await postJSON('/app/engine/restart').catch((e) => toast(e.message)); poll(); });
  $('pa-quit').addEventListener('click', async () => {
    if (!confirm(t('confirm.quit'))) return;
    S.quit = true;
    togglePop(false);
    try { await postJSON('/app/quit'); } catch { /* 已经没了 */ }
    showOverlay(t('overlay.quitTitle'), t('overlay.quitMsg'), 'quit');
  });

  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); run(); }
    else if (e.key === 'Escape') {
      if (S.running) stop();
      togglePop(false);
      closeNumbers();
      openDrawer(false);
    }
  });
  new ResizeObserver(() => { if (S.view === 'setup' && S.status) renderProgressIfVisible(); }).observe($('squiggle'));
}

function renderProgressIfVisible() {
  if (!document.querySelector('[data-pane="progress"]').hidden) {
    $('squiggle').dataset.w = '';
    renderProgress();
  }
}

init();
