export const DEFAULT_APP_URL = 'http://127.0.0.1:47615';

export function isDocsURL(value) {
  try { const url = new URL(value); return url.origin === 'https://docs.google.com' && url.pathname.startsWith('/document/'); }
  catch { return false; }
}

export function normalizeAppURL(value) {
  const url = new URL(value.trim());
  if (url.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(url.hostname) || url.username || url.password || url.search || url.hash || !['', '/'].includes(url.pathname)) {
    throw new Error('Enter the local app address, such as http://127.0.0.1:47615.');
  }
  return url.origin;
}

// Python str.strip includes the four information separator characters.
export const strip = (text) => text.replace(/^[\t\n\v\f\r\u001c-\u001f \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\t\n\v\f\r\u001c-\u001f \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g, '');
export const buildPrompt = (config, text) => config.instr + '\n\n' + strip(text) + config.sep;

export function checkConfig(config) {
  if (config.fingerprint !== 'cc51d66b4c593fbe' || buildPrompt(config, 'X') !== config.probe) {
    throw new Error('The app prompt format is incompatible. Update the app and extension together.');
  }
}

const hanCount = (text) => (text.match(/[㐀-䶿一-鿿豈-﫿]/g) || []).length;
const wordCount = (text) => (text.match(/[A-Za-z]+(?:['’\-][A-Za-z]+)*/g) || []).length;
export function languageDrift(draft, text) {
  const d = hanCount(draft), o = hanCount(text);
  return (d <= 2 && o > 15) || (d >= 20 && d > wordCount(draft) && o <= 2 && wordCount(text) > 15);
}

export function changedNumbers(draft, result) {
  const numbers = (text) => new Set(text.match(/\d+(?:[.,]\d+)*/g) || []);
  const before = numbers(draft), after = numbers(result);
  return { missing: [...before].filter((n) => !after.has(n)), added: [...after].filter((n) => !before.has(n)) };
}

export async function* streamEvents(response, signal) {
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  try {
    for (;;) {
      const { value, done } = await reader.read();
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true });
      let boundary;
      while ((boundary = buffer.indexOf('\n')) >= 0 || (done && buffer.length)) {
        const line = (boundary < 0 ? buffer : buffer.slice(0, boundary)).trim();
        buffer = boundary < 0 ? '' : buffer.slice(boundary + 1);
        if (signal?.aborted) throw signal.reason;
        if (line.startsWith('error:')) throw new Error(line.slice(6).trim());
        if (!line.startsWith('data:')) continue;
        const payload = line.slice(5).trim();
        if (payload === '[DONE]') return;
        const event = JSON.parse(payload);
        if (event.error) throw new Error(event.error.message || String(event.error));
        yield event;
      }
      if (done) return;
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
