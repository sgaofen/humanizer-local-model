package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ───────────────────────── 测试工具 ─────────────────────────

type fakeEngine struct {
	mu       sync.Mutex
	uses     bool
	calls    []string
	failNext int // 接下来几次 waitSettled 报错(模拟新模型起不来)
	busy     atomic.Int32
}

func (f *fakeEngine) usesTier(string) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.uses }
func (f *fakeEngine) stop()                { f.mu.Lock(); f.calls = append(f.calls, "stop"); f.mu.Unlock() }
func (f *fakeEngine) start(Tier)           { f.mu.Lock(); f.calls = append(f.calls, "start"); f.mu.Unlock() }
func (f *fakeEngine) inflight() int        { return int(f.busy.Load()) }
func (f *fakeEngine) waitSettled(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		return errors.New("模拟:引擎加载失败")
	}
	return nil
}
func (f *fakeEngine) callLog() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, ",")
}

func newTestUpdater(t *testing.T, ghURL, hfURL string) (*updater, *fakeEngine) {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(t)
	cfg.Update.GitHubAPI = ghURL
	cfg.Update.GitHubRepo = "owner/repo"
	eng := &fakeEngine{}
	u := &updater{
		cfg: cfg, dataDir: dir, version: "0.3.1", ua: "humanizer-app/0.3.1", client: newHTTPClient(),
		logf: t.Logf, eng: eng, mjobs: map[string]*modelJob{}, hashing: map[string]*progress{},
		inst:    installInfo{Kind: "mac_app", Mode: "replace", Suffix: "-macos-arm64.dmg"},
		rootCtx: context.Background(),
	}
	u.models = func() string { return hfURL }
	u.tierPath = func(t Tier) string { return filepath.Join(dir, "models", t.File) }
	u.load()
	return u, eng
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等不到:%s", what)
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// fakeDMG:随机内容,倒数第 512 字节处是 UDIF 尾块魔数 "koly"。
func fakeDMG(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	copy(b[n-512:], "koly")
	return b
}

// fakeGitHub 模拟 GitHub:/repos/owner/repo/releases 给发布列表(带 ETag,支持 304),
// /owner/repo/releases/download/<tag>/<name> 302 到 /blob/<name>(和 GitHub 一样跳到另一个地址),blob 支持 Range。
// dropFirst>0 时第一次 GET blob 传到这么多字节就断开。
type fakeGitHub struct {
	srv       *httptest.Server
	mu        sync.Mutex
	releases  []map[string]any
	blobs     map[string][]byte
	headers   []http.Header // 每个请求的头
	ranges    []string
	status    int // 非 0 时 releases 接口直接回这个状态码
	dropFirst int
	drops     atomic.Int32
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{blobs: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.headers = append(g.headers, r.Header.Clone())
		status, rels := g.status, g.releases
		g.mu.Unlock()
		if status != 0 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, `{"message":"API rate limit exceeded"}`, status)
			return
		}
		body, _ := json.Marshal(rels)
		etag := `"` + sha(body)[:16] + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	mux.HandleFunc("/owner/repo/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.headers = append(g.headers, r.Header.Clone())
		g.mu.Unlock()
		http.Redirect(w, r, "/blob/"+filepath.Base(r.URL.Path)+"?sig=x", http.StatusFound)
	})
	mux.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.headers = append(g.headers, r.Header.Clone())
		data, ok := g.blobs[filepath.Base(r.URL.Path)]
		if r.Method == http.MethodGet {
			g.ranges = append(g.ranges, r.Header.Get("Range"))
		}
		drop := g.dropFirst
		g.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if drop > 0 && r.Method == http.MethodGet && g.drops.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(200)
			w.Write(data[:drop])
			if hj, ok := w.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				c.Close()
			}
			return
		}
		http.ServeContent(w, r, "x", time.Time{}, bytes.NewReader(data))
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

// release 生成一条 GitHub 发布记录;assets 的 digest 按内容算(withDigest=false 时不给)。
func (g *fakeGitHub) release(tag string, draft, pre bool, withDigest bool, files map[string][]byte) map[string]any {
	var assets []map[string]any
	for name, data := range files {
		g.blobs[name] = data
		a := map[string]any{"name": name, "size": len(data), "state": "uploaded",
			"browser_download_url": g.srv.URL + "/owner/repo/releases/download/" + tag + "/" + name}
		if withDigest {
			a["digest"] = "sha256:" + sha(data)
		}
		assets = append(assets, a)
	}
	return map[string]any{"tag_name": tag, "name": "humanizer " + tag, "body": "## What changed in " + tag + "\n\n- **Fixed:** a thing",
		"html_url": "https://github.com/owner/repo/releases/tag/" + tag, "draft": draft, "prerelease": pre,
		"published_at": "2026-10-07T00:00:00Z", "assets": assets}
}

// ───────────────────────── App:检查 ─────────────────────────

