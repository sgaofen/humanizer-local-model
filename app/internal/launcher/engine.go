package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// engineProc 是一个正在运行的 llama-server 进程。
type engineProc struct {
	backend Backend
	layers  string
	cmd     *exec.Cmd
	port    int
	scan    *logScanner
	done    chan struct{}
	waitErr error
}

type attemptLog struct {
	Backend string `json:"backend"`
	Layers  string `json:"layers"`
	Result  string `json:"result"` // running / ok / failed / no_gpu
	Detail  string `json:"detail,omitempty"`
}

// logScanner 把引擎输出写进日志文件,同时按行抓关键信息。
type logScanner struct {
	mu        sync.Mutex
	w         io.Writer
	partial   []byte
	tail      []string
	offloaded int
	layers    int
	gotLoad   bool
	device    string
	build     string
	bindFail  bool
}

var (
	reOffload = regexp.MustCompile(`offloaded (\d+)/(\d+) layers to GPU`)
	reDevice  = regexp.MustCompile(`using device (\S+) \(([^)]+)\)`)
	reBuild   = regexp.MustCompile(`build: (\d+) \(([0-9a-f]+)\)`)
	reBind    = regexp.MustCompile(`(?i)couldn't bind|address already in use|failed to bind`)
)

func (s *logScanner) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil {
		_, _ = s.w.Write(p)
	}
	s.partial = append(s.partial, p...)
	for {
		i := indexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		s.line(strings.TrimRight(string(s.partial[:i]), "\r"))
		s.partial = s.partial[i+1:]
	}
	if len(s.partial) > 64<<10 { // 防止没有换行的超长输出撑爆内存
		s.partial = s.partial[:0]
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func (s *logScanner) line(l string) {
	if l == "" {
		return
	}
	s.tail = append(s.tail, l)
	if len(s.tail) > 60 {
		s.tail = s.tail[len(s.tail)-60:]
	}
	if m := reOffload.FindStringSubmatch(l); m != nil {
		s.offloaded, _ = strconv.Atoi(m[1])
		s.layers, _ = strconv.Atoi(m[2])
		s.gotLoad = true
	}
	if m := reDevice.FindStringSubmatch(l); m != nil && s.device == "" {
		s.device = m[2]
	}
	if m := reBuild.FindStringSubmatch(l); m != nil && s.build == "" {
		s.build = "b" + m[1] + " (" + m[2] + ")"
	}
	if reBind.MatchString(l) {
		s.bindFail = true
	}
}

func (s *logScanner) tailText(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tail
	if len(t) > n {
		t = t[len(t)-n:]
	}
	return strings.Join(t, "\n")
}

func (s *logScanner) snapshot() (offloaded, layers int, gotLoad bool, device, build string, bindFail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offloaded, s.layers, s.gotLoad, s.device, s.build, s.bindFail
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (a *App) engineArgs(b Backend, layers, model string, port int) []string {
	c := a.cfg.Engine
	args := []string{
		"-m", model,
		"--host", "127.0.0.1", // 只听本机
		"--port", strconv.Itoa(port),
		"-c", strconv.Itoa(c.CtxSize),
		"-np", strconv.Itoa(c.Parallel),
		"-ngl", layers,
		"--no-webui",
		"--alias", "humanizer",
		"--log-colors", "off",
	}
	args = append(args, b.Extra...)
	return append(args, c.ExtraArgs...)
}

func (a *App) launchOnce(b Backend, layers, model string, logw io.Writer) (*engineProc, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	args := a.engineArgs(b, layers, model, port)
	cmd := exec.Command(b.Path, args...)
	cmd.Dir = filepath.Dir(b.Path)
	cmd.Env = append(os.Environ(), "LLAMA_API_KEY="+a.apiKey) // 走环境变量,不出现在 ps 里
	sc := &logScanner{w: logw}
	cmd.Stdout = sc
	cmd.Stderr = sc
	prepareCmd(cmd)
	fmt.Fprintf(logw, "\n===== %s 启动 %s %s =====\n", time.Now().Format(time.RFC3339), b.Name, strings.Join(args, " "))
	a.logf("启动引擎 [%s] -ngl %s 端口 %d", b.Name, layers, port)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	afterStart(cmd)
	p := &engineProc{backend: b, layers: layers, cmd: cmd, port: port, scan: sc, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *engineProc) stop() {
	select {
	case <-p.done:
		return
	default:
	}
	terminate(p.cmd)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		forceKill(p.cmd)
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
		}
	}
}

func (p *engineProc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

var errEngineExited = errors.New("engine exited")

// waitHealthy 轮询 /health:503 = 还在加载,200 = 就绪。进程先退出就直接失败。
func waitHealthy(ctx context.Context, p *engineProc, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/health", p.port)
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fmt.Errorf("%w: %v", errEngineExited, p.waitErr)
		case <-tick.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("加载超时(%s)", timeout)
			}
			resp, err := client.Get(url)
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
	}
}

