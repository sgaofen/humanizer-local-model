# Humanizer for Google Docs

A Chrome Manifest V3 extension that rewrites selected Google Docs text with the **bundled local model in the Humanizer app**. Chrome does not load the 12B model itself. The app releases the model's RAM/VRAM after five minutes without a model request and reloads it for the next rewrite.

## Install

This extension needs the app built from this branch (or a future release containing the extension API). The existing 0.3.4 release does not have these routes.

1. Build/start the Humanizer app and finish downloading a model. See [app development instructions](../app/README.md#开发). Use the bundled engine; external services such as LM Studio are not supported by this extension because the app cannot unload a model it does not own.
2. Open `chrome://extensions`, turn on **Developer mode**, click **Load unpacked**, and choose this `extension` directory. No extension build or npm install is required. Chrome 116 or newer is required.
3. Open a Google Doc and click the extension's toolbar icon to open its sidebar. If you have just installed the extension, reload the Doc.
4. In **Local app connection**, check the app address (usually `http://127.0.0.1:47615`) and click **Connect app**. Click **Connect extension** on the local page that opens, then return to your Doc. If the app uses another port, use the address shown by the app.
5. Reconnect after the app restarts: pairing secrets change on every launch.

To develop without downloading weights, run `bash devtools/dev.sh` from `app/`, then connect to `http://127.0.0.1:47690`. Its fake engine exercises the integration but does not produce real model rewrites.

## Use

Select a passage in Google Docs and click the floating **Humanize selection** button. The Chrome right-click menu also offers **Humanize with local model** when the native menu is available (Docs sometimes shows its own menu). The selection appears in the sidebar and, if paired, starts rewriting automatically.

Docs uses a canvas rather than normal DOM text. The extension first reads a normal selection, then asks the editor's copy handler for the current selection. If Docs does not expose it, copy the passage using **Ctrl+C / ⌘C**, open the sidebar, and click **Use copied text**, then **Humanize**. That button is the only action that reads the existing system clipboard. You can also paste directly into the Selected text field.

The result streams into the sidebar. Review names, dates, numbers and claims; the model can change details. Number differences are highlighted in a note, and outputs in the wrong language are retried up to three times. Click **Copy result**, then paste over the selected passage in Docs. Pasting replaces the selection as plain text; Docs' Undo reverses it. The extension does not automatically edit the document.

Use **Stop** to cancel. Closing the sidebar also cancels the current request. Switching documents cancels a running rewrite and keeps drafts/results separate within the sidebar session. Very long selections are rejected if they cannot fit the model context; truncated or interrupted output cannot be copied with Copy result. Use smaller passages or the app/`hz` for long documents.

## Five-minute unload

- The first extension rewrite enables the app's five-minute model idle mode for this launch.
- The clock starts when model loading or the latest model request finishes. Requests from the app's own editor also reset it, since they use the same engine.
- Active tokenization/completion requests and updates prevent unloading. Merely polling app/extension status does not reset the clock.
- The Go launcher owns the timer, so closing the sidebar, suspending Chrome's service worker, or closing Chrome does not prevent unloading. It stops the actual llama-server process, releasing its model memory; downloaded model files remain on disk.
- The launcher stays available and reloads the model on the next extension or app-editor request. Reloading can take time. The existing app auto-exit setting still applies (normally 30 minutes without app activity); start the app again if it has exited.
- This is inference inactivity, not a timer that interrupts a rewrite exactly five minutes after it started. The timer checks once per second; graceful engine shutdown may take a few seconds.

## Privacy and permissions

Selected text is sent only to the paired loopback Humanizer app. The extension has no cloud inference, analytics, remote JavaScript, Google account authorization, or document write API. It reads only requested selections, and keeps pairing credentials/selection requests in Chrome session storage rather than account sync. The app address is the only preference written to persistent local storage. Capturing a canvas selection can copy it to the clipboard through Docs' own copy handler.

Permissions: Google Docs access for the selection button, loopback access for pairing/inference, `sidePanel` for the sidebar, `contextMenus` for Humanize, `storage` for session coordination, and clipboard read/write for explicit copy/paste actions. No debugger or all-sites permission is used.

Pairing requires a real click on the local app page and creates a random per-launch secret. The app's existing Host/Origin guards remain in effect for `/app/*` and `/api/*`; the authenticated extension API exposes only status, prompt configuration, wake, tokenization and completion. Text is rendered as text, never HTML.

## Tests

```sh
node --test extension/tests/*.test.mjs
cd app
go test -race ./...
node devtools/webtest.mjs
node devtools/webtest_update.mjs
bash devtools/e2e.sh
```

The extension tests cover local-only URLs, training prompt/sampling, streaming boundaries, cancellation, language retries, changed numbers, oversized selections, and truncated/interrupted output. The Go integration test starts the fake engine, exercises authenticated inference, advances the idle clock, verifies that the real child process stops, and checks automatic reloading through the app's existing API. No model download is needed for these tests.

For the installed extension and native sidebar browser smoke test, install the **development-only** browser tooling (still no extension build required):

```sh
cd extension
npm ci
npx playwright install chromium
npm run test:browser
```

The smoke test builds/starts the app with the fake engine, loads the real extension in Chromium, pairs on the local app page, captures DOM and canvas-style iframe selections, streams inference into the native sidebar, and cancels a rewrite. Google Docs' origin is served by test fixtures, so it does not require an account or access real documents. Temporary profiles, processes and test data are cleaned up. Run it separately from `app/devtools/e2e.sh`, whose process checks assume no other Humanizer engine is running.

Live Google Docs smoke check (requires a signed-in Chrome): select across paragraphs, humanize, review/copy/paste, Undo, test keyboard-only selection, test the clipboard fallback, stop a rewrite, close the sidebar, wait five idle minutes, and humanize again. Google Docs' private canvas/copy behavior can change independently of the extension.