func TestCheckAppPicksLatestAndOnlySendsUA(t *testing.T) {
	g := newFakeGitHub(t)
	dmg := fakeDMG(64<<10, 2)
	g.releases = []map[string]any{
		g.release("app-v0.9.9", true, false, true, nil),         // 草稿:不认
		g.release("app-v0.5.0", false, true, true, nil),         // 预发布:不认
		g.release("app-v0.4.0-beta.1", false, false, true, nil), // 预发布版本号:不认
		g.release("model-v9.0.0", false, false, true, nil),      // 别的标签:不认
		g.release("app-v0.3.2", false, false, true, map[string][]byte{"Humanizer-0.3.2-macos-arm64.dmg": dmg, "SHA256SUMS.txt": []byte("x")}),
		g.release("app-v0.3.1", false, false, true, nil),
	}
	u, _ := newTestUpdater(t, g.srv.URL, "http://127.0.0.1:1")
	u.check(context.Background(), true)

	st := u.status(true, u.cfg.Tiers)
	if st.App.Latest == nil || st.App.Latest.Version != "0.3.2" || !st.App.Available {
		t.Fatalf("应挑出 0.3.2 并标记有新版:%+v", st.App)
	}
	if st.App.Latest.Asset != "Humanizer-0.3.2-macos-arm64.dmg" || st.App.Latest.Size != int64(len(dmg)) {
		t.Fatalf("安装包挑错了:%+v", st.App.Latest)
	}
	if u.st.Latest.Assets[0].SHA256 != sha(dmg) || u.st.Latest.SumsURL == "" {
		t.Fatalf("digest / SHA256SUMS 没解析出来:%+v", u.st.Latest)
	}
	if st.Error != "" || st.LastOK.IsZero() {
		t.Fatalf("检查应成功:%+v", st)
	}

	// 隐私:只有 UA,不带任何身份信息
	h := g.headers[0]
	if h.Get("User-Agent") != "humanizer-app/0.3.1" {
		t.Fatalf("UA 应是 humanizer-app/0.3.1,实际 %q", h.Get("User-Agent"))
	}
	for k, vs := range h {
		for _, v := range vs {
			if strings.Contains(v, "@") || strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Cookie") {
				t.Fatalf("请求头里不该有身份信息:%s: %s", k, v)
			}
		}
	}

	// 第二次带 If-None-Match,服务器回 304,结果保持
	u.check(context.Background(), true)
	if len(g.headers) != 2 || g.headers[1].Get("If-None-Match") == "" {
		t.Fatalf("第二次检查应带 If-None-Match:%v", g.headers)
	}
	if u.status(true, u.cfg.Tiers).App.Latest.Version != "0.3.2" {
		t.Fatal("304 后应保留上次的结果")
	}
	// 状态落盘,重启后还在
	u2, _ := newTestUpdater(t, g.srv.URL, "")
	u2.dataDir = u.dataDir
	u2.load()
	if u2.st.Latest == nil || u2.st.Latest.Version != "0.3.2" {
		t.Fatal("update-state.json 没存上")
	}
}

func TestCheckAppQuietFailures(t *testing.T) {
	g := newFakeGitHub(t)
	g.status = 403
	u, _ := newTestUpdater(t, g.srv.URL, "http://127.0.0.1:1")
	u.check(context.Background(), false)
	if st := u.status(true, u.cfg.Tiers); st.Error != "rate_limited" || st.App.Latest != nil || st.Checking {
		t.Fatalf("GitHub 限流应安静地记成 rate_limited:%+v", st)
	}

	// 断网:连不上
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + l.Addr().String()
	l.Close()
	u2, _ := newTestUpdater(t, dead, dead)
	u2.check(context.Background(), false)
	if st := u2.status(true, u2.cfg.Tiers); st.Error != "offline" {
		t.Fatalf("断网应记成 offline,实际 %q", st.Error)
	}
	if !u2.st.LastOK.IsZero() || u2.st.LastAttempt.IsZero() {
		t.Fatal("失败的检查只记 LastAttempt,不记 LastOK(这样过一阵还会重试)")
	}
}

func TestPickAssetAndSums(t *testing.T) {
	as := []releaseAsset{{Name: "Humanizer-0.3.2-windows-x64-portable.zip"}, {Name: "Humanizer-0.3.2-windows-x64-setup.exe"}, {Name: "Humanizer-0.3.2-macos-arm64.dmg"}}
	if a := pickAsset(as, "-windows-x64-setup.exe"); a == nil || a.Name != as[1].Name {
		t.Fatal("Windows 安装版应挑 setup.exe")
	}
	if a := pickAsset(as, "-windows-x64-portable.zip"); a == nil || a.Name != as[0].Name {
		t.Fatal("便携版应挑 zip")
	}
	if pickAsset(as, "-linux-x64.tar.gz") != nil || pickAsset(as, "") != nil {
		t.Fatal("没有的平台应返回 nil")
	}
	h := strings.Repeat("ab", 32)
	sums := "1111111111111111111111111111111111111111111111111111111111111111  Humanizer-0.3.2-macos-arm64.dmg.bak\n" +
		h + " *Humanizer-0.3.2-macos-arm64.dmg\n"
	if got := parseSums([]byte(sums), "Humanizer-0.3.2-macos-arm64.dmg"); got != h {
		t.Fatalf("SHA256SUMS 解析错:%q", got)
	}
}

