// Command studio demonstrates document editing, animation, charts and media.
package main

import (
	"flag"
	"log"
	"strconv"
	"time"

	"github.com/zatxm/fego/v26"
	"github.com/zatxm/fego/v26/ui"
)

func main() {
	videoSource := flag.String("video", "", "local video file or browser media URL")
	dark := flag.Bool("dark", false, "use the dark theme")
	flag.Parse()
	app := fego.NewApp()
	var window *fego.Window
	app.WhenReady(func() {
		var err error
		doc := ui.View(studio(&window, *videoSource))
		if *dark {
			doc.Theme(ui.Dark)
		}
		window, err = app.NewWindow(fego.WindowOptions{Title: "Fego Studio", Width: 1000, Height: 690, Content: doc})
		if err != nil {
			log.Fatal(err)
		}
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func studio(window **fego.Window, source string) *ui.Element {
	sourceSample := "# Notes\n\n- Review document changes\n- Use Alt+click for another caret\n\n```go\nfunc greet() {\n    println(\"Hello\")\n}\n```"
	editor := ui.MarkdownEditor(sourceSample, nil).Grow(1)
	merge := ui.Merge("Summary\nStatus: Draft\n", "Summary\nStatus: Reviewed\n", "Summary\nStatus: Approved\n")
	status := ui.Text("CPU").Muted()
	var cancel func()
	var progress float64
	var curve ui.Path
	curve.MoveTo(28, 132)
	curve.CubicTo(90, 12, 185, 185, 270, 52)
	canvas := ui.Canvas(320, 160, func(p *ui.Painter) {
		p.StrokePath(&curve, 2, p.Theme().Chart[0])
		p.Circle(26+float32(progress)*260, 35, 6, p.Theme().Text)
	})
	sample := ui.Text("").Muted()
	area := ui.AreaChart(ui.Series{Name: "Received", Values: []float64{4, 6, 5, 10, 7, 13}}).
		OnSample(func(i int) { sample.SetText("Sample " + strconv.Itoa(i+1)) })
	animate := ui.PrimaryButton("Animate", func() {
		if cancel != nil {
			cancel()
		}
		cancel = ui.Animate(*window, 650*time.Millisecond, ui.Spring(.55, 1.5), func(value float64) {
			progress = value
			canvas.Refresh()
		})
	})
	charts := ui.ScrollView(ui.Column(ui.Row(animate, sample).Gap(8), area.Element, canvas).Gap(8)).Grow(1)
	tabs := []ui.Tab{{Title: "Editor", Content: editor}, {Title: "Merge", Content: merge.Element}, {Title: "Drawing", Content: charts}}
	if source != "" {
		player := ui.Video(source, 560, 315)
		player.OnEnd(func(err error) {
			if err != nil {
				status.SetText(err.Error())
			} else {
				status.SetText("Video ended")
			}
		})
		tabs = append(tabs, ui.Tab{Title: "Video", Content: ui.Column(player.Element, ui.Row(
			ui.Button("Play", func() {
				if err := player.Play(*window); err != nil {
					status.SetText(err.Error())
				}
			}),
			ui.Button("Stop", player.Stop),
		).Gap(8)).Gap(8)})
	}
	gpu := ui.Switch("GPU presentation", false).OnToggle(func(enabled bool) {
		(*window).SetGPUEnabled(enabled)
		switch {
		case (*window).GPUActive():
			status.SetText("Active")
		case enabled:
			status.SetText("Unavailable · CPU fallback")
		default:
			status.SetText("CPU")
		}
	})
	return ui.Column(ui.Row(gpu, status).Gap(12), ui.Tabs(tabs...).Element.Grow(1)).Padding(18).Gap(14).Fill()
}
