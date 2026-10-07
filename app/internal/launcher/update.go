package launcher

// 检查更新。
//
//   - App:查 GitHub Releases(只认 tag_prefix 开头、非草稿、非预发布的),按语义版本挑最新;
//     有新版就按平台挑安装包,下载(续传 + 大小 + sha256),能原地替换就替换后重启,
//     不能就下好后打开安装包。见 update_app.go。
//   - 模型:对本机已下载的每个档位,HEAD 一下 HF 上同名文件拿 sha256 和大小,
//     和本地指纹比;有新版可一键下载到 <模型>.update,校验后替换。见 update_models.go。
//
// 隐私:只请求 GitHub / HF(或用户选的镜像)的公开接口;UA 只有 "humanizer-app/<版本>",
// 不带账号、邮箱、设备信息、Cookie。网络不通时安静失败,只在用户手动检查时提示。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	updStateFile  = "update-state.json"
	updResultFile = "update-result.json"
	updHashFile   = "model-hashes.json"
)

type releaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

type appRelease struct {
	Version     string         `json:"version"`
	Tag         string         `json:"tag"`
	Name        string         `json:"name"`
	Notes       string         `json:"notes"`
	URL         string         `json:"url"`
	PublishedAt string         `json:"published_at,omitempty"`
	Assets      []releaseAsset `json:"assets"`
	SumsURL     string         `json:"sums_url,omitempty"` // SHA256SUMS.txt(资产没带 digest 时用)
}

