//go:build linux

package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// detectInstall(Linux):
//   - $APPIMAGE 指向自己 → AppImage:新文件原地替换旧文件(同目录可写时);
//   - 可执行文件旁边有 engine/ → 便携版(tar.gz):解压到旁边,退出后换目录;
//   - 其他(开发时直接跑) → 下好便携版后打开给用户手动换。
func detectInstall() installInfo {
	in := installInfo{Kind: "other", Mode: "manual", Reason: "dev", Suffix: "-linux-x64-portable.tar.gz"}
	if runtime.GOARCH != "amd64" {
		in.Mode, in.Reason, in.Suffix = "none", "unsupported", ""
		return in
	}
	exe, err := os.Executable()
	if err != nil {
		return in
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	in.Exe = exe
	// $APPIMAGE 只是环境变量,终端/别的 AppImage 的子进程都会继承:
	// 必须同时证明正在跑的可执行文件就在这个 AppImage 的挂载目录($APPDIR)里,
	// 否则会把毫不相干的另一个 AppImage 当成自己替换掉。
	if img := os.Getenv("APPIMAGE"); img != "" && filepath.IsAbs(img) && os.Getenv("APPDIR") != "" {
		if resolved, err := filepath.EvalSymlinks(img); err == nil {
			img = resolved
		} else {
			img = filepath.Clean(img)
		}
		appDir := os.Getenv("APPDIR")
		if resolved, err := filepath.EvalSymlinks(appDir); err == nil {
			appDir = resolved
		}
		if fileExists(img) && strings.HasPrefix(exe, appDir+string(filepath.Separator)) {
			in.Kind, in.Path, in.Suffix = "linux_appimage", img, "-linux-x64.AppImage"
			if dirWritable(filepath.Dir(img)) {
				in.Mode, in.Reason = "replace", ""
			} else {
				in.Mode, in.Reason = "manual", "readonly"
			}
			return in
		}
	}
	dir := filepath.Dir(exe)
	// 便携版要有打包时放的标记文件:光凭旁边有个 engine/ 目录就把整个目录
	// 当成可替换的安装目录,用户把二进制随手放进大目录时会误删无关文件。
	if dirExists(filepath.Join(dir, "engine")) && fileExists(filepath.Join(dir, "install-desktop.sh")) {
		in.Kind, in.Path = "linux_portable", dir
		if dirWritable(filepath.Dir(dir)) {
			in.Mode, in.Reason = "replace", ""
		} else {
			in.Mode, in.Reason = "manual", "readonly"
		}
	}
	return in
}

func prepareApply(ctx context.Context, in installInfo, pkg string, args []string, plan *applyPlan, logf func(string, ...any)) error {
	// 包里启动器永远叫 humanizer;用户把自己这份改了名也不能拿改后的名字去包里找。
	exeName := "humanizer"
	switch in.Kind {
	case "linux_appimage":
		if in.Mode != "replace" {
			return errors.New("这份 App 不能自动更新")
		}
		staged := stagedPathFor(in)
		if err := copyPackageFile(pkg, staged); err != nil {
			_ = os.Remove(staged)
			return fmt.Errorf("复制新版本失败:%w", err)
		}
		if err := checkVersionOutput(ctx, staged, plan.To); err != nil {
			_ = os.Remove(staged)
			return err
		}
		plan.Kind, plan.Staged = "swap", staged
	case "linux_portable":
		if in.Mode != "replace" {
			return errors.New("这份 App 不能自动更新")
		}
		staged := stagedPathFor(in)
		_ = os.RemoveAll(staged)
		logf("解压新版本到 %s", staged)
		if err := extractTarGz(pkg, staged); err != nil {
			_ = os.RemoveAll(staged)
			return fmt.Errorf("解压新版本失败:%w", err)
		}
		if !fileExists(filepath.Join(staged, exeName)) ||
			(!fileExists(filepath.Join(staged, "engine", "cpu", "llama-server")) &&
				!fileExists(filepath.Join(staged, "engine", "vulkan", "llama-server"))) {
			_ = os.RemoveAll(staged)
			return errors.New("新版本的压缩包内容不完整")
		}
		if err := checkVersionOutput(ctx, filepath.Join(staged, exeName), plan.To); err != nil {
			_ = os.RemoveAll(staged)
			return err
		}
		plan.Kind, plan.Staged = "swap", staged
	default:
		return errors.New("这份 App 不能自动更新")
	}
	plan.Target, plan.Backup = in.Path, backupPathFor(in)
	if in.Kind == "linux_appimage" {
		plan.Relaunch = append([]string{in.Path}, relaunchArgs(args)...)
	} else {
		plan.Relaunch = append([]string{filepath.Join(in.Path, exeName)}, relaunchArgs(args)...)
	}
	plan.Env = []string{"HUMANIZER_UPDATED_FROM=" + plan.From, "HUMANIZER_NO_BROWSER=1"}
	return nil
}

func copyPackageFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// staged 路径是固定的同目录 <名字>.update:先删掉再 O_EXCL 新建,
	// 防同目录里有人预先放一个符号链接让复制去截断别的文件。
	_ = os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// executePlan(Linux):旧进程已退出服务,本进程直接执行换文件 + 重启计划
// (Linux 上改名运行中的可执行文件/目录是允许的,不需要 Windows 式更新助手)。
func executePlan(plan *applyPlan, logf func(string, ...any)) {
	runApplyPlan(plan, defaultApplyHooks(logf))
}

func openPackageFile(p string) error { return openURL(p) }
