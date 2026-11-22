package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zatxm/fego/v26"
)

func lineReference(p *Painter, x1, y1, x2, y2, width float32, c Color) {
	x1 = (x1 + p.originX) * p.scale
	y1 = (y1 + p.originY) * p.scale
	x2 = (x2 + p.originX) * p.scale
	y2 = (y2 + p.originY) * p.scale
	width *= p.scale
	r := image.Rect(int(math.Floor(float64(min(x1, x2)-width))), int(math.Floor(float64(min(y1, y2)-width))), int(math.Ceil(float64(max(x1, x2)+width))), int(math.Ceil(float64(max(y1, y2)+width)))).Intersect(p.clip).Intersect(p.dst.Rect)
	dx, dy := x2-x1, y2-y1
	length := float32(dx*dx) + float32(dy*dy)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			px, py := float32(x)+.5, float32(y)+.5
			t := float32(0)
			if length > 0 {
				t = max(0, min(1, (float32((px-x1)*dx)+float32((py-y1)*dy))/length))
			}
			a, b := px-(x1+float32(t*dx)), py-(y1+float32(t*dy))
			cov := max(float32(0), min(float32(1), width/2+.5-float32(math.Sqrt(float64(float32(a*a)+float32(b*b))))))
			if cov > 0 {
				cc := c
				cc.A = uint8(float32(c.A) * cov)
				p.pixel(x, y, cc)
			}
		}
	}
}
func imageReference(p *Painter, img image.Image, r Rect) {
	if img == nil {
		return
	}
	r.X += p.originX
	r.Y += p.originY
	dst := r.pixels(p.scale)
	clip := dst.Intersect(p.clip).Intersect(p.dst.Rect)
	if clip.Empty() {
		return
	}
	src := img.Bounds()
	for y := clip.Min.Y; y < clip.Max.Y; y++ {
		sy := src.Min.Y + (y-dst.Min.Y)*src.Dy()/max(dst.Dy(), 1)
		for x := clip.Min.X; x < clip.Max.X; x++ {
			sx := src.Min.X + (x-dst.Min.X)*src.Dx()/max(dst.Dx(), 1)
			c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
			p.pixel(x, y, Color{R: c.R, G: c.G, B: c.B, A: c.A})
		}
	}
}
func TestLineSpanMatchesReference(t *testing.T) {
	for _, scale := range []float32{.75, 1, 1.5, 2} {
		for i := range 90 {
			a := image.NewRGBA(image.Rect(-3, 7, 127, 109))
			for j := range a.Pix {
				a.Pix[j] = byte(j * 31)
			}
			b := image.NewRGBA(a.Rect)
			copy(b.Pix, a.Pix)
			p := &Painter{dst: a, clip: image.Rect(2, 10, 118, 101), scale: scale, originX: 1.25, originY: -2.5}
			q := *p
			q.dst = b
			x1, y1, x2, y2 := float32(i*37%153-20), float32(i*19%147-20), float32(i*11%153-20), float32(i*29%147-20)
			width := []float32{.3, 1, 2, 5.7, 12}[i%5]
			ink := RGBA(17, 132, 231, uint8(i*37))
			p.Line(x1, y1, x2, y2, width, ink)
			lineReference(&q, x1, y1, x2, y2, width, ink)
			if !bytes.Equal(a.Pix, b.Pix) {
				for j := range a.Pix {
					if a.Pix[j] != b.Pix[j] {
						t.Fatalf("line differs scale=%v line=%d pixel=(%d,%d) channel=%d got=%d want=%d", scale, i, a.Rect.Min.X+(j%a.Stride)/4, a.Rect.Min.Y+j/a.Stride, j%4, a.Pix[j], b.Pix[j])
					}
				}
			}
		}
	}
}
func TestImageFastPathsMatchReference(t *testing.T) {
	r := image.Rect(3, 7, 34, 30)
	rgba := image.NewRGBA(r)
	opaque := image.NewRGBA(r)
	nrgba := image.NewNRGBA(r)
	gray := image.NewGray(r)
	alpha := image.NewAlpha(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := rgba.PixOffset(x, y)
			opaque.SetRGBA(x, y, color.RGBA{uint8(x * 3), uint8(y * 7), uint8(x + y), 255})
			for j := range 4 {
				rgba.Pix[i+j] = byte(i*37 + j*61)
				nrgba.Pix[i+j] = byte(i*19 + j*73)
			}
			gray.SetGray(x, y, color.Gray{uint8(x + y)})
			alpha.SetAlpha(x, y, color.Alpha{uint8(x * y)})
		}
	}
	for _, src := range []image.Image{rgba, opaque, nrgba, gray, alpha, rgba.SubImage(image.Rect(5, 9, 29, 26)), opaque.SubImage(image.Rect(5, 9, 29, 26)), image.NewRGBA(image.Rectangle{})} {
		for _, scale := range []float32{.75, 1, 2} {
			for _, rect := range []Rect{{2.5, 4, 73, 52}, {-11, 0, 21, 12}, {7, 14, 31, 23}} {
				a := image.NewRGBA(image.Rect(-3, 7, 120, 96))
				for i := range a.Pix {
					a.Pix[i] = byte(i * 13)
				}
				b := image.NewRGBA(a.Rect)
				copy(b.Pix, a.Pix)
				p := &Painter{dst: a, clip: image.Rect(8, 11, 109, 83), scale: scale, originX: 1.25, originY: 2.5}
				q := *p
				q.dst = b
				p.Image(src, rect)
				imageReference(&q, src, rect)
				if !bytes.Equal(a.Pix, b.Pix) {
					t.Fatalf("image differs %T scale=%v rect=%v", src, scale, rect)
				}
			}
		}
	}
}
func TestChartSnapshotReuseAndShrink(t *testing.T) {
	values := make([]float64, 4096)
	values[3] = 4
	chart := LineChart(Series{Name: "owned", Values: values})
	first := &chart.series[0].Values[0]
	values[3] = 9
	chart.SetSeries(Series{Name: "new", Values: values})
	if &chart.series[0].Values[0] != first || chart.series[0].Values[3] != 9 {
		t.Fatal("snapshot storage not reused")
	}
	values[3] = 17
	if chart.series[0].Values[3] != 9 {
		t.Fatal("chart borrowed mutable caller data")
	}
	chart.SetSeries(Series{Values: []float64{1, 2}})
	if cap(chart.series[0].Values) > 8 {
		t.Fatal("oversized sample buffer retained")
	}
	chart.SetSeries()
	if len(chart.series) != 0 || chart.hasData {
		t.Fatal("cleared chart retains data")
	}
}
func TestCalendarPartialUpdateMatchesFullPaint(t *testing.T) {
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	calendar := Calendar(date)
	view := NewTester(calendar.Element, 380, 420)
	defer view.Close()
	for _, next := range []time.Time{date.AddDate(0, 0, 2), date.AddDate(0, 0, 27), date.AddDate(0, 1, 0), date.In(time.FixedZone("same offset", 0))} {
		calendar.SetDate(next)
		view.Step()
		got := bytes.Clone(view.img.Pix)
		calendar.showMonth(calendar.month)
		view.Step()
		if !bytes.Equal(got, view.img.Pix) {
			t.Fatal("partial calendar update differs")
		}
		for i, d := range calendar.dates {
			if !calendar.days[i].hidden && d.Location() != next.Location() {
				t.Fatal("calendar lost date timezone")
			}
		}
	}
}
func TestEditorRuneCoordinatesAndAllocations(t *testing.T) {
	editor := CodeEditor("a中😀b\nCafe\u0301 and \xff\nlast").Fill()
	view := NewTester(editor, 480, 240)
	defer view.Close()
	v := view.Document.live
	inner := v.inputInner(editor)
	for _, x := range []float32{-100, 0, 16, 500} {
		for _, y := range []float32{-100, 40, 200, 1000} {
			if got, want := v.textPosition(editor, x, y), v.textPositionReference(editor, x, y); got != want {
				t.Fatalf("hit differs %v,%v: %d vs%d", x, y, got, want)
			}
		}
	}
	rs := []rune(editor.text)
	for cursor := -1; cursor <= len(rs)+2; cursor++ {
		editor.cursor = cursor
		prefix := rs[:max(0, min(cursor, len(rs)))]
		line := 0
		start := 0
		for i, r := range prefix {
			if r == '\n' {
				line++
				start = i + 1
			}
		}
		w, _ := v.text.Measure(string(prefix[start:]), v.font(editor), false)
		x, y := v.caretPosition(editor, inner)
		if x != inner.X+w-editor.scrollX || y != inner.Y+float32(line)*v.text.LineHeight(v.font(editor), false)-editor.scroll {
			t.Fatal("caret differs at rune", cursor)
		}
	}
	editor.SetText(strings.Repeat("line 中文\n", 30000))
	editor.cursor = utf8.RuneCountInString(editor.text)
	v.caretPosition(editor, inner)
	if n := testing.AllocsPerRun(20, func() { v.caretPosition(editor, inner) }); n != 0 {
		t.Fatal("caret allocates document-sized buffer", n)
	}
}

