package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// installInfo 说明这份 App 是怎么装的,决定能不能原地更新。
type installInfo struct {
	Kind   string // mac_app / win_installed / win_portable / other
	Path   string // .app 目录 / 安装目录 / 便携版目录
	Exe    string // 当前可执行文件
	Mode   string // replace(原地替换后重启)/ installer(静默跑安装包后重启)/ manual(下好后打开安装包)/ none
	Reason string // manual / none 的原因:dmg / translocated / readonly / dev / unsupported
	Suffix string // 本平台安装包的文件名后缀
}

// appJob:一次 App 更新。下载 → ready → 用户点「重启并更新」→ preparing → restarting。
type appJob struct {
	State     string // downloading / paused / ready / preparing / restarting / error
	Version   string
	Asset     releaseAsset
	Path      string // 下好的安装包
	Err       *errInfo
	Fallback  bool // 原地替换没成(比如系统不让改 /Applications),改成打开安装包
	dl        *Download
	cancel    context.CancelFunc
	fetchDone chan struct{}
}

func (u *updater) updatesDir() string { return filepath.Join(u.dataDir, "updates") }

// restoreAppJob:重启 App 后,已经下好的新版安装包 / 下了一半的都还在,接着用。
func (u *updater) restoreAppJob() {
	rel := u.st.Latest
	if rel == nil || !newerVersion(rel.Version, u.version) {
		return
	}
	a := pickAsset(rel.Assets, u.inst.Suffix)
	if a == nil {
		return
	}
	p := filepath.Join(u.updatesDir(), a.Name)
	if st, err := os.Stat(p); err == nil && (a.Size <= 0 || st.Size() == a.Size) {
		u.app = &appJob{State: "ready", Version: rel.Version, Asset: *a, Path: p}
		return
	}
	if st, err := os.Stat(p + ".part"); err == nil {
		d := &Download{Dest: p}
		d.Received.Store(st.Size())
		d.Total.Store(a.Size)
		u.app = &appJob{State: "paused", Version: rel.Version, Asset: *a, dl: d}
	}
}

// packageVerifier:下完后粗查一下文件是不是真的安装包(防网关/门户页把网页塞进来)。
func packageVerifier(name string) func(string) error {
	bad := func() error {
		return &PermanentError{Code: "not_package", Msg: "下载到的不是安装包(可能被网络网关替换成了网页),已删除"}
	}
	lower := strings.ToLower(name)
	return func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		head := make([]byte, 4)
		n, _ := io.ReadFull(f, head)
		switch {
		case strings.HasSuffix(lower, ".exe"):
			if n < 2 || string(head[:2]) != "MZ" {
				return bad()
			}
		case strings.HasSuffix(lower, ".zip"):
			if n < 4 || string(head) != "PK\x03\x04" {
				return bad()
			}
		case strings.HasSuffix(lower, ".dmg"):
			// UDIF 镜像结尾 512 字节是 "koly" 开头的尾块
			st, err := f.Stat()
			if err != nil || st.Size() < 512 {
				return bad()
			}
			tail := make([]byte, 4)
			if _, err := f.ReadAt(tail, st.Size()-512); err != nil || string(tail) != "koly" {
				return bad()
			}
		}
		return nil
	}
}