func TestPackageVerifier(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, b, 0o644)
		return p
	}
	html := []byte("<html><body>captive portal</body></html>" + strings.Repeat(" ", 600))
	cases := []struct {
		name string
		data []byte
		ok   bool
	}{
		{"a.dmg", fakeDMG(4096, 1), true},
		{"b.dmg", html, false},
		{"c.exe", append([]byte("MZ"), make([]byte, 100)...), true},
		{"d.exe", html, false},
		{"e.zip", append([]byte("PK\x03\x04"), make([]byte, 100)...), true},
		{"f.zip", html, false},
	}
	for _, c := range cases {
		err := packageVerifier(c.name)(write(c.name, c.data))
		if (err == nil) != c.ok {
			t.Errorf("%s:期望通过=%v,实际 %v", c.name, c.ok, err)
		}
	}
}

// ───────────────────────── App:下载 ─────────────────────────

func appDownloadState(u *updater) updAppView { return u.status(true, nil).App }

func TestAppDownloadResumesAndVerifies(t *testing.T) {
	g := newFakeGitHub(t)
	dmg := fakeDMG(3<<20, 3)
	g.dropFirst = 1 << 20 // 第一次下到 1 MiB 断线
	g.releases = []map[string]any{g.release("app-v0.3.2", false, false, true, map[string][]byte{"Humanizer-0.3.2-macos-arm64.dmg": dmg})}
	u, _ := newTestUpdater(t, g.srv.URL, "")
	u.check(context.Background(), true)
	if err := u.startAppDownload(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "App 安装包下完", 30*time.Second, func() bool {
		s := appDownloadState(u).State
		return s == "ready" || s == "error"
	})
	av := appDownloadState(u)
	if av.State != "ready" {
		t.Fatalf("应下载成功:%+v %+v", av, av.Error)
	}
	got, _ := os.ReadFile(filepath.Join(u.dataDir, "updates", "Humanizer-0.3.2-macos-arm64.dmg"))
	if !bytes.Equal(got, dmg) {
		t.Fatal("内容不一致")
	}
	if len(g.ranges) < 2 || !strings.HasPrefix(g.ranges[len(g.ranges)-1], "bytes=") {
		t.Fatalf("断线后应用 Range 续传:%q", g.ranges)
	}
	for _, h := range g.headers {
		if h.Get("User-Agent") != "humanizer-app/0.3.1" {
			t.Fatalf("下载请求的 UA 不对:%q", h.Get("User-Agent"))
		}
	}
	// App 重启后:已下好的安装包直接是 ready
	u2, _ := newTestUpdater(t, g.srv.URL, "")
	u2.dataDir = u.dataDir
	u2.app = nil
	u2.load()
	if u2.app == nil || u2.app.State != "ready" {
		t.Fatalf("重启后应识别出已下好的安装包:%+v", u2.app)
	}
}

func TestNewerReleaseDiscardsStalePackage(t *testing.T) {
	g := newFakeGitHub(t)
	g.releases = []map[string]any{g.release("app-v0.3.2", false, false, true, map[string][]byte{"Humanizer-0.3.2-macos-arm64.dmg": fakeDMG(64<<10, 6)})}
	u, _ := newTestUpdater(t, g.srv.URL, "")
	u.check(context.Background(), true)
	if err := u.startAppDownload(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "下完 0.3.2", 10*time.Second, func() bool { return appDownloadState(u).State == "ready" })
	if err := u.startAppDownload(); err != nil || appDownloadState(u).State != "ready" {
		t.Fatal("已经下好的版本再点下载应什么都不做")
	}
	old := filepath.Join(u.dataDir, "updates", "Humanizer-0.3.2-macos-arm64.dmg")
	g.releases = append([]map[string]any{g.release("app-v0.3.3", false, false, true, map[string][]byte{"Humanizer-0.3.3-macos-arm64.dmg": fakeDMG(64<<10, 7)})}, g.releases...)
	u.check(context.Background(), true)
	st := u.status(true, nil)
	if st.App.Latest.Version != "0.3.3" || st.App.State != "idle" {
		t.Fatalf("出了 0.3.3 后应回到「可下载 0.3.3」:%+v", st.App)
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("作废的 0.3.2 安装包应删掉")
	}
}

func TestAppDownloadRejectsBadFiles(t *testing.T) {
	dmg := fakeDMG(256<<10, 4)
	cases := []struct {
		name     string
		mutate   func(g *fakeGitHub, rel map[string]any)
		wantCode string
	}{
		{"sha256 不符", func(g *fakeGitHub, rel map[string]any) {
			rel["assets"].([]map[string]any)[0]["digest"] = "sha256:" + strings.Repeat("0", 64)
		}, "checksum"},
		{"大小不符", func(g *fakeGitHub, rel map[string]any) {
			rel["assets"].([]map[string]any)[0]["size"] = len(dmg) + 1
		}, "size_mismatch"},
		{"不是 dmg(被换成网页)", func(g *fakeGitHub, rel map[string]any) {
			page := bytes.Repeat([]byte("<html>"), len(dmg)/6)
			g.blobs["Humanizer-0.3.2-macos-arm64.dmg"] = page
			a := rel["assets"].([]map[string]any)[0]
			a["size"], a["digest"] = len(page), "sha256:"+sha(page)
		}, "not_package"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newFakeGitHub(t)
			rel := g.release("app-v0.3.2", false, false, true, map[string][]byte{"Humanizer-0.3.2-macos-arm64.dmg": dmg})
			c.mutate(g, rel)
			g.releases = []map[string]any{rel}
			u, _ := newTestUpdater(t, g.srv.URL, "")
			u.check(context.Background(), true)
			if err := u.startAppDownload(); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "下载结束", 20*time.Second, func() bool {
				s := appDownloadState(u).State
				return s == "ready" || s == "error"
			})
			av := appDownloadState(u)
			if av.State != "error" || av.Error == nil || av.Error.Code != c.wantCode {
				t.Fatalf("应报 %s,实际 %+v %+v", c.wantCode, av, av.Error)
			}
			if _, err := os.Stat(filepath.Join(u.dataDir, "updates", "Humanizer-0.3.2-macos-arm64.dmg")); err == nil {
				t.Fatal("坏文件不该留下")
			}
		})
	}
}

