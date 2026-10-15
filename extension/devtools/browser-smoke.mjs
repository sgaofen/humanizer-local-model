// Installed MV3 extension + real sidebar + launcher/fake engine. The Google
// Docs origin is intercepted with DOM and canvas-editor fixtures; no login or
// real document is read or changed. Run npm run test:browser in extension/.
import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { spawn, spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';

const extension = fileURLToPath(new URL('../', import.meta.url));
const app = fileURLToPath(new URL('../../app/', import.meta.url));
const scratch = await mkdtemp(path.join(tmpdir(), 'humanizer-extension-'));
const exe = process.platform === 'win32' ? '.exe' : '';
let launcher, browser, base;
const errors = [];

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(check, description, timeout = 15000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    const value = await check();
    if (value) return value;
    await wait(100);
  }
  throw new Error('Timed out: ' + description);
}

function cdpChannel(parent, sessionId) {
  let seq = 0;
  return async (method, params = {}) => {
    const id = ++seq;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        parent.off('Target.receivedMessageFromTarget', handler);
        reject(new Error('CDP timeout: ' + method));
      }, 15000);
      const handler = (event) => {
        if (event.sessionId !== sessionId) return;
        const message = JSON.parse(event.message);
        if (message.id !== id) return;
        clearTimeout(timer);
        parent.off('Target.receivedMessageFromTarget', handler);
        message.error ? reject(new Error(message.error.message)) : resolve(message.result);
      };
      parent.on('Target.receivedMessageFromTarget', handler);
      parent.send('Target.sendMessageToTarget', { sessionId, message: JSON.stringify({ id, method, params }) }).catch(reject);
    });
  };
}

