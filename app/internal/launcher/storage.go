package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 存储位置:两个目录,都能在「下载模型」那一页改。
//
//	运行目录(数据目录)设置、日志、更新包、更新结果都在这儿;
//	模型目录          GGUF 文件(.part 半截文件也在这儿)都在这儿。
//
// 两者默认都在系统盘(Windows %LOCALAPPDATA%\Humanizer),网页上可以改成别的盘。
// 改位置不会自动搬文件(几十 GB,又是系统自己做的决定),要搬得在网页上勾一下。

// modelsDir 模型文件放哪儿。locModelDir 为空就是数据目录下的 models/。
func (a *App) modelsDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.modelDirLocked()
}

// modelDirLocked 给已经拿着 a.mu 的地方用。
func (a *App) modelDirLocked() string {
	if a.locModelDir != "" {
		return a.locModelDir
	}
	return filepath.Join(a.dataDir, "models")
}

// modelPath 一个档位的本地文件。模型目录改了以后,这个跟着变。
func (a *App) modelPath(t Tier) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return filepath.Join(a.modelDirLocked(), filepath.FromSlash(t.File))
}

// pathsNow 一次性取两个目录,免得夹在两次加锁中间读到一半换过的值。
func (a *App) pathsNow() (dataDir, modelsDir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dataDir, a.modelDirLocked()
}

// applyPaths 换存储位置。两个入参都空字符串 = 回到默认。
// move 为真时把已经下载好的模型文件搬到新位置(跨盘符时是复制,可能要几分钟)。
// 换完按新位置重新判断开机状态:该下的重新下,该起的重新起。
func (a *App) applyPaths(dataDir, modelDir string, move bool) (moved int, err error) {
	curData, curModels := a.pathsNow()

	wantData := curData
	if s := strings.TrimSpace(dataDir); s != "" && !samePath(s, curData) {
		if a.dataDirLocked {
			return 0, errors.New("运行目录由启动参数 --data-dir 固定,改不了;要换就改启动参数")
		}
		p, err := cleanDir(s)
		if err != nil {
			return 0, err
		}
		if err := os.MkdirAll(filepath.Join(p, "logs"), 0o755); err != nil {
			return 0, fmt.Errorf("建不了日志目录:%v", err)
		}
		wantData = p
	}
	wantModels := ""
	if s := strings.TrimSpace(modelDir); s != "" {
		p, err := cleanDir(s)
		if err != nil {
			return 0, err
		}
		wantModels = p
	}
	newModels := wantModels
	if newModels == "" {
		newModels = filepath.Join(wantData, "models")
	}

	// 先停下来:引擎正开着模型文件,Windows 上不关掉就搬不动。
	// 手上正在改写的话等它写完,别把请求打断。
	for waited := time.Duration(0); a.inflight.Load() > 0 && waited < 30*time.Second; waited += 200 * time.Millisecond {
		time.Sleep(200 * time.Millisecond)
	}
	a.pauseDownload()
	a.stopEngine()
	if move && !samePath(curModels, newModels) {
		moved = a.moveModelFiles(curModels, newModels)
	}

	a.mu.Lock()
	a.locModelDir = wantModels
	if !samePath(wantData, a.dataDir) {
		a.dataDir = wantData
	}
	s := a.settings
	dataDirNow, modelsDirNow := a.dataDir, a.modelDirLocked()
	a.mu.Unlock()

	if err := saveLocation(a.pointerDir, dataDirNow, wantModels); err != nil {
		a.logf("记下存储位置失败:%v", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDirNow, "logs"), 0o755); err != nil {
		return moved, fmt.Errorf("建不了日志目录:%v", err)
	}
	// 设置跟着运行目录走:新目录里也写一份,档位、下载源、端口、开关都还在。
	if err := saveSettings(a.settingsPath(), s); err != nil {
		return moved, fmt.Errorf("保存设置失败:%v", err)
	}
	if !samePath(curData, dataDirNow) {
		if a.updates != nil {
			a.updates.setDataDir(dataDirNow)
		}
		carryOverFile(filepath.Join(curData, "config.json"), filepath.Join(dataDirNow, "config.json"))
		_ = os.Remove(filepath.Join(curData, "instance.json"))
		a.logf("运行目录改到 %s", dataDirNow)
	}
	a.writeInstance()
	a.logf("存储位置:运行目录 %s,模型目录 %s(搬了 %d 个文件)", dataDirNow, modelsDirNow, moved)
	a.boot()
	return moved, nil
}