func TestAppDownloadUsesSumsWhenNoDigest(t *testing.T) {
	g := newFakeGitHub(t)
	dmg := fakeDMG(128<<10, 5)
	wrong := []byte(strings.Repeat("0", 64) + "  Humanizer-0.3.2-macos-arm64.dmg\n")
	g.releases = []map[string]any{g.release("app-v0.3.2", false, false, false,
		map[string][]byte{"Humanizer-0.3.2-macos-arm64.dmg": dmg, "SHA256SUMS.txt": wrong})}
	u, _ := newTestUpdater(t, g.srv.URL, "")
	u.check(context.Background(), true)
	if err := u.startAppDownload(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "下载结束", 20*time.Second, func() bool { return appDownloadState(u).State == "error" || appDownloadState(u).State == "ready" })
	if av := appDownloadState(u); av.Error == nil || av.Error.Code != "checksum" {
		t.Fatalf("没有 digest 时应改用 SHA256SUMS.txt 校验(这里故意给错):%+v", av)
	}
}

func TestAppDownloadNoPackageForPlatform(t *testing.T) {
	g := newFakeGitHub(t)
	g.releases = []map[string]any{g.release("app-v0.3.2", false, false, true, map[string][]byte{"Humanizer-0.3.2-windows-x64-setup.exe": []byte("MZ...")})}
	u, _ := newTestUpdater(t, g.srv.URL, "")
	u.check(context.Background(), true)
	st := u.status(true, nil)
	if !st.App.Available || st.App.Mode != "none" || st.App.Reason != "no_package" {
		t.Fatalf("有新版但没有本平台安装包:应显示 none/no_package,实际 %+v", st.App)
	}
	var pe *PermanentError
	if err := u.startAppDownload(); !errors.As(err, &pe) || pe.Code != "no_package" {
		t.Fatalf("应拒绝下载:%v", err)
	}
}

// ───────────────────────── 模型 ─────────────────────────

// fakeHF:HEAD/GET /<repo>/resolve/main/<file> → 302 + X-Linked-Etag/X-Linked-Size,/cdn/<file> 支持 Range。
func fakeHF(t *testing.T, files map[string][]byte, withSHA bool) (*httptest.Server, *[]http.Header) {
	var mu sync.Mutex
	var hdrs []http.Header
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hdrs = append(hdrs, r.Header.Clone())
		mu.Unlock()
		name := filepath.Base(r.URL.Path)
		data, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/resolve/") {
			w.Header().Set("X-Linked-Size", strconv.Itoa(len(data)))
			if withSHA {
				w.Header().Set("X-Linked-Etag", `"`+sha(data)+`"`)
			}
			http.Redirect(w, r, "/cdn/"+name, http.StatusFound)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hdrs
}

func modelView(u *updater, id string) updModelView {
	for _, m := range u.status(true, u.cfg.Tiers).Models {
		if m.Tier == id {
			return m
		}
	}
	return updModelView{State: "missing"}
}

func TestModelCheckHashesLegacyFileOnce(t *testing.T) {
	q8 := "humanizer-12b-Q8_0.gguf"
	oldData, newData := fakeGGUF(1<<20), fakeGGUF(1<<20)
	newData[100] ^= 0xff // 同样大小、内容不同:只能靠 sha256 分辨
	srv, _ := fakeHF(t, map[string][]byte{q8: newData}, true)
	u, _ := newTestUpdater(t, "http://127.0.0.1:1", srv.URL)
	local := filepath.Join(u.dataDir, "models", q8)
	os.MkdirAll(filepath.Dir(local), 0o755)
	os.WriteFile(local, oldData, 0o644)

	if m := modelView(u, "q8"); m.State != "unknown" {
		t.Fatalf("没检查过应是 unknown:%+v", m)
	}
	u.check(context.Background(), true)
	m := modelView(u, "q8")
	if m.State != "available" || m.LocalSHA != sha(oldData) || m.RemoteSHA != sha(newData) {
		t.Fatalf("老文件应先算指纹再比较,得出有新版:%+v", m)
	}
	if fp := u.fps[q8]; fp.SHA256 != sha(oldData) {
		t.Fatal("指纹应记进 model-hashes.json")
	}
	// 其他档位没下载,不出现在列表里
	if n := len(u.status(true, u.cfg.Tiers).Models); n != 1 {
		t.Fatalf("只应列出已下载的档位,实际 %d 个", n)
	}
}

