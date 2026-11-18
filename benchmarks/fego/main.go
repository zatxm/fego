// Command fego is the independent toolkit's app for benchmarks/README.md.
package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/zatxm/fego"
	"github.com/zatxm/fego/ui"
)

func setting(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 1000000 {
		log.Fatalf("invalid %s", name)
	}
	return n
}

func cellValue(cell, frame int) float64 {
	return float64((int64(cell)*7919+int64(frame)*31)%100000) / 100
}

// cellLabel formats like "%.2f" into a reused buffer, as the FLTK fixture uses
// snprintf into a stack buffer before copy_label; only the label is allocated.
func cellLabel(buf []byte, v float64) ([]byte, string) {
	buf = strconv.AppendFloat(buf[:0], v, 'f', 2, 64)
	return buf, string(buf)
}

func rowLabel(row int) string {
	return fmt.Sprintf("Item %06d   Native UI row   %.2f", row, cellValue(row, 0))
}

func cellColor(value float64) ui.Color {
	if value < 333 {
		return ui.RGB(246, 201, 196)
	}
	if value < 666 {
		return ui.RGB(244, 232, 184)
	}
	return ui.RGB(198, 233, 200)
}

type recorder struct {
	warmup, frames, seen int
	ready, armed, done   bool
	last, started, next  time.Time
	intervals            []int64
	schedule             func(time.Duration)
	stopProfile          func()
}

func (r *recorder) frame() {
	now := time.Now()
	if !r.ready {
		r.ready = true
		fmt.Println(`renderer {"backend":"cpu"}`)
		fmt.Println("ready")
		r.next = now
	} else if r.armed {
		r.armed = false
		r.seen++
		if r.seen == r.warmup {
			r.started = now
			fmt.Println("start")
		} else if r.seen > r.warmup {
			r.intervals = append(r.intervals, now.Sub(r.last).Microseconds())
			if len(r.intervals) == r.frames {
				slices.Sort(r.intervals)
				if r.stopProfile != nil {
					r.stopProfile()
				}
				fmt.Printf("done {\"frames\":%d,\"wallUs\":%d,\"p50Us\":%d,\"p99Us\":%d,\"maxUs\":%d}\n", r.frames,
					now.Sub(r.started).Microseconds(), r.intervals[r.frames/2], r.intervals[r.frames*99/100], r.intervals[r.frames-1])
				r.done = true
			}
		}
		r.last = now
	} else {
		return // Expose events do not count as workload updates.
	}
	if r.schedule != nil && !r.done {
		r.next = r.next.Add(time.Second / 60)
		r.schedule(max(0, time.Until(r.next)))
	}
}

type measuredHost struct {
	fego.Host
	presented bool
}

func (h *measuredHost) Present(img *image.RGBA, damage []image.Rectangle) {
	h.Host.Present(img, damage)
	h.presented = true
}

// Preserve the real production accessibility path through the timing adapter.
func (h *measuredHost) SetAccessibility(nodes []fego.AccessNode) {
	h.Host.(interface{ SetAccessibility([]fego.AccessNode) }).SetAccessibility(nodes)
}
func (h *measuredHost) ScrollPixels(r image.Rectangle, delta image.Point) bool {
	return h.Host.(interface {
		ScrollPixels(image.Rectangle, image.Point) bool
	}).ScrollPixels(r, delta)
}

type regionHost interface {
	RetainsPixels() bool
	PresentRegion(*image.RGBA)
}

func (h *measuredHost) RetainsPixels() bool { return h.Host.(regionHost).RetainsPixels() }
func (h *measuredHost) PresentRegion(img *image.RGBA) {
	h.Host.(regionHost).PresentRegion(img)
	h.presented = true
}

type measuredView struct {
	fego.View
	host *measuredHost
	rec  *recorder
}

func (v *measuredView) Event(event fego.Event) {
	v.host.presented = false
	v.View.Event(event)
	if v.host.presented {
		v.rec.frame()
	}
}

// Forward retained invalidations without turning them into native expose events.
func (v *measuredView) PaintIfDirty() {
	v.host.presented = false
	if view, ok := v.View.(interface{ PaintIfDirty() }); ok {
		view.PaintIfDirty()
	} else {
		v.View.Event(fego.Event{Kind: fego.Paint})
	}
	if v.host.presented {
		v.rec.frame()
	}
}

type measuredContent struct {
	doc  *ui.Document
	view fego.View
	rec  *recorder
}

func (c *measuredContent) Mount(host fego.Host) fego.View {
	h := &measuredHost{Host: host}
	c.view = c.doc.Mount(h)
	return &measuredView{View: c.view, host: h, rec: c.rec}
}

func scene(name string) (*ui.Element, func(int), int, int) {
	switch name {
	case "counter":
		n := 0
		label := ui.Text("0").FontSize(56).Bold()
		change := func(delta int) { n += delta; label.SetText(strconv.Itoa(n)) }
		root := ui.Column(label, ui.Row(
			ui.Button("−", func() { change(-1) }).Width(44),
			ui.Button("Reset", func() { n = 0; label.SetText("0") }),
			ui.Button("+", func() { change(1) }).Width(44),
		).Gap(8)).Center().Gap(16)
		return root, nil, 360, 260
	case "grid":
		cells := make([]*ui.Element, 600)
		var buf []byte
		var label string
		for i := range cells {
			v := cellValue(i, 0)
			buf, label = cellLabel(buf, v)
			cells[i] = ui.Text(label).FontSize(11).TextColor(ui.RGB(16, 16, 16)).
				Size(40, 20).Radius(0).Background(cellColor(v))
		}
		advance := func(frame int) {
			for i, cell := range cells {
				v := cellValue(i, frame)
				buf, label = cellLabel(buf, v)
				cell.SetText(label).Background(cellColor(v))
			}
		}
		return ui.Grid(20, cells...).Gap(0).Fill(), advance, 800, 600
	case "list":
		return ui.List(100000, rowLabel).Fill().FontSize(13), nil, 800, 600
	default:
		return componentScene(name)
	}
}

func main() {
	name := "counter"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	root, advance, width, height := scene(name)
	rec := &recorder{warmup: setting("BENCH_WARMUP", 120), frames: setting("BENCH_FRAMES", 600)}
	rec.intervals = make([]int64, 0, rec.frames)
	startProfile(rec)
	content := &measuredContent{doc: ui.View(root), rec: rec}
	app := fego.NewApp()
	app.WhenReady(func() {
		window, err := app.NewWindow(fego.WindowOptions{Title: "Benchmark", Width: width, Height: height, Content: content})
		if err != nil {
			log.Fatal(err)
		}
		if name != "counter" {
			frame := 0
			rec.schedule = func(delay time.Duration) {
				time.AfterFunc(delay, func() {
					window.Update(func() {
						frame++
						rec.armed = true
						if advance == nil {
							content.view.Event(fego.Event{Kind: fego.Scroll, X: 20, Y: 20, DY: 8})
						} else {
							advance(frame)
						}
					})
				})
			}
		}
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
