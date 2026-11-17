package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type usage struct {
	cpu            time.Duration
	rss, pss, peak uint64
}

var tickRate = sync.OnceValues(func() (int64, error) {
	b, err := exec.Command("getconf", "CLK_TCK").Output()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err == nil && n <= 0 {
		err = fmt.Errorf("invalid CLK_TCK %d", n)
	}
	return n, err
})

func parseTicks(stat string) (int64, error) {
	// comm can contain spaces and parentheses; the closing delimiter is last.
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, fmt.Errorf("invalid /proc stat")
	}
	f := strings.Fields(stat[end+1:])
	if len(f) < 13 {
		return 0, fmt.Errorf("short /proc stat")
	}
	u, err := strconv.ParseInt(f[11], 10, 64)
	if err != nil {
		return 0, err
	}
	s, err := strconv.ParseInt(f[12], 10, 64)
	return u + s, err
}

func fieldKB(text, key string) (uint64, error) {
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, key+":") {
			f := strings.Fields(line)
			if len(f) != 3 || f[2] != "kB" {
				return 0, fmt.Errorf("invalid %s", key)
			}
			n, err := strconv.ParseUint(f[1], 10, 64)
			return n * 1024, err
		}
	}
	return 0, fmt.Errorf("missing %s", key)
}

func usageOf(pid int) (usage, error) {
	var u usage
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return u, err
	}
	ticks, err := parseTicks(string(stat))
	if err != nil {
		return u, err
	}
	hz, err := tickRate()
	if err != nil {
		return u, err
	}
	u.cpu = time.Duration(ticks) * time.Second / time.Duration(hz)
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return u, err
	}
	if u.peak, err = fieldKB(string(status), "VmHWM"); err != nil {
		return u, err
	}
	rollup, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err != nil {
		return u, err
	}
	if u.rss, err = fieldKB(string(rollup), "Rss"); err != nil {
		return u, err
	}
	u.pss, err = fieldKB(string(rollup), "Pss")
	return u, err
}

func cpuModel() string {
	b, _ := os.ReadFile("/proc/cpuinfo")
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			_, value, _ := strings.Cut(line, ":")
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}

func loadAverage() string {
	b, _ := os.ReadFile("/proc/loadavg")
	return strings.TrimSpace(string(b))
}

func hostInfo() map[string]string {
	b, _ := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor")
	hz, _ := tickRate()
	return map[string]string{"cpuGovernor": strings.TrimSpace(string(b)), "clockTicksPerSecond": strconv.FormatInt(hz, 10), "loadBefore": loadAverage(), "isolation": "Dedicated display-service process group; shared workstation, unrelated processes remain running."}
}
