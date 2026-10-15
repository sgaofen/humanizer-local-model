import { buildPrompt, checkConfig, languageDrift, normalizeAppURL, streamEvents, strip } from './core.js';

function delay(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(signal.reason); return; }
    const onAbort = () => { clearTimeout(timer); reject(signal.reason); };
    const timer = setTimeout(() => { signal.removeEventListener('abort', onAbort); resolve(); }, ms);
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

export class LocalClient {
  constructor(connection, fetcher = globalThis.fetch.bind(globalThis)) {
    this.base = normalizeAppURL(connection.base);
    this.token = connection.token;
    this.fetcher = fetcher;
  }

  async request(path, { body, signal, stream = false, timeout = 15000 } = {}) {
    const response = await this.fetcher(this.base + '/extension/' + path, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'X-Humanizer': '1', Authorization: 'Bearer ' + this.token, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(timeout)]) : AbortSignal.timeout(timeout),
      cache: 'no-store', credentials: 'omit', redirect: 'error',
    });
    if (!response.ok) {
      if (response.status === 401) throw new Error('Reconnect the extension. The app may have restarted.');
      let error;
      try { const data = await response.json(); error = data.error?.message || data.error; } catch { /* Non-JSON error. */ }
      throw new Error(typeof error === 'string' ? error : `The app returned HTTP ${response.status}. Check the Humanizer app.`);
    }
    return stream ? response : response.json();
  }

  async humanize(draft, { signal, onStatus, onText }) {
    const text = strip(draft);
    if (!text) throw new Error('Select some text in your Google Doc first.');
    onStatus('Loading the local model…');
    let status = await this.request('wake', { body: {}, signal });
    const deadline = Date.now() + Math.max(60, status.load_timeout_seconds || 900) * 1000;
    while (status.phase !== 'ready') {
      if (status.external || !['starting', 'sleeping'].includes(status.phase)) throw new Error(status.error?.message || 'Finish model setup in the Humanizer app.');
      if (Date.now() > deadline) throw new Error('Model loading timed out. Check the Humanizer app.');
      await delay(700, signal);
      status = await this.request('status', { signal });
    }
    const config = await this.request('config', { signal });
    checkConfig(config);
    const prompt = buildPrompt(config, text);
    onStatus('Preparing your selection…');
    const [{ tokens }, { tokens: draftTokens }] = await Promise.all([
      this.request('api/tokenize', { body: { content: prompt, add_special: true }, signal, timeout: 60000 }),
      this.request('api/tokenize', { body: { content: text, add_special: false }, signal, timeout: 60000 }),
    ]);
    if (!Array.isArray(tokens) || !Array.isArray(draftTokens)) throw new Error('The app did not return a token count.');
    const rule = config.n_predict;
    const predict = Math.min(rule.max, Math.max(rule.min, Math.ceil(draftTokens.length * rule.factor)));
    if (tokens.length + predict > config.ctx_size) throw new Error('This selection is too long. Select a shorter passage and try again.');
    for (let attempt = 0; attempt <= 3; attempt++) {
      onStatus(attempt ? 'Retrying to keep the original language…' : 'Humanizing on your computer…');
      onText('');
      const response = await this.request('api/completion', {
        body: { ...config.sampling, prompt, n_predict: predict, stream: true, cache_prompt: false },
        signal, stream: true, timeout: 20 * 60 * 1000,
      });
      let result = '', final;
      for await (const event of streamEvents(response, signal)) {
        result += event.content || '';
        onText(result);
        if (event.stop) final = event;
      }
      if (!final) throw new Error('The connection ended before the rewrite finished. Try again.');
      if (final.stop_type === 'limit' || final.truncated) throw new Error('The rewrite was cut off. Select a shorter passage and try again.');
      result = strip(result);
      if (!result) throw new Error('The model returned an empty rewrite. Try again.');
      if (!languageDrift(text, result) || attempt === 3) return { text: result, languageChanged: languageDrift(text, result) };
    }
  }
}