// remoteFile 是 HF 上一个模型文件的元数据。
type remoteFile struct {
	SHA256   string    `json:"sha256,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Endpoint string    `json:"endpoint,omitempty"`
	Checked  time.Time `json:"checked"`
	Err      string    `json:"err,omitempty"` // not_found / forbidden / offline
}

// updateState 持久化在数据目录的 update-state.json,重启后还能显示上次的结果、遵守「每天最多一次」。
type updateState struct {
	LastAttempt  time.Time             `json:"last_attempt,omitempty"`
	LastOK       time.Time             `json:"last_ok,omitempty"`
	ReleasesETag string                `json:"releases_etag,omitempty"`
	Latest       *appRelease           `json:"latest,omitempty"`
	Remote       map[string]remoteFile `json:"remote,omitempty"` // 文件名 → HF 元数据
	Dismissed    string                `json:"dismissed,omitempty"`
}

// fileFP 是本地模型文件的指纹:sha256 + 大小 + 修改时间(后两者变了就要重算)。
type fileFP struct {
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	ModNano int64  `json:"mod"`
}

// updateResult:上一次「重启并更新」的结局,由负责重启的进程写,新(或回滚后的)进程读给网页看。
type updateResult struct {
	OK    bool      `json:"ok"`
	From  string    `json:"from"`
	To    string    `json:"to"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

type progress struct {
	Stage      string  `json:"stage"` // probe / fetch / verify / hash
	Received   int64   `json:"received"`
	Total      int64   `json:"total"`
	Speed      float64 `json:"speed"`
	ETA        float64 `json:"eta"`
	VerifyDone int64   `json:"verify_done"`
	Attempt    int     `json:"attempt"`
}

func dlProgress(d *Download, live bool) *progress {
	if d == nil {
		return nil
	}
	p := &progress{Stage: d.Phase(), Received: d.Received.Load(), Total: d.Total.Load(),
		VerifyDone: d.VerifyDone.Load(), Attempt: int(d.Attempt.Load())}
	if live {
		p.Speed = d.Speed()
		if p.Speed > 0 && p.Total > 0 {
			p.ETA = float64(p.Total-p.Received) / p.Speed
		}
	}
	return p
}

// engineCtl 是模型替换时对引擎的操作;生产里是 App,单测里换成假的。
type engineCtl interface {
	usesTier(id string) bool // 引擎正在(或正要)用这个档位
	stop()
	start(t Tier)
	waitSettled(ctx context.Context) error // 等引擎就绪;起不来返回错误
	inflight() int                         // 正在进行的改写请求数
}

type updater struct {
	cfg     Config
	dataDir string
	version string
	ua      string
	client  *http.Client
	logf    func(string, ...any)
	eng     engineCtl
	models  func() string // 当前下载源 base
	inst    installInfo
	args    []string // 这次启动的命令行参数(重启时沿用)

	// 下面两个由 App 设置:准备好重启计划后让进程退出,退出后再执行计划
	requestRestart func(plan *applyPlan)
	tierPath       func(t Tier) string

	mu       sync.Mutex
	st       updateState
	fps      map[string]fileFP
	checking bool
	lastErr  string // 最近一次检查的问题:offline / rate_limited / http_xxx
	app      *appJob
	mjobs    map[string]*modelJob // 档位 id → 模型更新任务
	hashing  map[string]*progress // 文件名 → 正在算指纹
	rootCtx  context.Context
}

func newUpdater(a *App) *updater {
	u := &updater{
		cfg:     a.cfg,
		dataDir: a.dataDir,
		version: a.opts.Version,
		ua:      "humanizer-app/" + a.opts.Version,
		client:  newHTTPClient(),
		logf:    a.logf,
		eng:     appEngine{a},
		inst:    detectInstall(),
		mjobs:   map[string]*modelJob{},
		hashing: map[string]*progress{},
		rootCtx: context.Background(),
	}
	u.models = func() string {
		a.mu.Lock()
		id := a.settings.Endpoint
		a.mu.Unlock()
		if ep, ok := a.cfg.endpoint(id); ok {
			return ep.Base
		}
		return a.cfg.Endpoints[0].Base
	}
	u.tierPath = a.modelPath
	u.load()
	return u
}

func (u *updater) path(name string) string { return filepath.Join(u.dataDir, name) }

func (u *updater) load() {
	if b, err := os.ReadFile(u.path(updStateFile)); err == nil {
		_ = json.Unmarshal(b, &u.st)
	}
	if u.st.Remote == nil {
		u.st.Remote = map[string]remoteFile{}
	}
	u.fps = map[string]fileFP{}
	if b, err := os.ReadFile(u.path(updHashFile)); err == nil {
		_ = json.Unmarshal(b, &u.fps)
	}
	u.restoreJobs()
}

// saveLocked 要在持有 u.mu 时调用。
func (u *updater) saveLocked() {
	if b, err := json.MarshalIndent(u.st, "", "  "); err == nil {
		_ = writeFileAtomic(u.path(updStateFile), b)
	}
}

func (u *updater) saveFPsLocked() {
	if b, err := json.MarshalIndent(u.fps, "", "  "); err == nil {
		_ = writeFileAtomic(u.path(updHashFile), b)
	}
}

func (u *updater) autoEnabled(s Settings) bool {
	return s.AutoUpdateCheck == nil || *s.AutoUpdateCheck
}

// ───────────────────────── 检查 ─────────────────────────

var errOffline = errors.New("offline")

// classifyNetErr 把错误归成网页能翻译的几类。
func classifyNetErr(err error) string {
	if err == nil {
		return ""
	}
	var pe *PermanentError
	if errors.As(err, &pe) {
		return pe.Code
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, errOffline) || errors.Is(err, context.DeadlineExceeded) {
		return "offline"
	}
	var he *httpStatusErr
	if errors.As(err, &he) {
		if he.Code == 429 || (he.Code == 403 && he.RateLimited) {
			return "rate_limited"
		}
		return "http_" + strconv.Itoa(he.Code)
	}
	return "offline"
}

type httpStatusErr struct {
	Code        int
	RateLimited bool
}

func (e *httpStatusErr) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// check 检查 App 和模型。manual=false 时是后台静默检查。
// 只在没有别的检查在跑时才开始;结果写进 update-state.json。
func (u *updater) check(ctx context.Context, manual bool) {
	u.mu.Lock()
	if u.checking {
		u.mu.Unlock()
		return
	}
	u.checking = true
	u.st.LastAttempt = time.Now()
	u.mu.Unlock()
	if manual {
		u.logf("检查更新(手动)")
	} else {
		u.logf("检查更新(后台,每天最多一次)")
	}

	var wg sync.WaitGroup
	var appErr, modelErr error
	wg.Add(2)
	go func() { defer wg.Done(); appErr = u.checkApp(ctx) }()
	go func() { defer wg.Done(); modelErr = u.checkModels(ctx) }()
	wg.Wait()

	u.mu.Lock()
	defer u.mu.Unlock()
	u.checking = false
	err := appErr
	if err == nil {
		err = modelErr
	}
	u.lastErr = classifyNetErr(err)
	if appErr == nil {
		u.st.LastOK = time.Now()
	}
	if err != nil {
		u.logf("检查更新没完成:%v", err)
	}
	u.saveLocked()
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
		State  string `json:"state"`
	} `json:"assets"`
}