func (v *view) textPositionReference(e *Element, x, y float32) int {
	inner := v.inputInner(e)
	font := v.font(e)
	row := max(0, int((y-inner.Y+e.scroll)/v.text.LineHeight(font, false)))
	rs := []rune(e.text)
	start := 0
	for i, r := range rs {
		if row == 0 {
			break
		}
		if r == '\n' {
			start = i + 1
			row--
		}
	}
	if row > 0 {
		return len(rs)
	}
	end := start
	for end < len(rs) && rs[end] != '\n' {
		end++
	}
	target := x - inner.X + e.scrollX
	line := string(rs[start:end])
	if e.password {
		line = strings.Repeat("•", end-start)
	}
	return start + v.text.Hit(line, font, false, target)
}

func (v *view) paintSelectionReference(p *Painter, e *Element, inner Rect, font float32) {
	if v.focused != e || e.cursor == e.anchor {
		return
	}
	runes := []rune(e.text)
	start, end := min(e.cursor, e.anchor), max(e.cursor, e.anchor)
	lineStart := 0
	line := 0
	for i := 0; i <= len(runes); i++ {
		if i == len(runes) || runes[i] == '\n' {
			a, b := max(start, lineStart), min(end, i)
			if a < b {
				before := string(runes[lineStart:a])
				selected := string(runes[a:b])
				if e.password {
					before = strings.Repeat("•", a-lineStart)
					selected = strings.Repeat("•", b-a)
				}
				x, _ := v.text.Measure(before, font, false)
				w, _ := v.text.Measure(selected, font, false)
				lineH := v.text.LineHeight(font, false)
				p.Rect(Rect{inner.X + x - e.scrollX, inner.Y + float32(line)*lineH - e.scroll, w, lineH}, mix(v.theme.Surface, v.theme.Accent, .2))
			}
			lineStart = i + 1
			line++
		}
	}
}