func (u *updater) startAppDownload() error {
	u.mu.Lock()
	rel := u.st.Latest
	var prev chan struct{}
	if j := u.app; j != nil {
		if j.State == "downloading" || j.State == "preparing" || j.State == "restarting" ||
			(j.State == "ready" && rel != nil && j.Version == rel.Version) {
			u.mu.Unlock()
			return nil
		}
		prev = j.fetchDone
	}
	u.mu.Unlock()
	waitFetchDone(prev)
	if rel == nil || !newerVersion(rel.Version, u.version) {
		return &PermanentError{Code: "no_update", Msg: "已经是最新版本"}
	}
	if u.inst.Mode == "none" {
		return &PermanentError{Code: "unsupported", Msg: "这个平台没有可自动下载的安装包,请到 Releases 页面手动下载"}
	}
	asset := pickAsset(rel.Assets, u.inst.Suffix)
	if asset == nil {
		return &PermanentError{Code: "no_package", Msg: "这个版本没有本平台的安装包"}
	}
	sha := asset.SHA256
	if sha == "" && rel.SumsURL != "" {
		if b, err := u.fetchSums(u.rootCtx, rel.SumsURL); err == nil {
			sha = parseSums(b, asset.Name)
		}
	}
	if sha == "" {
		u.logf("警告:%s 没有 sha256,只校验大小", asset.Name)
	}
	_ = os.MkdirAll(u.updatesDir(), 0o755)
	dest := filepath.Join(u.updatesDir(), asset.Name)
	ctx, cancel := context.WithCancel(u.rootCtx)
	d := &Download{
		URL:        asset.URL,
		Dest:       dest,
		SHA256:     sha,
		ExpectSize: asset.Size,
		UserAgent:  u.ua,
		Client:     u.client,
		Logf:       u.logf,
		Verify:     packageVerifier(asset.Name),
	}
	a := *asset
	a.SHA256 = sha
	job := &appJob{State: "downloading", Version: rel.Version, Asset: a, dl: d, cancel: cancel, fetchDone: make(chan struct{})}
	u.mu.Lock()
	u.app = job
	u.mu.Unlock()
	u.logf("开始下载 App %s ← %s", rel.Version, asset.URL)
	go func() {
		err := d.Run(ctx)
		close(job.fetchDone)
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.app != job {
			return
		}
		switch {
		case err == nil:
			job.State, job.Path = "ready", dest
			u.logf("App %s 已下好并校验通过:%s", rel.Version, dest)
		case errors.Is(err, context.Canceled):
			if job.State == "downloading" {
				job.State = "paused"
			}
		default:
			code := "network"
			var pe *PermanentError
			if errors.As(err, &pe) {
				code = pe.Code
			}
			job.State = "error"
			job.Err = &errInfo{Kind: "download", Code: code, Message: err.Error()}
			u.logf("App 更新下载失败:%v", err)
		}
	}()
	return nil
}

func (u *updater) pauseApp() {
	u.mu.Lock()
	if j := u.app; j != nil && j.State == "downloading" && j.cancel != nil {
		j.cancel()
		j.State = "paused"
	}
	u.mu.Unlock()
}

// cancelApp 放弃这次 App 更新,删掉下载的安装包。
func (u *updater) cancelApp() {
	u.mu.Lock()
	j := u.app
	if j != nil && (j.State == "preparing" || j.State == "restarting") {
		u.mu.Unlock()
		return
	}
	u.app = nil
	u.mu.Unlock()
	if j == nil {
		return
	}
	if j.cancel != nil {
		j.cancel()
	}
	waitFetchDone(j.fetchDone)
	p := filepath.Join(u.updatesDir(), j.Asset.Name)
	for _, f := range []string{p, p + ".part", p + ".part.json"} {
		_ = os.Remove(f)
	}
}

// openPackage:手动模式下打开安装包(macOS 挂载 dmg 弹出 Finder 窗口;Windows 运行安装包/在资源管理器里选中 zip)。
func (u *updater) openPackage() error {
	u.mu.Lock()
	j := u.app
	u.mu.Unlock()
	if j == nil || j.Path == "" || !fileExists(j.Path) {
		return &PermanentError{Code: "no_package", Msg: "安装包还没下好"}
	}
	return openPackageFile(j.Path)
}

// apply:用户点了「重启并更新」。手动模式直接打开安装包;否则在后台准备(校验、解包、预检、
// 换文件),准备好了让进程退出,退出后执行重启计划。准备阶段出任何错,当前 App 原样不动。
func (u *updater) apply(inflight int) error {
	u.mu.Lock()
	j := u.app
	u.mu.Unlock()
	if j == nil || j.State != "ready" {
		return &PermanentError{Code: "not_ready", Msg: "新版本还没下好"}
	}
	if u.inst.Mode == "manual" || j.Fallback {
		return u.openPackage()
	}
	if inflight > 0 {
		return &PermanentError{Code: "busy", Msg: "正在改写,等这次改写结束再更新"}
	}
	u.mu.Lock()
	j.State, j.Err = "preparing", nil
	u.mu.Unlock()
	go func() {
		plan, err := u.prepare(j)
		u.mu.Lock()
		defer u.mu.Unlock()
		if err != nil {
			var fe *fallbackError
			if errors.As(err, &fe) {
				// 系统不让原地替换:改成手动,并直接把安装包打开给用户
				j.State, j.Fallback = "ready", true
				j.Err = &errInfo{Kind: "update", Code: "fallback", Message: fe.Error()}
				u.logf("没法原地替换(%v),改为打开安装包", fe.err)
				go func() { _ = openPackageFile(j.Path) }()
				return
			}
			j.State = "ready"
			j.Err = &errInfo{Kind: "update", Code: "prepare_failed", Message: err.Error()}
			u.logf("准备更新失败:%v", err)
			return
		}
		j.State = "restarting"
		u.logf("更新准备好了,重启到 %s", j.Version)
		if u.requestRestart != nil {
			go u.requestRestart(plan)
		}
	}()
	return nil
}

