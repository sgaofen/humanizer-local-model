package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type instanceInfo struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	EnginePID int    `json:"engine_pid,omitempty"`
	Version   string `json:"version,omitempty"`
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ";") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Main 是启动器入口,返回进程退出码。
func Main(o Options) int {
	fl := flag.NewFlagSet("humanizer", flag.ContinueOnError)
	dataDir := fl.String("data-dir", envOr("HUMANIZER_DATA_DIR", defaultDataDir()), "数据目录(模型、日志、设置)")
	var engines multiFlag
	fl.Var(&engines, "engine", "指定 llama-server 路径,可写 name=path,可重复(开发/测试用)")
	baseURL := fl.String("base-url", envOr("HUMANIZER_BASE_URL", os.Getenv("HF_ENDPOINT")), "自定义下载源(替代 huggingface.co)")
	noBrowser := fl.Bool("no-browser", os.Getenv("HUMANIZER_NO_BROWSER") != "", "不自动打开浏览器")
	webDir := fl.String("web-dir", os.Getenv("HUMANIZER_WEB_DIR"), "从磁盘读网页(开发用,改完刷新即可)")
	port := fl.Int("port", atoiOr(os.Getenv("HUMANIZER_PORT"), 0), "网页端口(默认用配置里的固定端口,保证历史记录不丢)")
	idle := fl.Int("idle-exit", atoiOr(os.Getenv("HUMANIZER_IDLE_EXIT"), -1), "网页关闭多少分钟后自动退出,0=不退出,-1=用配置")
	serve := fl.Bool("serve", false, "前台运行服务(macOS .app 内部使用)")
	showVer := fl.Bool("version", false, "打印版本")
	updateAPI := fl.String("update-api", os.Getenv("HUMANIZER_UPDATE_API"), "检查 App 更新用的 GitHub API 地址(开发/测试时指向假服务器)")
	applyPlanPath := fl.String("apply-update", "", "内部使用:按更新计划替换并重启(Windows 更新助手)")
	fl.SetOutput(io.Discard)
	var args []string
	for _, a := range o.Args { // 老版本 Finder 会塞一个 -psn_0_xxx
		if !strings.HasPrefix(a, "-psn_") {
			args = append(args, a)
		}
	}
	if err := fl.Parse(args); err != nil {
		fl.SetOutput(os.Stderr)
		fl.PrintDefaults()
		return 2
	}
	if *showVer {
		fmt.Println("humanizer", o.Version)
		return 0
	}
	if *applyPlanPath != "" {
		return runApplyHelper(*applyPlanPath)
	}
	if abs, err := filepath.Abs(*dataDir); err == nil { // 引擎的工作目录不是这里,必须用绝对路径
		*dataDir = abs
	}
	if e := os.Getenv("HUMANIZER_ENGINE"); e != "" && len(engines) == 0 {
		engines = append(engines, e)
	}

	if err := os.MkdirAll(filepath.Join(*dataDir, "logs"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "无法创建数据目录:", err)
		return 1
	}

	// 已经有一个在跑:直接打开它的页面
	if inst, ok := runningInstance(*dataDir); ok {
		u := fmt.Sprintf("http://127.0.0.1:%d/", inst.Port)
		fmt.Println("Humanizer 已在运行:", u)
		if !*noBrowser {
			_ = openURL(u)
		}
		return 0
	}

	// macOS 双击 .app:前台进程只负责把服务拉到后台然后立刻退出。
	// 这样 LaunchServices 认为 App 已结束,下次双击会再启动一个前台进程,
	// 它发现服务在跑就直接打开网页(纯 Go 程序收不到 "reopen" 事件,只能这么做)。
	if runtime.GOOS == "darwin" && !*serve && insideAppBundle() {
		return spawnDetached(args)
	}

	logPath := filepath.Join(*dataDir, "logs", "launcher.log")
	rotateLog(logPath, 2<<20)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	var logw io.Writer = os.Stderr
	if err == nil {
		defer lf.Close()
		logw = io.MultiWriter(lf, os.Stderr)
	}
	logger := log.New(logw, "", log.LstdFlags)
	logger.Printf("===== Humanizer %s 启动 (%s/%s) 数据目录 %s", o.Version, runtime.GOOS, runtime.GOARCH, *dataDir)

	cfg, err := loadConfig(o.DefaultConfig, filepath.Join(*dataDir, "config.json"))
	if err != nil {
		logger.Printf("配置错误:%v", err)
		return 1
	}
	if *baseURL != "" { // 开发/私有镜像:只用这一个下载源
		cfg.Endpoints = []Endpoint{{ID: "custom", Label: strings.TrimPrefix(strings.TrimPrefix(*baseURL, "https://"), "http://"), Base: *baseURL}}
	}
	if *updateAPI != "" {
		cfg.Update.GitHubAPI = *updateAPI
	}

	a := &App{opts: o, cfg: cfg, dataDir: *dataDir, logger: logger, apiKey: randomKey()}
	a.settings = loadSettings(a.settingsPath())
	if _, ok := cfg.endpoint(a.settings.Endpoint); !ok {
		a.settings.Endpoint = cfg.Endpoints[0].ID
	}
	a.sys = detectSys()
	a.backends = discoverBackends(engines, a.sys.GPU)
	logger.Printf("内存 %.1f GB,CPU %q,GPU %+v,可用引擎 %d 个", a.sys.RAMGB, a.sys.CPU, a.sys.GPU, len(a.backends))
	for _, b := range a.backends {
		logger.Printf("  引擎 %-6s %s", b.Name, b.Path)
	}
	a.web = o.Web
	if *webDir != "" {
		a.web = os.DirFS(*webDir)
		logger.Printf("网页从磁盘读取:%s", *webDir)
	}
	switch {
	case *idle >= 0:
		a.idleExit = time.Duration(*idle) * time.Minute
	default:
		a.idleExit = time.Duration(cfg.IdleExitMinutes) * time.Minute
	}

	// 上次被强杀留下的引擎
	if b, err := os.ReadFile(filepath.Join(*dataDir, "instance.json")); err == nil {
		var old instanceInfo
		if json.Unmarshal(b, &old) == nil && killStaleEngine(old.EnginePID) {
			logger.Printf("清理了上次残留的引擎进程 %d", old.EnginePID)
		}
	}

	if from := os.Getenv("HUMANIZER_UPDATED_FROM"); from != "" {
		// 刚从 from 更新过来:旧进程刚放开端口,等它一会儿,保证还用原来的端口(浏览器里的历史按端口存)
		logger.Printf("从 %s 更新而来", from)
		want := a.settings.Port
		if *port > 0 {
			want = *port
		}
		waitPortFree(want, 10*time.Second)
	}
	ln, err := listenPreferred(*port, a.settings.Port, cfg.PreferredPort)
	if err != nil {
		logger.Printf("无法监听端口:%v", err)
		return 1
	}
	a.port = ln.Addr().(*net.TCPAddr).Port
	a.url = fmt.Sprintf("http://127.0.0.1:%d/", a.port)
	if *port == 0 && a.settings.Port != a.port {
		a.settings.Port = a.port
		_ = saveSettings(a.settingsPath(), a.settings)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.rootCtx = ctx
	a.shutdown = cancel
	a.touch()
	a.writeInstance()

	a.updates = newUpdater(a)
	a.updates.rootCtx = ctx
	a.updates.args = args
	a.updates.requestRestart = func(plan *applyPlan) {
		a.mu.Lock()
		a.afterExit = func() { executePlan(plan, a.logf) }
		a.mu.Unlock()
		time.Sleep(400 * time.Millisecond) // 让网页先看到「正在重启」
		a.shutdown()
	}

	srv := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Printf("HTTP 服务异常:%v", err)
			cancel()
		}
	}()
	logger.Printf("网页地址 %s", a.url)
	fmt.Println("Humanizer:", a.url)

	a.boot()
	if !*noBrowser {
		if err := openURL(a.url); err != nil {
			logger.Printf("打开浏览器失败:%v", err)
		}
	}
	go a.idleLoop(ctx)
	go a.updates.autoLoop(ctx, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.updates.autoEnabled(a.settings)
	}, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.phase == phaseStarting || a.phase == phaseDownloading
	})
	go func() { // 上次更新留下的备份/旧安装包:等几分钟(负责重启的旧进程可能还在用)再清
		if sleepCtx(ctx, 5*time.Minute) {
			a.updates.cleanupStale()
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case s := <-sig:
		logger.Printf("收到信号 %v,退出", s)
	case <-ctx.Done():
	}
	cancel()
	a.stopAll()
	sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
	a.mu.Lock()
	after := a.afterExit
	a.mu.Unlock()
	if after != nil {
		logger.Printf("服务已关闭,执行更新")
		after()
	}
	logger.Printf("已退出")
	return 0
}

