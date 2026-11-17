//go:build !linux

package main

import "errors"

func displayUsageOf(int) (usage, []processUsage, error) {
	return usage{}, nil, errors.New("system measurement requires Linux /proc")
}
func gpuUsageOf([]int) (gpuUsage, error) {
	return gpuUsage{}, errors.New("GPU measurement requires Linux DRM fdinfo")
}
func goHeapResident(int) (uint64, bool, error) {
	return 0, false, errors.New("Go heap residency measurement requires Linux smaps")
}
