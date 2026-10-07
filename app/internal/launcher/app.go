package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	phaseSetup       = "setup"       // 还没有模型,等用户选档
	phaseDownloading = "downloading" // 下载中(含校验)
	phasePaused      = "paused"      // 下载暂停/中断,半截文件还在
	phaseStarting    = "starting"    // 正在拉起 llama-server、加载模型
	phaseReady       = "ready"
	phaseError       = "error"
	phaseStopped     = "stopped"
)

// Options 由 main 包传进来。
type Options struct {
	Web           fs.FS
	DefaultConfig []byte
	Version       string
	Args          []string
}

type errInfo struct {
	Kind    string `json:"kind"` // download / engine / config
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type engineInfo struct {
	Backend  string       `json:"backend,omitempty"`
	Layers   string       `json:"layers,omitempty"`
	Offload  string       `json:"offload,omitempty"`
	Device   string       `json:"device,omitempty"`
	Build    string       `json:"build,omitempty"`
	ReadyAt  time.Time    `json:"ready_at,omitempty"`
	Attempts []attemptLog `json:"attempts,omitempty"`
}

type App struct {
	opts     Options
	cfg      Config
	dataDir  string
	settings Settings
	sys      SysInfo
	backends []Backend
	web      fs.FS
	logger   *log.Logger
	port     int
	url      string
	apiKey   string // llama-server 的访问密钥,每次启动随机生成,只有启动器的反代知道

	rootCtx  context.Context
	shutdown context.CancelFunc

	mu           sync.Mutex
	phase        string
	tier         string // 当前选中的档位
	errInfo      *errInfo
	dl           *Download
	dlTier       string
	dlCancel     context.CancelFunc
	engine       *engineProc
	engineCancel context.CancelFunc
	engInfo      engineInfo
	restarts     int
	stopping     bool

	lastSeen atomic.Int64
	idleExit time.Duration

	updates   *updater     // 检查更新(update*.go)
	inflight  atomic.Int32 // 正在进行的改写请求
	afterExit func()       // 退出收尾后要做的事(App 更新:启动新版本并确认)
}

func (a *App) logf(format string, args ...any) {
	if a.logger != nil {
		a.logger.Printf(format, args...)
	}
}

func (a *App) settingsPath() string { return filepath.Join(a.dataDir, "settings.json") }

func (a *App) modelPath(t Tier) string {
	return filepath.Join(a.dataDir, "models", filepath.FromSlash(t.File))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (a *App) setError(kind, code, msg, detail string) {
	a.mu.Lock()
	a.phase = phaseError
	a.errInfo = &errInfo{Kind: kind, Code: code, Message: msg, Detail: detail}
	a.mu.Unlock()
	a.logf("错误 [%s/%s] %s", kind, code, msg)
}

// boot 决定开机状态:选过的档位已下载 → 直接起引擎;有半截文件 → 暂停态;
// 什么都没有 → 等用户在网页上选档。
func (a *App) boot() {
	rec := a.cfg.recommendTier(a.sys.RAMGB)
	id := a.settings.Tier
	if _, ok := a.cfg.tier(id); !ok {
		id = ""
	}
	if id == "" { // 没选过,但磁盘上已经有某个档位(比如重装后)
		order := append([]string{rec}, tierIDs(a.cfg.Tiers)...)
		for _, cand := range order {
			if t, ok := a.cfg.tier(cand); ok && fileExists(a.modelPath(t)) {
				id = cand
				break
			}
		}
	}
	if id == "" {
		a.mu.Lock()
		a.tier = rec
		a.phase = phaseSetup
		a.mu.Unlock()
		return
	}
	t, _ := a.cfg.tier(id)
	a.mu.Lock()
	a.tier = id
	a.mu.Unlock()
	if fileExists(a.modelPath(t)) {
		a.startEngine(t)
		return
	}
	part := a.modelPath(t) + ".part"
	if st, err := os.Stat(part); err == nil {
		d := &Download{Dest: a.modelPath(t)}
		d.Received.Store(st.Size())
		if b, err := os.ReadFile(part + ".json"); err == nil {
			var m remoteMeta
			if json.Unmarshal(b, &m) == nil {
				d.Total.Store(m.Size)
			}
		}
		a.mu.Lock()
		a.dl, a.dlTier, a.phase = d, id, phasePaused
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	a.phase = phaseSetup
	a.mu.Unlock()
}

func tierIDs(ts []Tier) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func (a *App) startEngine(t Tier) {
	a.mu.Lock()
	if a.engineCancel != nil {
		a.engineCancel()
	}
	ctx, cancel := context.WithCancel(a.rootCtx)
	a.engineCancel = cancel
	a.phase = phaseStarting
	a.tier = t.ID
	a.errInfo = nil
	a.engInfo = engineInfo{}
	a.mu.Unlock()
	go a.runEngine(ctx, t)
}

func (a *App) stopEngine() {
	a.mu.Lock()
	p := a.engine
	a.engine = nil
	cancel := a.engineCancel
	a.engineCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if p != nil {
		p.stop()
	}
}

func (a *App) startDownload(t Tier, ep Endpoint) {
	ctx, cancel := context.WithCancel(a.rootCtx)
	d := &Download{
		URL:       a.cfg.fileURL(ep.Base, t.File),
		Dest:      a.modelPath(t),
		SHA256:    t.SHA256,
		Logf:      a.logf,
		UserAgent: "humanizer-app/" + a.opts.Version,
	}
	_ = os.MkdirAll(filepath.Dir(d.Dest), 0o755)
	a.mu.Lock()
	if a.dlCancel != nil {
		a.dlCancel()
	}
	a.dl, a.dlTier, a.dlCancel = d, t.ID, cancel
	a.tier = t.ID
	a.phase = phaseDownloading
	a.errInfo = nil
	a.mu.Unlock()
	a.logf("开始下载 %s ← %s", t.File, d.URL)

	go func() {
		err := d.Run(ctx)
		a.mu.Lock()
		if a.dl != d { // 已经被新的下载顶替
			a.mu.Unlock()
			return
		}
		a.dlCancel = nil
		switch {
		case err == nil:
			a.dl = nil
			a.mu.Unlock()
			a.logf("下载完成 %s", d.Dest)
			if a.updates != nil { // 记下指纹,以后检查模型更新不用再算
				a.updates.recordFP(t.File, d.Dest, d.SHA)
			}
			a.startEngine(t)
			return
		case errors.Is(err, context.Canceled):
			if a.phase == phaseDownloading {
				a.phase = phasePaused
			}
			a.mu.Unlock()
			a.logf("下载已暂停,已收到 %d 字节", d.Received.Load())
			return
		default:
			code := "network"
			var pe *PermanentError
			if errors.As(err, &pe) {
				code = pe.Code
			}
			a.phase = phaseError
			a.errInfo = &errInfo{Kind: "download", Code: code, Message: err.Error()}
			a.mu.Unlock()
			a.logf("下载失败:%v", err)
		}
	}()
}

func (a *App) pauseDownload() {
	a.mu.Lock()
	if a.dlCancel != nil {
		a.dlCancel()
		a.dlCancel = nil
		a.phase = phasePaused
	}
	a.mu.Unlock()
}

// writeInstance 记下自己的端口和引擎 pid:
// 第二次双击时据此直接打开已有页面;上次被强杀时据此清理残留引擎。
func (a *App) writeInstance() {
	a.mu.Lock()
	inst := instanceInfo{PID: os.Getpid(), Port: a.port, Version: a.opts.Version}
	if a.engine != nil && a.engine.cmd.Process != nil {
		inst.EnginePID = a.engine.cmd.Process.Pid
	}
	a.mu.Unlock()
	b, _ := json.Marshal(inst)
	_ = writeFileAtomic(filepath.Join(a.dataDir, "instance.json"), b)
}

// stopAll 退出前收尾:停下载(半截文件留着下次续)、关引擎。
func (a *App) stopAll() {
	a.mu.Lock()
	a.stopping = true
	if a.dlCancel != nil {
		a.dlCancel()
	}
	a.phase = phaseStopped
	a.mu.Unlock()
	a.stopEngine()
	_ = os.Remove(filepath.Join(a.dataDir, "instance.json"))
}

func (a *App) idleLoop(ctx context.Context) {
	if a.idleExit <= 0 {
		return
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.mu.Lock()
			busy := a.phase == phaseDownloading
			a.mu.Unlock()
			if a.updates != nil && a.updates.busy() { // 更新在下载/安装:别中途退出
				busy = true
			}
			if busy {
				a.touch()
				continue
			}
			if time.Since(time.Unix(0, a.lastSeen.Load())) > a.idleExit {
				a.logf("网页已关闭超过 %s,自动退出", a.idleExit)
				a.shutdown()
				return
			}
		}
	}
}

func (a *App) touch() { a.lastSeen.Store(time.Now().UnixNano()) }
