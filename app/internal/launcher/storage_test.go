package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a := &App{cfg: testConfig(t), dataDir: dir, rootCtx: context.Background(), opts: Options{Version: "0.3.2"}}
	a.settings = loadSettings(a.settingsPath())
	return a
}

func TestModelsDirDefault(t *testing.T) {
	a := testApp(t)
	if got, want := a.modelsDir(), filepath.Join(a.dataDir, "models"); got != want {
		t.Fatalf("默认模型目录 %s ≠ %s", got, want)
	}
	a.locModelDir = filepath.Join(t.TempDir(), "D-models")
	if got := a.modelsDir(); got != a.locModelDir {
		t.Fatalf("改过以后应跟着变,实际 %s", got)
	}
	// modelPath 要跟着模型目录走(引擎加载的正是这个路径)
	tier, _ := a.cfg.tier("q8")
	if !strings.HasPrefix(a.modelPath(tier), a.locModelDir) {
		t.Fatalf("modelPath %s 不在 %s 下面", a.modelPath(tier), a.locModelDir)
	}
}

func TestResolveStartPathsDefaults(t *testing.T) {
	sp := resolveStartPaths("")
	if sp.DataDir != defaultDataDir() || sp.DataDirLock || sp.PointerDir != defaultDataDir() || sp.ModelDir != "" {
		t.Fatalf("没设过就该是默认位置:%+v", sp)
	}
}

func TestResolveStartPathsLocked(t *testing.T) {
	dir := t.TempDir()
	sp := resolveStartPaths(dir)
	if !sp.DataDirLock || sp.PointerDir != "" {
		t.Fatalf("显式指定的运行目录应锁死且不记指针:%+v", sp)
	}
	if !samePath(sp.DataDir, dir) {
		t.Fatalf("运行目录应是 %s,实际 %s", dir, sp.DataDir)
	}
}

func TestSaveLocationRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("HOME", tmp)
	data2, models2 := filepath.Join(tmp, "D", "Humanizer"), filepath.Join(tmp, "D", "gguf")
	if err := saveLocation(defaultDataDir(), data2, models2); err != nil {
		t.Fatal(err)
	}
	sp := resolveStartPaths("")
	if !samePath(sp.DataDir, data2) || !samePath(sp.ModelDir, models2) {
		t.Fatalf("重启后没找回位置:%+v", sp)
	}
	if sp.DataDirLock || sp.PointerDir != defaultDataDir() {
		t.Fatalf("网页上改的应可再改:%+v", sp)
	}
	// 都回默认时把指针删掉,免得留下一个指向旧位置的空壳
	if err := saveLocation(defaultDataDir(), defaultDataDir(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, pointerName)); !os.IsNotExist(err) {
		t.Fatalf("回到默认后应删掉 %s", pointerName)
	}
}

func TestSaveLocationIgnoresBadPointer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("HOME", tmp)
	// 指到一个建不出来的目录(是文件,不是目录):当没设过,回默认
	file := filepath.Join(tmp, "a-file")
	os.WriteFile(file, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(tmp, pointerName), []byte(`{"data_dir":`+strconvQuote(file)+`}`), 0o644)
	if sp := resolveStartPaths(""); !samePath(sp.DataDir, defaultDataDir()) {
		t.Fatalf("坏指针应被忽略,实际 %s", sp.DataDir)
	}
	os.WriteFile(filepath.Join(tmp, pointerName), []byte(`{不是 json`), 0o644)
	if sp := resolveStartPaths(""); !samePath(sp.DataDir, defaultDataDir()) {
		t.Fatalf("坏 JSON 应被忽略,实际 %s", sp.DataDir)
	}
}