// moveModelFiles 把 from 下的模型文件(含 .part 半截文件)搬到 to。
// 搬不动的留在原地(比如权限问题),记一条日志。跨盘符 os.Rename 会失败,退化成复制。
func (a *App) moveModelFiles(from, to string) int {
	if from == "" || to == "" || samePath(from, to) {
		return 0
	}
	ents, err := os.ReadDir(from)
	if err != nil {
		a.logf("读不到旧模型目录 %s:%v", from, err)
		return 0
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		a.logf("建不了新模型目录 %s:%v", to, err)
		return 0
	}
	n := 0
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.Contains(strings.ToLower(name), ".gguf") {
			continue
		}
		src, dst := filepath.Join(from, name), filepath.Join(to, name)
		if fileExists(dst) { // 新位置已经有了,别覆盖
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			if err := cloneFile(src, dst); err != nil {
				a.logf("搬 %s 失败:%v", name, err)
				continue
			}
			if err := os.Remove(src); err != nil {
				a.logf("删掉旧文件 %s 失败(新位置已经有了):%v", src, err)
			}
		}
		n++
	}
	return n
}

// carryOverFile 把一个设置文件带到新目录(目标已经有了就不动)。
func carryOverFile(src, dst string) {
	if fileExists(dst) {
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = writeFileAtomic(dst, b)
}

func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Chmod(dst, 0o644)
}

// ───────────────────────── HTTP ─────────────────────────

// handlePaths 保存网页上改的存储位置。
func (a *App) handlePaths(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DataDir  string `json:"data_dir"`
		ModelDir string `json:"model_dir"`
		Move     bool   `json:"move"`
		Reset    bool   `json:"reset"` // 「恢复默认」:两个目录都回默认
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "请求格式不对"})
		return
	}
	dataDir, modelDir := req.DataDir, req.ModelDir
	if req.Reset {
		// 恢复默认:模型目录回「运行目录下的 models」,运行目录回系统默认位置
		// (被 --data-dir 钉死时,那一半本来就动不了,别在这里报错)
		dataDir, modelDir = defaultDataDir(), ""
		if a.dataDirLocked {
			dataDir, _ = a.pathsNow()
		}
	}
	moved, err := a.applyPaths(dataDir, modelDir, req.Move)
	if err != nil {
		a.logf("改存储位置失败:%v", err)
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	dataDir, modelsDir := a.pathsNow()
	writeJSON(w, 200, map[string]any{
		"ok": true, "moved": moved, "data_dir": dataDir, "model_dir": modelsDir,
	})
}

// handlePickDir 弹系统的「选文件夹」窗口。what=data|models;取消时返回空路径。
func (a *App) handlePickDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		What string `json:"what"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req)
	what := "models"
	switch req.What {
	case "data":
		what = "data"
	case "models":
	default:
		writeJSON(w, 400, map[string]any{"error": "不知道要选哪个目录"})
		return
	}
	if what == "data" && a.dataDirLocked {
		writeJSON(w, 409, map[string]any{"error": "运行目录由启动参数 --data-dir 固定,改不了"})
		return
	}
	dataDir, modelsDir := a.pathsNow()
	from, title := modelsDir, "选择模型文件存放的文件夹"
	if what == "data" {
		from, title = dataDir, "选择运行目录(设置、日志、引擎更新)"
	}
	_ = os.MkdirAll(from, 0o755) // 选择器要一个已经存在的起始目录
	// 用户关掉网页不该让选择器留在屏幕上,所以不走请求的 context。
	// 两分钟上限:选择器要是压根没弹出来(用户目录被改坏等),也不能让按钮一直转着。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := pickDir(ctx, from, title)
	if err != nil {
		a.logf("打开目录选择器失败:%v", err)
		msg := "打不开文件夹选择器:可以把路径直接粘到输入框里,再点「保存位置」"
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "文件夹选择器两分钟没反应,已经关掉了:可以把路径直接粘到输入框里,再点「保存位置」"
		}
		writeJSON(w, 500, map[string]any{"error": msg})
		return
	}
	if p != "" {
		p, err = cleanDir(p)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
	}
	writeJSON(w, 200, map[string]any{"path": p})
}