func TestEditorSelectionMatchesReference(t *testing.T) {
	editor := CodeEditor("a中😀b\nCafe\u0301 and \xff\nlast\n").Fill()
	view := NewTester(editor, 480, 120)
	defer view.Close()
	v := view.Document.live
	v.focused = editor
	for _, scroll := range []float32{0, 13, 50} {
		for _, selection := range [][2]int{{0, 5}, {3, 16}, {4, 10}, {1, 1}, {0, 25}} {
			editor.scroll = scroll
			editor.anchor, editor.cursor = selection[0], min(selection[1], utf8.RuneCountInString(editor.text))
			a := image.NewRGBA(view.img.Rect)
			b := image.NewRGBA(a.Rect)
			p := &Painter{dst: a, clip: a.Rect, scale: 1, text: v.text, theme: v.theme}
			q := *p
			q.dst = b
			v.paintSelection(p, editor, v.inputInner(editor), 14)
			v.paintSelectionReference(&q, editor, v.inputInner(editor), 14)
			if !bytes.Equal(a.Pix, b.Pix) {
				t.Fatal("selection changed", scroll, selection)
			}
		}
	}
}

func roundRectReference(p *Painter, r Rect, radius float32, c Color) {
	r.X += p.originX
	r.Y += p.originY
	x0, y0, x1, y1 := r.X*p.scale, r.Y*p.scale, (r.X+r.W)*p.scale, (r.Y+r.H)*p.scale
	rad := min(radius*p.scale, (x1-x0)/2, (y1-y0)/2)
	b := r.pixels(p.scale).Intersect(p.clip).Intersect(p.dst.Rect)
	if b.Empty() || c.A == 0 {
		return
	}
	if rad <= 0 {
		p.fillPixels(b, c)
		return
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		py := float32(y) + .5
		dy := max(y0+rad-py, py-(y1-rad), 0)
		inset := float32(0)
		if dy > 0 {
			inset = rad - float32(math.Sqrt(float64(max(0, rad*rad-dy*dy))))
		}
		lo, hi := max(b.Min.X, int(math.Ceil(float64(x0+inset)))), min(b.Max.X, int(math.Floor(float64(x1-inset))))
		if lo < hi {
			rowColor := c
			rowColor.A = uint8(float32(c.A) * min(float32(1), max(float32(0), min(py-y0+.5, y1-py+.5))))
			p.fillPixels(image.Rect(lo, y, hi, y+1), rowColor)
		}
		for _, span := range [][2]int{{b.Min.X, min(lo, b.Max.X)}, {max(hi, b.Min.X), b.Max.X}} {
			for x := span[0]; x < span[1]; x++ {
				px := float32(x) + .5
				dx := max(x0+rad-px, px-(x1-rad), 0)
				cov := min(float32(1), max(float32(0), rad+.5-float32(math.Sqrt(float64(dx*dx+dy*dy)))))
				if cov > 0 {
					cc := c
					cc.A = uint8(float32(c.A) * cov)
					p.pixel(x, y, cc)
				}
			}
		}
	}
}