func TestModelCheckCurrentAndSizeOnly(t *testing.T) {
	q8, q4 := "humanizer-12b-Q8_0.gguf", "humanizer-12b-Q4_K_M.gguf"
	same, bigger := fakeGGUF(256<<10), fakeGGUF(300<<10)
	srv, _ := fakeHF(t, map[string][]byte{q8: same, q4: bigger}, true)
	u, _ := newTestUpdater(t, "http://127.0.0.1:1", srv.URL)
	md := filepath.Join(u.dataDir, "models")
	os.MkdirAll(md, 0o755)
	os.WriteFile(filepath.Join(md, q8), same, 0o644)
	os.WriteFile(filepath.Join(md, q4), fakeGGUF(256<<10), 0o644)
	u.recordFP(q8, filepath.Join(md, q8), sha(same)) // 新版本下载时记下的指纹
	u.check(context.Background(), true)
	if m := modelView(u, "q8"); m.State != "current" {
		t.Fatalf("sha 一样应是最新:%+v", m)
	}
	if m := modelView(u, "q4"); m.State != "available" {
		t.Fatalf("大小不同直接判定有新版:%+v", m)
	}
	if _, ok := u.fps[q4]; ok {
		t.Fatal("大小已经不同,不该再花时间算本地指纹")
	}
}

func TestModelUpdateDownloadsAndRestartsEngine(t *testing.T) {
	q8 := "humanizer-12b-Q8_0.gguf"
	oldData, newData := fakeGGUF(2<<20), fakeGGUF(3<<20)
	newData[7] = 'N'
	srv, hdrs := fakeHF(t, map[string][]byte{q8: newData}, true)
	u, eng := newTestUpdater(t, "http://127.0.0.1:1", srv.URL)
	eng.uses = true // 引擎正在用 q8
	local := filepath.Join(u.dataDir, "models", q8)
	os.MkdirAll(filepath.Dir(local), 0o755)
	os.WriteFile(local, oldData, 0o644)
	u.check(context.Background(), true)
	if err := u.startModelDownload("q8"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "模型更新完成", 20*time.Second, func() bool {
		s := modelView(u, "q8").State
		return s == "done" || s == "error"
	})
	m := modelView(u, "q8")
	if m.State != "done" {
		t.Fatalf("应更新成功:%+v %+v", m, m.Error)
	}
	got, _ := os.ReadFile(local)
	if !bytes.Equal(got, newData) {
		t.Fatal("模型文件没换成新的")
	}
	if eng.callLog() != "stop,start" {
		t.Fatalf("引擎在用这个模型:应先停再起,实际 %q", eng.callLog())
	}
	for _, f := range []string{local + ".bak", local + ".update", local + ".update.part", local + ".update.part.json"} {
		if _, err := os.Stat(f); err == nil {
			t.Fatalf("不该留下 %s", f)
		}
	}
	if u.fps[q8].SHA256 != sha(newData) {
		t.Fatal("新指纹没记上")
	}
	u.ackModel("q8")
	if m := modelView(u, "q8"); m.State != "current" {
		t.Fatalf("确认后应显示已是最新:%+v", m)
	}
	for _, h := range *hdrs {
		if h.Get("User-Agent") != "humanizer-app/0.3.1" {
			t.Fatalf("HF 请求的 UA 不对:%q", h.Get("User-Agent"))
		}
	}
}

func TestModelUpdatePauseResume(t *testing.T) {
	q8 := "humanizer-12b-Q8_0.gguf"
	newData := fakeGGUF(8 << 20)
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/resolve/") {
			w.Header().Set("X-Linked-Size", strconv.Itoa(len(newData)))
			w.Header().Set("X-Linked-Etag", `"`+sha(newData)+`"`)
			http.Redirect(w, r, "/cdn/"+q8, http.StatusFound)
			return
		}
		var off int
		if rg := r.Header.Get("Range"); rg != "" {
			fmt.Sscanf(rg, "bytes=%d-", &off)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, len(newData)-1, len(newData)))
			w.Header().Set("Content-Length", strconv.Itoa(len(newData)-off))
			w.WriteHeader(206)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(newData)))
		}
		if r.Method == http.MethodHead {
			return
		}
		for i := off; i < len(newData); i += 64 << 10 {
			if _, err := w.Write(newData[i:min(i+64<<10, len(newData))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	})
	srv := httptest.NewServer(slow)
	defer srv.Close()
	u, eng := newTestUpdater(t, "http://127.0.0.1:1", srv.URL)
	local := filepath.Join(u.dataDir, "models", q8)
	os.MkdirAll(filepath.Dir(local), 0o755)
	os.WriteFile(local, fakeGGUF(1<<20), 0o644)
	u.check(context.Background(), true)
	if err := u.startModelDownload("q8"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	u.pauseModel("q8")
	waitFor(t, "暂停", 5*time.Second, func() bool { return modelView(u, "q8").State == "paused" })
	u.mu.Lock()
	done := u.mjobs["q8"].fetchDone
	u.mu.Unlock()
	waitFetchDone(done)
	if st, err := os.Stat(local + ".update.part"); err != nil || st.Size() == 0 {
		t.Fatal("暂停后半截文件应保留")
	}
	if old, _ := os.ReadFile(local); len(old) != 1<<20 {
		t.Fatal("下载期间旧模型应原封不动")
	}
	// 模拟 App 重启:没有任务对象了,只剩 .update.part → 显示已暂停,点继续接着下
	u.mu.Lock()
	delete(u.mjobs, "q8")
	u.mu.Unlock()
	if m := modelView(u, "q8"); m.State != "paused" || m.Progress == nil || m.Progress.Received == 0 {
		t.Fatalf("重启后应显示已暂停和已下字节:%+v", m)
	}
	if err := u.startModelDownload("q8"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "续传完成", 20*time.Second, func() bool { return modelView(u, "q8").State == "done" })
	got, _ := os.ReadFile(local)
	if !bytes.Equal(got, newData) {
		t.Fatal("续传后内容不对")
	}
	if eng.callLog() != "" {
		t.Fatalf("引擎没在用这个模型,不该动引擎:%q", eng.callLog())
	}
}

func TestReplaceModelRollsBackWhenEngineFails(t *testing.T) {
	dir := t.TempDir()
	cur, nw := filepath.Join(dir, "m.gguf"), filepath.Join(dir, "m.gguf.update")
	os.WriteFile(cur, []byte("GGUF old"), 0o644)
	os.WriteFile(nw, []byte("GGUF new"), 0o644)
	eng := &fakeEngine{uses: true, failNext: 1}
	err := replaceModelFile(context.Background(), eng, Tier{ID: "q8", File: "m.gguf"}, cur, nw, t.Logf)
	if err == nil {
		t.Fatal("新模型起不来应报错")
	}
	if b, _ := os.ReadFile(cur); string(b) != "GGUF old" {
		t.Fatalf("应换回旧模型,实际 %q", b)
	}
	if eng.callLog() != "stop,start,stop,start" {
		t.Fatalf("应:停 → 起新的(失败)→ 停 → 起旧的,实际 %q", eng.callLog())
	}
	for _, f := range []string{cur + ".bak", cur + ".rejected"} {
		if _, err := os.Stat(f); err == nil {
			t.Fatalf("不该留下 %s", f)
		}
	}
}

func TestReplaceModelMissingNewFileKeepsOld(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "m.gguf")
	os.WriteFile(cur, []byte("GGUF old"), 0o644)
	eng := &fakeEngine{uses: true}
	if err := replaceModelFile(context.Background(), eng, Tier{ID: "q8"}, cur, filepath.Join(dir, "nope"), t.Logf); err == nil {
		t.Fatal("新文件不存在应报错")
	}
	if b, _ := os.ReadFile(cur); string(b) != "GGUF old" {
		t.Fatal("旧模型应恢复原位")
	}
	if eng.callLog() != "stop,start" {
		t.Fatalf("应重新拉起旧模型:%q", eng.callLog())
	}
}

