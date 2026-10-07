package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Tier 是一个可下载的模型档位(一个 GGUF 文件)。
type Tier struct {
	ID       string            `json:"id"`
	Label    string            `json:"label"`
	File     string            `json:"file"` // 仓库内相对路径,可以带子目录(lite/xxx.gguf)
	ApproxGB float64           `json:"approx_gb"`
	MinRAMGB float64           `json:"min_ram_gb"`
	SHA256   string            `json:"sha256,omitempty"` // 留空则用 HF 返回的 X-Linked-Etag
	Note     map[string]string `json:"note,omitempty"`
}

// Endpoint 是下载源(HF 官方 / 镜像 / 自定义)。
type Endpoint struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Base  string `json:"base"`
}

type RAMRule struct {
	MinGB float64 `json:"min_gb"`
	Tier  string  `json:"tier"`
}

type EngineConfig struct {
	CtxSize        int      `json:"ctx_size"`
	GPULayers      string   `json:"gpu_layers"` // "all" | "auto" | 数字
	Parallel       int      `json:"parallel"`
	LoadTimeoutSec int      `json:"load_timeout_sec"`
	ExtraArgs      []string `json:"extra_args"`
}

type NPredictRule struct {
	Factor float64 `json:"factor"`
	Min    int     `json:"min"`
	Max    int     `json:"max"`
}

// UpdateConfig:App 安装包从 GitHub Releases 拿,模型从上面 repo/revision 的同名文件比。
type UpdateConfig struct {
	GitHubRepo    string `json:"github_repo"`    // owner/name
	GitHubAPI     string `json:"github_api"`     // 测试时换成本地假服务器
	TagPrefix     string `json:"tag_prefix"`     // 只认这个前缀的标签(app-v0.3.2)
	IntervalHours int    `json:"interval_hours"` // 自动检查的最小间隔
}

type Config struct {
	Schema          int            `json:"schema"`
	Repo            string         `json:"repo"`
	Revision        string         `json:"revision"`
	Endpoints       []Endpoint     `json:"endpoints"`
	Tiers           []Tier         `json:"tiers"`
	TierByRAM       []RAMRule      `json:"tier_by_ram"`
	RAMSlackGB      float64        `json:"ram_slack_gb"`
	Engine          EngineConfig   `json:"engine"`
	Sampling        map[string]any `json:"sampling"`
	NPredict        NPredictRule   `json:"n_predict"`
	IdleExitMinutes int            `json:"idle_exit_minutes"`
	PreferredPort   int            `json:"preferred_port"`
	Update          UpdateConfig   `json:"update"`
}

// loadConfig 读内置默认配置,再用数据目录里的 config.json(若存在)覆盖。
// JSON 覆盖语义:出现的字段替换,数组整体替换,sampling 这种 map 按键合并。
func loadConfig(def []byte, overridePath string) (Config, error) {
	var c Config
	if err := json.Unmarshal(def, &c); err != nil {
		return c, fmt.Errorf("内置配置损坏: %w", err)
	}
	if overridePath != "" {
		b, err := os.ReadFile(overridePath)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("%s 不是合法 JSON: %w", overridePath, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return c, err
		}
	}
	return c, c.validate()
}

func (c *Config) validate() error {
	if c.Repo == "" || c.Revision == "" {
		return errors.New("配置缺少 repo/revision")
	}
	if len(c.Tiers) == 0 {
		return errors.New("配置里没有任何模型档位")
	}
	seen := map[string]bool{}
	for _, t := range c.Tiers {
		if t.ID == "" || t.File == "" {
			return fmt.Errorf("档位缺少 id 或 file: %+v", t)
		}
		if seen[t.ID] {
			return fmt.Errorf("档位 id 重复: %s", t.ID)
		}
		if strings.Contains(t.File, "..") || strings.HasPrefix(t.File, "/") || strings.Contains(t.File, `\`) {
			return fmt.Errorf("档位文件名不合法: %s", t.File)
		}
		seen[t.ID] = true
	}
	for _, r := range c.TierByRAM {
		if !seen[r.Tier] {
			return fmt.Errorf("tier_by_ram 指向不存在的档位: %s", r.Tier)
		}
	}
	if len(c.Endpoints) == 0 {
		return errors.New("配置里没有下载源")
	}
	if c.Engine.CtxSize <= 0 {
		c.Engine.CtxSize = 8192
	}
	if c.Engine.Parallel <= 0 {
		c.Engine.Parallel = 1
	}
	if c.Engine.GPULayers == "" {
		c.Engine.GPULayers = "all"
	}
	if c.Engine.LoadTimeoutSec <= 0 {
		c.Engine.LoadTimeoutSec = 900
	}
	if c.NPredict.Max <= 0 {
		c.NPredict = NPredictRule{Factor: 2.5, Min: 256, Max: 2048}
	}
	if c.Update.GitHubAPI == "" {
		c.Update.GitHubAPI = "https://api.github.com"
	}
	if c.Update.TagPrefix == "" {
		c.Update.TagPrefix = "app-v"
	}
	if c.Update.IntervalHours <= 0 {
		c.Update.IntervalHours = 24
	}
	return nil
}

func (c *Config) tier(id string) (Tier, bool) {
	for _, t := range c.Tiers {
		if t.ID == id {
			return t, true
		}
	}
	return Tier{}, false
}

func (c *Config) endpoint(id string) (Endpoint, bool) {
	for _, e := range c.Endpoints {
		if e.ID == id {
			return e, true
		}
	}
	return Endpoint{}, false
}

// recommendTier 按物理内存挑档位。ram_slack_gb 是容差:标称 32 GB 的机器
// 系统常报 31.x GB(显存/固件保留),不能因此掉档。
func (c *Config) recommendTier(ramGB float64) string {
	for _, r := range c.TierByRAM {
		if ramGB+c.RAMSlackGB >= r.MinGB {
			return r.Tier
		}
	}
	if n := len(c.TierByRAM); n > 0 {
		return c.TierByRAM[n-1].Tier
	}
	return c.Tiers[len(c.Tiers)-1].ID
}

// fileURL 拼出 HF 风格下载地址:{base}/{repo}/resolve/{revision}/{file}
func (c *Config) fileURL(base, file string) string {
	parts := strings.Split(file, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.TrimRight(base, "/") + "/" + c.Repo + "/resolve/" + url.PathEscape(c.Revision) + "/" + strings.Join(parts, "/")
}

// Settings 是用户在网页上做的选择,持久化在数据目录的 settings.json。
type Settings struct {
	Tier     string `json:"tier,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Port     int    `json:"port,omitempty"`
	// AutoUpdateCheck:启动后每天最多静默检查一次更新。nil = 没设置过 = 开。
	AutoUpdateCheck *bool `json:"auto_update_check,omitempty"`
}

func loadSettings(path string) Settings {
	var s Settings
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveSettings(path string, s Settings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeFileAtomic(path, b)
}

func writeFileAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
