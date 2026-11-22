package ui

import (
	"bytes"
	"image"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zatxm/fego/v26"
)

func TestSourceEditorMultipleCaretsAndFold(t *testing.T) {
	e := Editor("α one\nβ two\nγ three\nend")
	host := NewTester(e.Element.Fill(), 520, 340)
	defer host.Close()
	r := e.Input.Bounds()
	host.Click(r.X+60, r.Y+16)
	e.SetSelections(Selection{1, 1}, Selection{7, 7})
	host.Type("🙂")
	if got := e.Text(); got != "α🙂 one\nβ🙂 two\nγ three\nend" {
		t.Fatalf("edit = %q", got)
	}
	host.Key("Backspace", false, false)
	if got := e.Text(); got != "α one\nβ two\nγ three\nend" {
		t.Fatalf("delete = %q", got)
	}
	if !e.Fold(0, 2) || e.Input.sourceIndex().visibleCount() != 2 {
		t.Fatal("fold did not hide lines")
	}
	e.SetSelections(Selection{1, 1})
	host.Type("x")
	if e.Input.sourceIndex().visibleCount() != 2 {
		t.Fatal("same-line edit expanded an adjacent fold")
	}
	if !e.FindNext("three") || e.Input.sourceIndex().visibleCount() != 4 {
		t.Fatal("find did not reveal a folded result")
	}
	if e.ReplaceAll("three", "四") != 1 || !strings.Contains(e.Text(), "γ 四") {
		t.Fatalf("replace = %q", e.Text())
	}
	host.Key("Home", false, false)
	host.Key("End", false, false)
}

func TestSourceIndexSpliceMatchesRebuild(t *testing.T) {
	for _, old := range []string{"", "abc", "\n", "aβ\ncd\n⁕が\n", "😀α\n\n中", strings.Repeat("α\nβ\n", 9)} {
		for _, insert := range []string{"", "e", "\n", "か\n😀\n"} {
			s := CodeEditor(old).sourceIndex()
			for a := 0; a <= s.runes; a++ {
				for b := a; b <= s.runes; b++ {
					e := CodeEditor(old)
					checked := e.sourceIndex()
					start, end := checked.byteAt(old, a), checked.byteAt(old, b)
					checked.splice(old, a, b, start, end, insert, len([]rune(insert)))
					e.text = old[:start] + insert + old[end:]
					checked = e.sourceIndex()
					fresh := CodeEditor(e.text).sourceIndex()
					if checked.runes != fresh.runes || !slices.Equal(checked.lines, fresh.lines) {
						t.Fatalf("index %q %q [%d:%d] = %v, want %v", old, insert, a, b, checked.lines, fresh.lines)
					}
				}
			}
		}
	}
}

func TestMergeIndependentChangesAndConflicts(t *testing.T) {
	for _, tt := range []struct {
		base, ours, theirs, want string
	}{
		{"a\nb\nc\n", "α\nb\nc\n", "a\nb\nγ\n", "α\nb\nγ\n"},
		{"a\nb\n", "a\n", "a\nb\nc\n", "a\nc\n"},
		{"base\n", "before\nbase\n", "base\nafter\n", "before\nbase\nafter\n"},
		{"tail", "tail\n", "tail\n", "tail\n"},
	} {
		m := Merge(tt.base, tt.ours, tt.theirs)
		got, resolved := m.Text()
		if !resolved || got != tt.want {
			t.Fatalf("merge = %q (resolved %t), want %q", got, resolved, tt.want)
		}
	}
	m := Merge("a\n", "ours", "theirs")
	if m.Conflicts() != 1 {
		t.Fatalf("conflicts = %d", m.Conflicts())
	}
	if got, resolved := m.Text(); resolved || !strings.Contains(got, "ours\n=======\ntheirs\n") {
		t.Fatalf("unresolved = %q", got)
	}
	if !m.Resolve(0, KeepBoth) {
		t.Fatal("could not resolve")
	}
	if got, resolved := m.Text(); !resolved || got != "ours\ntheirs" {
		t.Fatalf("resolved = %q", got)
	}
}

func TestLongMergeKeepsIndependentEdits(t *testing.T) {
	base := make([]string, 2000)
	for i := range base {
		base[i] = strconv.Itoa(i) + "\n"
	}
	ours, theirs := slices.Clone(base), slices.Clone(base)
	ours[100], ours[1900] = "original first\n", "original last\n"
	theirs[1000] = "independent change\n"
	m := Merge(strings.Join(base, ""), strings.Join(ours, ""), strings.Join(theirs, ""))
	if m.Conflicts() != 0 {
		t.Fatalf("expected separated long edits, got %d conflicts", m.Conflicts())
	}
	ours[1000] = theirs[1000]
	if got, ok := m.Text(); !ok || got != strings.Join(ours, "") {
		t.Fatal("separated edits were lost")
	}
}

