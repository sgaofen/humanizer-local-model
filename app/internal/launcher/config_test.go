package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	b, err := os.ReadFile("../../config/default.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(b, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRecommendTier(t *testing.T) {
	c := testConfig(t)
	cases := map[float64]string{128: "q8", 32: "q8", 31.4: "q8", 24: "q6", 16: "q6", 15.5: "q6", 8: "q4", 4: "q4"}
	for ram, want := range cases {
		if got := c.recommendTier(ram); got != want {
			t.Errorf("%.1f GB → %s,期望 %s", ram, got, want)
		}
	}
}

func TestFileURL(t *testing.T) {
	c := testConfig(t)
	got := c.fileURL("https://huggingface.co/", "lite/humanizer-lite-Q8_0.gguf")
	want := "https://huggingface.co/jialinyyzz/humanizer/resolve/main/lite/humanizer-lite-Q8_0.gguf"
	if got != want {
		t.Fatalf("%s\n≠ %s", got, want)
	}
}

func TestConfigOverride(t *testing.T) {
	b, _ := os.ReadFile("../../config/default.json")
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"tier_by_ram":[{"min_gb":32,"tier":"q8"},{"min_gb":16,"tier":"q6"},{"min_gb":0,"tier":"q4"}],"sampling":{"temperature":0.9}}`), 0o644)
	c, err := loadConfig(b, p)
	if err != nil {
		t.Fatal(err)
	}
	if c.recommendTier(16) != "q6" {
		t.Fatal("覆盖 tier_by_ram 没生效")
	}
	if c.Sampling["temperature"] != 0.9 || c.Sampling["top_p"] != 0.95 {
		t.Fatalf("sampling 应按键合并:%v", c.Sampling)
	}
	os.WriteFile(p, []byte(`{"tier_by_ram":[{"min_gb":0,"tier":"nope"}]}`), 0o644)
	if _, err := loadConfig(b, p); err == nil {
		t.Fatal("指向不存在档位的配置应报错")
	}
}

func TestEngineOverrides(t *testing.T) {
	bs := parseEngineOverrides([]string{`cuda=C:\x\llama-server.exe;cpu=/y/llama-server`, "/z/llama-server"})
	if len(bs) != 3 || bs[0].Name != "cuda" || !bs[0].GPU || bs[1].Name != "cpu" || bs[1].GPU || bs[2].Name != "custom" {
		t.Fatalf("%+v", bs)
	}
}
