// humanizer Space: language switch, theme toggle, sample drafts, word counter, copy buttons,
// Create fact (keep selected passages word for word) and .docx / PDF import.
// Runs from <head>; everything is delegated from document, so Gradio re-renders don't matter.
(function () {
  var MAX_WORDS = 700, MAX_CJK = 1200, PARTS = 3;  // keep in step with page.py
  var MAX_FACTS = 50;                              // keep in step with facts.py
  var LONG_DRAFT_TOKENS = 1000;                    // the app's long-draft note (app/web/js/text.js)
  var root = document.documentElement;
  var KEY = 'hz-space-lang';

  function store(k, v) { try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (e) { return null; } }

  var lang = null;
  try { var q = new URLSearchParams(location.search).get('lang'); if (q === 'zh' || q === 'en') lang = q; } catch (e) {}
  if (!lang) lang = store(KEY);
  if (lang !== 'zh' && lang !== 'en') lang = 'en';   // English unless ?lang=zh or the visitor picked 中文 before
  root.dataset.lang = lang;
  root.lang = lang === 'zh' ? 'zh-CN' : 'en';

  var PH = { en: 'Paste the AI-written draft here…', zh: '把 AI 写的草稿贴在这里……' };

  function $(s, el) { return (el || document).querySelector(s); }
  function draftArea() { return $('#draft textarea'); }
  function draftValue() { var ta = draftArea(); return ta ? ta.value : ''; }
  function isZh() { return root.dataset.lang === 'zh'; }
  function busy() { return root.classList.contains('hz-busy'); }

  // ── word counter (same units as hz: English words + Chinese characters) ──
  var CJK = /[぀-ヿ㐀-䶿一-鿿豈-﫿가-힯]/g;
  function count(s) {
    var cjk = (s.match(CJK) || []).length;
    var latin = s.replace(CJK, ' ').split(/\s+/).filter(function (t) { return /[\p{L}\p{N}]/u.test(t); }).length;
    return { cjk: cjk, latin: latin };
  }
  function updateCount() {
    var ta = draftArea(), el = $('#draft-count');
    if (!ta || !el) return;
    var c = count(ta.value || ''), zh = c.cjk > c.latin;
    var n = c.cjk + c.latin, size = c.latin + c.cjk * MAX_WORDS / MAX_CJK;
    var unitEn = zh ? 'chars' : 'words', unitZh = '字';
    if (!n) zh = isZh();
    var lim = (zh ? MAX_CJK * PARTS : MAX_WORDS * PARTS).toLocaleString('en-US');
    el.innerHTML = '<b>' + n.toLocaleString('en-US') + '</b> <span class="l-en">' + unitEn + '</span><span class="l-zh">' + unitZh +
      '</span> <span class="dim">/ ' + lim + '</span>';
    el.classList.toggle('over', size > MAX_WORDS * PARTS);
    ta.classList.toggle('cjk', zh);
    // long documents may drift on facts (rough token estimate, as in the app: 1.5 per English word, 0.95 per character)
    var note = $('#draft-long');
    if (note) note.hidden = Math.round(c.latin * 1.5 + c.cjk * 0.95) < LONG_DRAFT_TOKENS;
  }

  function applyLang() {
    var ta = draftArea();
    if (ta) ta.placeholder = PH[root.dataset.lang] || PH.en;
    document.querySelectorAll('[data-title-en]').forEach(function (el) {
      el.title = isZh() ? el.dataset.titleZh : el.dataset.titleEn;
    });
    renderFacts();
  }
  function setLang(l) {
    root.dataset.lang = l;
    root.lang = l === 'zh' ? 'zh-CN' : 'en';
    store(KEY, l);
    applyLang();
  }

  function setDraft(text) {
    var ta = draftArea();
    if (!ta) return;
    var setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
    setter.call(ta, text);
    ta.dispatchEvent(new Event('input', { bubbles: true }));
    facts = [];
    renderFacts();
    updateCount();
  }

  function copyText(text, btn) {
    function done() { if (!btn) return; btn.classList.add('copied'); setTimeout(function () { btn.classList.remove('copied'); }, 1600); }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { fallback(text); done(); });
    } else { fallback(text); done(); }
  }
  function fallback(text) {
    var t = document.createElement('textarea');
    t.value = text; t.style.position = 'fixed'; t.style.opacity = '0';
    document.body.appendChild(t); t.select();
    try { document.execCommand('copy'); } catch (e) {}
    t.remove();
  }

  // ── toasts (the ones from the server land in #hz-toast; these are made here) ──
  function toast(en, zh, kind) {
    var host = $('.gradio-container') || document.body;   // the stylesheet is scoped to .gradio-container
    document.querySelectorAll('.toast.js').forEach(function (t) { t.remove(); });
    var old = $('#hz-toast .toast'); if (old) old.classList.add('gone');
    var t = document.createElement('div');
    t.className = 'toast js';
    t.dataset.kind = kind || 'ok';
    t.setAttribute('role', 'status');
    var a = document.createElement('span'); a.className = 'l-en'; a.textContent = en;
    var b = document.createElement('span'); b.className = 'l-zh'; b.lang = 'zh-CN'; b.textContent = zh;
    t.appendChild(a); t.appendChild(b);
    t.addEventListener('animationend', function () { t.remove(); });
    host.appendChild(t);
  }

  // ── Create fact: selected draft text comes back word for word (rules in facts.py / the app's facts.js) ──
  var facts = [];
  function pruneFacts() {
    var v = draftValue(), seen = {};
    facts = facts.filter(function (f) {
      if (typeof f !== 'string' || !f.trim() || v.indexOf(f) < 0 || seen[f]) return false;
      seen[f] = true; return true;
    }).slice(0, MAX_FACTS);
  }
  function renderFacts(fresh) {
    var panel = $('#facts-panel'), list = $('#facts-list'), cnt = $('#facts-count');
    if (!panel || !list) return;
    panel.hidden = !facts.length;
    if (cnt) cnt.textContent = facts.length > 1 ? String(facts.length) : '';
    list.textContent = '';
    facts.forEach(function (f, i) {
      var li = document.createElement('li');
      li.className = 'fact-chip' + (f === fresh ? ' new' : '');
      li.title = f;
      var sp = document.createElement('span');
      sp.className = 'fact-text';
      sp.textContent = f.replace(/\s+/g, ' ');
      var x = document.createElement('button');
      x.type = 'button';
      x.className = 'fact-x';
      x.dataset.i = String(i);
      x.innerHTML = '<svg aria-hidden="true"><use href="#i-close"/></svg>';
      x.setAttribute('aria-label', (isZh() ? '不再保留：' : 'Stop keeping: ') + f);
      x.title = isZh() ? '移除' : 'Remove';
      li.appendChild(sp); li.appendChild(x);
      list.appendChild(li);
    });
  }
  function selectedFact() {
    var ta = draftArea();
    if (!ta || busy()) return '';
    return ta.value.slice(ta.selectionStart, ta.selectionEnd).trim();
  }

  // Where the selection sits inside the textarea: a hidden mirror with the same layout (textareas give no coordinates).
  var MIRROR = ['boxSizing', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft', 'fontFamily', 'fontSize', 'fontWeight', 'fontStyle',
    'fontVariationSettings', 'letterSpacing', 'lineHeight', 'textTransform', 'wordSpacing', 'textIndent', 'tabSize'];
  function caretXY(ta, pos) {
    var cs = getComputedStyle(ta), m = document.createElement('div');
    MIRROR.forEach(function (p) { m.style[p] = cs[p]; });
    Object.assign(m.style, { position: 'absolute', visibility: 'hidden', top: '0', left: '-9999px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', width: ta.clientWidth + 'px', border: '0' });
    m.textContent = ta.value.slice(0, pos);
    var mark = document.createElement('span');
    mark.textContent = '​';
    m.appendChild(mark);
    document.body.appendChild(m);
    var r = { x: mark.offsetLeft, y: mark.offsetTop - ta.scrollTop, h: parseFloat(cs.lineHeight) || 28 };
    m.remove();
    return r;
  }
  function placePop() {
    var ta = draftArea(), pop = $('#btn-create-fact'), layer = pop && pop.closest('.draft-overlays');
    if (!ta || !pop || !layer) return;
    var tr = ta.getBoundingClientRect(), sr = layer.getBoundingClientRect();
    var ox = tr.left - sr.left, oy = tr.top - sr.top;
    var w = pop.offsetWidth, h = pop.offsetHeight, W = ta.clientWidth, H = ta.clientHeight;
    var x = W - w - 16, y = H - h - 12;   // a very long draft: no coordinates, bottom right
    if (ta.value.length < 60000) {
      var a = caretXY(ta, ta.selectionStart), b = caretXY(ta, ta.selectionEnd);
      x = (a.y === b.y ? (a.x + b.x) / 2 : a.x + 60) - w / 2;
      y = a.y - h - 6;                  // above the first line of the selection; too high -> below the last line
      if (y < 6) y = b.y + b.h + 4;
    }
    pop.style.left = Math.round(ox + Math.min(Math.max(x, 10), W - w - 10)) + 'px';
    pop.style.top = Math.round(oy + Math.min(Math.max(y, 6), H - h - 6)) + 'px';
  }
  function updatePop() {
    var pop = $('#btn-create-fact'), ta = draftArea();
    if (!pop || !ta) return;
    var sel = selectedFact();
    var focused = document.activeElement === ta || document.activeElement === pop;
    var show = !!sel && focused;
    pop.hidden = !show;
    if (!show) return;
    var kept = facts.indexOf(sel) >= 0;
    pop.disabled = kept;
    pop.classList.toggle('kept', kept);
    placePop();
  }
  function createFact() {
    var f = selectedFact();
    if (!f || facts.indexOf(f) >= 0) return;
    if (facts.length >= MAX_FACTS) { toast('You can keep up to 50 facts.', '最多保留 50 段文字。', 'err'); return; }
    facts.push(f);
    renderFacts(f);
    var ta = draftArea();
    ta.focus({ preventScroll: true });
    ta.setSelectionRange(ta.selectionEnd, ta.selectionEnd);
    updatePop();
    toast('Fact created: this text will be kept word for word.', '好的，这段文字改写时一字不改。', 'ok');
  }

  // ── import .docx / .pdf: checked here, handed to the hidden Gradio upload, read on the server ──
  var importing = false, beforeImport = null, importTimer = 0;
  function setImporting(on) {
    importing = on;
    var b = $('#btn-import');
    if (!b) return;
    b.classList.toggle('busy', on);
    b.disabled = on;
    b.setAttribute('aria-busy', String(on));
    var u = b.querySelector('use');
    if (u) u.setAttribute('href', on ? '#i-spin' : '#i-import');
  }
  function importFile(file) {
    if (!file || importing || busy()) return;
    var fi = $('#doc-file'), max = (fi && +fi.dataset.max) || 10485760, mb = Math.round(max / 1048576);
    if (!/\.(docx|pdf)$/i.test(file.name)) {
      toast('Only .docx and .pdf files are supported. Save old .doc files as .docx first.', '只支持 .docx 和 .pdf。旧版 .doc 请先在 Word 里另存为 .docx。', 'err');
      return;
    }
    if (file.size > max) {
      toast('This file is over ' + mb + ' MB, the limit on this page. The app takes files up to 20 MB.', '文件超过 ' + mb + ' MB，本页读不了。App 能读 20 MB 以内的文件。', 'err');
      return;
    }
    var cur = draftValue();
    if (cur.trim() && !confirm(isZh() ? '用「' + file.name + '」里的文字替换当前草稿？' : 'Replace the current draft with the text from “' + file.name + '”?')) return;
    var input = $('#hz-upload input[type=file]') || $('input[type=file][data-testid$="-upload-button"]');
    if (!input) { toast('Couldn’t read this file. Reload the page and try again.', '读不了这个文件，请刷新页面再试。', 'err'); return; }
    var dt = new DataTransfer();
    dt.items.add(file);
    input.files = dt.files;
    beforeImport = cur;
    setImporting(true);
    clearTimeout(importTimer);
    importTimer = setTimeout(function () { setImporting(false); beforeImport = null; }, 120000);  // never stay stuck
    input.dispatchEvent(new Event('change', { bubbles: true }));
  }
  function afterImport() {
    clearTimeout(importTimer);
    document.querySelectorAll('.toast.js').forEach(function (t) { t.remove(); });
    var before = beforeImport;
    beforeImport = null;
    setImporting(false);
    // Gradio writes the new text into the textarea a moment after the event ends
    setTimeout(function () {
      if (before !== null && draftValue() !== before) {   // replaced: the old kept passages go, as in the app
        facts = [];
        renderFacts();
        var ta = draftArea();
        if (ta) { ta.scrollTop = 0; ta.setSelectionRange(0, 0); }
      }
      updateCount();
    }, 150);
  }

  // drop a file on the draft sheet; dropping it anywhere else must not navigate away from the page
  var dragDepth = 0;
  function sheetEl() { var ta = draftArea(); return ta && ta.closest('.draft-sheet'); }
  function hasFiles(e) { return Array.prototype.indexOf.call((e.dataTransfer && e.dataTransfer.types) || [], 'Files') >= 0; }
  function onSheet(e) { var s = sheetEl(); return !!(s && e.target && e.target.nodeType === 1 && s.contains(e.target)); }
  function hideDrop() { dragDepth = 0; var z = $('#dropzone'); if (z) z.hidden = true; }
  document.addEventListener('dragenter', function (e) {
    if (!hasFiles(e) || !onSheet(e) || busy() || importing) return;
    e.preventDefault();
    dragDepth += 1;
    var z = $('#dropzone'); if (z) z.hidden = false;
  });
  document.addEventListener('dragover', function (e) {
    if (!hasFiles(e)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = onSheet(e) && !busy() && !importing ? 'copy' : 'none';
  });
  document.addEventListener('dragleave', function (e) {
    if (hasFiles(e) && onSheet(e) && --dragDepth <= 0) hideDrop();
  });
  document.addEventListener('drop', function (e) {
    if (!hasFiles(e)) return;
    e.preventDefault();
    var inside = onSheet(e);
    hideDrop();
    if (inside && e.dataTransfer.files && e.dataTransfer.files[0]) importFile(e.dataTransfer.files[0]);
  });
  window.addEventListener('dragend', hideDrop);

  window.HZ = {
    factsJSON: function () { pruneFacts(); return JSON.stringify(facts); },
    afterImport: afterImport,
  };

  document.addEventListener('click', function (e) {
    var a = e.target.closest('a[href^="#"]');
    if (a && a.getAttribute('href').length > 1) {
      // On huggingface.co the Space sits in an auto-height iframe, so a plain #hash jump scrolls nothing.
      // scrollIntoView also scrolls the parent page.
      var dest = document.getElementById(a.getAttribute('href').slice(1));
      if (dest) { e.preventDefault(); dest.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
      return;
    }
    if (e.target.closest('#btn-create-fact')) { if (!busy()) createFact(); return; }
    var x = e.target.closest('.fact-x');
    if (x) {
      if (busy()) return;
      facts.splice(+x.dataset.i, 1);
      renderFacts();
      updatePop();
      return;
    }
    var t = e.target.closest('[data-setlang],[data-act],[data-sample]');
    if (!t) return;
    if (t.dataset.setlang) { setLang(t.dataset.setlang); return; }
    if (t.dataset.sample) {
      var s = (window.HZ_SAMPLES || {})[t.dataset.sample];
      if (s) {
        setDraft(s);
        if (t.closest('#hz-body')) { var ed = $('#editor'); if (ed) ed.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
      }
      return;
    }
    switch (t.dataset.act) {
      case 'theme': document.body.classList.toggle('dark'); break;
      case 'clear': setDraft(''); var ta = draftArea(); if (ta) ta.focus(); break;
      case 'diff': root.dataset.diff = root.dataset.diff === 'off' ? 'on' : 'off'; break;
      case 'import': { var fi = $('#doc-file'); if (fi && !importing && !busy()) fi.click(); break; }
      case 'copy-out': {
        var pre = t.closest('.out-wrap') && t.closest('.out-wrap').querySelector('pre.plain');
        if (pre) copyText(pre.textContent, t);
        break;
      }
      case 'copy-code': {
        var code = t.closest('.code') && t.closest('.code').querySelector('code');
        if (code) copyText(code.textContent, t);
        break;
      }
    }
  });

  document.addEventListener('change', function (e) {
    if (e.target && e.target.id === 'doc-file') {
      var f = e.target.files && e.target.files[0];
      e.target.value = '';
      if (f) importFile(f);
    }
  });

  // keep the selection while the Create fact button is pressed
  document.addEventListener('mousedown', function (e) { if (e.target.closest && e.target.closest('#btn-create-fact')) e.preventDefault(); });

  document.addEventListener('input', function (e) {
    if (e.target && e.target.closest && e.target.closest('#draft')) {
      var n = facts.length;
      pruneFacts();
      if (facts.length !== n) renderFacts();
      updateCount();
      updatePop();
    }
  });

  document.addEventListener('selectionchange', updatePop);
  ['mouseup', 'keyup'].forEach(function (ev) {
    document.addEventListener(ev, function (e) { if (e.target && e.target.closest && e.target.closest('#draft')) updatePop(); });
  });
  document.addEventListener('scroll', function (e) {
    var pop = $('#btn-create-fact');
    if (pop && !pop.hidden && e.target === draftArea()) placePop();
  }, true);
  document.addEventListener('focusout', function (e) {
    var pop = $('#btn-create-fact');
    if (pop && e.target === draftArea() && e.relatedTarget !== pop) pop.hidden = true;
  });
  window.addEventListener('resize', function () { var pop = $('#btn-create-fact'); if (pop && !pop.hidden) placePop(); });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && e.target.closest && e.target.closest('#draft')) {
      e.preventDefault();
      var go = $('#go');
      if (go) go.click();
    }
  });

  // On a direct *.hf.space visit Hugging Face pins its own badge (#huggingface-space-header) over the
  // top-right corner; move the bar below it so the language switch stays reachable.
  [500, 1500, 3000, 6000].forEach(function (ms) {
    setTimeout(function () {
      if (document.getElementById('huggingface-space-header')) root.classList.add('hz-hfbadge');
    }, ms);
  });

  // Gradio renders after this script runs: wait for the textarea once, then set placeholder and counter,
  // and move the layer with the Create fact button and the drop overlay onto the draft sheet.
  var tries = 0;
  var iv = setInterval(function () {
    tries += 1;
    var ta = draftArea();
    if (ta && document.getElementById('ex-4') && $('#btn-create-fact')) {
      var sheet = ta.closest('.draft-sheet'), layer = $('.draft-overlays');
      if (sheet && layer) { sheet.appendChild(layer); layer.hidden = false; }
      applyLang(); updateCount();
      if (root.dataset.lang === 'zh') document.getElementById('ex-4').checked = true;  // open on a Chinese example
      var k = $('.go-kbd');
      if (k && !/Mac|iPhone|iPad/.test(navigator.platform || '')) k.textContent = 'Ctrl ↵';
      clearInterval(iv);
    } else if (tries > 300) clearInterval(iv);
  }, 100);
})();
