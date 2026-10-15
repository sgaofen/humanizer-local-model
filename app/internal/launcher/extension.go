package launcher

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const modelIdleTimeout = 5 * time.Minute

var extensionOrigin = regexp.MustCompile(`^chrome-extension://[a-p]{32}$`)

func (a *App) registerExtensionRoutes(mux *http.ServeMux) {
	// The token is available only to the local app page, never to an extension
	// origin. The pairing content script sends it after a trusted button click.
	mux.HandleFunc("GET /app/extension-token", a.handleExtensionToken)
	mux.HandleFunc("GET /extension/status", a.handleExtensionStatus)
	mux.HandleFunc("GET /extension/config", a.handleConfig)
	mux.HandleFunc("POST /extension/wake", a.handleExtensionWake)
	proxy := a.countInflight(a.apiProxy())
	for _, path := range []string{"/completion", "/tokenize"} {
		mux.Handle("POST /extension/api"+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.mu.Lock()
			ext := a.externalOn()
			a.mu.Unlock()
			if ext {
				writeJSON(w, 409, map[string]any{"error": "Use the bundled model in the Humanizer app. External services cannot be unloaded by this extension."})
				return
			}
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/extension")
			// Limit selected text and completion bodies before proxying.
			r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
			proxy.ServeHTTP(w, r)
		}))
	}
}

func (a *App) handleExtensionToken(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Humanizer") != "1" {
		http.Error(w, "missing X-Humanizer header", http.StatusForbidden)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.extensionToken == "" {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			writeJSON(w, 500, map[string]any{"error": "Could not create pairing token."})
			return
		}
		a.extensionToken = hex.EncodeToString(secret[:])
	}
	writeJSON(w, 200, map[string]any{"token": a.extensionToken})
}

// Keep the existing Host/Origin/X-Humanizer guard intact for /app and /api.
// Only this narrow API accepts extension origins, with a per-launch secret.
func (a *App) guardExtension(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" && !extensionOrigin.MatchString(origin) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return false
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	if origin != "" {
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		if origin == "" || (r.Header.Get("Access-Control-Request-Method") != "GET" && r.Header.Get("Access-Control-Request-Method") != "POST") {
			http.Error(w, "forbidden preflight", http.StatusForbidden)
			return false
		}
		h.Set("Access-Control-Allow-Methods", "GET, POST")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Humanizer")
		h.Set("Access-Control-Allow-Private-Network", "true")
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	a.mu.Lock()
	secret := a.extensionToken
	a.mu.Unlock()
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if secret == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 || r.Header.Get("X-Humanizer") != "1" {
		http.Error(w, "Reconnect the extension from the Humanizer app.", http.StatusUnauthorized)
		return false
	}
	return true
}

func (a *App) handleExtensionStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	phase := a.phase
	if a.modelSleeping && !a.externalOn() {
		phase = "sleeping"
	}
	writeJSON(w, 200, map[string]any{
		"app": "humanizer", "phase": phase, "external": a.externalOn(),
		"idle_unload_seconds":  int(modelIdleTimeout / time.Second),
		"load_timeout_seconds": a.cfg.Engine.LoadTimeoutSec,
		"error":                a.errInfo,
	})
}

func (a *App) handleExtensionWake(w http.ResponseWriter, r *http.Request) {
	a.modelUseMu.Lock()
	defer a.modelUseMu.Unlock()
	a.mu.Lock()
	if a.externalOn() || (a.phase != phaseReady && a.phase != phaseStarting) {
		a.mu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "Open the Humanizer app and finish setting up its bundled model first."})
		return
	}
	a.modelIdle = true
	a.modelLastUsed = time.Now()
	a.mu.Unlock()
	a.wakeSleepingModel()
	a.handleExtensionStatus(w, r)
}

// Caller holds modelUseMu. A sleeping model keeps phaseReady so the app's
// editor stays editable; /api requests reload it before they reach the proxy.
func (a *App) wakeSleepingModel() bool {
	a.mu.Lock()
	if !a.modelSleeping || a.externalOn() || a.stopping {
		a.mu.Unlock()
		return false
	}
	t, ok := a.cfg.tier(a.tier)
	if !ok {
		a.mu.Unlock()
		return false
	}
	a.modelSleeping = false
	a.mu.Unlock()
	a.startEngine(t)
	return true
}

func (a *App) waitForModel(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.cfg.Engine.LoadTimeoutSec)*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		a.mu.Lock()
		ready := a.phase == phaseReady && a.engine != nil && a.engine.alive()
		failed := a.phase == phaseError || a.stopping || a.externalOn()
		a.mu.Unlock()
		if ready || failed {
			return ready
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func (a *App) modelIdleLoop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			a.unloadIdleModel(now)
		}
	}
}

func (a *App) unloadIdleModel(now time.Time) bool {
	a.modelUseMu.Lock()
	defer a.modelUseMu.Unlock()
	if a.inflight.Load() != 0 || (a.updates != nil && a.updates.busy()) {
		return false
	}
	a.mu.Lock()
	if !a.modelIdle || a.modelSleeping || a.stopping || a.externalOn() || a.phase != phaseReady || a.engine == nil || now.Sub(a.modelLastUsed) < modelIdleTimeout {
		a.mu.Unlock()
		return false
	}
	p, cancel := a.engine, a.engineCancel
	a.engine, a.engineCancel = nil, nil
	a.modelSleeping = true
	a.mu.Unlock()
	// Cancel before stopping: watchEngine must not restart an intentional unload.
	if cancel != nil {
		cancel()
	}
	p.stop()
	a.logf("Model unloaded after %s of inference inactivity", modelIdleTimeout)
	return true
}
