import { DEFAULT_APP_URL, changedNumbers, isDocsURL, normalizeAppURL } from './core.js';
import { LocalClient } from './client.js';

const $ = (id) => document.getElementById(id);
let connection, tab, controller, pendingAuto = false, finished = false, seen = new Set();
const documents = new Map();

function status(text, error = false) {
  $('status').textContent = text;
  $('status').classList.toggle('error', error);
}
function controls() {
  const busy = !!controller, docs = isDocsURL(tab?.url);
  for (const id of ['draft', 'selection', 'clipboard', 'clear', 'connect', 'app-url']) $(id).disabled = busy;
  $('selection').disabled = busy || !docs;
  $('clipboard').disabled = busy || !docs;
  $('humanize').disabled = busy || !connection || !docs || !$('draft').value.trim();
  $('stop').hidden = !busy;
  $('copy').disabled = busy || !finished;
  $('connection-badge').textContent = connection ? 'Local app paired' : 'Not connected';
}

async function humanize() {
  if (controller || !connection || !isDocsURL(tab?.url) || !$('draft').value.trim()) return;
  pendingAuto = false;
  const ownController = new AbortController();
  controller = ownController;
  finished = false;
  $('result').value = '';
  $('review').hidden = true;
  controls();
  const draft = $('draft').value;
  const sourceTab = tab.id;
  try {
    const result = await new LocalClient(connection).humanize(draft, {
      signal: ownController.signal,
      onStatus: (text) => { if (tab?.id === sourceTab) status(text); },
      onText: (text) => { if (tab?.id === sourceTab) $('result').value = text; },
    });
    if (tab?.id !== sourceTab) return;
    $('result').value = result.text;
    const { missing, added } = changedNumbers(draft, result.text);
    const notes = ['Check numbers, dates, names and claims before pasting.'];
    if (missing.length) notes.push('Numbers missing or changed: ' + missing.join(', ') + '.');
    if (added.length) notes.push('New or changed numbers: ' + added.join(', ') + '.');
    if (result.languageChanged) notes.push('The output language changed even after retries; try a new rewrite.');
    $('review').textContent = notes.join(' ');
    $('review').hidden = false;
    finished = true;
    status('Ready to review. The model unloads after five idle minutes.');
  } catch (error) {
    if (tab?.id === sourceTab) status(ownController.signal.aborted ? 'Stopped. You can try again.' : error.message === 'Failed to fetch' ? 'Couldn’t reach the local app. Start Humanizer, then reconnect.' : error.message, !ownController.signal.aborted);
  } finally {
    if (controller === ownController) controller = null;
    controls();
    await consumeRequest();
    if (pendingAuto) await humanize();
  }
}

async function consumeRequest() {
  if (!tab || controller) return;
  const key = `request:${tab.id}`;
  const request = (await chrome.storage.session.get(key))[key];
  if (!request || seen.has(request.id)) return;
  seen.add(request.id);
  await chrome.storage.session.remove(key);
  $('draft').value = request.text || '';
  $('result').value = '';
  $('review').hidden = true;
  finished = false;
  pendingAuto = !!request.text?.trim();
  if (!pendingAuto) status('Docs couldn’t expose this selection. Copy it in Docs, then click Use copied text.');
  else if (!connection) status('Selection captured. Connect the local app to humanize it.');
  controls();
  if (pendingAuto && connection) await humanize();
}

async function currentTab() {
  const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (tab?.id !== active?.id || tab?.url !== active?.url) {
    if (tab && isDocsURL(tab.url) && tab.id !== active?.id) {
      documents.set(tab.id, { draft: $('draft').value, result: $('result').value, review: $('review').textContent, finished, pendingAuto });
    }
    controller?.abort();
    // Let the previous run finish its cancellation before using a new document.
    tab = active;
    const saved = documents.get(active?.id);
    $('draft').value = saved?.draft || '';
    $('result').value = saved?.result || '';
    $('review').textContent = saved?.review || '';
    $('review').hidden = !saved?.finished;
    finished = !!saved?.finished;
    pendingAuto = !!saved?.pendingAuto;
    status(isDocsURL(active?.url) ? 'Select a passage in this Doc to humanize.' : 'Open a Google Doc to get started.');
  }
  controls();
  await consumeRequest();
  if (pendingAuto && connection) await humanize();
}

$('connect').addEventListener('click', async () => {
  try {
    const base = normalizeAppURL($('app-url').value);
    await chrome.tabs.create({ url: base + '/extension-connect.html' });
    status('Click Connect extension in the app page, then return to your Doc.');
  } catch (error) { status(error.message, true); }
});
$('humanize').addEventListener('click', humanize);
$('stop').addEventListener('click', () => controller?.abort());
$('draft').addEventListener('input', () => { finished = false; controls(); });
$('clear').addEventListener('click', async () => {
  pendingAuto = false;
  finished = false;
  $('draft').value = $('result').value = '';
  $('review').hidden = true;
  controls();
  status('Select another passage to get started.');
});
$('selection').addEventListener('click', async () => {
  try {
    const reply = await chrome.tabs.sendMessage(tab.id, { type: 'read-selection' });
    if (!reply?.text?.trim()) throw new Error('Copy the selected text in Docs, then click Use copied text.');
    $('draft').value = reply.text;
    finished = false;
    controls();
    status('Selection captured. Click Humanize when ready.');
  } catch (error) { status(error.message, true); }
});
$('clipboard').addEventListener('click', async () => {
  try {
    // Only this explicit button reads the system clipboard.
    const text = await navigator.clipboard.readText();
    if (!text.trim()) throw new Error('Copy the selected text in Docs first.');
    $('draft').value = text;
    finished = false;
    controls();
    status('Copied text loaded. Check it, then click Humanize.');
  } catch (error) { status(error.message || 'Paste the copied text into Selected text.', true); }
});
$('copy').addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText($('result').value);
    status('Copied. Return to Docs and paste over the selected passage.');
  } catch { status('Copy failed. Select and copy the result manually.', true); }
});

chrome.storage.onChanged.addListener(async (changes, area) => {
  if (area !== 'session') return;
  if (changes.connection) {
    connection = changes.connection.newValue;
    $('settings').open = !connection;
    controls();
    if (pendingAuto && connection) await humanize();
  }
  if (changes[`request:${tab?.id}`]?.newValue) await consumeRequest();
});
chrome.tabs.onActivated.addListener(() => currentTab().catch(console.error));
chrome.tabs.onRemoved.addListener((id) => documents.delete(id));
chrome.tabs.onUpdated.addListener((id, changes) => {
  if (!changes.url) return;
  documents.delete(id);
  if (id === tab?.id) currentTab().catch(console.error);
});
window.addEventListener('pagehide', () => controller?.abort());

async function init() {
  connection = (await chrome.storage.session.get('connection')).connection;
  $('app-url').value = (await chrome.storage.local.get('appURL')).appURL || DEFAULT_APP_URL;
  $('settings').open = !connection;
  await currentTab();
}
init().catch((error) => status(error.message, true));
