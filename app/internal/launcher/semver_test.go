package launcher

import "testing"

func TestParseSemver(t *testing.T) {
	ok := map[string]string{
		"0.3.1":          "0.3.1",
		"v0.3.2":         "0.3.2",
		"app-v0.3.2":     "0.3.2",
		"1.2":            "1.2.0",
		"0.3.2-beta.1":   "0.3.2-beta.1",
		"0.3.2+build.7":  "0.3.2",
		" 10.20.30 ":     "10.20.30",
		"1.0.0-rc.1+abc": "1.0.0-rc.1",
	}
	for in, want := range ok {
		v, good := parseSemver(in)
		if !good || v.String() != want {
			t.Errorf("parseSemver(%q) = %v %v,期望 %s", in, v, good, want)
		}
	}
	for _, bad := range []string{"", "dev", "1", "1.2.3.4", "01.2.3", "1.x.3", "1.2.3-", "1.2.3-a..b", "-1.2.3", "v"} {
		if _, good := parseSemver(bad); good {
			t.Errorf("parseSemver(%q) 应该失败", bad)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	// 从旧到新排好的序列(semver.org 第 11 条的例子 + 本项目的版本号)
	order := []string{
		"0.1.0", "0.2.0", "0.3.0", "0.3.1", "0.3.2-alpha", "0.3.2-alpha.1", "0.3.2-alpha.beta",
		"0.3.2-beta", "0.3.2-beta.2", "0.3.2-beta.11", "0.3.2-rc.1", "0.3.2", "0.3.10", "0.10.0", "1.0.0",
	}
	for i := range order {
		for j := range order {
			a, _ := parseSemver(order[i])
			b, _ := parseSemver(order[j])
			got := compareSemver(a, b)
			want := cmpInt(i, j)
			if got != want {
				t.Errorf("compare(%s, %s) = %d,期望 %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.3.2", "0.3.1", true},
		{"app-v0.4.0", "0.3.9", true},
		{"0.3.1", "0.3.1", false},
		{"0.3.0", "0.3.1", false}, // 不降级
		{"0.3.2", "dev", false},   // 开发版不比较
		{"garbage", "0.3.1", false},
		{"0.3.2", "0.3.2-rc.1", true},
	}
	for _, c := range cases {
		if got := newerVersion(c.latest, c.current); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v,期望 %v", c.latest, c.current, got, c.want)
		}
	}
}