func TestInstallModelWaitsForRewrite(t *testing.T) {
	dir := t.TempDir()
	cur, nw := filepath.Join(dir, "m.gguf"), filepath.Join(dir, "m.gguf.update")
	os.WriteFile(cur, []byte("GGUF old"), 0o644)
	os.WriteFile(nw, []byte("GGUF new"), 0o644)
	u, eng := newTestUpdater(t, "", "")
	u.tierPath = func(Tier) string { return cur }
	eng.uses = true
	eng.busy.Store(1) // 正在改写
	job := &modelJob{Tier: "q8", State: "waiting"}
	done := make(chan struct{})
	go func() { u.installModel(context.Background(), job, Tier{ID: "q8", File: "m.gguf"}, nw, ""); close(done) }()
	time.Sleep(300 * time.Millisecond)
	if eng.callLog() != "" {
		t.Fatal("改写进行中不该停引擎")
	}
	eng.busy.Store(0)
	<-done
	if job.State != "done" || eng.callLog() != "stop,start" {
		t.Fatalf("改写结束后才替换:%s %q", job.State, eng.callLog())
	}
}

// ───────────────────────── 重启计划 / 回滚 ─────────────────────────

func mkTree(t *testing.T, dir, content string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "engine"), 0o755)
	os.WriteFile(filepath.Join(dir, "version.txt"), []byte(content), 0o644)
	os.WriteFile(filepath.Join(dir, "engine", "llama-server"), []byte(content+"-engine"), 0o644)
}

func readVer(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "version.txt"))
	return string(b)
}

type hookLog struct {
	mu       sync.Mutex
	launches []string
}

// testHooks:ready(version) 由 okVersions 决定;launch 记录当时 Target 里是哪个版本。
func testHooks(t *testing.T, target string, okVersions ...string) (applyHooks, *hookLog) {
	hl := &hookLog{}
	ok := map[string]bool{}
	for _, v := range okVersions {
		ok[v] = true
	}
	return applyHooks{
		logf: t.Logf,
		launch: func(argv, env []string) error {
			hl.mu.Lock()
			hl.launches = append(hl.launches, readVer(target))
			hl.mu.Unlock()
			return nil
		},
		ready:   func(dataDir, v string, _ time.Duration) bool { return ok[v] },
		waitPID: func(int, time.Duration) {},
		kill:    func(string) {},
	}, hl
}

func readResult(t *testing.T, dir string) updateResult {
	var r updateResult
	b, err := os.ReadFile(filepath.Join(dir, updResultFile))
	if err != nil {
		t.Fatal("没写 update-result.json")
	}
	json.Unmarshal(b, &r)
	return r
}

