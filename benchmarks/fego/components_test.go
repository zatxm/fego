package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/zatxm/fego"
	"github.com/zatxm/fego/ui"
)

func TestComponentScenesRender(t *testing.T) {
	for _, name := range componentScenes {
		t.Run(name, func(t *testing.T) {
			root, update, w, h := scene(name)
			view := ui.NewTester(root, w, h)
			defer view.Close()
			before := view.Document.Stats().Frames
			if update == nil {
				view.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 20, DY: 8})
			} else {
				update(1)
				view.Step()
			}
			if view.Document.Stats().Frames <= before {
				t.Fatal("component update did not paint")
			}
			if dir := os.Getenv("FEGO_COMPONENT_SNAPSHOTS"); dir != "" {
				f, err := os.Create(filepath.Join(dir, name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := errors.Join(png.Encode(f, view.Snapshot()), f.Close()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// Core layout/paint/allocation measurements; native submission and AT-SPI are
// measured separately by benchmarks/run with these same scene fixtures.
func BenchmarkComponents(b *testing.B) {
	for _, name := range componentScenes {
		b.Run(name, func(b *testing.B) {
			root, update, w, h := scene(name)
			view := ui.NewTester(root, w, h)
			defer view.Close()
			frame := 0
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				frame++
				if update == nil {
					view.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 20, DY: 8})
				} else {
					update(frame)
					view.Step()
				}
				if update == nil && frame%20 == 0 {
					view.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 20, DY: -160})
				}
			}
		})
	}
}
