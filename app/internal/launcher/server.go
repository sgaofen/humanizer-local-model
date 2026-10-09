package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 反代给 llama-server 的白名单。其余(/slots、/metrics、/v1/* 等)一律不暴露。
var apiAllow = map[string]bool{
	"/completion": true,
	"/tokenize":   true,
	"/detokenize": true,
	"/health":     true,
	"/props":      true,
}

func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app/ping", a.handlePing)
	mux.HandleFunc("GET /app/status", a.handleStatus)
	mux.HandleFunc("GET /app/config", a.handleConfig)
	mux.HandleFunc("POST /app/setup", a.handleSetup)
	mux.HandleFunc("POST /app/document", a.handleDocument)
	mux.HandleFunc("POST /app/download/pause", a.handlePause)
	mux.HandleFunc("POST /app/paths", a.handlePaths)
	mux.HandleFunc("POST /app/pick-dir", a.handlePickDir)
	mux.HandleFunc("POST /app/engine/restart", a.handleRestart)
	mux.HandleFunc("POST /app/reveal", a.handleReveal)
	mux.HandleFunc("POST /app/quit", a.handleQuit)
	a.registerUpdateRoutes(mux)
	mux.Handle("/api/", a.countInflight(a.apiProxy()))
	mux.Handle("/", a.static())
	return a.guard(mux)
}

// guard 挡掉三类东西:
//  1. Host 不是 127.0.0.1/localhost(防 DNS rebinding);
//  2. 跨站页面带着 Origin 来调接口;
//  3. 没带 X-Humanizer 头的写操作(自定义头会触发 CORS 预检,别的网站发不出来)。
func (a *App) guard(next http.Handler) http.Handler {
	okHost := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", a.port): true,
		fmt.Sprintf("localhost:%d", a.port): true,
	}
	okOrigin := map[string]bool{
		fmt.Sprintf("http://127.0.0.1:%d", a.port): true,
		fmt.Sprintf("http://localhost:%d", a.port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !okHost[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !okOrigin[o] {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		isAPI := strings.HasPrefix(r.URL.Path, "/app/") || strings.HasPrefix(r.URL.Path, "/api/")
		if isAPI && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Humanizer") != "1" {
			http.Error(w, "missing X-Humanizer header", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if r.URL.Path != "/app/ping" {
			a.touch()
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) static() http.Handler {
	fsrv := http.FileServerFS(a.web)
	csp := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Cache-Control", "no-cache")
		fsrv.ServeHTTP(w, r)
	})
}

func (a *App) apiProxy() http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			port, _ := strconv.Atoi(pr.In.Header.Get("X-Engine-Port"))
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/api")
			pr.Out.URL.RawPath = ""
			pr.SetURL(&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)})
			pr.Out.Host = pr.Out.URL.Host
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Del("Referer")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("X-Humanizer")
			pr.Out.Header.Del("X-Engine-Port")
			pr.Out.Header.Set("Authorization", "Bearer "+a.apiKey)
		},
		FlushInterval: -1, // SSE:每个 token 立刻往浏览器推
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil { // 用户点了停止,浏览器断开
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"code": 502, "message": "引擎连接失败:" + err.Error()}})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := strings.TrimPrefix(r.URL.Path, "/api")
		if !apiAllow[sub] {
			http.NotFound(w, r)
			return
		}
		a.mu.Lock()
		p := a.engine
		ready := a.phase == phaseReady && p != nil && p.alive()
		a.mu.Unlock()
		if !ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": 503, "message": "模型还没准备好"}})
			return
		}
		r.Header.Set("X-Engine-Port", strconv.Itoa(p.port))
		rp.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"app": "humanizer", "version": a.opts.Version, "pid": os.Getpid()})
}

type tierStatus struct {
	Tier
	Downloaded bool  `json:"downloaded"`
	Partial    int64 `json:"partial,omitempty"`
	Fits       bool  `json:"fits"`
}

type dlStatus struct {
	Tier       string  `json:"tier"`
	File       string  `json:"file"`
	Stage      string  `json:"stage"` // probe / fetch / verify
	Received   int64   `json:"received"`
	Total      int64   `json:"total"`
	Speed      float64 `json:"speed"`
	ETA        float64 `json:"eta"`
	VerifyDone int64   `json:"verify_done"`
	Attempt    int     `json:"attempt"`
}

type statusResp struct {
	App             string       `json:"app"`
	Version         string       `json:"version"`
	Phase           string       `json:"phase"`
	Sys             SysInfo      `json:"sys"`
	Recommended     string       `json:"recommended"`
	Tier            string       `json:"tier"`
	Endpoint        string       `json:"endpoint"`
	Tiers           []tierStatus `json:"tiers"`
	Endpoints       []Endpoint   `json:"endpoints"`
	Download        *dlStatus    `json:"download,omitempty"`
	Engine          *engineInfo  `json:"engine,omitempty"`
	Backends        []string     `json:"backends"`
	Error           *errInfo     `json:"error,omitempty"`
	DataDir         string       `json:"data_dir"`
	ModelDir        string       `json:"model_dir"`
	DefaultDataDir  string       `json:"default_data_dir"`
	DataDirLocked   bool         `json:"data_dir_locked"`
	IdleExitMinutes int          `json:"idle_exit_minutes"`
	CtxSize         int          `json:"ctx_size"`
	// Notice:"lite_retired" = 这台机器以前选的是已下线的 lite 档,网页提示换成推荐的 12B 档位;旧文件不删。
	Notice string `json:"notice,omitempty"`
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	dataDir, modelsDir := a.pathsNow()
	defDir := a.pointerDir
	if defDir == "" {
		defDir = defaultDataDir()
	}
	s := statusResp{
		App: "humanizer", Version: a.opts.Version, Sys: a.sys,
		Recommended: a.cfg.recommendTier(a.sys.RAMGB),
		Endpoints:   a.cfg.Endpoints,
		DataDir:     dataDir, ModelDir: modelsDir, DefaultDataDir: defDir, DataDirLocked: a.dataDirLocked,
		IdleExitMinutes: int(a.idleExit / time.Minute),
		CtxSize:         a.cfg.Engine.CtxSize,
	}
	for _, b := range a.backends {
		s.Backends = append(s.Backends, b.Name)
	}
	for _, t := range a.cfg.Tiers {
		ts := tierStatus{Tier: t, Fits: a.sys.RAMGB+a.cfg.RAMSlackGB >= t.MinRAMGB}
		p := a.modelPath(t)
		ts.Downloaded = fileExists(p)
		if st, err := os.Stat(p + ".part"); err == nil {
			ts.Partial = st.Size()
		}
		s.Tiers = append(s.Tiers, ts)
	}
	a.mu.Lock()
	s.Phase = a.phase
	s.Tier = a.tier
	s.Endpoint = a.settings.Endpoint
	if _, ok := a.cfg.tier(a.settings.Tier); !ok && a.settings.Tier == "lite" {
		s.Notice = "lite_retired"
	}
	if s.Endpoint == "" {
		s.Endpoint = a.cfg.Endpoints[0].ID
	}
	if a.errInfo != nil {
		e := *a.errInfo
		s.Error = &e
	}
	if d := a.dl; d != nil && (a.phase == phaseDownloading || a.phase == phasePaused || (a.phase == phaseError && a.errInfo != nil && a.errInfo.Kind == "download")) {
		t, _ := a.cfg.tier(a.dlTier)
		ds := &dlStatus{Tier: a.dlTier, File: t.File, Stage: d.Phase(), Received: d.Received.Load(), Total: d.Total.Load(),
			VerifyDone: d.VerifyDone.Load(), Attempt: int(d.Attempt.Load())}
		if a.phase == phaseDownloading {
			ds.Speed = d.Speed()
			if ds.Speed > 0 && ds.Total > 0 {
				ds.ETA = float64(ds.Total-ds.Received) / ds.Speed
			}
		}
		s.Download = ds
	}
	if a.phase == phaseStarting || a.phase == phaseReady || (a.phase == phaseError && a.errInfo != nil && a.errInfo.Kind == "engine") {
		e := a.engInfo
		e.Attempts = append([]attemptLog(nil), a.engInfo.Attempts...)
		s.Engine = &e
	}
	a.mu.Unlock()
	writeJSON(w, 200, s)
}

// handleConfig 给网页拼提示词和采样参数用。probe = BuildPrompt("X"),
// 网页用自己拼的结果和它逐字比对,对不上就拒绝改写(防止两边漂移)。
func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"instr":       promptInstr,
		"sep":         promptSep,
		"probe":       BuildPrompt("X"),
		"fingerprint": PromptFingerprint(),
		"sampling":    a.cfg.Sampling,
		"n_predict":   a.cfg.NPredict,
		"ctx_size":    a.cfg.Engine.CtxSize,
	})
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tier     string `json:"tier"`
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad json"})
		return
	}
	t, ok := a.cfg.tier(req.Tier)
	if !ok {
		writeJSON(w, 400, map[string]any{"error": "unknown tier"})
		return
	}
	if req.Endpoint == "" {
		req.Endpoint = a.settings.Endpoint
	}
	ep, ok := a.cfg.endpoint(req.Endpoint)
	if !ok {
		ep = a.cfg.Endpoints[0]
	}
	a.mu.Lock()
	phase, cur := a.phase, a.tier
	a.mu.Unlock()
	if cur == t.ID && (phase == phaseDownloading || phase == phaseStarting || phase == phaseReady) {
		writeJSON(w, 200, map[string]any{"ok": true, "phase": phase})
		return
	}
	a.mu.Lock()
	a.settings.Tier, a.settings.Endpoint = t.ID, ep.ID
	a.restarts = 0
	s := a.settings
	a.mu.Unlock()
	if err := saveSettings(a.settingsPath(), s); err != nil {
		a.logf("保存设置失败:%v", err)
	}
	a.stopEngine()
	if fileExists(a.modelPath(t)) {
		a.startEngine(t)
	} else {
		a.startDownload(t, ep)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handlePause(w http.ResponseWriter, r *http.Request) {
	a.pauseDownload()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleRestart(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	t, ok := a.cfg.tier(a.tier)
	a.restarts = 0
	a.mu.Unlock()
	if !ok || !fileExists(a.modelPath(t)) {
		writeJSON(w, 409, map[string]any{"error": "no model"})
		return
	}
	a.stopEngine()
	a.startEngine(t)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		What string `json:"what"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req)
	dataDir, modelsDir := a.pathsNow()
	dir := modelsDir
	switch req.What {
	case "logs":
		dir = filepath.Join(dataDir, "logs")
	case "data":
		dir = dataDir
	}
	_ = os.MkdirAll(dir, 0o755)
	if err := revealPath(dir); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
	go func() {
		time.Sleep(150 * time.Millisecond) // 先让响应发出去
		a.shutdown()
	}()
}
