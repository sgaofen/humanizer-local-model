(() => {
  const host = document.createElement('div');
  host.style.cssText = 'position:fixed;bottom:24px;right:24px;z-index:2147483647';
  const root = host.attachShadow({ mode: 'closed' });
  const style = document.createElement('style');
  style.textContent = 'button{font:600 14px system-ui;color:white;background:#274e3b;border:1px solid #ffffff40;border-radius:24px;padding:12px 19px;box-shadow:0 3px 15px #0003;cursor:pointer}button:focus-visible{outline:3px solid #91b7ff;outline-offset:3px}';
  const button = document.createElement('button');
  button.type = 'button';
  button.textContent = 'Humanize selection';
  root.append(style, button);
  document.documentElement.append(host);
  host.hidden = true;

  function editorDocuments() {
    const docs = [document];
    for (const frame of document.querySelectorAll('iframe.docs-texteventtarget-iframe')) {
      try { if (frame.contentDocument) docs.unshift(frame.contentDocument); } catch { /* Cross origin. */ }
    }
    return docs;
  }

  function selectedText() {
    for (const doc of editorDocuments()) {
      const selection = doc.getSelection();
      const text = selection?.toString() || '';
      if (text.trim()) return text;
    }
    return '';
  }

  async function readSelection() {
    const text = selectedText();
    if (text.trim()) return text;
    // Docs draws on canvas. Ask its own copy handler for the current selection;
    // never read an old clipboard value automatically or synthesize keystrokes.
    for (const doc of editorDocuments()) {
      let copied = '';
      const capture = (event) => {
        // Bubble at the window, after the editor's target/document handlers.
        copied = event.clipboardData?.getData('text/plain') || '';
      };
      doc.defaultView.addEventListener('copy', capture);
      try {
        doc.execCommand('copy');
        await Promise.resolve();
      } catch { /* Clipboard fallback is offered in the sidebar. */ }
      finally { doc.defaultView.removeEventListener('copy', capture); }
      if (copied.trim()) return copied;
    }
    return '';
  }

  function refresh() {
    const canvasSelection = [...document.querySelectorAll('.kix-selection-overlay')]
      .some((node) => { const r = node.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
    host.hidden = !selectedText().trim() && !canvasSelection;
  }
  let pending = false;
  function scheduleRefresh() {
    if (pending) return;
    pending = true;
    requestAnimationFrame(() => { pending = false; refresh(); });
  }
  document.addEventListener('mouseup', scheduleRefresh);
  document.addEventListener('keyup', scheduleRefresh);
  document.addEventListener('selectionchange', scheduleRefresh);
  const observer = new MutationObserver(scheduleRefresh);
  observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['style', 'class'] });
  button.addEventListener('mousedown', (event) => event.preventDefault());
  button.addEventListener('click', () => {
    chrome.runtime.sendMessage({ type: 'open-humanizer' }).catch(() => {
      button.textContent = 'Reload this Doc to reconnect';
    });
  });
  chrome.runtime.onMessage.addListener((message, _sender, respond) => {
    if (message?.type !== 'read-selection') return;
    readSelection().then((text) => respond({ text }), () => respond({ text: '' }));
    return true;
  });
  refresh();
})();