func TestApplySwapSuccess(t *testing.T) {
	root := t.TempDir()
	target, staged, backup := filepath.Join(root, "Humanizer.app"), filepath.Join(root, ".Humanizer.app.update"), filepath.Join(root, ".Humanizer.app.backup")
	mkTree(t, target, "0.3.1")
	mkTree(t, staged, "0.3.2")
	h, hl := testHooks(t, target, "0.3.2")
	res := runApplyPlan(&applyPlan{Kind: "swap", Target: target, Staged: staged, Backup: backup, From: "0.3.1", To: "0.3.2", DataDir: root, Relaunch: []string{"x"}}, h)
	if !res.OK || readVer(target) != "0.3.2" {
		t.Fatalf("应换成新版本:%+v %s", res, readVer(target))
	}
	if _, err := os.Stat(backup); err == nil {
		t.Fatal("成功后备份应删掉")
	}
	if _, err := os.Stat(staged); err == nil {
		t.Fatal("暂存目录应已挪走")
	}
	if strings.Join(hl.launches, ",") != "0.3.2" {
		t.Fatalf("只应启动一次新版本:%v", hl.launches)
	}
	if r := readResult(t, root); !r.OK || r.To != "0.3.2" {
		t.Fatalf("结果文件不对:%+v", r)
	}
}

func TestApplyRollsBackWhenNewVersionDoesNotStart(t *testing.T) {
	root := t.TempDir()
	target, staged, backup := filepath.Join(root, "Humanizer.app"), filepath.Join(root, ".Humanizer.app.update"), filepath.Join(root, ".Humanizer.app.backup")
	mkTree(t, target, "0.3.1")
	mkTree(t, staged, "0.3.2")
	h, hl := testHooks(t, target, "0.3.1") // 新版本起不来,旧版本可以
	res := runApplyPlan(&applyPlan{Kind: "swap", Target: target, Staged: staged, Backup: backup, From: "0.3.1", To: "0.3.2", DataDir: root, Relaunch: []string{"x"}}, h)
	if res.OK || readVer(target) != "0.3.1" {
		t.Fatalf("应回滚到旧版本:%+v 现在是 %s", res, readVer(target))
	}
	if b, _ := os.ReadFile(filepath.Join(target, "engine", "llama-server")); string(b) != "0.3.1-engine" {
		t.Fatal("整个目录都应换回旧的(包括引擎)")
	}
	if strings.Join(hl.launches, ",") != "0.3.2,0.3.1" {
		t.Fatalf("应先试新版本,失败后启动旧版本:%v", hl.launches)
	}
	for _, p := range []string{backup, target + ".failed"} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("回滚后不该留下 %s", p)
		}
	}
	if r := readResult(t, root); r.OK || !strings.Contains(r.Error, "已换回") {
		t.Fatalf("结果应记失败并说明已换回:%+v", r)
	}
}

func TestApplySwapFailsBeforeTouchingOldVersion(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Humanizer")
	mkTree(t, target, "0.3.1")
	h, hl := testHooks(t, target, "0.3.1")
	res := runApplyPlan(&applyPlan{Kind: "swap", Target: target, Staged: filepath.Join(root, "missing"), Backup: target + ".backup", From: "0.3.1", To: "0.3.2", DataDir: root, Relaunch: []string{"x"}}, h)
	if res.OK || readVer(target) != "0.3.1" {
		t.Fatalf("暂存目录不在:旧版本应原封不动 %+v", res)
	}
	if strings.Join(hl.launches, ",") != "0.3.1" {
		t.Fatalf("应重新启动旧版本:%v", hl.launches)
	}
}

func TestApplyInstallerSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		ok     bool
	}{
		{"安装成功", `mkdir -p "$1/engine" && printf 0.3.2 > "$1/version.txt"`, true},
		{"安装包装到一半失败", `mkdir -p "$1" && printf half > "$1/version.txt"; exit 3`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "Programs", "Humanizer")
			mkTree(t, target, "0.3.1")
			script := filepath.Join(root, "setup.sh")
			os.WriteFile(script, []byte(tc.script), 0o755)
			h, hl := testHooks(t, target, "0.3.2", "0.3.1")
			res := runApplyPlan(&applyPlan{Kind: "installer", Target: target, Backup: target + ".backup",
				Installer: []string{"/bin/sh", script, target}, From: "0.3.1", To: "0.3.2", DataDir: root, Relaunch: []string{"x"}}, h)
			if res.OK != tc.ok {
				t.Fatalf("结果 %+v", res)
			}
			want := map[bool]string{true: "0.3.2", false: "0.3.1"}[tc.ok]
			if readVer(target) != want {
				t.Fatalf("安装目录应是 %s,实际 %s", want, readVer(target))
			}
			if _, err := os.Stat(target + ".backup"); err == nil {
				t.Fatal("不该留下备份目录")
			}
			if hl.launches[len(hl.launches)-1] != want {
				t.Fatalf("最后启动的应是 %s:%v", want, hl.launches)
			}
		})
	}
}

// TestApplyRealLaunch 用真的子进程跑一遍:启动 → 新进程写 instance.json 并在 /app/ping 报版本 → 确认成功。
func TestApplyRealLaunch(t *testing.T) {
	root := t.TempDir()
	target, staged := filepath.Join(root, "app"), filepath.Join(root, "app.update")
	mkTree(t, target, "0.3.1")
	mkTree(t, staged, "0.3.2")
	plan := &applyPlan{Kind: "swap", Target: target, Staged: staged, Backup: target + ".backup", From: "0.3.1", To: "0.3.2", DataDir: root,
		Relaunch: []string{os.Args[0], "-test.run=^TestHelperFakeApp$"},
		Env:      []string{"HZ_FAKE_APP=1", "HZ_DATA=" + root, "HZ_VERSION=0.3.2"}, ReadySec: 15}
	res := runApplyPlan(plan, defaultApplyHooks(t.Logf))
	if !res.OK {
		t.Fatalf("应确认新版本起来了:%+v", res)
	}
	var inst instanceInfo
	b, _ := os.ReadFile(filepath.Join(root, "instance.json"))
	json.Unmarshal(b, &inst)
	if inst.Version != "0.3.2" || inst.PID == os.Getpid() {
		t.Fatalf("instance.json 应是新进程写的:%+v", inst)
	}
	killInstance(root)
}

