package main

import (
	"testing"
	"time"

	"github.com/zatxm/fego/ui"
)

func TestSceneFixturesAndUpdates(t *testing.T) {
	if cellValue(599, 321) != 534.32 || rowLabel(1) != "Item 000001   Native UI row   79.19" {
		t.Fatal("scene generator disagrees with FLTK fixture")
	}
	root, advance, width, height := scene("grid")
	view := ui.NewTester(root, width, height)
	defer view.Close()
	before := view.Document.Stats()
	advance(1)
	view.Step()
	if view.Document.Stats().Frames != before.Frames+1 {
		t.Fatal("grid update did not render exactly one frame")
	}
	if root.Bounds().W != 800 || root.Bounds().H != 600 {
		t.Fatal("scene dimensions changed")
	}
}

func TestRecorderCountsPresentedUpdatesOnly(t *testing.T) {
	scheduled := 0
	r := &recorder{warmup: 1, frames: 2, schedule: func(time.Duration) { scheduled++ }}
	r.frame()
	r.frame()
	if !r.ready || r.seen != 0 || scheduled != 1 {
		t.Fatal("expose counted as work")
	}
	for range 3 {
		r.armed = true
		r.frame()
	}
	if !r.done || r.seen != 3 || len(r.intervals) != 2 || scheduled != 3 {
		t.Fatalf("wrong work count: %+v, scheduled %d", r, scheduled)
	}
}
