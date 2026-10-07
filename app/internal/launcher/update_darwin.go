//go:build darwin

package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// detectInstall(macOS):在 .app 里、所在目录可写 → 原地替换;
// 从 dmg 里直接运行 / 被系统「隔离运行」(App Translocation)/ 目录没权限 → 下好后打开 dmg 让用户拖。
func detectInstall() installInfo {
	in := installInfo{Kind: "other", Mode: "manual", Reason: "dev"}
	if runtime.GOARCH != "arm64" {
		in.Mode, in.Reason = "none", "unsupported"
		return in
	}
	in.Suffix = "-macos-arm64.dmg"
	exe, err := os.Executable()
	if err != nil {
		return in
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	in.Exe = exe
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return in // 开发时直接跑的二进制
	}
	in.Kind, in.Path = "mac_app", exe[:i+len(".app")]
	switch {
	case strings.Contains(in.Path, "/AppTranslocation/"):
		in.Mode, in.Reason = "manual", "translocated"
	case !dirWritable(filepath.Dir(in.Path)):
		in.Mode, in.Reason = "manual", "readonly"
		if strings.HasPrefix(in.Path, "/Volumes/") {
			in.Reason = "dmg"
		}
	default:
		in.Mode, in.Reason = "replace", ""
	}
	return in
}

// prepareApply(macOS):挂载 dmg → 核对里面的 App(标识、版本、签名、团队、能跑)→ 复制到旁边的暂存目录
// → 把旧 .app 挪成备份、新的放到原位。这一切都在服务还在跑的时候做:任何一步失败都撤回,当前 App 不受影响。
// 系统不让改(权限 / App 管理保护)时返回 fallbackError,改成打开 dmg 让用户手动拖。
func prepareApply(ctx context.Context, in installInfo, pkg string, args []string, plan *applyPlan, logf func(string, ...any)) error {
	if in.Kind != "mac_app" || in.Mode != "replace" {
		return errors.New("这份 App 不能原地更新")
	}
	staged, backup := stagedPathFor(in), backupPathFor(in)
	exeName, err := stageMacBundle(ctx, pkg, in.Path, staged, plan.To, logf)
	if err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	if err := swapInPlace(in.Path, staged, backup); err != nil {
		_ = os.RemoveAll(staged)
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return &fallbackError{err}
		}
		return fmt.Errorf("替换 App 失败(旧版本没动):%w", err)
	}
	logf("已把新版本放到 %s,旧版本暂存在 %s", in.Path, backup)
	plan.Kind, plan.Target, plan.Backup, plan.Swapped = "swap", in.Path, backup, true
	plan.Relaunch = append([]string{filepath.Join(in.Path, "Contents", "MacOS", exeName)}, relaunchArgs(args)...)
	plan.Env = []string{"HUMANIZER_UPDATED_FROM=" + plan.From, "HUMANIZER_NO_BROWSER=1"}
	return nil
}

// executePlan(macOS):旧进程已经关掉服务和引擎,就在本进程里启动新版本、确认、必要时回滚。
func executePlan(plan *applyPlan, logf func(string, ...any)) {
	runApplyPlan(plan, defaultApplyHooks(logf))
}

func stageMacBundle(ctx context.Context, dmg, cur, staged, want string, logf func(string, ...any)) (string, error) {
	mnt, err := os.MkdirTemp("", "hz-dmg-")
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-noautoopen", "-readonly", "-mountpoint", mnt, dmg).CombinedOutput()
	if err != nil {
		_ = os.Remove(mnt)
		return "", fmt.Errorf("打不开安装包:%v %s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		if err := exec.Command("hdiutil", "detach", mnt).Run(); err != nil {
			_ = exec.Command("hdiutil", "detach", "-force", mnt).Run()
		}
		_ = os.Remove(mnt)
	}()
	src := filepath.Join(mnt, filepath.Base(cur))
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		m, _ := filepath.Glob(filepath.Join(mnt, "*.app"))
		if len(m) != 1 {
			return "", errors.New("安装包里找不到 Humanizer.app")
		}
		src = m[0]
	}
	if err := checkMacBundle(ctx, src, cur, want); err != nil {
		return "", err
	}
	_ = os.RemoveAll(staged)
	if out, err := exec.CommandContext(ctx, "ditto", src, staged).CombinedOutput(); err != nil {
		return "", fmt.Errorf("复制新版本失败:%v %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", staged).Run()
	if out, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", staged).CombinedOutput(); err != nil {
		return "", fmt.Errorf("复制后的新版本签名校验没通过:%s", strings.TrimSpace(string(out)))
	}
	exe := plistValue(staged, "CFBundleExecutable")
	if exe == "" {
		exe = "Humanizer"
	}
	if err := checkVersionOutput(ctx, filepath.Join(staged, "Contents", "MacOS", exe), want); err != nil {
		return "", err
	}
	logf("新版本 %s 已复制到暂存位置并通过检查", want)
	return exe, nil
}

// checkMacBundle:标识要和当前 App 一样、版本号要对、签名完整;
// 当前 App 是开发者证书签的,新的也必须是同一个团队签的(防止被换成别人的包)。
func checkMacBundle(ctx context.Context, src, cur, want string) error {
	if curID := plistValue(cur, "CFBundleIdentifier"); curID != "" {
		if id := plistValue(src, "CFBundleIdentifier"); id != curID {
			return fmt.Errorf("安装包里的 App 标识是 %q,和当前的 %q 不一样", id, curID)
		}
	}
	if v := plistValue(src, "CFBundleShortVersionString"); v != want {
		return fmt.Errorf("安装包里的版本是 %q,应为 %s", v, want)
	}
	if out, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", src).CombinedOutput(); err != nil {
		return fmt.Errorf("安装包里的 App 签名校验没通过:%s", strings.TrimSpace(string(out)))
	}
	if team := teamID(cur); team != "" {
		if got := teamID(src); got != team {
			return fmt.Errorf("新版本的签名团队(%q)和当前的(%q)不一样", got, team)
		}
	}
	return nil
}

func plistValue(bundle, key string) string {
	out, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", filepath.Join(bundle, "Contents", "Info.plist")).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// teamID:Developer ID 签名的团队号;ad-hoc 签名返回空。
func teamID(path string) string {
	out, _ := exec.Command("codesign", "-dv", "--verbose=2", path).CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "TeamIdentifier="); ok && v != "not set" {
			return v
		}
	}
	return ""
}

// openPackageFile:双击 dmg 的效果(挂载并在 Finder 里打开)。
func openPackageFile(p string) error { return exec.Command("open", p).Start() }
