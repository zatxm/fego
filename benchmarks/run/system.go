package main

import "time"

type processUsage struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	CPUNS int64  `json:"cpuNS"`
	RSS   uint64 `json:"rssBytes"`
	PSS   uint64 `json:"pssBytes"`
}

type drmClient struct {
	ID        string            `json:"id"`
	Device    string            `json:"device"`
	Driver    string            `json:"driver"`
	PIDs      []int             `json:"pids"`
	Resident  map[string]uint64 `json:"residentBytes"`
	Shared    map[string]uint64 `json:"sharedRequestedBytes"`
	Requested map[string]uint64 `json:"requestedBytes"`
	Engines   map[string]uint64 `json:"engineNS"`
}

type gpuUsage struct {
	Clients        []drmClient                  `json:"clients"`
	ResidentSum    map[string]uint64            `json:"residentBytesSum"`
	ResidentMax    map[string]uint64            `json:"residentBytesLargestClient"`
	DeviceUsed     map[string]map[string]uint64 `json:"deviceGlobalUsedBytes,omitempty"`
	MemoryReported bool                         `json:"memoryReported"`
}

func (g gpuUsage) engineDelta(start gpuUsage) (map[string]time.Duration, bool) {
	previous := make(map[string]drmClient)
	for _, client := range start.Clients {
		previous[client.Device+"/"+client.ID] = client
	}
	delta := make(map[string]time.Duration)
	if len(g.Clients) != len(start.Clients) {
		return nil, false
	}
	for _, client := range g.Clients {
		before, ok := previous[client.Device+"/"+client.ID]
		if !ok {
			return nil, false
		}
		for engine, value := range client.Engines {
			old, ok := before.Engines[engine]
			if !ok || value < old || value-old > uint64(1<<63-1) {
				return nil, false
			}
			delta[engine] += time.Duration(value - old)
		}
	}
	return delta, true
}

type sampleDetails struct {
	DisplayBefore     []processUsage `json:"displayBefore"`
	DisplayStart      []processUsage `json:"displayStart"`
	DisplayAfter      []processUsage `json:"displayAfter"`
	GPUBefore         gpuUsage       `json:"gpuBefore"`
	GPUStart          gpuUsage       `json:"gpuStart"`
	GPUAfter          gpuUsage       `json:"gpuAfter"`
	GPUCountersStable bool           `json:"gpuCountersStable"`
}

func processIDs(processes []processUsage) []int {
	pids := make([]int, 0, len(processes))
	for _, process := range processes {
		pids = append(pids, process.PID)
	}
	return pids
}

func addSystemMetrics(m map[string]float64, app, display, baseline usage, current, before gpuUsage) {
	m["totalRSSMiBSum"] = float64(app.rss+display.rss) / (1 << 20)
	m["totalPSSMiB"] = float64(app.pss+display.pss) / (1 << 20)
	m["displayRSSChangeMiB"] = (float64(display.rss) - float64(baseline.rss)) / (1 << 20)
	m["displayPSSChangeMiB"] = (float64(display.pss) - float64(baseline.pss)) / (1 << 20)
	// DRM descriptors are deduplicated by client; buffer objects shared across
	// clients can still overlap, so this sum is not added to RSS or PSS.
	if current.MemoryReported {
		m["gpuMemoryReported"] = 1
		for region, value := range current.ResidentSum {
			m["gpuResident_"+region+"_MiBSum"] = float64(value) / (1 << 20)
			m["gpuBaselineResident_"+region+"_MiBSum"] = float64(before.ResidentSum[region]) / (1 << 20)
		}
	}
}