func TestRoundRectBulkMatchesReference(t *testing.T) {
	for _, scale := range []float32{.75, 1, 1.25, 2} {
		for i := range 100 {
			a := image.NewRGBA(image.Rect(-3, 7, 87, 89))
			for j := range a.Pix {
				a.Pix[j] = byte(j * 31)
			}
			b := image.NewRGBA(a.Rect)
			copy(b.Pix, a.Pix)
			p := &Painter{dst: a, clip: []image.Rectangle{a.Rect, image.Rect(5, 10, 75, 76)}[i%2], scale: scale}
			q := *p
			q.dst = b
			r := []Rect{{0, 8, 74, 68}, {3.25, 9.5, 47.5, 31.25}, {-4, -7, 58, 43}, {1, 12, 2, 54}, {8, 10, 61, 1}}[i%5]
			radius := []float32{.1, .5, 1, 4, 14, 100}[i%6]
			ink := RGBA(27, 88, 171, []uint8{0, 37, 143, 255}[i%4])
			p.RoundRect(r, radius, ink)
			roundRectReference(&q, r, radius, ink)
			if !bytes.Equal(a.Pix, b.Pix) {
				t.Fatalf("round rect differs scale=%v case=%d", scale, i)
			}
		}
	}
}

func TestPaintContextsDoNotAllocateForOrdinaryControls(t *testing.T) {
	label := Text("Stable label")
	view := NewTester(Column(label), 240, 100)
	defer view.Close()
	label.Refresh()
	view.Step()
	if n := testing.AllocsPerRun(30, func() { label.Refresh(); view.Step() }); n != 0 {
		t.Fatalf("ordinary repaint allocated %v times", n)
	}
}

func TestCanvasContextsStayLocalAndReuseStorage(t *testing.T) {
	calls := 0
	draw := func(p *Painter) {
		heights := []float32{20, 35}
		origins := []float32{0, 32}
		if p.Bounds().H != heights[calls%2] || p.originY != origins[calls%2] {
			t.Fatal("canvas drawing context escaped its local coordinates")
		}
		calls++
		p.Rect(p.Bounds(), RGB(30, 80, 150))
	}
	root := Column(Canvas(80, 20, draw), Canvas(80, 35, draw))
	view := NewTester(root, 180, 120)
	defer view.Close()
	root.Refresh()
	view.Step()
	if n := testing.AllocsPerRun(30, func() { root.Refresh(); view.Step() }); n != 1 {
		t.Fatalf("two canvases allocated %v drawing contexts", n)
	}
}

