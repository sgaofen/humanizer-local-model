(() => {
  const button = document.getElementById('humanizer-connect');
  const status = document.getElementById('humanizer-connect-status');
  if (!button || !status) return;
  button.disabled = false;
  status.textContent = 'Click Connect extension, then return to your Google Doc.';
  button.addEventListener('click', async (event) => {
    if (!event.isTrusted) return;
    button.disabled = true;
    try {
      const response = await fetch('/app/extension-token', { headers: { 'X-Humanizer': '1' }, cache: 'no-store' });
      if (!response.ok) throw new Error('This app version does not support the extension. Update or build the app from this branch.');
      const { token } = await response.json();
      const reply = await chrome.runtime.sendMessage({ type: 'pair', token });
      if (!reply?.ok) throw new Error('Connection failed. Reload this page and try again.');
      status.textContent = 'Connected. Return to Google Docs and select text to humanize.';
      button.textContent = 'Connected';
    } catch (error) {
      status.textContent = error.message;
      button.disabled = false;
    }
  });
})();