// fallbackError:原地替换做不到(权限、系统保护),但安装包本身没问题,可以让用户手动装。
type fallbackError struct{ err error }

func (e *fallbackError) Error() string {
	return "没法自动替换旧版本(" + e.err.Error() + "),已为你打开安装包,按提示手动替换即可"
}
func (e *fallbackError) Unwrap() error { return e.err }

// prepare 校验安装包 → 按平台准备好重启计划。
func (u *updater) prepare(j *appJob) (*applyPlan, error) {
	if j.Asset.SHA256 != "" { // 下完到现在可能过了很久,再核一遍
		got, err := hashFileProgress(u.rootCtx, j.Path, nil)
		if err != nil {
			return nil, err
		}
		if got != j.Asset.SHA256 {
			_ = os.Remove(j.Path)
			return nil, fmt.Errorf("安装包校验失败,已删除,请重新下载")
		}
	}
	plan := &applyPlan{From: u.version, To: j.Version, DataDir: u.dataDir}
	if v, err := strconv.Atoi(os.Getenv("HUMANIZER_UPDATE_READY_SEC")); err == nil && v > 0 { // 测试用:缩短「等新版本起来」的时间
		plan.ReadySec = v
	}
	if err := prepareApply(u.rootCtx, u.inst, j.Path, u.args, plan, u.logf); err != nil {
		return nil, err
	}
	return plan, nil
}

// ───────────────────────── 重启计划 ─────────────────────────

