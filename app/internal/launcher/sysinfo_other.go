//go:build !darwin && !windows && !linux

package launcher

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func detectSys() SysInfo {
	s := SysInfo{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 && fs[0] == "MemTotal:" {
				kb, _ := strconv.ParseUint(fs[1], 10, 64)
				s.RAMBytes = kb * 1024
			}
		}
	}
	s.RAMGB = float64(s.RAMBytes) / gib
	_, err := os.Stat("/proc/driver/nvidia/version")
	s.GPU.NVIDIA = err == nil
	return s
}
