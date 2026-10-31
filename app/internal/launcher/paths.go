package launcher

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultDataDir:Mac ~/Library/Application Support/Humanizer,
// Windows %LOCALAPPDATA%\Humanizer,其他系统 $XDG_DATA_HOME/Humanizer。
func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Humanizer")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Humanizer")
		}
		return filepath.Join(home, "AppData", "Local", "Humanizer")
	default:
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "Humanizer")
		}
		return filepath.Join(home, ".local", "share", "Humanizer")
	}
}

// pointerName:一个很小的文件,记着用户在网页上把存储位置改到了哪儿。
// 平时放在默认数据目录里,所以运行目录整个换掉、甚至搬走了,下次启动照样找得到;
// 运行目录被 --data-dir 钉死时放在那个目录里,只记模型目录。
// Windows 卸载程序也读它(humanizer.iss),写法见 locationJSON。
const pointerName = "location.json"

// location 只存路径,别的一概不放:这样数据目录丢了还能靠它找回来。
// 字段为空 = 那一项用默认位置。
type location struct {
	DataDir  string `json:"data_dir,omitempty"`
	ModelDir string `json:"model_dir,omitempty"`
}

// startPaths 是一次启动定下来的存储位置。
type startPaths struct {
	DataDir     string // 这次运行的数据目录(设置、日志、更新包)
	ModelDir    string // 网页上设过的模型目录;空 = 数据目录下的 models
	PointerDir  string // location.json 放哪儿
	DataDirLock bool   // 运行目录由命令行/环境变量固定,网页上不让改
	Note        string // 记下的运行目录用不了、退回默认时的说明(进启动日志)
}

// resolveStartPaths 定出这次的存储位置:
// 命令行 --data-dir / 环境变量 > 默认目录里的 location.json > 默认目录。
// 给了 explicit 时完全不看默认目录(开发、测试、便携用法不该被本机装的 App 影响)。
func resolveStartPaths(explicit string) startPaths {
	if strings.TrimSpace(explicit) != "" {
		dir := absDir(explicit)
		sp := startPaths{DataDir: dir, PointerDir: dir, DataDirLock: true}
		if loc, ok := readLocation(dir); ok {
			sp.ModelDir = cleanRecorded(loc.ModelDir)
		}
		return sp
	}
	def := defaultDataDir()
	sp := startPaths{DataDir: def, PointerDir: def}
	if loc, ok := readLocation(def); ok {
		// 运行目录存不存在都要管:盘拔了、目录被删了,回默认,别卡在一个用不了的位置上。
		if d := cleanRecorded(loc.DataDir); d != "" {
			if usableDir(d) {
				sp.DataDir = d
			} else {
				sp.Note = "记下的运行目录 " + d + " 现在用不了(盘没插或没有写权限),这次先用默认位置"
			}
		}
		// 模型目录不要求已经存在(它下次下载时才建):这时悄悄回默认,
		// 等于把几十 GB 的模型挪了个地方,不如照旧记着,等用户自己处理。
		sp.ModelDir = cleanRecorded(loc.ModelDir)
	}
	return sp
}

func readLocation(dir string) (location, bool) {
	var loc location
	b, err := os.ReadFile(filepath.Join(dir, pointerName))
	if err != nil || json.Unmarshal(b, &loc) != nil {
		return location{}, false
	}
	return loc, true
}

// cleanRecorded:location.json 里只认绝对路径,相对路径(手改坏了)当没写。
func cleanRecorded(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

// saveLocation 记下"存储位置搬到哪儿了"。dataDir 就是 pointerDir(默认位置或被钉死的目录)时不记,
// 两项都是默认就把这个文件删掉。
func saveLocation(pointerDir, dataDir, modelDir string) error {
	if pointerDir == "" {
		return nil
	}
	p := filepath.Join(pointerDir, pointerName)
	loc := location{ModelDir: modelDir}
	if !samePath(dataDir, pointerDir) {
		loc.DataDir = dataDir
	}
	if loc == (location{}) {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeFileAtomic(p, locationJSON(loc))
}

// locationJSON:UTF-8,中文和 & < > 原样写(不用 \uXXXX)。
// Windows 卸载程序(humanizer.iss)按 UTF-8 读这个文件,只认 \\ 和 \" 两种转义,别的转义一律当没设过。
func locationJSON(loc location) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(loc)
	return b.Bytes()
}

func absDir(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// modelDirLog 启动日志里显示的模型目录。
func modelDirLog(dataDir, modelDir string) string {
	if modelDir != "" {
		return modelDir
	}
	return filepath.Join(dataDir, "models")
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return false
}

// usableDir:路径能建出来(或已经是文件夹)且写得进去。
func usableDir(p string) bool {
	_, err := cleanDir(p)
	return err == nil
}

// cleanDir 把用户给的目录收拾干净:绝对路径、去尾分隔符、建出来、试写一下。
// 顺便挡掉「这里其实是个文件」和只读的目录。
func cleanDir(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("路径是空的")
	}
	dir := absDir(p)
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return "", fmt.Errorf("%s 不是一个文件夹", dir)
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("建不了这个文件夹:%v", err)
	}
	probe := filepath.Join(dir, ".humanizer-write-test")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return "", fmt.Errorf("这个文件夹写不进去:%v", err)
	}
	_ = os.Remove(probe)
	return dir, nil
}