// waitPortFree:等某个端口能监听(旧进程刚关,最多等 timeout)。
func waitPortFree(port int, timeout time.Duration) {
	if port <= 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			l.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// randomKey:llama-server 默认允许任意网站跨域访问,只靠随机端口不够,
// 加一把每次启动都不同的钥匙,网页只能经过启动器的反代(由它带上钥匙)才能调到引擎。
func randomKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// listenPreferred:优先用固定端口。localStorage 按 "源"(含端口)隔离,
// 端口一变,浏览器里的历史记录就看不到了,所以尽量每次都用同一个。
func listenPreferred(forced, saved, preferred int) (net.Listener, error) {
	if forced > 0 {
		return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", forced))
	}
	var cands []int
	if saved > 0 {
		cands = append(cands, saved)
	}
	for i := 0; i < 10 && preferred > 0; i++ {
		cands = append(cands, preferred+i)
	}
	for _, p := range cands {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			return l, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func runningInstance(dataDir string) (instanceInfo, bool) {
	var inst instanceInfo
	b, err := os.ReadFile(filepath.Join(dataDir, "instance.json"))
	if err != nil || json.Unmarshal(b, &inst) != nil || inst.Port <= 0 {
		return inst, false
	}
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/app/ping", inst.Port), nil)
	req.Host = fmt.Sprintf("127.0.0.1:%d", inst.Port)
	resp, err := c.Do(req)
	if err != nil {
		return inst, false
	}
	defer resp.Body.Close()
	var p struct {
		App string `json:"app"`
	}
	if json.NewDecoder(resp.Body).Decode(&p) != nil || p.App != "humanizer" {
		return inst, false
	}
	return inst, true
}

func insideAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

func spawnDetached(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	cmd := exec.Command(exe, append([]string{"--serve"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "无法启动后台服务:", err)
		return 1
	}
	_ = cmd.Process.Release()
	return 0
}
