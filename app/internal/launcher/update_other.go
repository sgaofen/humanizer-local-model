//go:build !darwin && !windows

package launcher

import (
	"context"
	"errors"
)

// 其他系统(Linux)没有发布安装包:只提示有新版本,链接到 Releases 页面。
func detectInstall() installInfo {
	return installInfo{Kind: "other", Mode: "none", Reason: "unsupported"}
}

func prepareApply(ctx context.Context, in installInfo, pkg string, args []string, plan *applyPlan, logf func(string, ...any)) error {
	return errors.New("这个平台不支持自动更新")
}

func executePlan(plan *applyPlan, logf func(string, ...any)) {
	runApplyPlan(plan, defaultApplyHooks(logf))
}

func openPackageFile(p string) error { return openURL(p) }