func TestCleanDir(t *testing.T) {
	tmp := t.TempDir()
	got, err := cleanDir(filepath.Join(tmp, "new", "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(tmp, "new", "nested") {
		t.Fatalf("路径没被收拾干净:%s", got)
	}
	if st, err := os.Stat(got); err != nil || !st.IsDir() {
		t.Fatalf("目录应该已经建出来:%v", err)
	}
	file := filepath.Join(tmp, "f.txt")
	os.WriteFile(file, nil, 0o644)
	if _, err := cleanDir(file); err == nil {
		t.Fatal("指向一个文件应该报错")
	}
	if _, err := cleanDir("  "); err == nil {
		t.Fatal("空路径应该报错")
	}
}

func TestApplyPathsMovesModels(t *testing.T) {
	a := testApp(t)
	old := filepath.Join(a.dataDir, "models")
	os.MkdirAll(old, 0o755)
	model := filepath.Join(old, "humanizer-12b-Q8_0.gguf")
	os.WriteFile(model, []byte("GGUF fake"), 0o644)

	newModels := filepath.Join(t.TempDir(), "gguf")
	moved, err := a.applyPaths("", newModels, true)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("应搬 1 个文件,实际 %d", moved)
	}
	if !fileExists(filepath.Join(newModels, "humanizer-12b-Q8_0.gguf")) {
		t.Fatal("文件没搬到新位置")
	}
	if fileExists(model) {
		t.Fatal("旧位置的文件应该已经不在了")
	}
	if a.modelsDir() != newModels {
		t.Fatalf("模型目录应是 %s,实际 %s", newModels, a.modelsDir())
	}
}

func TestApplyPathsWithoutMoveKeepsFiles(t *testing.T) {
	a := testApp(t)
	old := filepath.Join(a.dataDir, "models")
	os.MkdirAll(old, 0o755)
	model := filepath.Join(old, "humanizer-12b-Q8_0.gguf")
	os.WriteFile(model, []byte("GGUF fake"), 0o644)

	moved, err := a.applyPaths("", filepath.Join(t.TempDir(), "gguf"), false)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 0 || !fileExists(model) {
		t.Fatalf("不勾「一起搬」就不该动文件(moved=%d, 旧文件在=%v)", moved, fileExists(model))
	}
}

func TestApplyPathsMovesPartFile(t *testing.T) {
	a := testApp(t)
	old := filepath.Join(a.dataDir, "models")
	os.MkdirAll(old, 0o755)
	// 下了一半的文件也得带走,不然续传要重新下几十 GB
	os.WriteFile(old+filepath.FromSlash("/humanizer-12b-Q8_0.gguf.part"), []byte("half"), 0o644)

	newModels := filepath.Join(t.TempDir(), "gguf")
	if moved, err := a.applyPaths("", newModels, true); err != nil || moved != 1 {
		t.Fatalf("半截文件应被搬走:%d %v", moved, err)
	}
	if !fileExists(filepath.Join(newModels, "humanizer-12b-Q8_0.gguf.part")) {
		t.Fatal("半截文件没在新位置")
	}
}

func TestApplyPathsBlockedWhenLocked(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	a := testApp(t)
	a.dataDirLocked = true
	if _, err := a.applyPaths(filepath.Join(tmp, "other"), "", false); err == nil {
		t.Fatal("运行目录被 --data-dir 固定时不该接受改动")
	}
	// 模型目录还是能改的
	if _, err := a.applyPaths("", filepath.Join(tmp, "m"), false); err != nil {
		t.Fatalf("模型目录应该还能改:%v", err)
	}
}

func TestApplyPathsResetsToDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("HOME", tmp)
	a := testApp(t)
	a.pointerDir = defaultDataDir()
	if _, err := a.applyPaths(filepath.Join(tmp, "elsewhere"), filepath.Join(tmp, "m"), false); err != nil {
		t.Fatal(err)
	}
	if !samePath(a.modelsDir(), filepath.Join(tmp, "m")) {
		t.Fatalf("模型目录没改过来:%s", a.modelsDir())
	}
	if _, err := a.applyPaths("", "", false); err != nil {
		t.Fatal(err)
	}
	if !samePath(a.modelsDir(), filepath.Join(a.dataDir, "models")) {
		t.Fatalf("恢复默认后应是 %s,实际 %s", filepath.Join(a.dataDir, "models"), a.modelsDir())
	}
	if sp := resolveStartPaths(""); sp.ModelDir != "" {
		t.Fatalf("指针里不该再留模型目录:%+v", sp)
	}
}

func TestApplyPathsCarriesSettings(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("HOME", tmp)
	a := testApp(t)
	a.pointerDir = defaultDataDir()
	a.settings.Tier = "q8"
	a.settings.Endpoint = "mirror"
	if err := saveSettings(a.settingsPath(), a.settings); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(tmp, "next")
	if _, err := a.applyPaths(next, "", false); err != nil {
		t.Fatal(err)
	}
	if !samePath(a.dataDir, next) {
		t.Fatalf("运行目录应是 %s,实际 %s", next, a.dataDir)
	}
	// 新目录里要有设置(档位、下载源都在),否则下次启动要重选
	got := loadSettings(a.settingsPath())
	if got.Tier != "q8" || got.Endpoint != "mirror" {
		t.Fatalf("设置没带过去:%+v", got)
	}
	if _, err := os.Stat(filepath.Join(next, "logs")); err != nil {
		t.Fatalf("新运行目录里应该有 logs 子目录:%v", err)
	}
	// 指针得让下次启动找得到
	if sp := resolveStartPaths(""); !samePath(sp.DataDir, next) {
		t.Fatalf("重启后没找回新运行目录:%+v", sp)
	}
}

