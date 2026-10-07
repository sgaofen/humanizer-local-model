package launcher

import (
	"strconv"
	"strings"
)

// semver 是语义化版本号(主.次.修订[-预发布][+构建])。
// 只用于比较 App 版本:Release 标签形如 app-v0.3.2,启动器自己的版本形如 0.3.1。
type semver struct {
	Major, Minor, Patch int
	Pre                 []string // 预发布标识,比如 beta.2 → ["beta", "2"];正式版为空
}

// parseSemver 认这些写法:0.3.2、v0.3.2、app-v0.3.2、0.3(补 .0)、0.3.2-beta.1、0.3.2+build.5。
// "dev"、空串、多余的段都算解析失败。
func parseSemver(s string) (semver, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "app-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexByte(s, '+'); i >= 0 { // 构建元数据不参与比较
		s = s[:i]
	}
	var v semver
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core = s[:i]
		pre := s[i+1:]
		if pre == "" {
			return v, false
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" {
				return v, false
			}
			v.Pre = append(v.Pre, id)
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return v, false
	}
	nums := [3]int{}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, true
}

func (v semver) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// compareSemver 按 semver 2.0 规则:-1 表示 a 更旧,1 表示 a 更新。
// 正式版 > 同号的预发布版;预发布标识逐段比,数字按数值、字母按字典序、数字 < 字母、段少的更旧。
func compareSemver(a, b semver) int {
	if c := cmpInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		x, y := a.Pre[i], b.Pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if c := cmpInt(xn, yn); c != 0 {
				return c
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(a.Pre), len(b.Pre))
}

// newerVersion 报告 latest 是否比 current 新。两边任一解析不了(比如开发版 "dev")都返回 false。
func newerVersion(latest, current string) bool {
	l, ok1 := parseSemver(latest)
	c, ok2 := parseSemver(current)
	return ok1 && ok2 && compareSemver(l, c) > 0
}
