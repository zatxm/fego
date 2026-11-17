//go:build !linux

package main

import (
	"errors"
	"time"
)

type usage struct {
	cpu            time.Duration
	rss, pss, peak uint64
}

func usageOf(int) (usage, error) {
	return usage{}, errors.New("fltk-run measures Linux /proc; run it on Linux")
}
func cpuModel() string            { return "unknown" }
func loadAverage() string         { return "unknown" }
func hostInfo() map[string]string { return map[string]string{} }