try {
  for (const [name, pkg] of [['humanizer', '.'], ['fakellama', './devtools/fakellama']]) {
    const result = spawnSync('go', ['build', '-o', path.join(scratch, name + exe), pkg], { cwd: app, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
  }
  const data = path.join(scratch, 'data');
  await mkdir(path.join(data, 'models'), { recursive: true });
  await writeFile(path.join(data, 'models/humanizer-12b-Q8_0.gguf'), 'GGUF');
  await writeFile(path.join(data, 'settings.json'), '{"tier":"q8"}');
  const listener = net.createServer();
  await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  base = `http://127.0.0.1:${port}`;
  launcher = spawn(path.join(scratch, 'humanizer' + exe), [
    '--data-dir', data, '--engine', 'cpu=' + path.join(scratch, 'fakellama' + exe),
    '--web-dir', path.join(app, 'web'), '--port', String(port), '--no-browser', '--idle-exit', '0',
  ], { cwd: app, env: { ...process.env, FAKE_LOAD_MS: '10', FAKE_TOKEN_MS: '10', HUMANIZER_UPDATE_API: 'http://127.0.0.1:9', HUMANIZER_UPDATE_DELAY_SEC: '3600' }, stdio: 'ignore' });
  await until(async () => {
    try { return (await (await fetch(base + '/app/status')).json()).phase === 'ready'; }
    catch { return false; }
  }, 'launcher ready');

  browser = await chromium.launchPersistentContext(path.join(scratch, 'chrome'), {
    channel: 'chromium', headless: true,
    args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`],
  });
  const worker = browser.serviceWorkers()[0] || await browser.waitForEvent('serviceworker');
  worker.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  const pairing = await browser.newPage();
  await pairing.goto(base + '/extension-connect.html');
  await pairing.getByRole('button', { name: 'Connect extension', exact: true }).click();
  await pairing.getByText('Connected. Return to Google Docs and select text to humanize.', { exact: true }).waitFor();
  assert.ok(await worker.evaluate(async () => !!(await chrome.storage.session.get('connection')).connection));
  console.log('PASS: trusted pairing in installed extension');

  const docs = await browser.newPage();
  await docs.route('https://docs.google.com/**', (route) => route.fulfill({
    contentType: 'text/html', body: '<html><body><p id="draft">We reviewed 42 files in Toronto.</p></body></html>',
  }));
  await docs.goto('https://docs.google.com/document/d/fixture/edit');
  await docs.evaluate(() => {
    const range = document.createRange();
    range.selectNodeContents(document.querySelector('#draft'));
    getSelection().removeAllRanges();
    getSelection().addRange(range);
    document.dispatchEvent(new Event('selectionchange'));
  });
  // The button lives in a closed shadow root; click its host's center.
  await docs.locator('html > div').click();
  const parent = await browser.newCDPSession(docs);
  const target = await until(async () => (await parent.send('Target.getTargets')).targetInfos.find((t) => t.url.endsWith('/sidepanel.html')), 'native sidebar opened');
  // Playwright does not expose native sidebars as pages. Attach via CDP.
  const { sessionId } = await parent.send('Target.attachToTarget', { targetId: target.targetId, flatten: false });
  const send = cdpChannel(parent, sessionId);
  const evaluate = async (expression) => {
    const response = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (response.exceptionDetails) throw new Error(response.exceptionDetails.text);
    return response.result.value;
  };
  const state = () => evaluate('document.querySelector("#draft") && ({draft:document.querySelector("#draft").value,result:document.querySelector("#result").value,status:document.querySelector("#status").textContent,copyDisabled:document.querySelector("#copy").disabled})');
  const completed = await until(async () => {
    const value = await state();
    if (value?.status && /failed|couldn.t reach|cut off/i.test(value.status)) throw new Error(value.status);
    return value && !value.copyDisabled && value;
  }, 'streaming rewrite finished');
  assert.equal(completed.draft, 'We reviewed 42 files in Toronto.');
  assert.match(completed.result, /42/);
  assert.ok(await evaluate('document.querySelector("#review").textContent.includes("Check numbers")'));
  console.log('PASS: DOM selection → sidebar → paired local inference → review');

  await evaluate('document.querySelector("#draft").value = "A longer draft with 42 files. ".repeat(20); document.querySelector("#draft").dispatchEvent(new Event("input")); document.querySelector("#humanize").click();');
  await until(async () => (await state()).status.includes('Humanizing'), 'rewrite started');
  await evaluate('document.querySelector("#stop").click()');
  await until(async () => (await state()).status.includes('Stopped'), 'rewrite stopped');
  assert.ok((await state()).copyDisabled);
  console.log('PASS: Stop cancels inference and disables copying partial output');

  // Canvas-style fixture: no DOM selection; the editable iframe owns copy.
  await docs.evaluate(() => {
    getSelection().removeAllRanges();
    const overlay = document.createElement('div');
    overlay.className = 'kix-selection-overlay';
    overlay.style.cssText = 'position:absolute;top:30px;left:10px;width:200px;height:20px;background:#aac8';
    document.body.append(overlay);
    const frame = document.createElement('iframe');
    frame.className = 'docs-texteventtarget-iframe';
    document.body.append(frame);
    const doc = frame.contentDocument;
    doc.body.contentEditable = 'true';
    doc.addEventListener('copy', (event) => {
      event.clipboardData.setData('text/plain', 'A canvas selection with 17 files.');
      event.preventDefault();
    });
    doc.body.focus();
  });
  await docs.locator('html > div').click();
  await until(async () => {
    const value = await state();
    return value.draft === 'A canvas selection with 17 files.' && !value.copyDisabled;
  }, 'canvas selection rewrite');
  console.log('PASS: canvas selection captured through editable iframe copy handler');
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  if (launcher) {
    try { await fetch(base + '/app/quit', { method: 'POST', headers: { 'X-Humanizer': '1' } }); } catch { /* Already stopped. */ }
    await until(() => launcher.exitCode !== null, 'launcher shutdown', 10000).catch(() => launcher.kill());
  }
  await rm(scratch, { recursive: true, force: true });
}
