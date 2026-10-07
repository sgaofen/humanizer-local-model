//go:build !windows

package launcher

import (
	"syscall"
	"time"
)

// waitPIDExit 等某个进程退出(Unix 上只在 Windows 式的「更新助手」流程里会用到,主要给测试)。
func waitPIDExit(pid int, timeout time.Duration) {
	if pid <= 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