// checkApp 拉 GitHub Releases。带 If-None-Match:没变化时 GitHub 回 304,不占匿名接口的配额。
func (u *updater) checkApp(ctx context.Context) error {
	if u.cfg.Update.GitHubRepo == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	api := strings.TrimRight(u.cfg.Update.GitHubAPI, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+u.cfg.Update.GitHubRepo+"/releases?per_page=20", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", u.ua)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	u.mu.Lock()
	etag := u.st.ReleasesETag
	if u.st.Latest == nil {
		etag = ""
	}
	u.mu.Unlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return nil
	case resp.StatusCode != http.StatusOK:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return &httpStatusErr{Code: resp.StatusCode, RateLimited: resp.Header.Get("X-RateLimit-Remaining") == "0"}
	}
	var rels []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels); err != nil {
		return fmt.Errorf("GitHub 返回的不是预期的 JSON: %w", err)
	}
	best := pickLatestRelease(rels, u.cfg.Update.TagPrefix)
	u.mu.Lock()
	u.st.Latest = best
	u.st.ReleasesETag = resp.Header.Get("ETag")
	// 又出了更新的版本:之前下好(或下了一半)的旧安装包作废,删掉,免得装成中间版本
	var stale *appJob
	if j := u.app; j != nil && (best == nil || j.Version != best.Version) &&
		j.State != "downloading" && j.State != "preparing" && j.State != "restarting" {
		stale, u.app = j, nil
	}
	u.mu.Unlock()
	if stale != nil {
		p := filepath.Join(u.updatesDir(), stale.Asset.Name)
		for _, f := range []string{p, p + ".part", p + ".part.json"} {
			_ = os.Remove(f)
		}
	}
	if best != nil {
		u.logf("GitHub 上最新的 App 是 %s(当前 %s)", best.Version, u.version)
	}
	return nil
}

var shaDigestRe = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)

// pickLatestRelease:只认 prefix 开头、能解析成语义版本的正式发布,挑版本号最大的。
func pickLatestRelease(rels []ghRelease, prefix string) *appRelease {
	var best *appRelease
	var bestV semver
	for _, r := range rels {
		if r.Draft || r.Prerelease || !strings.HasPrefix(r.TagName, prefix) {
			continue
		}
		v, ok := parseSemver(strings.TrimPrefix(r.TagName, prefix))
		if !ok || len(v.Pre) > 0 {
			continue
		}
		if best != nil && compareSemver(v, bestV) <= 0 {
			continue
		}
		notes := r.Body
		if len(notes) > 64<<10 {
			notes = notes[:64<<10]
		}
		rel := &appRelease{Version: v.String(), Tag: r.TagName, Name: r.Name, Notes: notes, URL: r.HTMLURL, PublishedAt: r.PublishedAt}
		for _, a := range r.Assets {
			if a.State != "" && a.State != "uploaded" {
				continue
			}
			if strings.EqualFold(a.Name, "SHA256SUMS.txt") {
				rel.SumsURL = a.URL
				continue
			}
			ra := releaseAsset{Name: a.Name, URL: a.URL, Size: a.Size}
			if m := shaDigestRe.FindStringSubmatch(strings.ToLower(a.Digest)); m != nil {
				ra.SHA256 = m[1]
			}
			rel.Assets = append(rel.Assets, ra)
		}
		best, bestV = rel, v
	}
	return best
}

// pickAsset 按平台后缀挑安装包(Humanizer-0.3.2-macos-arm64.dmg 之类)。
func pickAsset(assets []releaseAsset, suffix string) *releaseAsset {
	if suffix == "" {
		return nil
	}
	for i, a := range assets {
		if strings.HasSuffix(strings.ToLower(a.Name), strings.ToLower(suffix)) && strings.HasPrefix(a.Name, "Humanizer-") {
			return &assets[i]
		}
	}
	return nil
}

// parseSums 从 SHA256SUMS.txt("<哈希>  <文件名>" 或 "<哈希> *<文件名>")里找某个文件的哈希。
func parseSums(b []byte, name string) string {
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) != 2 {
			continue
		}
		h, n := strings.ToLower(f[0]), strings.TrimPrefix(f[1], "*")
		if n == name && sha256Re.MatchString(h) {
			return h
		}
	}
	return ""
}

