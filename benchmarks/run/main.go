// Command run measures independent fego and FLTK binaries on one display.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
)

type series struct {
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

func summarize(values []float64) series {
	v := slices.Clone(values)
	slices.Sort(v)
	m := v[len(v)/2]
	if len(v)%2 == 0 {
		m = (v[len(v)/2-1] + m) / 2
	}
	return series{m, v[0], v[len(v)-1]}
}

type sample struct {
	Round     int                `json:"round"`
	Framework string             `json:"framework"`
	Scene     string             `json:"scene"`
	Metrics   map[string]float64 `json:"metrics"`
	Renderer  map[string]string  `json:"renderer,omitempty"`
	Details   *sampleDetails     `json:"details,omitempty"`
}

type binaryInfo struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type result struct {
	DateUTC     string                                  `json:"dateUTC"`
	GoVersion   string                                  `json:"goVersion"`
	OS          string                                  `json:"os"`
	Arch        string                                  `json:"arch"`
	CPU         string                                  `json:"cpu"`
	CPUs        int                                     `json:"cpus"`
	Display     string                                  `json:"display"`
	ServerPID   int                                     `json:"serverPID"`
	Frames      int                                     `json:"frames"`
	Warmup      int                                     `json:"warmup"`
	IdleSeconds float64                                 `json:"idleSeconds"`
	Environment map[string]string                       `json:"environment"`
	Host        map[string]string                       `json:"host"`
	Versions    map[string]string                       `json:"versions"`
	Binaries    map[string]binaryInfo                   `json:"binaries"`
	Samples     []sample                                `json:"samples"`
	Summary     map[string]map[string]map[string]series `json:"summary"`
	Measurement string                                  `json:"measurement"`
}

func main() {
	paths := map[string]*string{}
	for _, name := range []string{"fego", "fltk"} {
		paths[name] = flag.String(name+"-bin", "", "path to the "+name+" benchmark binary")
	}
	before := flag.String("before-bin", "", "optional previous fego binary for a same-host comparison")
	runs := flag.Int("runs", 5, "rounds, with rotating framework order")
	frames := flag.Int("frames", 600, "measured frames")
	warmup := flag.Int("warmup", 120, "unmeasured initial frames")
	idle := flag.Duration("idle", 5*time.Second, "counter settling time and subsequent idle measurement interval")
	server := flag.Int("server-pid", 0, "dedicated display-service root PID; includes compositor and descendant processes")
	out := flag.String("out", "", "JSON output file (default stdout)")
	scenesFlag := flag.String("scenes", "counter,list,grid", "comma-separated scenes")
	flag.Parse()
	log.SetFlags(0)
	if *runs < 1 || *frames < 1 || *frames > 1000000 || *warmup < 1 || *warmup > 1000000 || *idle <= 0 || *server <= 0 {
		log.Fatal("runs, frames, warmup, idle and server-pid must be positive; frame limits are 1000000")
	}
	scenes := strings.Split(*scenesFlag, ",")
	for _, scene := range scenes {
		if !slices.Contains([]string{"counter", "list", "grid", "table", "tree", "form", "editor", "linechart", "linechart-sparse", "barchart", "calendar", "image", "navigation", "richtext"}, scene) {
			log.Fatalf("unknown scene %q", scene)
		}
	}
	res := result{
		DateUTC: time.Now().UTC().Format(time.RFC3339), GoVersion: runtime.Version(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, CPU: cpuModel(), CPUs: runtime.NumCPU(),
		Display: os.Getenv("DISPLAY"), ServerPID: *server, Frames: *frames,
		Warmup: *warmup, IdleSeconds: idle.Seconds(), Binaries: map[string]binaryInfo{},
		Environment: map[string]string{}, Versions: map[string]string{"go": runtime.Version()}, Summary: map[string]map[string]map[string]series{},
		Host:        hostInfo(),
		Measurement: "Linux /proc process CPU (all threads), RSS/PSS and VmHWM. Display CPU/memory includes the specified root and all descendant processes (e.g. Weston, Xwayland, desktop shell). Total CPU and PSS sum application and this display group; RSS sum can count shared mappings multiple times. DRM fdinfo is deduplicated by device/client-id across file descriptors and PIDs. Resident GPU buffer sums may still count buffers shared between clients, and may overlap CPU PSS; GPU regions are reported separately, never added to PSS as unique physical memory. Device-global VRAM/GTT counters also include unrelated clients. Shared DBus/accessibility services are outside the process group. Frames are native submissions, not display completion/input latency. No 5% pass is inferred from timer-paced FPS.",
	}
	for _, key := range []string{"FLTK_BACKEND", "FLTK_SCALING_FACTOR", "GDK_SCALE", "LIBGL_ALWAYS_SOFTWARE", "GALLIUM_DRIVER", "GOMAXPROCS", "GOGC", "GOMEMLIMIT", "GODEBUG", "FEGO_RENDERER", "MESA_LOADER_DRIVER_OVERRIDE", "MESA_SHADER_CACHE_DISABLE", "DRI_PRIME", "EGL_PLATFORM", "LANG", "LC_ALL", "DISPLAY", "XMODIFIERS"} {
		res.Environment[key] = os.Getenv(key)
	}
	names := []string{"fego"}
	if *paths["fltk"] != "" {
		names = append(names, "fltk")
	}
	if *before != "" {
		names = append(names, "before")
		paths["before"] = before
	}
	for _, name := range names {
		if *paths[name] == "" {
			log.Fatalf("-%s-bin is required", name)
		}
		info, err := identify(*paths[name])
		if err != nil {
			log.Fatal(err)
		}
		res.Binaries[name] = info
	}
	if *paths["fltk"] != "" {
		version, err := exec.Command(*paths["fltk"], "--version").Output()
		if err != nil {
			log.Fatalf("FLTK version: %v", err)
		}
		res.Versions["fltk"] = strings.TrimSpace(string(version))
	}
	if _, _, err := displayUsageOf(*server); err != nil {
		log.Fatalf("display server: %v", err)
	}
	// Prime the executable and font caches equally; omit these startup runs.
	for _, name := range names {
		p, err := start(*paths[name], "counter", *warmup, *frames)
		if err == nil {
			_, err = p.wait("ready")
			p.stop()
		}
		if err != nil {
			log.Fatalf("warm %s: %v", name, err)
		}
	}
	for round := range *runs {
		for _, scene := range scenes {
			for i := range names {
				name := names[(i+round)%len(names)]
				s, err := measure(*paths[name], scene, *warmup, *frames, *idle, *server)
				if err != nil {
					log.Fatalf("round %d %s %s: %v", round+1, name, scene, err)
				}
				s.Round, s.Framework, s.Scene = round+1, name, scene
				res.Samples = append(res.Samples, s)
				log.Printf("round %d %s %s: %v", round+1, name, scene, s.Metrics)
			}
		}
	}
	for _, name := range names {
		res.Summary[name] = map[string]map[string]series{}
		for _, scene := range scenes {
			values := map[string][]float64{}
			for _, s := range res.Samples {
				if s.Framework == name && s.Scene == scene {
					for key, value := range s.Metrics {
						values[key] = append(values[key], value)
					}
				}
			}
			res.Summary[name][scene] = map[string]series{}
			for key, values := range values {
				res.Summary[name][scene][key] = summarize(values)
			}
		}
	}
	res.Host["loadAfter"] = loadAverage()
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if *out == "" {
		fmt.Println(string(b))
	} else if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
}

func identify(path string) (binaryInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return binaryInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return binaryInfo{path, hex.EncodeToString(h.Sum(nil)), n}, err
}

type line struct {
	text string
	at   time.Time
}

type process struct {
	cmd      *exec.Cmd
	lines    chan line
	started  time.Time
	pending  map[string]line
	renderer map[string]string
}

func start(path, scene string, warmup, frames int) (*process, error) {
	cmd := exec.Command(path, scene)
	cmd.Env = append(os.Environ(), fmt.Sprintf("BENCH_WARMUP=%d", warmup), fmt.Sprintf("BENCH_FRAMES=%d", frames))
	cmd.Stderr = os.Stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &process{cmd: cmd, lines: make(chan line, 16), started: time.Now(), pending: make(map[string]line)}
	if err := cmd.Start(); err != nil {
		pipe.Close()
		return nil, err
	}
	go func() {
		defer close(p.lines)
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			p.lines <- line{scanner.Text(), time.Now()}
		}
	}()
	return p, nil
}

func (p *process) stop() {
	p.cmd.Process.Kill()
	p.cmd.Wait()
	time.Sleep(100 * time.Millisecond) // Let the display service retire the closed window.
}

func (p *process) wait(prefix string) (line, error) {
	if l, ok := p.pending[prefix]; ok {
		delete(p.pending, prefix)
		return l, nil
	}
	guard := time.NewTimer(3 * time.Minute)
	defer guard.Stop()
	for {
		select {
		case l, ok := <-p.lines:
			if !ok {
				return line{}, fmt.Errorf("app exited before %q", prefix)
			}
			if strings.HasPrefix(l.text, "renderer ") {
				var info map[string]string
				if err := json.Unmarshal([]byte(strings.TrimPrefix(l.text, "renderer ")), &info); err != nil {
					return line{}, fmt.Errorf("renderer marker: %w", err)
				}
				p.renderer = info
				continue
			}
			if l.text == prefix || strings.HasPrefix(l.text, prefix+" ") {
				return l, nil
			}
			kind, _, _ := strings.Cut(l.text, " ")
			if kind == "ready" || kind == "start" || kind == "done" {
				p.pending[kind] = l
			}
		case <-guard.C:
			return line{}, fmt.Errorf("app did not report %q", prefix)
		}
	}
}

type frameResult struct {
	Frames int     `json:"frames"`
	WallUS float64 `json:"wallUs"`
	P50US  float64 `json:"p50Us"`
	P99US  float64 `json:"p99Us"`
	MaxUS  float64 `json:"maxUs"`
}

func decodeFrame(text string, frames int) (frameResult, error) {
	var f frameResult
	if err := json.Unmarshal([]byte(strings.TrimPrefix(text, "done ")), &f); err != nil {
		return f, err
	}
	if f.Frames != frames || f.WallUS <= 0 || f.P50US <= 0 || f.P99US < f.P50US || f.MaxUS < f.P99US {
		return f, errors.New("invalid frame count or timing in done marker")
	}
	return f, nil
}

func measure(path, scene string, warmup, frames int, idle time.Duration, server int) (sample, error) {
	record := sample{Metrics: make(map[string]float64), Details: &sampleDetails{}}
	m, details := record.Metrics, record.Details
	baseline, processes, err := displayUsageOf(server)
	if err != nil {
		return record, err
	}
	details.DisplayBefore = processes
	details.GPUBefore, err = gpuUsageOf(processIDs(processes))
	if err != nil {
		return record, err
	}
	p, err := start(path, scene, warmup, frames)
	if err != nil {
		return record, err
	}
	defer p.stop()
	ready, err := p.wait("ready")
	if err != nil {
		return record, err
	}
	m["startupMs"] = float64(ready.at.Sub(p.started)) / float64(time.Millisecond)
	if scene == "counter" {
		time.Sleep(idle)
	} else if _, err := p.wait("start"); err != nil {
		return record, err
	}
	a, err := usageOf(p.cmd.Process.Pid)
	if err != nil {
		return record, err
	}
	xa, processes, err := displayUsageOf(server)
	if err != nil {
		return record, err
	}
	details.DisplayStart = processes
	details.GPUStart, err = gpuUsageOf(append(processIDs(processes), p.cmd.Process.Pid))
	if err != nil {
		return record, err
	}
	started := time.Now()
	if scene == "counter" {
		time.Sleep(idle)
	} else {
		l, err := p.wait("done")
		if err != nil {
			return record, err
		}
		f, err := decodeFrame(l.text, frames)
		if err != nil {
			return record, err
		}
		m["fps"] = float64(frames) * 1e6 / f.WallUS
		m["frameP50Ms"], m["frameP99Ms"], m["frameMaxMs"] = f.P50US/1000, f.P99US/1000, f.MaxUS/1000
		// Let the last asynchronous display submission reach the dedicated compositor.
		time.Sleep(50 * time.Millisecond)
	}
	elapsed := time.Since(started)
	b, err := usageOf(p.cmd.Process.Pid)
	if err != nil {
		return record, err
	}
	xb, processes, err := displayUsageOf(server)
	if err != nil {
		return record, err
	}
	details.DisplayAfter = processes
	if !slices.Equal(processIDs(details.DisplayStart), processIDs(processes)) {
		return record, errors.New("display-service processes changed during measurement")
	}
	details.GPUAfter, err = gpuUsageOf(append(processIDs(processes), p.cmd.Process.Pid))
	if err != nil {
		return record, err
	}
	record.Renderer = p.renderer
	heap, found, err := goHeapResident(p.cmd.Process.Pid)
	if err != nil {
		return record, err
	}
	if found {
		m["goHeapResidentMiB"] = float64(heap) / (1 << 20)
	}
	m["rssMiB"], m["pssMiB"], m["peakRSSMiB"] = float64(b.rss)/(1<<20), float64(b.pss)/(1<<20), float64(b.peak)/(1<<20)
	m["serverBaselineRSSMiB"], m["serverBaselinePSSMiB"] = float64(baseline.rss)/(1<<20), float64(baseline.pss)/(1<<20)
	m["serverRSSMiB"], m["serverPSSMiB"] = float64(xb.rss)/(1<<20), float64(xb.pss)/(1<<20)
	addSystemMetrics(m, b, xb, baseline, details.GPUAfter, details.GPUBefore)
	engines, stable := details.GPUAfter.engineDelta(details.GPUStart)
	details.GPUCountersStable = stable
	if stable {
		for engine, duration := range engines {
			if scene == "counter" {
				m["gpuEngine_"+engine+"_Percent"] = 100 * float64(duration) / float64(elapsed)
			} else {
				m["gpuEngine_"+engine+"_UsPerFrame"] = float64(duration) / float64(time.Microsecond) / float64(frames)
			}
		}
	}
	appCPU, serverCPU := b.cpu-a.cpu, xb.cpu-xa.cpu
	if appCPU < 0 || serverCPU < 0 {
		return record, errors.New("CPU counters moved backwards")
	}
	if scene == "counter" {
		m["appCPUPercent"] = 100 * float64(appCPU) / float64(elapsed)
		m["serverCPUPercent"] = 100 * float64(serverCPU) / float64(elapsed)
		m["totalCPUPercent"] = m["appCPUPercent"] + m["serverCPUPercent"]
	} else {
		m["appCPUUsPerFrame"] = float64(appCPU) / float64(time.Microsecond) / float64(frames)
		m["serverCPUUsPerFrame"] = float64(serverCPU) / float64(time.Microsecond) / float64(frames)
		m["totalCPUUsPerFrame"] = m["appCPUUsPerFrame"] + m["serverCPUUsPerFrame"]
	}
	return record, nil
}