func TestMoveModelFilesSkipsExisting(t *testing.T) {
	a := testApp(t)
	from, to := filepath.Join(t.TempDir(), "from"), filepath.Join(t.TempDir(), "to")
	os.MkdirAll(from, 0o755)
	os.MkdirAll(to, 0o755)
	os.WriteFile(filepath.Join(from, "a.gguf"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(to, "a.gguf"), []byte("already here"), 0o644)
	if n := a.moveModelFiles(from, to); n != 0 {
		t.Fatalf("新位置已有的文件不该被覆盖,却搬了 %d 个", n)
	}
	b, _ := os.ReadFile(filepath.Join(to, "a.gguf"))
	if string(b) != "already here" {
		t.Fatal("新位置的文件被覆盖了")
	}
}

func TestHandlePaths(t *testing.T) {
	a := testApp(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/paths", a.handlePaths)
	do := func(body string) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/app/paths", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var m map[string]any
		json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	newModels := filepath.Join(t.TempDir(), "gguf")
	code, m := do(`{"model_dir":` + strconvQuote(newModels) + `}`)
	if code != 200 || m["ok"] != true {
		t.Fatalf("保存模型目录:%d %v", code, m)
	}
	if got := m["model_dir"]; got != newModels {
		t.Fatalf("返回的模型目录 %v ≠ %s", got, newModels)
	}
	// 指向一个文件:报 400,位置保持不变
	file := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(file, nil, 0o644)
	if code, m := do(`{"model_dir":` + strconvQuote(file) + `}`); code != 400 || m["error"] == nil {
		t.Fatalf("指向文件应报 400:%d %v", code, m)
	}
	if a.modelsDir() != newModels {
		t.Fatalf("失败后不该改位置,实际 %s", a.modelsDir())
	}
}

func TestHandlePathsReset(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("HOME", tmp)
	a := testApp(t)
	a.pointerDir = defaultDataDir()
	if _, err := a.applyPaths("", filepath.Join(tmp, "m"), false); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/paths", a.handlePaths)
	req := httptest.NewRequest("POST", "/app/paths", strings.NewReader(`{"reset":true}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("恢复默认:%d %s", rec.Code, rec.Body.String())
	}
	if !samePath(a.modelsDir(), filepath.Join(a.dataDir, "models")) {
		t.Fatalf("恢复默认后模型目录应是 %s,实际 %s", filepath.Join(a.dataDir, "models"), a.modelsDir())
	}
	// 数据目录是钉死的,恢复默认时那一半别动
	a2 := testApp(t)
	a2.dataDirLocked = true
	mux2 := http.NewServeMux()
	mux2.HandleFunc("POST /app/paths", a2.handlePaths)
	before, _ := a2.pathsNow()
	req2 := httptest.NewRequest("POST", "/app/paths", strings.NewReader(`{"reset":true}`))
	rec2 := httptest.NewRecorder()
	mux2.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("钉死时恢复默认不该报错:%d %s", rec2.Code, rec2.Body.String())
	}
	if after, _ := a2.pathsNow(); !samePath(before, after) {
		t.Fatalf("钉死时运行目录不该变:%s → %s", before, after)
	}
}

func TestHandlePickDirRejectsUnknown(t *testing.T) {
	a := testApp(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/pick-dir", a.handlePickDir)
	req := httptest.NewRequest("POST", "/app/pick-dir", strings.NewReader(`{"what":"nope"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("未知的目标应报 400,实际 %d", rec.Code)
	}
}

func TestHandlePickDirLocked(t *testing.T) {
	a := testApp(t)
	a.dataDirLocked = true
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/pick-dir", a.handlePickDir)
	req := httptest.NewRequest("POST", "/app/pick-dir", strings.NewReader(`{"what":"data"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("被钉死时应报 409,实际 %d", rec.Code)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
