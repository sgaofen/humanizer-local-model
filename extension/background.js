import { isDocsURL, normalizeAppURL } from './core.js';

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: 'humanize', title: 'Humanize with local model',
      contexts: ['selection', 'editable', 'page'],
      documentUrlPatterns: ['https://docs.google.com/document/*'],
    });
  });
});
chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }).catch(console.error);

async function capture(tabId, selectionText) {
  let text = selectionText || '';
  if (!text.trim()) {
    try { text = (await chrome.tabs.sendMessage(tabId, { type: 'read-selection' }))?.text || ''; }
    catch { /* The sidebar offers an explicit clipboard fallback. */ }
  }
  await chrome.storage.session.set({ [`request:${tabId}`]: { id: crypto.randomUUID(), text, tabId } });
}

chrome.contextMenus.onClicked.addListener((info, tab) => {
  if (info.menuItemId !== 'humanize' || !tab?.id || !isDocsURL(tab.url)) return;
  // Open before any await so Chrome still has the context-menu user gesture.
  chrome.sidePanel.open({ tabId: tab.id }).catch(console.error);
  capture(tab.id, info.selectionText).catch(console.error);
});

chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (message?.type === 'open-humanizer' && sender.tab && isDocsURL(sender.url)) {
    chrome.sidePanel.open({ tabId: sender.tab.id }).catch(console.error);
    capture(sender.tab.id).then(() => respond({ ok: true }), () => respond({ ok: false }));
    return true;
  }
  if (message?.type === 'pair' && sender.tab) {
    let base;
    try {
      const url = new URL(sender.url);
      if (url.pathname !== '/extension-connect.html' || !/^[0-9a-f]{64}$/.test(message.token)) return;
      base = normalizeAppURL(url.origin);
    } catch { return; }
    // Pairing secrets live in browser-session storage, never synced to an account.
    chrome.storage.session.set({ connection: { base, token: message.token } })
      .then(() => chrome.storage.local.set({ appURL: base }))
      .then(() => respond({ ok: true }), () => respond({ ok: false }));
    return true;
  }
});

chrome.tabs.onRemoved.addListener((tabId) => {
  chrome.storage.session.remove(`request:${tabId}`).catch(console.error);
});
chrome.tabs.onUpdated.addListener((tabId, changes) => {
  if (changes.url) chrome.storage.session.remove(`request:${tabId}`).catch(console.error);
});