func TestFocusRingDoesNotTintButtonContents(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 2, 3} {
		for _, theme := range []Theme{Light, Dark} {
			button := Button("Focused button", nil).Width(180)
			view := NewDocumentTester(View(Column(button).Padding(12)).Theme(theme), 220, 90)
			view.Width, view.Height, view.Scale = int(220*scale), int(90*scale), scale
			view.Document.live.frame(true)
			before := view.Snapshot()
			x, y := int((button.rect.X-2)*scale), int((button.rect.Y+button.rect.H/2)*scale)
			clickElement(view, button)
			if view.Document.live.focused != button || before.RGBAAt(x, y) != view.img.RGBAAt(x, y) {
				t.Fatal("pointer focus should not draw a ring")
			}
			view.Key("Tab", false, false)
			inner := button.rect.pixels(scale).Inset(int(math.Ceil(float64(5 * scale))))
			for y := inner.Min.Y; y < inner.Max.Y; y++ {
				a, b := before.PixOffset(inner.Min.X, y), before.PixOffset(inner.Max.X, y)
				if !bytes.Equal(before.Pix[a:b], view.img.Pix[a:b]) {
					t.Fatal("focus tinted button contents")
				}
			}
			if before.RGBAAt(x, y) == view.img.RGBAAt(x, y) {
				t.Fatal("focus ring is missing")
			}
			actual := bytes.Clone(view.img.Pix)
			view.Document.live.needsLayout = true
			view.Document.live.frame(false)
			if !bytes.Equal(actual, view.img.Pix) {
				t.Fatalf("partial focus repaint differs at scale %v", scale)
			}
			view.Close()
		}
	}
}

func TestCollapsedTreeReleasesExpansionBuffer(t *testing.T) {
	root := &TreeNode{Label: "Root", Children: make([]*TreeNode, 50000)}
	for i := range root.Children {
		root.Children[i] = &TreeNode{Label: "Child"}
	}
	tree := Tree(root)
	tree.expanded[root] = true
	tree.rebuildTree()
	before := cap(tree.visibleTree)
	if len(tree.visibleTree) != 50001 {
		t.Fatal("expanded tree is incomplete")
	}
	tree.expanded[root] = false
	tree.rebuildTree()
	if len(tree.visibleTree) != 1 || tree.visibleTree[0].node != root || cap(tree.visibleTree) > 256 {
		t.Fatal("collapsed tree retained the large row buffer")
	}
	t.Logf("visible-row buffer capacity: %d → %d rows", before, cap(tree.visibleTree))
	tree.expanded[root] = true
	tree.rebuildTree()
	if len(tree.visibleTree) != 50001 || tree.visibleTree[50000].node != root.Children[49999] {
		t.Fatal("re-expansion lost nodes")
	}
}

func TestCardShadowPartialRepaintMatchesFull(t *testing.T) {
	for _, scale := range []float32{.75, 1, 1.25, 2, 3} {
		card := Card(Text("Shadow")).Size(140, 60)
		view := NewTester(Column(card).Padding(20), 240, 110)
		view.Width, view.Height, view.Scale = int(240*scale), int(110*scale), scale
		view.Document.live.frame(true)
		card.Radius(22)
		view.Step()
		actual := bytes.Clone(view.img.Pix)
		view.Document.live.needsLayout = true
		view.Document.live.frame(false)
		if !bytes.Equal(actual, view.img.Pix) {
			t.Fatalf("partial shadow repaint differs at scale %v", scale)
		}
		view.Close()
	}
}

