//go:build !windows

package launcher

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// pickDir 弹系统的「选文件夹」窗口。取消或没有可用工具时返回空字符串。
// 只在本机弹窗,不联网;标题和起始目录通过参数/env 传,不拼进脚本字符串。
func pickDir(ctx context.Context, from, title string) (string, error) {
	if from == "" {
		from = "."
	}
	if p, err := osascriptPick(ctx, from, title); err == nil {
		return p, nil
	}
	for _, c := range [][]string{
		{"zenity", "--file-selection", "--directory", "--title=" + title},
		{"kdialog", "--title=" + title, "--getexistingdirectory", from},
	} {
		if _, lookErr := exec.LookPath(c[0]); lookErr != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, c[0], c[1:]...).Output()
		if err != nil && len(out) == 0 {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue // 用户取消了(退出码非 0,没输出)
		}
		return absDir(strings.TrimSpace(string(out))), nil
	}
	return "", nil
}

// osascriptPick macOS 的原生选择器(可以 ⇧⌘G 直接输入路径)。
func osascriptPick(ctx context.Context, from, title string) (string, error) {
	if _, err := exec.LookPath("osascript"); err != nil {
		return "", err
	}
	script := `on run argv
	set p to (choose folder with prompt (item 1 of argv) default location (POSIX file (item 2 of argv)))
	return POSIX path of p
end run`
	out, err := exec.CommandContext(ctx, "osascript", "-e", script, title, from).Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ctx.Err()
		}
		return "", err // 取消也走这里,调用方当成"没选"
	}
	return absDir(strings.TrimSpace(string(out))), nil
}
