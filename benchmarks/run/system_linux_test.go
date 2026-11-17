package main

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestDRMUnitsAliasesAndClientDeduplication(t *testing.T) {
	data := "drm-driver: amdgpu\ndrm-client-id: 23\ndrm-pdev: 0000:c2:00.0\ndrm-resident-gtt: 42 KiB\ndrm-memory-gtt: 80 KiB\ndrm-memory-vram: 2 MiB\ndrm-shared-vram: 4 KiB\ndrm-total-vram: 8 KiB\ndrm-engine-gfx: 12345 ns\ndrm-total-cycles-gfx: 99999\n"
	client, ok, err := parseDRM(data)
	if err != nil || !ok || client.Resident["gtt"] != 42<<10 || client.Resident["vram"] != 2<<20 || client.Engines["gfx"] != 12345 || len(client.Requested) != 1 {
		t.Fatal(client, ok, err)
	}
	clients := make(map[string]*drmClient)
	addDRMClient(clients, client, 100)
	addDRMClient(clients, client, 100)
	addDRMClient(clients, client, 200)
	if len(clients) != 1 || !slices.Equal(clients[client.Device+"/23"].PIDs, []int{100, 200}) {
		t.Fatal("same client counted twice", clients)
	}
	client.Device = "0000:c3:00.0"
	addDRMClient(clients, client, 100)
	if len(clients) != 2 {
		t.Fatal("different devices merged")
	}
	for _, bad := range []string{"", "1 bytes", "-1 KiB", "1.5 MiB", "18446744073709551615 KiB"} {
		if _, err := parseQuantity(bad, false); err == nil {
			t.Fatal("invalid counter accepted", bad)
		}
	}
	if _, err := parseQuantity("2 ns", false); err == nil {
		t.Fatal("time counted as memory")
	}
	if _, err := parseQuantity("2 KiB", true); err == nil {
		t.Fatal("memory counted as time")
	}
	if _, ok, err := parseDRM("pos: 0\nflags: 0100002\n"); err != nil || ok {
		t.Fatal("non-DRM file interpreted as GPU")
	}
}

func TestDisplayProcessTreeAndGoHeap(t *testing.T) {
	child := exec.Command("sleep", "5")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	u, processes, err := displayUsageOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if u.rss == 0 || u.pss == 0 || !slices.Contains(processIDs(processes), child.Process.Pid) {
		t.Fatal("display descendants omitted", u, processes)
	}
	if bytes, found, err := goHeapResident(os.Getpid()); err != nil || found && bytes == 0 {
		t.Fatal("Go heap residency", bytes, found, err)
	}
	if _, err := gpuUsageOf([]int{os.Getpid()}); err != nil {
		t.Fatal("live DRM snapshot", err)
	}
}

func TestGPUCountersAndSeparateMemoryAccounting(t *testing.T) {
	a := gpuUsage{Clients: []drmClient{{ID: "1", Device: "gpu", Engines: map[string]uint64{"gfx": 100}}}}
	b := gpuUsage{Clients: []drmClient{{ID: "1", Device: "gpu", Engines: map[string]uint64{"gfx": 200}}}}
	if delta, stable := b.engineDelta(a); !stable || delta["gfx"] != 100*time.Nanosecond {
		t.Fatal(delta, stable)
	}
	b.Clients[0].ID = "2"
	if _, stable := b.engineDelta(a); stable {
		t.Fatal("client replacement counted as stable")
	}
	b.Clients[0].ID = "1"
	b.Clients[0].Engines["gfx"] = 10
	if _, stable := b.engineDelta(a); stable {
		t.Fatal("backwards engine counter accepted")
	}
	m := make(map[string]float64)
	addSystemMetrics(m, usage{rss: 10 << 20, pss: 5 << 20}, usage{rss: 20 << 20, pss: 10 << 20}, usage{rss: 18 << 20, pss: 9 << 20}, gpuUsage{MemoryReported: true, ResidentSum: map[string]uint64{"gtt": 8 << 20}}, gpuUsage{})
	if m["totalRSSMiBSum"] != 30 || m["totalPSSMiB"] != 15 || m["gpuResident_gtt_MiBSum"] != 8 || m["displayPSSChangeMiB"] != 1 {
		t.Fatal("GPU memory double-counted into system PSS", m)
	}
}
