import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { LocalClient } from '../client.js';
import { buildPrompt, changedNumbers, checkConfig, isDocsURL, languageDrift, normalizeAppURL, streamEvents, strip } from '../core.js';
import { pyStrip } from '../../app/web/js/text.js';

const instr = 'Rewrite the text below so it reads like a person wrote it, not a language model.\n\nReorganize it as you see fit. Vary sentence length on purpose. Cut hedging,\nthroat-clearing, and any sentence that only announces what comes next.\nPrefer the concrete word over the abstract one. It is fine to sound uneven.\n\nEvery fact, number, unit, date, name and quotation must survive unchanged.';
const config = { instr, sep: '\n\n### Rewritten:\n\n', fingerprint: 'cc51d66b4c593fbe', sampling: { temperature: 1, top_p: .95, top_k: 0, min_p: 0, repeat_penalty: 1 }, n_predict: { factor: 2.5, min: 256, max: 2048 }, ctx_size: 8192 };
config.probe = buildPrompt(config, 'X');
const connection = { base: 'http://127.0.0.1:47615', token: 'test-secret' };

function fakeClient({ output = 'We checked 42 files.', huge = false, cutOff = false, disconnect = false, responses } = {}) {
  const calls = [];
  let attempt = 0;
  const fetcher = async (url, options) => {
    calls.push({ url, ...options });
    if (options.signal.aborted) throw options.signal.reason;
    if (url.endsWith('/wake')) return Response.json({ phase: 'ready' });
    if (url.endsWith('/config')) return Response.json(config);
    if (url.endsWith('/tokenize')) return Response.json({ tokens: Array.from({ length: huge ? 8000 : 100 }, (_, i) => i) });
    if (url.endsWith('/completion')) {
      const text = responses?.[attempt++] || output;
      return new Response(`: ping\r\ndata: ${JSON.stringify({ content: text })}\r\n\r\n${disconnect ? '' : `data: ${JSON.stringify({ stop: true, stop_type: cutOff ? 'limit' : 'eos' })}\n\n`}`);
    }
    throw new Error('Unexpected request: ' + url);
  };
  return { client: new LocalClient(connection, fetcher), calls };
}
const callbacks = () => ({ signal: new AbortController().signal, onStatus() {}, onText() {} });

test('local-only connection validation and Google Docs scope', () => {
  assert.equal(normalizeAppURL(' http://localhost:1234/ '), 'http://localhost:1234');
  for (const url of ['https://example.com', 'http://127.0.0.1.evil.test', 'http://user:pass@localhost', 'https://localhost', 'http://localhost/path', 'http://localhost?token=x']) assert.throws(() => normalizeAppURL(url));
  assert.ok(isDocsURL('https://docs.google.com/document/d/123/edit'));
  assert.ok(!isDocsURL('https://docs.google.com.evil.test/document/d/123'));
  assert.ok(!isDocsURL('https://docs.google.com/spreadsheets/d/123'));
});

test('prompt matches the training fingerprint and Python whitespace rules', () => {
  assert.equal(createHash('sha256').update(config.probe).digest('hex').slice(0, 16), config.fingerprint);
  for (const text of [' X ', '\x1c\x85X\u3000', '\ufeffX\ufeff', '\u0085\n\tX\n', '']) assert.equal(strip(text), pyStrip(text));
  checkConfig(config);
  assert.throws(() => checkConfig({ ...config, probe: 'wrong' }));
});

test('humanizing uses paired loopback inference and the exact prompt/sampler', async () => {
  const { client, calls } = fakeClient();
  const result = await client.humanize(' We reviewed 42 files. ', callbacks());
  assert.equal(result.text, 'We checked 42 files.');
  const body = JSON.parse(calls.find((call) => call.url.endsWith('/completion')).body);
  assert.equal(body.prompt, buildPrompt(config, 'We reviewed 42 files.'));
  assert.equal(body.n_predict, 256);
  assert.equal(body.stream, true);
  for (const [key, value] of Object.entries(config.sampling)) assert.equal(body[key], value);
  assert.equal(body.stop, undefined);
  for (const call of calls) {
    assert.ok(call.url.startsWith(connection.base + '/extension/'));
    assert.equal(call.headers.Authorization, 'Bearer test-secret');
    assert.equal(call.headers['X-Humanizer'], '1');
    assert.equal(call.redirect, 'error');
  }
});

test('oversized selections fail before completion; interrupted/truncated output fails', async () => {
  const { client, calls } = fakeClient({ huge: true });
  await assert.rejects(client.humanize('draft', callbacks()), /too long/);
  assert.ok(!calls.some((call) => call.url.endsWith('/completion')));
  await assert.rejects(fakeClient({ cutOff: true }).client.humanize('draft', callbacks()), /cut off/);
  await assert.rejects(fakeClient({ disconnect: true }).client.humanize('draft', callbacks()), /before the rewrite finished/);
});

test('language drift retries with unchanged sampling; retries are bounded', async () => {
  const chinese = '这是中文输出，这是中文输出，这是中文输出，这是中文输出。';
  assert.ok(languageDrift('English draft', chinese));
  const { client, calls } = fakeClient({ responses: [chinese, 'An English rewrite.'] });
  assert.equal((await client.humanize('English draft', callbacks())).text, 'An English rewrite.');
  const completions = calls.filter((call) => call.url.endsWith('/completion'));
  assert.equal(completions.length, 2);
  assert.equal(completions[0].body, completions[1].body);
  const bounded = fakeClient({ output: chinese });
  assert.ok((await bounded.client.humanize('English draft', callbacks())).languageChanged);
  assert.equal(bounded.calls.filter((call) => call.url.endsWith('/completion')).length, 4);
});

test('number changes are shown and cancellation is forwarded', async () => {
  assert.deepEqual(changedNumbers('37 files in 2026', '38 files in 2026'), { missing: ['37'], added: ['38'] });
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(fakeClient().client.humanize('draft', { ...callbacks(), signal: controller.signal }), { name: 'AbortError' });
});

test('stream parser handles split UTF-8 and final lines without newlines', async () => {
  const bytes = new TextEncoder().encode('data: {"content":"你好"}\r\n\r\ndata: {"stop":true}');
  const response = new Response(new ReadableStream({ start(c) { for (const byte of bytes) c.enqueue(Uint8Array.of(byte)); c.close(); } }));
  const events = [];
  for await (const event of streamEvents(response)) events.push(event);
  assert.deepEqual(events, [{ content: '你好' }, { stop: true }]);
});

test('manifest bundles all entry files with no remote code or Google write access', () => {
  const manifest = JSON.parse(readFileSync(new URL('../manifest.json', import.meta.url)));
  assert.equal(manifest.manifest_version, 3);
  assert.ok(!manifest.permissions.includes('debugger'));
  assert.ok(!manifest.host_permissions.includes('<all_urls>'));
  for (const script of manifest.content_scripts.flatMap((entry) => entry.js).concat(manifest.background.service_worker, manifest.side_panel.default_path)) assert.ok(readFileSync(new URL('../' + script, import.meta.url)).length);
});
