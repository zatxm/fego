package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func displayUsageOf(root int) (usage, []processUsage, error) {
	var total usage
	var processes []processUsage
	seen := make(map[int]bool)
	var visit func(int) error
	visit = func(pid int) error {
		if seen[pid] {
			return nil
		}
		seen[pid] = true
		u, err := usageOf(pid)
		if err != nil {
			return err
		}
		name, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		if err != nil {
			return err
		}
		processes = append(processes, processUsage{pid, strings.TrimSpace(string(name)), int64(u.cpu), u.rss, u.pss})
		total.cpu += u.cpu
		total.rss += u.rss
		total.pss += u.pss
		total.peak += u.peak
		tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		if err != nil {
			return err
		}
		for _, task := range tasks {
			data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, task.Name()))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			for _, text := range strings.Fields(string(data)) {
				child, err := strconv.Atoi(text)
				if err != nil {
					return err
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := visit(root)
	slices.SortFunc(processes, func(a, b processUsage) int { return a.PID - b.PID })
	return total, processes, err
}

func parseQuantity(value string, nanoseconds bool) (uint64, error) {
	fields := strings.Fields(value)
	if len(fields) < 1 || len(fields) > 2 {
		return 0, fmt.Errorf("invalid DRM quantity %q", value)
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	scale := uint64(1)
	if len(fields) == 2 {
		switch fields[1] {
		case "KiB":
			if nanoseconds {
				return 0, errors.New("memory units on engine counter")
			}
			scale = 1 << 10
		case "MiB":
			if nanoseconds {
				return 0, errors.New("memory units on engine counter")
			}
			scale = 1 << 20
		case "ns":
			if !nanoseconds {
				return 0, errors.New("time units on memory counter")
			}
		default:
			return 0, fmt.Errorf("unknown DRM unit %q", fields[1])
		}
	}
	if n > math.MaxUint64/scale {
		return 0, errors.New("DRM counter overflows bytes")
	}
	return n * scale, nil
}

func parseDRM(data string) (drmClient, bool, error) {
	client := drmClient{Resident: make(map[string]uint64), Shared: make(map[string]uint64), Requested: make(map[string]uint64), Engines: make(map[string]uint64)}
	legacy := make(map[string]uint64)
	for line := range strings.SplitSeq(data, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(key, "drm-total-cycles-") {
			continue
		}
		switch key {
		case "drm-client-id":
			client.ID = value
		case "drm-pdev":
			client.Device = value
		case "drm-driver":
			client.Driver = value
		default:
			var target map[string]uint64
			var prefix string
			engine := false
			for _, kind := range []struct {
				prefix string
				target map[string]uint64
			}{
				{"drm-resident-", client.Resident}, {"drm-shared-", client.Shared}, {"drm-total-", client.Requested}, {"drm-memory-", legacy}, {"drm-engine-", client.Engines},
			} {
				if strings.HasPrefix(key, kind.prefix) {
					target, prefix = kind.target, kind.prefix
					engine = prefix == "drm-engine-"
					break
				}
			}
			if target != nil {
				n, err := parseQuantity(value, engine)
				if err != nil {
					return client, false, fmt.Errorf("%s: %w", key, err)
				}
				target[strings.TrimPrefix(key, prefix)] = n
			}
		}
	}
	if client.ID == "" {
		return client, false, nil
	}
	for region, value := range legacy {
		if _, exists := client.Resident[region]; !exists {
			client.Resident[region] = value
		}
	}
	return client, true, nil
}

func gpuUsageOf(pids []int) (gpuUsage, error) {
	result := gpuUsage{ResidentSum: make(map[string]uint64), ResidentMax: make(map[string]uint64), DeviceUsed: make(map[string]map[string]uint64)}
	clients := make(map[string]*drmClient)
	for _, pid := range pids {
		path := fmt.Sprintf("/proc/%d/fdinfo", pid)
		files, err := os.ReadDir(path)
		if err != nil {
			return result, err
		}
		for _, file := range files {
			data, err := os.ReadFile(filepath.Join(path, file.Name()))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return result, err
			}
			client, ok, err := parseDRM(string(data))
			if err != nil {
				return result, err
			}
			if !ok {
				continue
			}
			if client.Device == "" {
				client.Device, err = os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, file.Name()))
				if err != nil {
					return result, err
				}
			}
			addDRMClient(clients, client, pid)
		}
	}
	keys := make([]string, 0, len(clients))
	for key := range clients {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result.MemoryReported = len(keys) != 0
	for _, key := range keys {
		client := clients[key]
		result.Clients = append(result.Clients, *client)
		if len(client.Resident) == 0 {
			result.MemoryReported = false
		}
		for region, value := range client.Resident {
			result.ResidentSum[region] += value
			result.ResidentMax[region] = max(result.ResidentMax[region], value)
		}
		if filepath.Base(client.Device) != client.Device {
			continue
		}
		if _, exists := result.DeviceUsed[client.Device]; exists {
			continue
		}
		used := make(map[string]uint64)
		for _, region := range []string{"vram", "gtt"} {
			data, err := os.ReadFile(filepath.Join("/sys/bus/pci/devices", client.Device, "mem_info_"+region+"_used"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return result, err
			}
			value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
			if err != nil {
				return result, err
			}
			used[region] = value
		}
		result.DeviceUsed[client.Device] = used
	}
	return result, nil
}

func addDRMClient(clients map[string]*drmClient, client drmClient, pid int) {
	key := client.Device + "/" + client.ID
	if previous := clients[key]; previous != nil {
		if !slices.Contains(previous.PIDs, pid) {
			previous.PIDs = append(previous.PIDs, pid)
		}
	} else {
		client.PIDs = []int{pid}
		clients[key] = &client
	}
}

func goHeapResident(pid int) (uint64, bool, error) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/smaps", pid))
	if err != nil {
		return 0, false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	inHeap, found := false, false
	var total uint64
	for scanner.Scan() {
		line := scanner.Text()
		if len(strings.Fields(line)) >= 5 && strings.Contains(strings.Fields(line)[0], "-") {
			inHeap = strings.Contains(line, "[anon: Go: heap]")
			found = found || inHeap
		} else if inHeap && strings.HasPrefix(line, "Rss:") {
			value, err := fieldKB(line, "Rss")
			if err != nil {
				return 0, false, err
			}
			total += value
		}
	}
	return total, found, scanner.Err()
}
