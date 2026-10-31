//go:build linux

package launcher

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
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
				break
			}
		}
	}
	s.RAMGB = float64(s.RAMBytes) / gib
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "model name"); ok {
				if _, name, found := strings.Cut(v, ":"); found {
					s.CPU = strings.TrimSpace(name)
					break
				}
			}
		}
	}
	_, err := os.Stat("/proc/driver/nvidia/version")
	s.GPU.NVIDIA = err == nil
	// Vulkan:任一标准 ICD 目录里有驱动描述文件,就认为 Vulkan 运行时可用。
	for _, dir := range []string{"/usr/share/vulkan/icd.d", "/etc/vulkan/icd.d",
		filepath.Join(os.Getenv("HOME"), ".local/share/vulkan/icd.d")} {
		if m, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(m) > 0 {
			s.GPU.Vulkan = true
			break
		}
	}
	// 只在确实有 NVIDIA 驱动时才跑 nvidia-smi,且最多等 2 秒:
	// detectSys 在启动路径上,坏掉的驱动/同名脚本不能把启动卡住。
	if s.GPU.NVIDIA {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					s.GPU.Names = append(s.GPU.Names, line)
				}
			}
		}
	}
	// GPU 名字只从 nvidia-smi 拿(和 Windows 一致):lspci 在有些机器上要跑两秒,
	// detectSys 在启动路径上,不能被它拖慢(端到端测试和残留引擎清理都等它)。
	return s
}