// TestHelperFakeApp 不是测试:是 TestApplyRealLaunch 拉起的「新版本 App」。
func TestHelperFakeApp(t *testing.T) {
	if os.Getenv("HZ_FAKE_APP") != "1" {
		t.Skip("只作为子进程运行")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	port := l.Addr().(*net.TCPAddr).Port
	v := os.Getenv("HZ_VERSION")
	b, _ := json.Marshal(instanceInfo{PID: os.Getpid(), Port: port, Version: v})
	os.WriteFile(filepath.Join(os.Getenv("HZ_DATA"), "instance.json"), b, 0o644)
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"app": "humanizer", "version": v})
	}))
	time.Sleep(20 * time.Second)
	os.Exit(0)
}

func TestRelaunchArgs(t *testing.T) {
	got := relaunchArgs([]string{"--serve", "--data-dir", "/x", "-psn_0_1", "--port", "47700"})
	if strings.Join(got, " ") != "--data-dir /x --port 47700" {
		t.Fatalf("%q", got)
	}
}

func TestExtractZip(t *testing.T) {
	mk := func(entries map[string]string) string {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, body := range entries {
			w, _ := zw.Create(name)
			w.Write([]byte(body))
		}
		zw.Close()
		p := filepath.Join(t.TempDir(), "x.zip")
		os.WriteFile(p, buf.Bytes(), 0o644)
		return p
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractZip(mk(map[string]string{"Humanizer.exe": "MZ", "engine/cpu/llama-server.exe": "MZ", "engine/": ""}), dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "engine", "cpu", "llama-server.exe")); string(b) != "MZ" {
		t.Fatal("解压内容不对")
	}
	for _, evil := range []string{"../evil.txt", "a/../../evil.txt", "/abs/evil.txt", `..\evil.txt`, "C:/evil.txt"} {
		out := filepath.Join(t.TempDir(), "o")
		if err := extractZip(mk(map[string]string{evil: "x"}), out); err == nil {
			t.Errorf("%q 应被拒绝", evil)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(out), "evil.txt")); err == nil {
			t.Errorf("%q 写到了目标目录外面", evil)
		}
	}
}

func TestVersionInPackageName(t *testing.T) {
	if versionInPackageName("Humanizer-0.3.2-macos-arm64.dmg") != "0.3.2" || versionInPackageName("other.dmg") != "" {
		t.Fatal("从安装包文件名取版本号不对")
	}
}

// ───────────────────────── HTTP 接口 ─────────────────────────

func TestUpdateRoutes(t *testing.T) {
	g := newFakeGitHub(t)
	g.releases = []map[string]any{g.release("app-v0.3.2", false, false, true, map[string][]byte{"Humanizer-0.3.2-macos-arm64.dmg": fakeDMG(4096, 9)})}
	dir := t.TempDir()
	cfg := testConfig(t)
	cfg.Update.GitHubAPI, cfg.Update.GitHubRepo = g.srv.URL, "owner/repo"
	a := &App{cfg: cfg, dataDir: dir, opts: Options{Version: "0.3.1"}, rootCtx: context.Background()}
	a.updates = newUpdater(a)
	a.updates.rootCtx = context.Background()
	mux := http.NewServeMux()
	a.registerUpdateRoutes(mux)
	do := func(method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var m map[string]any
		json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	if code, m := do("GET", "/app/update", ""); code != 200 || m["current"] != "0.3.1" || m["auto"] != true {
		t.Fatalf("GET /app/update:%d %v", code, m)
	}
	if code, _ := do("POST", "/app/update/check", ""); code != 200 {
		t.Fatal("check 应立即返回")
	}
	waitFor(t, "后台检查完成", 10*time.Second, func() bool {
		_, m := do("GET", "/app/update", "")
		app, _ := m["app"].(map[string]any)
		return m["checking"] == false && app["available"] == true
	})
	if code, _ := do("POST", "/app/update/auto", `{"auto":false}`); code != 200 {
		t.Fatal("关自动检查失败")
	}
	if _, m := do("GET", "/app/update", ""); m["auto"] != false {
		t.Fatal("自动检查开关没生效")
	}
	if s := loadSettings(filepath.Join(dir, "settings.json")); s.AutoUpdateCheck == nil || *s.AutoUpdateCheck {
		t.Fatal("开关应存进 settings.json")
	}
	if code, m := do("POST", "/app/update/model-download", `{"tier":"q8"}`); code != 409 || m["error"].(map[string]any)["code"] != "not_downloaded" {
		t.Fatalf("没下载过的档位不能更新:%d %v", code, m)
	}
	if code, _ := do("POST", "/app/update/nope", ""); code != 404 {
		t.Fatal("未知动作应 404")
	}
}
