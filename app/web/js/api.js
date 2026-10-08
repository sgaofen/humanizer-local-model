// 和启动器说话。所有写操作都带 X-Humanizer 头(启动器据此挡掉别的网站发来的请求)。
const H = { 'X-Humanizer': '1' };

export async function getJSON(path, { timeout = 6000 } = {}) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), timeout);
  try {
    const r = await fetch(path, { cache: 'no-store', signal: ctl.signal });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    return await r.json();
  } finally {
    clearTimeout(timer);
  }
}

export async function postJSON(path, body, { signal } = {}) {
  const r = await fetch(path, {
    method: 'POST',
    headers: { ...H, 'Content-Type': 'application/json' },
    body: JSON.stringify(body || {}),
    signal,
  });
  let data = null;
  try { data = await r.json(); } catch { /* 空响应 */ }
  if (!r.ok) throw new Error((data && (data.error?.message || data.error)) || `HTTP ${r.status}`);
  return data;
}

/** 草稿 token 数(llama-server /tokenize,不加 BOS)。 */
export async function countTokens(content, options) {
  const d = await postJSON('/api/tokenize', { content, add_special: false }, options);
  return Array.isArray(d.tokens) ? d.tokens.length : 0;
}

/**
 * 流式补全:逐个 yield llama-server 的 SSE 事件对象。
 * 跳过 ": ping" 这类 SSE 注释;遇到 error 事件抛异常。
 */
export async function* streamCompletion(body, signal) {
  const r = await fetch('/api/completion', {
    method: 'POST',
    headers: { ...H, 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify(body),
    signal,
  });
  if (!r.ok) {
    let msg = `HTTP ${r.status}`;
    try { const j = await r.json(); msg = j.error?.message || msg; } catch { /* 不是 JSON */ }
    throw new Error(msg);
  }
  const reader = r.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let nl;
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl).replace(/\r$/, '');
      buf = buf.slice(nl + 1);
      if (!line || line.startsWith(':')) continue;
      let payload = null;
      if (line.startsWith('data:')) payload = line.slice(5).trim();
      else if (line.startsWith('error:')) {
        let msg = line.slice(6).trim();
        try { msg = JSON.parse(msg).message || msg; } catch { /* 原文 */ }
        throw new Error(msg);
      }
      if (!payload || payload === '[DONE]') continue;
      let ev;
      try { ev = JSON.parse(payload); } catch { continue; }
      if (ev.error) throw new Error(ev.error.message || 'engine error');
      yield ev;
    }
  }
}