// Backend 是一个可以尝试的 llama-server 二进制。
type Backend struct {
	Name string `json:"name"` // metal / cuda / vulkan / cpu / custom
	Path string `json:"path"`
	GPU  bool   `json:"gpu"`
	// CPU 模式下额外加的参数(Mac 上用同一个二进制跑纯 CPU 时要 --device none)
	Extra []string `json:"-"`
}

// discoverBackends 找随包附带的引擎,按优先级排好。
//
//	macOS  .app:  Humanizer.app/Contents/MacOS/Humanizer
//	              Humanizer.app/Contents/Resources/engine/metal/llama-server
//	Windows:      安装目录\Humanizer.exe
//	              安装目录\engine\{cuda,vulkan,cpu}\llama-server.exe
//	开发/便携:    启动器同目录下的 engine/<name>/llama-server
func discoverBackends(overrides []string, gpu GPUInfo) []Backend {
	if len(overrides) > 0 {
		return parseEngineOverrides(overrides)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	dir := filepath.Dir(exe)
	roots := []string{
		filepath.Join(dir, "..", "Resources", "engine"), // .app 里
		filepath.Join(dir, "engine"),                    // Windows 安装目录 / 便携版
	}
	bin := "llama-server"
	if runtime.GOOS == "windows" {
		bin = "llama-server.exe"
	}
	find := func(name string) string {
		for _, r := range roots {
			p := filepath.Join(r, name, bin)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return filepath.Clean(p)
			}
		}
		return ""
	}

	var out []Backend
	switch runtime.GOOS {
	case "darwin":
		if p := find("metal"); p != "" {
			out = append(out, Backend{Name: "metal", Path: p, GPU: true})
			// Metal 起不来时同一个二进制退回纯 CPU
			out = append(out, Backend{Name: "cpu", Path: p, GPU: false, Extra: []string{"--device", "none"}})
		}
	case "windows":
		// 有 NVIDIA 驱动才试 CUDA;有 Vulkan 运行时才试 Vulkan;CPU 兜底。
		if gpu.NVIDIA {
			if p := find("cuda"); p != "" {
				out = append(out, Backend{Name: "cuda", Path: p, GPU: true})
			}
		}
		if gpu.Vulkan {
			if p := find("vulkan"); p != "" {
				out = append(out, Backend{Name: "vulkan", Path: p, GPU: true})
			}
		}
		if p := find("cpu"); p != "" {
			out = append(out, Backend{Name: "cpu", Path: p, GPU: false})
		}
	default:
		// Linux:和 Windows 一样先看驱动再试 GPU 后端,免得没有 Vulkan/NVIDIA
		// 的机器先白等一轮 GPU 引擎加载;CPU 永远兜底。
		if gpu.NVIDIA {
			if p := find("cuda"); p != "" {
				out = append(out, Backend{Name: "cuda", Path: p, GPU: true})
			}
		}
		if gpu.Vulkan {
			if p := find("vulkan"); p != "" {
				out = append(out, Backend{Name: "vulkan", Path: p, GPU: true})
			}
		}
		if p := find("cpu"); p != "" {
			out = append(out, Backend{Name: "cpu", Path: p, GPU: false})
		}
	}
	return out
}

// parseEngineOverrides:开发时用 --engine / HUMANIZER_ENGINE 指定引擎。
// 形如 "/path/llama-server" 或 "cuda=/a;cpu=/b"(多个用 ; 分隔,也可以重复 --engine)。
func parseEngineOverrides(items []string) []Backend {
	var out []Backend
	for _, it := range items {
		for _, part := range strings.Split(it, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, path := "custom", part
			if i := strings.Index(part, "="); i > 0 && !strings.ContainsAny(part[:i], `/\`) {
				name, path = part[:i], part[i+1:]
			}
			out = append(out, Backend{Name: name, Path: path, GPU: name != "cpu"})
		}
	}
	return out
}