func TestEffectBoundsSurviveNeighborRepaint(t *testing.T) {
	for _, scale := range []float32{1.25, 2, 3} {
		for _, shadow := range []bool{false, true} {
			control := Button("", nil).Size(100, 40)
			if shadow {
				control = Card().Size(100, 40)
			}
			view := NewTester(Column(control).Padding(20), 180, 100)
			defer view.Close()
			view.Width, view.Height, view.Scale = int(180*scale), int(100*scale), scale
			view.Document.live.frame(true)
			if !shadow {
				view.Document.live.focus(control)
				view.Step()
			}
			r := control.rect.pixels(scale)
			x, y := r.Min.X-int(math.Ceil(float64(2.5*scale))), (r.Min.Y+r.Max.Y)/2
			if shadow {
				x, y = (r.Min.X+r.Max.X)/2, r.Max.Y+int(math.Ceil(float64(4*scale)))
			}
			if view.img.RGBAAt(x, y) == Light.Background {
				t.Fatal("test point misses the painted effect")
			}
			before := bytes.Clone(view.img.Pix)
			view.Document.live.damage(image.Rect(x, y, x+1, y+1))
			view.Document.live.frame(false)
			if !bytes.Equal(before, view.img.Pix) {
				t.Fatalf("neighbor repaint erased an effect, scale=%v shadow=%v", scale, shadow)
			}
		}
	}
}

func TestPrimaryButtonHoverAndPressHaveDistinctFeedback(t *testing.T) {
	for _, theme := range []Theme{Light, Dark} {
		button := PrimaryButton("Action", nil).Width(150)
		view := NewDocumentTester(View(Column(button).Padding(12)).Theme(theme), 210, 90)
		x, y := int(button.rect.X+button.rect.W/2), int(button.rect.Y+3)
		normal := view.img.RGBAAt(x, y)
		view.Send(fego.Event{Kind: fego.PointerMove, X: float32(x), Y: float32(y)})
		hover := view.img.RGBAAt(x, y)
		view.Send(fego.Event{Kind: fego.PointerDown, X: float32(x), Y: float32(y), Button: 1})
		pressed := view.img.RGBAAt(x, y)
		if normal == hover || hover == pressed || normal == pressed {
			t.Fatal("primary button states have identical feedback")
		}
		view.Send(fego.Event{Kind: fego.PointerUp, X: float32(x), Y: float32(y), Button: 1})
		if view.img.RGBAAt(x, y) != hover {
			t.Fatal("released button did not restore its hover state")
		}
		view.Close()
	}
}

func TestRetainedPaintSkipsCleanUploadAndKeepsExpose(t *testing.T) {
	label := Text("before").Width(120)
	h := NewTester(Column(label).Padding(8), 240, 160)
	defer h.Close()
	v := h.Document.live
	h.damage = nil
	v.PaintIfDirty()
	if len(h.damage) != 0 {
		t.Fatal("clean invalidation uploaded unchanged pixels")
	}
	label.SetText("after")
	v.PaintIfDirty()
	if len(h.damage) == 0 {
		t.Fatal("dirty retained invalidation was dropped")
	}
	want := bytes.Clone(h.img.Pix)
	h.damage = nil
	v.PaintIfDirty()
	if len(h.damage) != 0 {
		t.Fatal("already presented changes were uploaded again")
	}
	v.Event(fego.Event{Kind: fego.Paint})
	if len(h.damage) != 1 || h.damage[0] != h.img.Rect || !bytes.Equal(want, h.img.Pix) {
		t.Fatal("native exposure did not restore the full backing image")
	}
}

func TestRichTextRetainsAccessibleText(t *testing.T) {
	text := RichText(Span{Text: "你好，"}, Span{Text: "bold", Bold: true}, Span{Text: " link", Link: "https://example.com"})
	h := newScrollScreen(View(text), 1, false)
	defer h.Close()
	if len(h.nodes) != 1 || h.nodes[0].Label != "你好，bold link" {
		t.Fatal("joined rich text label changed", h.nodes)
	}
	text.Label("Custom label")
	h.Step()
	if h.nodes[0].Label != "Custom label" {
		t.Fatal("explicit rich text label was lost", h.nodes)
	}
}

// Layout-only elements share noWidget; any write through them would leak into
// every Box and Text, so the whole package run must leave it untouched.
func TestMain(m *testing.M) {
	code := m.Run()
	if !reflect.DeepEqual(noWidget, widget{selected: -1}) {
		fmt.Fprintf(os.Stderr, "shared widget state was modified: %+v\n", noWidget)
		code = 1
	}
	os.Exit(code)
}
