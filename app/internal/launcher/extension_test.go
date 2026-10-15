package launcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testExtensionOrigin = "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func pairedRequest(t *testing.T, base, path, method, body, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	req.Header.Set("Origin", testExtensionOrigin)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Humanizer", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func pairingToken(t *testing.T, base string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/app/extension-token", nil)
	req.Header.Set("X-Humanizer", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct{ Token string }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Token) != 64 || resp.StatusCode != 200 {
		t.Fatalf("pairing token: %d, %v, %+v", resp.StatusCode, err, result)
	}
	return result.Token
}

func TestExtensionGuard(t *testing.T) {
	a, base := testLauncher(t)
	secret := pairingToken(t, base)
	if other := pairingToken(t, base); other != secret {
		t.Fatal("token changed within one app launch")
	}
	for _, tc := range []struct {
		path, token, origin, host, header string
		want                              int
	}{
		{"/extension/status", secret, testExtensionOrigin, "", "1", 200},
		{"/extension/status", "", testExtensionOrigin, "", "1", 401},
		{"/extension/status", "wrong", testExtensionOrigin, "", "1", 401},
		{"/extension/status", secret, "https://docs.google.com", "", "1", 403},
		{"/extension/status", secret, "chrome-extension://bad", "", "1", 403},
		{"/extension/status", secret, testExtensionOrigin, "evil.test", "1", 403},
		{"/extension/status", secret, testExtensionOrigin, "", "", 401},
		{"/app/extension-token", secret, testExtensionOrigin, "", "1", 403},
		{"/app/extension-token", "", "", "", "", 403},
		{"/app/status", secret, testExtensionOrigin, "", "1", 403},
		{"/api/props", secret, testExtensionOrigin, "", "1", 403},
		{"/extension/api/props", secret, testExtensionOrigin, "", "1", 404},
	} {
		req := httptest.NewRequest("GET", base+tc.path, nil)
		if tc.host != "" {
			req.Host = tc.host
		}
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		req.Header.Set("X-Humanizer", tc.header)
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s origin=%s token=%q: got %d want %d", tc.path, tc.origin, tc.token, w.Code, tc.want)
		}
	}
	req := httptest.NewRequest("OPTIONS", base+"/extension/wake", nil)
	req.Header.Set("Origin", testExtensionOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != testExtensionOrigin {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
	b, _ := testLauncher(t)
	if b.extensionToken != "" {
		t.Fatal("secret leaked into another app launch")
	}
}

func TestExtensionUnloadAndReload(t *testing.T) {
	a, base := testLauncher(t)
	t.Setenv("FAKE_LOAD_MS", "1")
	t.Setenv("FAKE_TOKEN_MS", "1")
	binary := filepath.Join(t.TempDir(), "fakellama")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../devtools/fakellama")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake engine: %v\n%s", err, out)
	}
	a.backends = []Backend{{Name: "test", Path: binary}}
	tier := a.cfg.Tiers[0]
	a.cfg.Engine.LoadTimeoutSec = 10
	if err := os.MkdirAll(filepath.Dir(a.modelPath(tier)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.modelPath(tier), []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.startEngine(tier)
	t.Cleanup(a.stopAll)
	if !a.waitForModel(context.Background()) {
		t.Fatal("fake engine did not start")
	}
	secret := pairingToken(t, base)
	resp := pairedRequest(t, base, "/extension/wake", "POST", `{}`, secret)
	if resp.StatusCode != 200 {
		t.Fatalf("wake: %d", resp.StatusCode)
	}
	a.mu.Lock()
	first, lastUsed := a.engine, a.modelLastUsed
	a.mu.Unlock()
	if a.unloadIdleModel(lastUsed.Add(modelIdleTimeout - time.Nanosecond)) {
		t.Fatal("unloaded before five minutes")
	}
	a.inflight.Add(1)
	if a.unloadIdleModel(lastUsed.Add(2 * modelIdleTimeout)) {
		t.Fatal("unloaded during a model request")
	}
	a.inflight.Add(-1)
	resp = pairedRequest(t, base, "/extension/status", "GET", "", secret)
	io.Copy(io.Discard, resp.Body)
	a.mu.Lock()
	unchanged := a.modelLastUsed.Equal(lastUsed)
	a.mu.Unlock()
	if !unchanged {
		t.Fatal("status polling postponed model unloading")
	}
	if !a.unloadIdleModel(lastUsed.Add(modelIdleTimeout)) || first.alive() {
		t.Fatal("idle unload did not stop the actual engine process")
	}
	resp = pairedRequest(t, base, "/extension/status", "GET", "", secret)
	var sleeping map[string]any
	json.NewDecoder(resp.Body).Decode(&sleeping)
	if sleeping["phase"] != "sleeping" || sleeping["idle_unload_seconds"] != float64(300) {
		t.Fatalf("sleep status: %v", sleeping)
	}
	// The existing desktop app's /api route must also wake a sleeping engine.
	code, tokens := postApp(t, base, "/api/tokenize", `{"content":"42 files"}`)
	if code != 200 || tokens["tokens"] == nil {
		t.Fatalf("automatic reload: %d %v", code, tokens)
	}
	a.mu.Lock()
	second, reset := a.engine, a.modelLastUsed.After(lastUsed)
	a.mu.Unlock()
	if second == first || second == nil || !reset || a.inflight.Load() != 0 {
		t.Fatal("engine was not replaced or idle deadline/count was not reset")
	}
	body, _ := json.Marshal(map[string]any{"prompt": BuildPrompt("We reviewed 42 files."), "stream": true, "n_predict": 256, "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0})
	resp = pairedRequest(t, base, "/extension/api/completion", "POST", string(body), secret)
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(out), `"stop":true`) {
		t.Fatalf("paired completion: %d %s", resp.StatusCode, out)
	}
}

func TestExtensionWakeRequiresBundledModel(t *testing.T) {
	a, base := testLauncher(t)
	secret := pairingToken(t, base)
	for _, external := range []bool{false, true} {
		a.mu.Lock()
		a.phase = phaseSetup
		if external {
			a.phase = phaseReady
			a.settings.External = &ExternalSettings{Enabled: true, URL: "http://localhost:1234/v1"}
		}
		a.mu.Unlock()
		resp := pairedRequest(t, base, "/extension/wake", "POST", `{}`, secret)
		if resp.StatusCode != 409 || a.modelIdle {
			t.Fatalf("invalid wake external=%v: %d", external, resp.StatusCode)
		}
	}
}
