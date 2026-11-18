package ui

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zatxm/fego"
)

func TestTabsSegmentsAndAccordion(t *testing.T) {
	first := TextInput("kept")
	second := Text("Second page")
	tabs := Tabs(Tab{"First", first}, Tab{"Second", second})
	changes := 0
	tabs.OnSelect(func(index int) {
		changes++
		if index != 1 {
			t.Fatalf("callback index %d", index)
		}
	})
	view := NewTester(tabs.Element, 480, 200)
	defer view.Close()
	if first.hidden || !second.hidden || tabs.Selected() != 0 {
		t.Fatal("initial tab visibility")
	}
	clickElement(view, tabs.buttons[1])
	if !first.hidden || second.hidden || tabs.Selected() != 1 || changes != 1 {
		t.Fatal("tab click did not switch pages")
	}
	tabs.SetSelected(0)
	view.Step()
	if first.Text() != "kept" || first.hidden || !second.hidden || changes != 1 {
		t.Fatal("programmatic tab switch lost state or emitted a click")
	}

	segments := Segmented([]string{"Day", "Week", "Month"}, 0)
	selection := -1
	segments.OnSelect(func(index int) { selection = index })
	segView := NewTester(segments.Element, 400, 60)
	defer segView.Close()
	clickElement(segView, segments.buttons[2])
	if selection != 2 || segments.Selected() != 2 || segments.buttons[0].primary || !segments.buttons[2].primary {
		t.Fatal("segments are not mutually exclusive")
	}
	segments.SetSelected(-1)
	for _, button := range segments.buttons {
		if button.primary {
			t.Fatal("segment remained selected")
		}
	}

	content := TextInput("retained")
	accordion := Accordion(Section{Title: "Details", Content: content})
	accView := NewTester(accordion, 400, 160)
	defer accView.Close()
	header := accordion.children[0].children[0]
	if !content.hidden {
		t.Fatal("closed section visible")
	}
	clickElement(accView, header)
	if content.hidden {
		t.Fatal("section did not expand")
	}
	clickElement(accView, header)
	if !content.hidden || content.Text() != "retained" {
		t.Fatal("collapse lost content")
	}
}

func TestMenuBreadcrumbsAndDialog(t *testing.T) {
	chosen := 0
	menu := Dropdown("Actions", MenuItem{Label: "Blocked", Action: func() { chosen += 100 }, Disabled: true}, MenuItem{Label: "Run", Action: func() { chosen++ }})
	crumbs := Breadcrumbs(Breadcrumb{"Home", func() { chosen += 10 }}, Breadcrumb{Label: "Current"})
	input := TextInput("")
	field := Field("Name", input, "Shown on your profile")
	view := NewTester(Column(menu, crumbs, Form(field)), 500, 320)
	defer view.Close()
	if input.label != "Name" || field.children[0].Text() != "Name" {
		t.Fatal("field label missing")
	}
	clickElement(view, menu)
	view.Key("Enter", false, false)
	if chosen != 0 || view.Document.live.popup != nil {
		t.Fatal("disabled menu action ran")
	}
	clickElement(view, menu)
	view.Key("ArrowDown", false, false)
	view.Key("Enter", false, false)
	if chosen != 1 {
		t.Fatal("menu action did not run")
	}
	clickElement(view, crumbs.children[0])
	if chosen != 11 {
		t.Fatal("breadcrumb did not navigate")
	}
	close := Button("Close", view.Document.CloseDialog)
	view.Document.ShowDialog(Dialog("Confirm", Text("Modal body"), close))
	view.Step()
	clickElement(view, close)
	if view.Document.live.dialog != nil {
		t.Fatal("dialog action did not close modal")
	}
}