// applyPlan 是「退出旧版本 → 换上新版本 → 启动 → 确认起来了」的完整计划。
// macOS:服务进程自己在退出前换好 .app,退出后执行剩下的(启动、确认、失败回滚)。
// Windows:运行中的 exe 和它的目录换不了,所以把自己复制一份到数据目录当「更新助手」,
// 旧进程退出后由助手执行整份计划。
type applyPlan struct {
	Kind      string   `json:"kind"`             // swap(换目录)/ installer(静默安装)
	Target    string   `json:"target"`           // 当前安装位置
	Staged    string   `json:"staged,omitempty"` // swap:准备好的新版本
	Backup    string   `json:"backup"`           // 旧版本挪到这里,成功后删,失败时换回
	Installer []string `json:"installer,omitempty"`
	Relaunch  []string `json:"relaunch"` // 启动 Target 里的 App(新旧版本都用它)
	Env       []string `json:"env,omitempty"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	DataDir   string   `json:"data_dir"`
	WaitPID   int      `json:"wait_pid,omitempty"` // 先等这个进程(旧版本)退出
	Swapped   bool     `json:"swapped,omitempty"`  // 已经在旧进程里换好了
	ReadySec  int      `json:"ready_sec,omitempty"`
}

// swapInPlace:target → backup,staged → target。第二步失败就把第一步撤回。
func swapInPlace(target, staged, backup string) error {
	if _, err := os.Stat(staged); err != nil {
		return fmt.Errorf("新版本不在:%w", err)
	}
	_ = os.RemoveAll(backup)
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if rerr := os.Rename(backup, target); rerr != nil {
			return fmt.Errorf("%v;而且没能换回旧版本:%v", err, rerr)
		}
		return err
	}
	return nil
}

// undoSwap:新版本挪走(删掉),旧版本放回原处。
func undoSwap(target, backup string) error {
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("找不到旧版本备份:%w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		failed := target + ".failed"
		_ = os.RemoveAll(failed)
		if err := os.Rename(target, failed); err != nil {
			return fmt.Errorf("挪不开新版本:%w", err)
		}
		defer os.RemoveAll(failed)
	}
	return os.Rename(backup, target)
}

type applyHooks struct {
	logf    func(string, ...any)
	launch  func(argv, env []string) error
	ready   func(dataDir, version string, timeout time.Duration) bool
	waitPID func(pid int, timeout time.Duration)
	kill    func(dataDir string)
}

func defaultApplyHooks(logf func(string, ...any)) applyHooks {
	return applyHooks{logf: logf, launch: launchDetached, ready: waitInstanceReady, waitPID: waitPIDExit, kill: killInstance}
}

// runApplyPlan 执行重启计划,返回结局(同时写进 update-result.json)。
// 原则:任何一步失败都要回到「旧版本能跑」的状态,并把旧版本拉起来。
func runApplyPlan(p *applyPlan, h applyHooks) updateResult {
	res := updateResult{From: p.From, To: p.To}
	fail := func(format string, args ...any) updateResult {
		res.OK, res.Error = false, fmt.Sprintf(format, args...)
		h.logf("更新失败:%s", res.Error)
		writeResult(p.DataDir, res)
		return res
	}
	readyTimeout := time.Duration(p.ReadySec) * time.Second
	if readyTimeout <= 0 {
		readyTimeout = 90 * time.Second
	}
	relaunchOld := func() {
		if err := h.launch(p.Relaunch, p.Env); err != nil {
			h.logf("重新启动旧版本失败:%v", err)
			return
		}
		if !h.ready(p.DataDir, p.From, readyTimeout) {
			h.logf("旧版本也没有在 %s 内起来", readyTimeout)
		}
	}
	if p.WaitPID > 0 {
		h.waitPID(p.WaitPID, 60*time.Second)
	}

	if !p.Swapped {
		switch p.Kind {
		case "swap":
			if err := swapInPlace(p.Target, p.Staged, p.Backup); err != nil {
				_ = os.RemoveAll(p.Staged)
				relaunchOld()
				return fail("替换文件失败(旧版本没动):%v", err)
			}
		case "installer":
			_ = os.RemoveAll(p.Backup)
			if err := os.Rename(p.Target, p.Backup); err != nil {
				relaunchOld()
				return fail("挪不开旧版本(可能有文件被占用,旧版本没动):%v", err)
			}
			h.logf("运行安装包:%s", strings.Join(p.Installer, " "))
			if err := runInstaller(p.Installer, 15*time.Minute); err != nil {
				if uerr := undoSwap(p.Target, p.Backup); uerr != nil {
					h.logf("严重:回滚失败:%v", uerr)
				}
				relaunchOld()
				return fail("安装包没装成功,已换回旧版本:%v", err)
			}
		default:
			relaunchOld()
			return fail("未知的更新方式 %q", p.Kind)
		}
	}

	h.logf("启动新版本 %s", p.To)
	if err := h.launch(p.Relaunch, p.Env); err == nil && h.ready(p.DataDir, p.To, readyTimeout) {
		_ = os.RemoveAll(p.Backup)
		res.OK = true
		writeResult(p.DataDir, res)
		h.logf("已更新到 %s", p.To)
		return res
	} else if err != nil {
		h.logf("启动新版本失败:%v", err)
	}
	h.kill(p.DataDir)
	if err := undoSwap(p.Target, p.Backup); err != nil {
		h.logf("严重:回滚失败:%v", err)
		return fail("新版本没能启动,回滚也失败了:%v", err)
	}
	relaunchOld()
	return fail("新版本 %s 没能启动,已换回 %s", p.To, p.From)
}

func runInstaller(argv []string, timeout time.Duration) error {
	if len(argv) == 0 {
		return errors.New("没有安装包命令")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	prepareCmd(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		if msg != "" {
			return fmt.Errorf("%v: %s", err, msg)
		}
		return err
	}
	return nil
}

// launchDetached 启动 App(不等它结束)。
func launchDetached(argv, env []string) error {
	if len(argv) == 0 {
		return errors.New("没有启动命令")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = filepath.Dir(argv[0])
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // macOS 上前台进程马上退出,收掉它
	return nil
}

// waitInstanceReady:新进程起来后会写 instance.json(端口、版本);按它去 ping,版本对上才算成功。
// 端口以 instance.json 为准:万一原端口被占、新进程换了端口,也能认出来。
func waitInstanceReady(dataDir, version string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	for time.Now().Before(deadline) {
		var inst instanceInfo
		if b, err := os.ReadFile(filepath.Join(dataDir, "instance.json")); err == nil && json.Unmarshal(b, &inst) == nil && inst.Port > 0 {
			if pingVersion(c, inst.Port) == version {
				return true
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return false
}

func pingVersion(c *http.Client, port int) string {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/app/ping", port), nil)
	req.Host = fmt.Sprintf("127.0.0.1:%d", port)
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var p struct {
		App     string `json:"app"`
		Version string `json:"version"`
	}
	if json.NewDecoder(resp.Body).Decode(&p) != nil || p.App != "humanizer" {
		return ""
	}
	return p.Version
}

// killInstance:新版本起来了但不对劲(超时),按 instance.json 里的 pid 结束它,好把文件换回去。
func killInstance(dataDir string) {
	var inst instanceInfo
	b, err := os.ReadFile(filepath.Join(dataDir, "instance.json"))
	if err != nil || json.Unmarshal(b, &inst) != nil || inst.PID <= 0 || inst.PID == os.Getpid() {
		return
	}
	if p, err := os.FindProcess(inst.PID); err == nil {
		_ = p.Kill()
	}
	if inst.EnginePID > 0 {
		killStaleEngine(inst.EnginePID)
	}
	time.Sleep(500 * time.Millisecond)
	_ = os.Remove(filepath.Join(dataDir, "instance.json"))
}

// relaunchArgs:重启时沿用这次启动的参数(去掉 macOS 内部用的 --serve)。
func relaunchArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if a == "--serve" || a == "-serve" || strings.HasPrefix(a, "-psn_") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// cleanupStale:删掉过期的东西(比当前版本旧的安装包、更新助手、上次成功更新留下的备份)。
// 启动几分钟后才跑:刚重启时负责重启的旧进程可能还在用备份。
func (u *updater) cleanupStale() {
	ents, err := os.ReadDir(u.updatesDir())
	if err == nil {
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, "humanizer-updater") {
				_ = os.RemoveAll(filepath.Join(u.updatesDir(), name))
				continue
			}
			// Humanizer-0.3.1-macos-arm64.dmg → 0.3.1
			if v := versionInPackageName(name); v != "" && !newerVersion(v, u.version) {
				_ = os.RemoveAll(filepath.Join(u.updatesDir(), name))
			}
		}
	}
	if u.inst.Path != "" {
		for _, p := range []string{backupPathFor(u.inst), stagedPathFor(u.inst)} {
			if p != "" {
				if _, err := os.Stat(p); err == nil {
					u.logf("清理上次更新留下的 %s", p)
					_ = os.RemoveAll(p)
				}
			}
		}
	}
}

func versionInPackageName(name string) string {
	if !strings.HasPrefix(name, "Humanizer-") {
		return ""
	}
	rest := strings.TrimPrefix(name, "Humanizer-")
	if i := strings.Index(rest, "-"); i > 0 {
		if _, ok := parseSemver(rest[:i]); ok {
			return rest[:i]
		}
	}
	return ""
}

// 备份/暂存位置和当前安装放在同一个目录里(同一个磁盘分区,改名是原子的)。
func backupPathFor(in installInfo) string {
	if in.Path == "" {
		return ""
	}
	dir, base := filepath.Dir(in.Path), filepath.Base(in.Path)
	if in.Kind == "mac_app" {
		return filepath.Join(dir, "."+base+".backup")
	}
	return filepath.Join(dir, base+".backup")
}

func stagedPathFor(in installInfo) string {
	if in.Path == "" || in.Kind == "win_installed" {
		return ""
	}
	dir, base := filepath.Dir(in.Path), filepath.Base(in.Path)
	if in.Kind == "mac_app" {
		return filepath.Join(dir, "."+base+".update")
	}
	return filepath.Join(dir, base+".update")
}

// dirWritable:能不能在这个目录里建文件(=能不能在旁边放暂存/备份并改名)。
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".hz-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// checkVersionOutput 运行新版本的 --version,确认二进制能跑、版本号对。
func checkVersionOutput(ctx context.Context, exe, want string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--version")
	prepareCmd(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("新版本的程序跑不起来:%v", err)
	}
	got := strings.TrimSpace(string(out))
	if f := strings.Fields(got); len(f) == 2 && f[0] == "humanizer" && f[1] == want {
		return nil
	}
	return fmt.Errorf("新版本的程序报告的版本是 %q,应为 %s", got, want)
}