func TestVectorPathClipsToPaintBand(t *testing.T) {
	r := image.Rect(0, 0, 120, 100)
	full := image.NewRGBA(r)
	var path Path
	path.MoveTo(8, 20)
	path.CubicTo(20, 2, 125, 90, 100, 80)
	path.LineTo(22, 75)
	path.Close()
	color := RGB(64, 20, 190)
	p := Painter{dst: full, clip: r, scale: 1}
	p.FillPath(&path, color)
	if full.RGBAAt(40, 55).A == 0 {
		t.Fatal("path has no filled interior")
	}
	band := image.NewRGBA(r)
	p = Painter{dst: band, clip: image.Rect(0, 43, 120, 67), scale: 1}
	p.FillPath(&path, color)
	for y := 43; y < 67; y++ {
		a, b := full.PixOffset(0, y), full.PixOffset(0, y+1)
		// Fixed-point raster clipping can differ by one coverage level.
		for i, v := range full.Pix[a:b] {
			got := band.Pix[a+i]
			if int(v)-int(got) > 1 || int(got)-int(v) > 1 {
				t.Fatalf("paint band differs at %d,%d: %d / %d", i/4, y, got, v)
			}
		}
	}
}

func TestValueDamageMatchesFullPaint(t *testing.T) {
	slider, progress := Slider(0, 0, 100), Progress(0)
	root := Column(slider, progress).Fill()
	host := NewTester(root, 760, 150)
	defer host.Close()
	for _, value := range []float64{1, .2, 8.5, 64, 65, 100, 99, 0} {
		slider.SetValue(value)
		progress.SetValue(value / 100)
		host.Step()
		pixels := bytes.Clone(host.img.Pix)
		root.Refresh()
		host.Step()
		if !bytes.Equal(pixels, host.img.Pix) {
			t.Fatalf("partial value paint differs at %v", value)
		}
	}
}

func TestChartHoverAndKeyboardTooltip(t *testing.T) {
	selected := -1
	chart := LineChart(Series{Name: "Requests", Values: []float64{4, 8, 3}}).OnSample(func(i int) { selected = i })
	root := Column(chart.Element).Fill()
	host := NewTester(root, 460, 300)
	defer host.Close()
	before := host.Snapshot()
	x, y := chart.rect.X+chart.x(1, chart.plot), chart.rect.Y+chart.plot.Y+10
	host.Send(fego.Event{Kind: fego.PointerMove, X: x, Y: y})
	if chart.hoveredIndex != 1 || bytes.Equal(before.Pix, host.Snapshot().Pix) {
		t.Fatal("hover did not draw its sample marker and tooltip")
	}
	host.Send(fego.Event{Kind: fego.PointerMove, X: -1, Y: -1})
	if chart.hoveredIndex != -1 {
		t.Fatal("tooltip remained after leaving the chart")
	}
	root.view.focus(chart.Element)
	host.Key("End", false, false)
	if selected != 2 {
		t.Fatal("keyboard did not select the last sample")
	}
}

func TestNewChartsPaintAndSelect(t *testing.T) {
	series := []Series{{Name: "A", Values: []float64{1, 4, -2}}, {Name: "B", Values: []float64{2, 5, -3}}}
	area := AreaChart(series...)
	stack := StackedBarChart(series...)
	var selected int
	pie := PieChart([]Slice{{Label: "First", Value: 3}, {Label: "Second", Value: 1}}, func(i int) { selected = i })
	root := Column(area.Element, stack.Element, pie).Fill()
	host := NewTester(root, 600, 800)
	defer host.Close()
	if len(area.areaPaths[0].commands) == 0 || len(stack.barRects[0]) != 3 ||
		stack.low != -5 || stack.high != 9 {
		t.Fatal("chart geometry missing or wrong")
	}
	img := bytes.Clone(host.img.Pix)
	host.Send(fego.Event{Kind: fego.AccessAction})
	root.view.focus(pie)
	host.Key("End", false, false)
	if selected != 1 || bytes.Equal(host.img.Pix, img) {
		t.Fatal("keyboard selection did not repaint pie")
	}
}

type testAnimationClock struct {
	callback func()
}

func (c *testAnimationClock) After(_ time.Duration, fn func()) func() {
	if c.callback != nil {
		panic("multiple active frame timers")
	}
	c.callback = func() { c.callback = nil; fn() }
	return func() { c.callback = nil }
}

func TestKeyframesAndTimeline(t *testing.T) {
	curve := Keyframes(Keyframe{At: 1, Value: 8}, Keyframe{At: 0, Value: 2}, Keyframe{At: .5, Value: 4})
	if curve(-1) != 2 || curve(.75) != 6 || curve(1) != 8 || Spring(.4, 2)(1) != 1 {
		t.Fatal("invalid endpoints or interpolation")
	}
	c := &testAnimationClock{}
	finished := 0
	stop := Timeline(c, TimelineTrack{Duration: time.Second, Apply: func(float64) { finished++ }},
		TimelineTrack{Duration: time.Second, Apply: func(float64) { finished++ }})
	if finished != 2 || c.callback == nil {
		t.Fatal("timeline did not schedule a shared tick")
	}
	stop()
	if c.callback != nil {
		t.Fatal("cancel retained a pending tick")
	}
}