func TestMarkdownSpansMatchInlineExpression(t *testing.T) {
	inline := regexp.MustCompile("\\*\\*([^*\n]+)\\*\\*|`([^`\n]+)`|\\[([^\\]\n]+)\\]\\(([^)\n]*)\\)")
	pieces := []string{"*", "**", "`", "[", "]", "(", ")", "](", "\n", "a", "中", "x y", "https://e.com", "\xff"}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 50000 {
		var b strings.Builder
		for range rng.IntN(16) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		text := b.String()
		var want, got []string
		for _, m := range inline.FindAllStringSubmatchIndex(text, -1) {
			body := m[2:4]
			if m[4] >= 0 {
				body = m[4:6]
			} else if m[6] >= 0 {
				body = m[6:8]
			}
			want = append(want, fmt.Sprint(m[0], m[1], text[body[0]:body[1]]))
		}
		var scan markdownScanner
		for i := 0; i < len(text); i++ {
			if end, span, ok := scan.match(text, i); ok {
				got = append(got, fmt.Sprint(i, end, span.Text))
				i = end - 1
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%q: got %q want %q", text, got, want)
		}
	}
}

func TestMarkdownLiteralHTMLAndSafeLinks(t *testing.T) {
	spans := markdownSpans("plain **bold** `code` [site](https://example.com) <script>alert(1)</script>")
	bold, code, safe := false, false, false
	var rendered strings.Builder
	for _, span := range spans {
		rendered.WriteString(span.Text)
		bold = bold || span.Text == "bold" && span.Bold
		code = code || span.Text == "code" && span.Bold
		safe = safe || span.Link == "https://example.com"
	}
	if !bold || !code || !safe || !strings.Contains(rendered.String(), "<script>alert(1)</script>") {
		t.Fatal("inline formatting or literal HTML lost")
	}
	for _, target := range []string{"javascript:alert", "JaVaScRiPt:alert", "data:text/html,x", "file:///etc/passwd", "https%3Aevil"} {
		for _, span := range markdownSpans("[link](" + target + ")") {
			if span.Link != "" {
				t.Fatalf("actionable unsafe scheme %q", target)
			}
		}
	}
	malformed := strings.Repeat("[a*", 20000)
	got := markdownSpans(malformed)
	if len(got) != 1 || got[0].Text != malformed {
		t.Fatal("malformed text changed")
	}
	link := ""
	md := Markdown("# Heading\n- item\n2. next\n```go\n<x>\n```\n[Open](https://example.com)", func(s string) { link = s })
	view := NewTester(md, 400, 320)
	defer view.Close()
	last := md.children[len(md.children)-1]
	view.Click(last.rect.X+10, last.rect.Y+last.rect.H/2)
	if link != "https://example.com" {
		t.Fatal("link callback not reachable")
	}
}

func TestChartDataSnapshotUpdatesAndMissingValues(t *testing.T) {
	red := RGB(211, 17, 33)
	values := []float64{1, math.NaN(), 1}
	chart := LineChart(Series{Values: values, Color: red})
	values[0] = 999
	if chart.series[0].Values[0] != 1 {
		t.Fatal("chart did not own its data snapshot")
	}
	view := NewTester(Column(chart.Element), 400, 300)
	defer view.Close()
	before := view.Snapshot()
	plot := Rect{48, 44, chart.rect.W - 64, chart.rect.H - 78}
	x, y := int(chart.x(1, plot)), int(chart.y(1, plot))
	if before.RGBAAt(x, y) == red {
		t.Fatal("line bridged a missing sample")
	}
	stats := view.Document.Stats()
	chart.SetSeries(Series{Values: []float64{1, 1, 1}, Color: red})
	after := view.Snapshot()
	if after.RGBAAt(x, y) != red || bytes.Equal(before.Pix, after.Pix) || view.Document.Stats().Frames <= stats.Frames {
		t.Fatal("chart update did not invalidate and render")
	}
	for _, series := range [][]Series{
		{{Values: []float64{-math.MaxFloat64, math.MaxFloat64}}},
		{{Values: []float64{math.SmallestNonzeroFloat64}}},
		{{Values: []float64{math.NaN(), math.Inf(1)}}},
		nil,
	} {
		chart.SetSeries(series...)
		view.Step()
		if !finiteValue(chart.low) || !finiteValue(chart.high) || chart.low >= chart.high {
			t.Fatal("invalid finite chart domain")
		}
	}
	bar := BarChart(Series{Name: "Balance", Values: []float64{-3, 0, 5}})
	barView := NewTester(Column(bar.Element), 400, 280)
	defer barView.Close()
	if bar.low != -3 || bar.high != 5 {
		t.Fatal("bar domain lost zero/negative values")
	}
}

func TestDenseChartKeepsNarrowSpikes(t *testing.T) {
	values := make([]float64, 2000)
	values[1001] = 10
	red := RGB(211, 17, 33)
	chart := LineChart(Series{Values: values, Color: red})
	view := NewTester(Column(chart.Element), 400, 280)
	defer view.Close()
	plot := Rect{48, 44, chart.rect.W - 64, chart.rect.H - 78}
	x, y := int(chart.x(1001, plot)), int(chart.y(10, plot))
	found := false
	img := view.Snapshot()
	for dy := -2; dy <= 3; dy++ {
		for dx := -2; dx <= 3; dx++ {
			p := img.RGBAAt(x+dx, y+dy)
			found = found || p.R > 180 && p.G < 100 && p.B < 100
		}
	}
	if !found {
		t.Fatal("dense-line reduction discarded spike")
	}
}

func TestChartShapeCacheFollowsDataAndPlot(t *testing.T) {
	wave := func(phase float64) []float64 {
		values := make([]float64, 600)
		for i := range values {
			values[i] = math.Sin(float64(i)/17+phase) * 50
		}
		values[300] = math.NaN()
		return values
	}
	render := func(chart *Chart, width, height int) []byte {
		view := NewTester(chart.Element.Fill(), width, height)
		defer view.Close()
		return view.Snapshot().Pix
	}
	for _, newChart := range []func(...Series) *Chart{LineChart, BarChart} {
		checkChartCache(t, newChart, wave, render)
	}
}

func checkChartCache(t *testing.T, newChart func(...Series) *Chart, wave func(float64) []float64, render func(*Chart, int, int) []byte) {
	cached := newChart(Series{Values: wave(0)})
	view := NewTester(cached.Element.Fill(), 420, 260)
	defer view.Close()
	view.Snapshot()
	cached.SetSeries(Series{Values: wave(1)}, Series{Values: wave(2)})
	if !bytes.Equal(view.Snapshot().Pix, render(newChart(Series{Values: wave(1)}, Series{Values: wave(2)}), 420, 260)) {
		t.Fatal("chart reused shapes after its data changed")
	}
	view.Width, view.Height = 300, 200
	view.Send(fego.Event{Kind: fego.Resize})
	if !bytes.Equal(view.Snapshot().Pix, render(newChart(Series{Values: wave(1)}, Series{Values: wave(2)}), 300, 200)) {
		t.Fatal("chart reused shapes after its plot changed")
	}
}