func (u *updater) fetchSums(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", u.ua)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &httpStatusErr{Code: resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

// autoLoop:启动一会儿后(避开装载模型最忙的时候)静默检查;之后每 30 分钟看一眼是否到了下一次。
// 成功检查过的 24 小时内不再查;失败的 3 小时内不重试。
func (u *updater) autoLoop(ctx context.Context, enabled func() bool, busy func() bool) {
	delay := 20 * time.Second
	if v, err := strconv.Atoi(os.Getenv("HUMANIZER_UPDATE_DELAY_SEC")); err == nil && v >= 0 {
		delay = time.Duration(v) * time.Second
	}
	interval := time.Duration(u.cfg.Update.IntervalHours) * time.Hour
	for {
		if !sleepCtx(ctx, delay) {
			return
		}
		delay = 30 * time.Minute
		if !enabled() {
			continue
		}
		if busy() { // 正在装载或下载模型:过一会儿再看
			delay = 15 * time.Second
			continue
		}
		u.mu.Lock()
		due := time.Since(u.st.LastOK) >= interval && time.Since(u.st.LastAttempt) >= 3*time.Hour
		u.mu.Unlock()
		if due {
			u.check(ctx, false)
		}
	}
}

// ───────────────────────── 状态 ─────────────────────────

type updAppView struct {
	Latest    *appReleaseView `json:"latest,omitempty"`
	Available bool            `json:"available"`
	Mode      string          `json:"mode"`             // replace / installer / manual / none
	Reason    string          `json:"reason,omitempty"` // manual/none 的原因
	Kind      string          `json:"kind"`             // mac_app / win_installed / win_portable / other
	State     string          `json:"state"`            // idle / downloading / paused / ready / preparing / restarting / error
	Progress  *progress       `json:"progress,omitempty"`
	Error     *errInfo        `json:"error,omitempty"`
	Fallback  bool            `json:"fallback,omitempty"` // 原地替换没成,改成手动
	Package   string          `json:"package,omitempty"`  // 下好的安装包文件名
}

type appReleaseView struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at,omitempty"`
	Asset       string `json:"asset,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type updModelView struct {
	Tier       string    `json:"tier"`
	Label      string    `json:"label"`
	File       string    `json:"file"`
	Active     bool      `json:"active"`
	State      string    `json:"state"` // current / available / unknown / hashing / downloading / paused / waiting / installing / done / error
	LocalSize  int64     `json:"local_size"`
	RemoteSize int64     `json:"remote_size,omitempty"`
	LocalSHA   string    `json:"local_sha256,omitempty"`
	RemoteSHA  string    `json:"remote_sha256,omitempty"`
	Partial    int64     `json:"partial,omitempty"`
	Progress   *progress `json:"progress,omitempty"`
	Error      *errInfo  `json:"error,omitempty"`
	Checked    time.Time `json:"checked,omitempty"`
}

type updStatus struct {
	Current   string         `json:"current"`
	Dev       bool           `json:"dev"`
	Auto      bool           `json:"auto"`
	Checking  bool           `json:"checking"`
	LastCheck time.Time      `json:"last_check,omitempty"`
	LastOK    time.Time      `json:"last_ok,omitempty"`
	Error     string         `json:"error,omitempty"`
	Dismissed string         `json:"dismissed,omitempty"`
	Busy      bool           `json:"busy"`
	App       updAppView     `json:"app"`
	Models    []updModelView `json:"models"`
	Result    *updateResult  `json:"result,omitempty"`
	Platform  string         `json:"platform"`
}

func (u *updater) status(auto bool, tiers []Tier) updStatus {
	_, devErr := parseSemver(u.version)
	s := updStatus{Current: u.version, Dev: !devErr, Auto: auto, Platform: runtime.GOOS}
	if b, err := os.ReadFile(u.path(updResultFile)); err == nil {
		var r updateResult
		if json.Unmarshal(b, &r) == nil {
			s.Result = &r
		}
	}
	active := map[string]bool{}
	for _, t := range tiers {
		active[t.ID] = u.eng.usesTier(t.ID)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	s.Checking = u.checking
	s.LastCheck = u.st.LastAttempt
	s.LastOK = u.st.LastOK
	s.Error = u.lastErr
	s.Dismissed = u.st.Dismissed
	s.Busy = u.busyLocked()

	// App
	av := updAppView{Mode: u.inst.Mode, Reason: u.inst.Reason, Kind: u.inst.Kind, State: "idle"}
	if rel := u.st.Latest; rel != nil {
		v := &appReleaseView{Version: rel.Version, Name: rel.Name, Notes: rel.Notes, URL: rel.URL, PublishedAt: rel.PublishedAt}
		if a := pickAsset(rel.Assets, u.inst.Suffix); a != nil {
			v.Asset, v.Size = a.Name, a.Size
		}
		av.Latest = v
		av.Available = newerVersion(rel.Version, u.version)
		if av.Available && v.Asset == "" && av.Mode != "none" {
			av.Mode, av.Reason = "none", "no_package"
		}
	}
	if j := u.app; j != nil {
		av.State = j.State
		av.Error = j.Err
		av.Fallback = j.Fallback
		if j.Fallback {
			av.Mode = "manual"
		}
		if j.State == "downloading" || j.State == "paused" {
			av.Progress = dlProgress(j.dl, j.State == "downloading")
		}
		if j.Path != "" {
			av.Package = filepath.Base(j.Path)
		}
	}
	s.App = av

	// 模型:只列本机已经下载的档位
	for _, t := range tiers {
		p := u.tierPath(t)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		mv := updModelView{Tier: t.ID, Label: t.Label, File: t.File, Active: active[t.ID], LocalSize: st.Size()}
		fp, fpOK := u.fps[t.File]
		if fpOK && fp.Size == st.Size() && fp.ModNano == st.ModTime().UnixNano() {
			mv.LocalSHA = fp.SHA256
		}
		rf, haveRemote := u.st.Remote[t.File]
		if haveRemote {
			mv.RemoteSHA, mv.RemoteSize, mv.Checked = rf.SHA256, rf.Size, rf.Checked
		}
		if pst, err := os.Stat(p + ".update.part"); err == nil {
			mv.Partial = pst.Size()
		}
		switch {
		case u.mjobs[t.ID] != nil:
			j := u.mjobs[t.ID]
			mv.State, mv.Error = j.State, j.Err
			if j.State == "downloading" || j.State == "paused" {
				mv.Progress = dlProgress(j.dl, j.State == "downloading")
			}
		case u.hashing[t.File] != nil:
			mv.State = "hashing"
			hp := *u.hashing[t.File]
			mv.Progress = &hp
		default:
			mv.State = modelState(mv, haveRemote && rf.Err == "")
			if mv.State == "available" && mv.Partial > 0 { // 上次没下完(或 App 重启过):显示成已暂停,点继续接着下
				mv.State = "paused"
				total := mv.RemoteSize
				if total <= 0 {
					total = partTotal(p + ".update.part")
				}
				mv.Progress = &progress{Received: mv.Partial, Total: total}
			}
		}
		s.Models = append(s.Models, mv)
	}
	return s
}

// modelState 比较本地和远端:有 sha 比 sha,远端没给 sha 才退回比大小。
func modelState(m updModelView, haveRemote bool) string {
	if !haveRemote {
		return "unknown"
	}
	if m.RemoteSize > 0 && m.RemoteSize != m.LocalSize {
		return "available"
	}
	if m.RemoteSHA == "" {
		if m.RemoteSize > 0 {
			return "current"
		}
		return "unknown"
	}
	if m.LocalSHA == "" {
		return "unknown" // 本地指纹还没算(下次检查会算)
	}
	if m.LocalSHA != m.RemoteSHA {
		return "available"
	}
	return "current"
}

func (u *updater) busyLocked() bool {
	if j := u.app; j != nil && (j.State == "downloading" || j.State == "preparing" || j.State == "restarting") {
		return true
	}
	for _, j := range u.mjobs {
		if j.State == "downloading" || j.State == "waiting" || j.State == "installing" {
			return true
		}
	}
	return false
}

// busy:有更新在下载/安装时不要因为「网页关了」自动退出。
func (u *updater) busy() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.busyLocked()
}

func (u *updater) dismiss(version string) {
	u.mu.Lock()
	u.st.Dismissed = version
	u.saveLocked()
	u.mu.Unlock()
}

func (u *updater) ackResult() { _ = os.Remove(u.path(updResultFile)) }

func writeResult(dataDir string, r updateResult) {
	r.At = time.Now()
	if b, err := json.MarshalIndent(r, "", "  "); err == nil {
		_ = writeFileAtomic(filepath.Join(dataDir, updResultFile), b)
	}
}