// runEngine 依次尝试各个后端,直到有一个健康地跑起来。
//
//	Windows:CUDA(全部层) → CUDA(auto,显存不够时让 llama.cpp 自己挑层数)
//	         → Vulkan(全部层) → Vulkan(auto) → CPU
//	macOS:  Metal(全部层) → Metal(auto) → CPU(--device none)
//
// GPU 后端起来了但一层都没上 GPU(驱动太旧、装错卡),也算失败,换下一个。
func (a *App) runEngine(ctx context.Context, tier Tier) {
	model := a.modelPath(tier)
	backends := a.backends
	if len(backends) == 0 {
		a.setError("engine", "no_engine", "找不到随包附带的 llama-server,安装可能不完整", "")
		return
	}
	engineDataDir, _ := a.pathsNow()
	logPath := filepath.Join(engineDataDir, "logs", "llama-server.log")
	rotateLog(logPath, 4<<20)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logf = nil
	}
	var logw io.Writer = io.Discard
	if logf != nil {
		logw = logf
	}
	closeLog := func() {
		if logf != nil {
			logf.Close()
		}
	}

	type attempt struct {
		b      Backend
		layers string
	}
	var plan []attempt
	for _, b := range backends {
		if b.GPU {
			plan = append(plan, attempt{b, a.cfg.Engine.GPULayers})
			if a.cfg.Engine.GPULayers != "auto" {
				plan = append(plan, attempt{b, "auto"})
			}
		} else {
			plan = append(plan, attempt{b, "0"})
		}
	}

	timeout := time.Duration(a.cfg.Engine.LoadTimeoutSec) * time.Second
	skip := map[string]bool{}
	var lastTail string
	for _, at := range plan {
		if skip[at.b.Name] {
			continue
		}
		var p *engineProc
		var err error
		for tries := 0; tries < 3; tries++ { // 端口被抢:换个端口再来
			a.noteAttempt(attemptLog{Backend: at.b.Name, Layers: at.layers, Result: "running"})
			p, err = a.launchOnce(at.b, at.layers, model, logw)
			if err != nil {
				break
			}
			err = waitHealthy(ctx, p, timeout)
			if err == nil || ctx.Err() != nil {
				break
			}
			_, _, _, _, _, bindFail := p.scan.snapshot()
			p.stop()
			if !bindFail {
				break
			}
			a.logf("端口冲突,换端口重试")
		}
		if ctx.Err() != nil {
			if p != nil {
				p.stop()
			}
			closeLog()
			return
		}
		if err != nil {
			detail := err.Error()
			if p != nil {
				p.stop()
				lastTail = p.scan.tailText(12)
			}
			a.logf("引擎 [%s -ngl %s] 失败:%s", at.b.Name, at.layers, detail)
			a.finishAttempt("failed", detail)
			if p == nil { // 二进制都起不来(缺 DLL、没权限),同后端别再试
				skip[at.b.Name] = true
			}
			continue
		}
		off, total, gotLoad, device, build, _ := p.scan.snapshot()
		if at.b.GPU && gotLoad && off == 0 {
			p.stop()
			a.logf("引擎 [%s] 跑起来了,但 0/%d 层在 GPU 上,换下一个后端", at.b.Name, total)
			a.finishAttempt("no_gpu", fmt.Sprintf("0/%d 层在 GPU 上", total))
			skip[at.b.Name] = true
			continue
		}
		a.mu.Lock()
		if ctx.Err() != nil {
			a.mu.Unlock()
			p.stop()
			closeLog()
			return
		}
		a.engine = p
		a.engInfo.Backend = at.b.Name
		a.engInfo.Layers = at.layers
		if gotLoad {
			a.engInfo.Offload = fmt.Sprintf("%d/%d", off, total)
		}
		a.engInfo.Device = device
		a.engInfo.Build = build
		a.engInfo.ReadyAt = time.Now()
		a.phase = phaseReady
		a.errInfo = nil
		a.mu.Unlock()
		a.finishAttempt("ok", "")
		if build == "" {
			if b := a.fetchBuild(p.port); b != "" {
				a.mu.Lock()
				a.engInfo.Build = b
				a.mu.Unlock()
			}
		}
		a.writeInstance()
		a.logf("引擎就绪 [%s] 设备=%q 卸载=%d/%d", at.b.Name, device, off, total)
		go a.watchEngine(ctx, p, tier, closeLog)
		return
	}
	closeLog()
	a.setError("engine", "engine_failed", "推理引擎没能启动(所有后端都试过了)", lastTail)
}

// watchEngine:引擎意外退出时自动重启,短时间内连崩两次就报错。
func (a *App) watchEngine(ctx context.Context, p *engineProc, tier Tier, closeLog func()) {
	defer closeLog()
	select {
	case <-ctx.Done():
		return
	case <-p.done:
	}
	a.mu.Lock()
	if a.engine == p {
		a.engine = nil
	}
	stopping := a.stopping || ctx.Err() != nil
	a.restarts++
	n := a.restarts
	a.mu.Unlock()
	if stopping {
		return
	}
	tail := p.scan.tailText(12)
	a.logf("引擎意外退出(%v),第 %d 次重启", p.waitErr, n)
	if n > 2 {
		a.setError("engine", "engine_crash", "推理引擎反复崩溃", tail)
		return
	}
	a.startEngine(tier)
}

func (a *App) noteAttempt(l attemptLog) {
	a.mu.Lock()
	a.engInfo.Attempts = append(a.engInfo.Attempts, l)
	a.mu.Unlock()
}

func (a *App) finishAttempt(result, detail string) {
	a.mu.Lock()
	if n := len(a.engInfo.Attempts); n > 0 {
		a.engInfo.Attempts[n-1].Result = result
		a.engInfo.Attempts[n-1].Detail = detail
	}
	a.mu.Unlock()
}

// fetchBuild 从 /props 读 llama.cpp 版本号(新版本日志开头不一定打印 build 行)。
func (a *App) fetchBuild(port int) string {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/props", port), nil)
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var p struct {
		BuildInfo string `json:"build_info"`
	}
	if json.NewDecoder(resp.Body).Decode(&p) != nil {
		return ""
	}
	return p.BuildInfo
}

func rotateLog(path string, max int64) {
	if st, err := os.Stat(path); err == nil && st.Size() > max {
		_ = os.Rename(path, path+".1")
	}
}
