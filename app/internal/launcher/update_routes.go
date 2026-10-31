package launcher

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// registerUpdateRoutes:
//
//	GET  /app/update            更新状态(App 最新版本和说明、各档位模型、下载进度、上次结果)
//	POST /app/update/{action}   check / auto / dismiss / ack /
//	                            app-download / app-pause / app-cancel / app-apply / app-open /
//	                            model-download / model-pause / model-cancel / model-ack(带 tier)
func (a *App) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /app/update", a.handleUpdateStatus)
	mux.HandleFunc("POST /app/update/{action}", a.handleUpdateAction)
}

// countInflight 记下正在进行的改写请求数:模型更新要等改写结束才重启引擎,App 更新也要避开。
func (a *App) countInflight(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/completion" || r.URL.Path == "/api/v1/chat/completions" { // 后者:外部服务模式
			a.inflight.Add(1)
			defer a.inflight.Add(-1)
		}
		h.ServeHTTP(w, r)
	})
}

func (a *App) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	auto := a.updates.autoEnabled(a.settings)
	a.mu.Unlock()
	writeJSON(w, 200, a.updates.status(auto, a.cfg.Tiers))
}

func (a *App) handleUpdateAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tier    string `json:"tier"`
		Auto    *bool  `json:"auto"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": "bad_json", "message": "bad json"}})
		return
	}
	u := a.updates
	var err error
	switch r.PathValue("action") {
	case "check":
		go u.check(a.rootCtx, true)
	case "auto":
		if req.Auto == nil {
			err = &PermanentError{Code: "bad_request", Msg: "缺少 auto"}
			break
		}
		v := *req.Auto
		a.mu.Lock()
		a.settings.AutoUpdateCheck = &v
		s := a.settings
		a.mu.Unlock()
		err = saveSettings(a.settingsPath(), s)
	case "dismiss":
		u.dismiss(req.Version)
	case "ack":
		u.ackResult()
	case "app-download":
		err = u.startAppDownload()
	case "app-pause":
		u.pauseApp()
	case "app-cancel":
		u.cancelApp()
	case "app-apply":
		err = u.apply(int(a.inflight.Load()))
	case "app-open":
		err = u.openPackage()
	case "model-download":
		err = u.startModelDownload(req.Tier)
	case "model-pause":
		u.pauseModel(req.Tier)
	case "model-cancel":
		u.cancelModel(req.Tier)
	case "model-ack":
		u.ackModel(req.Tier)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		code := "failed"
		var pe *PermanentError
		if errors.As(err, &pe) {
			code = pe.Code
		}
		writeJSON(w, 409, map[string]any{"error": map[string]any{"code": code, "message": err.Error()}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// runApplyHelper:Windows 更新助手的入口(Humanizer.exe --apply-update <计划>)。
// 它是从数据目录里的一份副本跑起来的,所以可以放心替换安装目录。
func runApplyHelper(planPath string) int {
	b, err := os.ReadFile(planPath)
	if err != nil {
		return 1
	}
	var p applyPlan
	if err := json.Unmarshal(b, &p); err != nil || p.DataDir == "" {
		return 1
	}
	_ = os.MkdirAll(filepath.Join(p.DataDir, "logs"), 0o755)
	lf, err := os.OpenFile(filepath.Join(p.DataDir, "logs", "update.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	var w io.Writer = io.Discard
	if err == nil {
		defer lf.Close()
		w = lf
	}
	logger := log.New(w, "", log.LstdFlags)
	logger.Printf("===== 更新助手:%s → %s", p.From, p.To)
	res := runApplyPlan(&p, defaultApplyHooks(logger.Printf))
	_ = os.Remove(planPath)
	if !res.OK {
		return 1
	}
	return 0
}

// extractZip 解压便携版。拒绝绝对路径和 ../ 之类跳出目标目录的条目。
func extractZip(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	root, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(name) || strings.Contains(name, ":") {
			return fmt.Errorf("压缩包里有不安全的路径:%q", f.Name)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Errorf("压缩包里有不安全的路径:%q", f.Name)
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包里有符号链接:%q", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// extractTarGz 解压 Linux 便携版。拒绝绝对路径、../ 和硬链接;
// 只允许指向包内相对位置的符号链接(引擎的 .so 名链接),
// 文件权限上限 0755,总量和条目数有上限,防解压炸弹。
const (
	maxTarEntries   = 10000
	maxTarTotalSize = 2 << 30
)

func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	root, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for entries := 0; ; entries++ {
		if entries >= maxTarEntries {
			return fmt.Errorf("压缩包条目太多(>%d)", maxTarEntries)
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name == "." || name == "" && h.Typeflag == tar.TypeDir {
			continue // tar -C dir . 产生的根条目
		}
		if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
			return fmt.Errorf("压缩包里有不安全的路径:%q", h.Name)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Errorf("压缩包里有不安全的路径:%q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("压缩包里有不安全的符号链接:%q", h.Name)
			}
			linkTarget := filepath.Join(filepath.Dir(target), filepath.FromSlash(h.Linkname))
			rel, err := filepath.Rel(root, linkTarget)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("压缩包里的符号链接指向包外:%q", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			mode := os.FileMode(h.Mode).Perm() & 0o755
			if mode == 0 {
				mode = 0o644
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, io.LimitReader(tr, maxTarTotalSize-total+1))
			total += n
			if err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			if total > maxTarTotalSize {
				return fmt.Errorf("压缩包解压后太大(>%d 字节)", maxTarTotalSize)
			}
		default:
			return fmt.Errorf("压缩包里有链接或特殊文件:%q", h.Name)
		}
	}
}

func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	mode := os.FileMode(0o644)
	if f.FileInfo().Mode()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if !f.Modified.IsZero() {
		_ = os.Chtimes(target, time.Now(), f.Modified)
	}
	return nil
}
