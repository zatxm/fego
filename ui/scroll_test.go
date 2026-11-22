package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"slices"
	"strings"
	"testing"

	"github.com/zatxm/fego/v26"
)

type scrollScreen struct {
	*Tester
	screen           *image.RGBA
	supported        bool
	copies, uploaded int
	nodes            []fego.AccessNode
}

func (h *scrollScreen) SetAccessibility(nodes []fego.AccessNode) {
	h.nodes = append(h.nodes[:0], nodes...)
}

func (h *scrollScreen) Present(img *image.RGBA, damage []image.Rectangle) {
	h.Tester.Present(img, damage)
	if h.screen == nil || h.screen.Rect != img.Rect {
		h.screen = image.NewRGBA(img.Rect)
	}
	for _, r := range damage {
		draw.Draw(h.screen, r, img, r.Min, draw.Src)
		h.uploaded += r.Dx() * r.Dy()
	}
}
func (h *scrollScreen) ScrollPixels(r image.Rectangle, delta image.Point) bool {
	if !h.supported {
		return false
	}
	h.copies++
	movePixels(h.screen, r, delta.Y)
	return true
}
func newScrollScreen(doc *Document, scale float32, supported bool) *scrollScreen {
	h := &scrollScreen{Tester: &Tester{Document: doc, Width: 640, Height: 460, Scale: scale}, supported: supported}
	doc.Mount(h)
	doc.live.frame(false)
	return h
}
func checkScrollPixels(t *testing.T, h *scrollScreen) {
	t.Helper()
	actual := bytes.Clone(h.img.Pix)
	nodes := slices.Clone(h.nodes)
	type geometry struct {
		e          *Element
		rect, clip Rect
	}
	var before []geometry
	var collect func(*Element)
	collect = func(e *Element) {
		before = append(before, geometry{e, e.rect, e.clip})
		for _, child := range e.children {
			collect(child)
		}
	}
	collect(h.Document.root)
	if !bytes.Equal(actual, h.screen.Pix) {
		t.Fatal("native copy + upload differs from backing image")
	}
	h.Document.live.needsLayout = true
	h.Document.live.frame(false)
	for _, old := range before {
		if old.rect != old.e.rect || old.clip != old.e.clip {
			t.Fatalf("cached geometry differs kind=%v text=%q rect=%v/%v clip=%v/%v", old.e.kind, old.e.text, old.rect, old.e.rect, old.clip, old.e.clip)
		}
	}
	if !slices.Equal(nodes, h.nodes) {
		t.Fatal("scroll accessibility differs from full layout")
	}
	if !bytes.Equal(actual, h.img.Pix) {
		for y := 0; y < h.img.Rect.Dy(); y++ {
			for x := 0; x < h.img.Rect.Dx(); x++ {
				i := h.img.PixOffset(x, y)
				if !bytes.Equal(actual[i:i+4], h.img.Pix[i:i+4]) {
					t.Fatalf("scroll differs at %d,%d: %v vs %v", x, y, actual[i:i+4], h.img.Pix[i:i+4])
				}
			}
		}
	}
}
func TestEditorReusesScrollPixels(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 2} {
		e := CodeEditor(strings.Repeat("First line 中文\nSecond line\n", 200)).Fill()
		host := newScrollScreen(View(e), scale, true)
		host.Send(fego.Event{Kind: fego.Scroll, X: 120 * scale, Y: 140 * scale, DY: 40 * scale})
		if host.copies != 1 {
			t.Fatalf("editor at %.2f did not copy pixels", scale)
		}
		checkScrollPixels(t, host)
		host.Send(fego.Event{Kind: fego.Scroll, X: 120 * scale, Y: 140 * scale, DY: -16 * scale})
		checkScrollPixels(t, host)
		host.Close()
	}
}

func TestScrollingMatchesFullRepaint(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 2} {
		for _, supported := range []bool{false, true} {
			for _, kind := range []string{"list", "table", "tree"} {
				t.Run(fmt.Sprint(kind, scale, supported), func(t *testing.T) {
					var rows *Element
					switch kind {
					case "list":
						rows = List(1000, func(i int) string { return fmt.Sprintf("Row %d 中文\nclipped", i) })
					case "table":
						data := make([][]string, 1000)
						for i := range data {
							data[i] = []string{fmt.Sprint(i), "汉字 table"}
						}
						rows = Table([]ColumnDef{{Title: "ID", Width: 70}, {Title: "Value"}}, data)
					case "tree":
						nodes := make([]*TreeNode, 1000)
						for i := range nodes {
							nodes[i] = &TreeNode{Label: fmt.Sprintf("Tree %d", i)}
						}
						rows = Tree(nodes...)
					}
					rows.Fill().SetSelected(2)
					doc := View(Column(rows).Padding(7))
					h := newScrollScreen(doc, scale, supported)
					defer h.Close()
					doc.live.focus(rows)
					h.Step()
					for _, dy := range []float32{8, 8, 48, -16, 1, -9, 700, -700, .25, 8} {
						h.Send(fego.Event{Kind: fego.Scroll, X: 40, Y: 65, DY: dy})
						checkScrollPixels(t, h)
					}
					if supported && h.copies == 0 {
						t.Fatal("copy path never used")
					}
				})
			}
		}
	}
}

func TestScrollInvalidationAndOverlayFallBack(t *testing.T) {
	rows := List(1000, func(i int) string { return fmt.Sprint(i) }).Fill()
	root := Column(rows)
	h := newScrollScreen(View(root), 1, true)
	defer h.Close()
	v := h.Document.live
	for _, change := range []func(){
		func() { rows.Refresh() },
		func() { root.Padding(12) },
		func() { h.Document.ShowToast("overlay") },
		func() { h.Document.ShowDialog(Dialog("modal", Text("content"))) },
	} {
		v.input(fego.Event{Kind: fego.Scroll, X: 40, Y: 65, DY: 8})
		change()
		before := h.copies
		h.Step()
		if h.copies != before {
			t.Fatal("stale copy survived invalidation")
		}
		checkScrollPixels(t, h)
		h.Document.CloseDialog()
		v.toast = ""
		root.Padding(0)
		h.Step()
	}
	before := h.copies
	h.uploaded = 0
	for range 4 {
		v.input(fego.Event{Kind: fego.Scroll, X: 40, Y: 65, DY: 8})
	}
	h.Step()
	if h.copies != before+1 || h.uploaded >= h.Width*h.Height/5 {
		t.Fatalf("queued scroll copied %d times and uploaded %d pixels", h.copies-before, h.uploaded)
	}
	checkScrollPixels(t, h)
}

func TestScrollUploadsOnlyExposedPixels(t *testing.T) {
	rows := List(100000, func(i int) string { return fmt.Sprint(i) }).Fill()
	h := newScrollScreen(View(rows), 1, true)
	defer h.Close()
	h.uploaded = 0
	h.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 50, DY: 8})
	if h.uploaded >= h.Width*h.Height/5 || h.copies != 1 {
		t.Fatal("scroll uploaded too much", h.uploaded, h.copies)
	}
	checkScrollPixels(t, h)
}

func TestListLabelsRefreshAndScrolling(t *testing.T) {
	version := 0
	calls := map[int]int{}
	rows := List(100000, func(i int) string {
		calls[i]++
		if i == 0 {
			return ""
		}
		return fmt.Sprintf("version %d row %d", version, i)
	}).Fill()
	h := newScrollScreen(View(rows), 1, true)
	defer h.Close()
	checkCalls := func() {
		t.Helper()
		for row, n := range calls {
			if n != 1 {
				t.Fatalf("row %d fetched %d times in a frame", row, n)
			}
		}
	}
	checkCalls()
	clear(calls)
	h.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 50, DY: 8})
	checkCalls()
	if len(calls) > 1 {
		t.Fatal("scroll refetched retained rows", len(calls))
	}
	clear(calls)
	version++
	rows.Refresh()
	h.Step()
	checkCalls()
	for _, n := range h.nodes {
		if n.Role == "row" && n.Label != "" && !strings.HasPrefix(n.Label, "version 1 ") {
			t.Fatalf("stale accessible label: %q", n.Label)
		}
	}
	checkScrollPixels(t, h)
	v := h.Document.live
	h.Close()
	if v.rowLabels != nil {
		t.Fatal("closed list retains cached labels")
	}
}

func TestListLabelCacheBounded(t *testing.T) {
	rows := List(100000, func(i int) string {
		if i%2 == 0 {
			return strings.Repeat("x", 1025)
		}
		return fmt.Sprintf("row %d", i)
	}).Fill()
	h := newScrollScreen(View(rows), 1, true)
	defer h.Close()
	for range 10 {
		h.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 50, DY: 400})
		if len(h.Document.live.rowLabels) > 64 {
			t.Fatal("list label cache exceeded its entry budget")
		}
		for _, label := range h.Document.live.rowLabels {
			if len(label) > 1024 {
				t.Fatal("oversized label retained")
			}
		}
		checkScrollPixels(t, h)
	}
}

func TestScrollViewCopiesMatchFullLayout(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 2} {
		for _, native := range []bool{false, true} {
			for _, kind := range []string{"root", "nested-opaque", "nested-transparent", "canvas", "overlap"} {
				t.Run(fmt.Sprint(kind, scale, native), func(t *testing.T) {
					content := Column().Gap(3)
					for i := range 80 {
						content.Append(Text(fmt.Sprintf("Row %d 中文", i)).Height(28).Background(RGBA(43, uint8(i*3), 120, 83)))
					}
					scroller := ScrollView(content).Fill()
					var root *Element = scroller
					switch kind {
					case "nested-opaque":
						root = Row(Box().Width(19), scroller.Background(RGB(170, 190, 210))).Padding(7)
					case "nested-transparent":
						root = Row(Box().Width(19), scroller).Padding(7).Background(RGB(170, 190, 210))
					case "overlap":
						scroller.Background(RGB(170, 190, 210))
						root = Grid(1, scroller, Text("outside").Height(25)).Gap(-30).Fill()
					case "canvas":
						content.Append(Canvas(200, 800, func(p *Painter) { p.Rect(p.Bounds(), RGB(30, uint8(content.Bounds().Y), 100)) }))
					}
					h := newScrollScreen(View(root), scale, native)
					defer h.Close()
					for _, dy := range []float32{8, 8, -8, 1, 13.25, -.25, 200, -100, 3000, -3000, 8} {
						h.Send(fego.Event{Kind: fego.Scroll, X: 50 * scale, Y: 65 * scale, DY: dy * scale})
						checkScrollPixels(t, h)
					}
					if native && (kind == "root" || kind == "nested-opaque") && h.copies == 0 {
						t.Fatal("opaque viewport did not reuse pixels")
					}
					if (kind == "nested-transparent" || kind == "canvas" || kind == "overlap") && h.copies != 0 {
						t.Fatal("unsafe viewport copied pixels", kind, h.copies)
					}
					before := h.copies
					for range 2 {
						h.Document.live.input(fego.Event{Kind: fego.Scroll, X: 50 * scale, Y: 65 * scale, DY: 8 * scale})
					}
					h.Step()
					checkScrollPixels(t, h)
					expected := before
					if native && (kind == "root" || kind == "nested-opaque") {
						expected++
					}
					if h.copies != expected {
						t.Fatalf("queued viewport copy count %d, want %d", h.copies, expected)
					}
					scroller.Children()
					h.Step()
					if scroller.scroll != 0 || scroller.maxScroll() != 0 {
						t.Fatal("empty viewport retained scrolling")
					}
				})
			}
		}
	}
}
func TestMovePixelsContiguousAndPadded(t *testing.T) {
	for _, padded := range []bool{false, true} {
		for _, dy := range []int{-40, -8, -1, 0, 1, 8, 40} {
			parent := image.NewRGBA(image.Rect(4, 7, 79, 51))
			img := parent
			if padded {
				img = parent.SubImage(image.Rect(7, 9, 74, 49)).(*image.RGBA)
			}
			for i := range parent.Pix {
				parent.Pix[i] = byte(i * 37)
			}
			before := bytes.Clone(parent.Pix)
			want := bytes.Clone(before)
			r := img.Rect
			r.Min.Y += 2
			r.Max.Y -= 2
			dst := r.Intersect(r.Add(image.Pt(0, dy)))
			for y := dst.Min.Y; y < dst.Max.Y; y++ {
				d := img.PixOffset(dst.Min.X, y)
				s := img.PixOffset(dst.Min.X, y-dy)
				offset := parent.PixOffset(img.Rect.Min.X, img.Rect.Min.Y)
				copy(want[offset+d:offset+d+dst.Dx()*4], before[offset+s:offset+s+dst.Dx()*4])
			}
			if got := movePixels(img, r, dy); got != dst || !bytes.Equal(parent.Pix, want) {
				t.Fatal("contiguous move differs", padded, dy)
			}
		}
	}
}

func TestScrollViewLargeCoordinatesFallBack(t *testing.T) {
	content := Column(Box().Height(1<<23), Text("precision boundary 中文").Height(100), Box().Height(1000))
	scroller := ScrollView(content).Padding(7.5).Fill()
	h := newScrollScreen(View(scroller), 1, true)
	defer h.Close()
	h.Document.live.scrollRows(scroller, 8388615)
	h.Step()
	checkScrollPixels(t, h)
	before := h.copies
	h.Send(fego.Event{Kind: fego.Scroll, X: 100, Y: 100, DY: 1})
	checkScrollPixels(t, h)
	if h.copies != before {
		t.Fatal("rounded layout shift reused stale pixels")
	}
}

func TestCachedScrollLayoutMatchesNestedFullLayout(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 2, 3} {
		for _, supported := range []bool{false, true} {
			t.Run(fmt.Sprint(scale, supported), func(t *testing.T) {
				content := Column().Gap(7.25).Padding(2.5, 3.25)
				var nested *Element
				for i := range 12 {
					left := Column(Text(fmt.Sprint("Title ", i)).Height(22), Button("Action", nil).Height(28)).Gap(3.5).Padding(.25).Grow(1)
					grid := Grid(2, Text("A").Height(14), Text("B").Height(17), Text("C").Height(19), Text("Hidden").Hidden(i%2 == 0)).Gap(2.5).Width(120)
					clipped := Box(Column(Text("Clipped").Height(90))).Size(70, 35).Padding(1.25).Clip()
					content.Append(Row(left, grid, clipped).AlignItems([]Align{Start, Center, End, Stretch}[i%4]).Gap(4.25).Padding(.5))
					if i == 2 {
						nested = ScrollView(Column(Text("First").Height(24), Text("Second").Height(25), Text("Third").Height(26))).Height(35).Padding(1.5).Background(RGB(220, 230, 240))
						content.Append(nested)
					}
				}
				scroller := ScrollView(content).Fill().Background(RGB(240, 245, 250))
				h := newScrollScreen(View(scroller), scale, supported)
				defer h.Close()
				h.Document.live.scrollRows(nested, 7.5)
				h.Step()
				checkScrollPixels(t, h)
				for _, dy := range []float32{8, 8, 17, -11, .25, 31, -7, 100, -100} {
					h.Document.live.scrollRows(scroller, max(0, min(scroller.maxScroll(), scroller.scroll+dy)))
					h.Step()
					checkScrollPixels(t, h)
				}
				if supported && h.copies == 0 {
					t.Fatal("cached layout never exercised scroll copy")
				}
			})
		}
	}
}

func TestCleanScrollViewReusesFrameStorage(t *testing.T) {
	content := Column().Gap(0)
	for range 100 {
		content.Append(Text("Retained row").Height(24))
	}
	scroller := ScrollView(content).Fill()
	view := NewTester(scroller, 480, 320)
	defer view.Close()
	step := func() {
		view.Document.live.scrollRows(scroller, 8)
		view.Step()
		view.Document.live.scrollRows(scroller, 0)
		view.Step()
	}
	step()
	before := view.Document.Stats().Frames
	if n := testing.AllocsPerRun(30, step); n != 0 {
		t.Fatalf("clean scroll allocated %v times", n)
	}
	if view.Document.Stats().Frames-before < 60 {
		t.Fatal("allocation check did not exercise repeated frames")
	}
}

type regionScreen struct {
	*scrollScreen
	regions int
}

func (h *regionScreen) RetainsPixels() bool { return true }
func (h *regionScreen) PresentRegion(img *image.RGBA) {
	if h.screen == nil || h.screen.Rect != image.Rect(0, 0, h.Width, h.Height) {
		h.screen = image.NewRGBA(image.Rect(0, 0, h.Width, h.Height))
	}
	if !img.Rect.In(h.screen.Rect) || len(img.Pix) > regionBandBytes {
		panic(fmt.Sprint("region outside window or band too large: ", img.Rect))
	}
	draw.Draw(h.screen, img.Rect, img, img.Rect.Min, draw.Src)
	h.regions++
	h.uploaded += img.Rect.Dx() * img.Rect.Dy()
}
func (h *regionScreen) Present(*image.RGBA, []image.Rectangle) {
	panic("retained window received a full-surface present")
}

func checkRegionPixels(t *testing.T, h *regionScreen) {
	t.Helper()
	v := h.Document.live
	if v.img != nil {
		t.Fatal("retained window kept a full-window surface")
	}
	want := image.NewRGBA(v.size)
	v.paintRegion(want, v.size)
	if !bytes.Equal(h.screen.Pix, want.Pix) {
		for i := 0; i < len(want.Pix); i += 4 {
			if !bytes.Equal(h.screen.Pix[i:i+4], want.Pix[i:i+4]) {
				t.Fatalf("region screen differs at %d,%d", i/4%v.size.Dx(), i/4/v.size.Dx())
			}
		}
	}
}

func TestRegionPresentMatchesFullRepaint(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 2} {
		for _, native := range []bool{false, true} {
			t.Run(fmt.Sprint(scale, native), func(t *testing.T) {
				rows := List(1000, func(i int) string { return fmt.Sprintf("Row %d 中文", i) }).Height(220).SetSelected(2)
				content := Column().Gap(3)
				for i := range 40 {
					content.Append(Text(fmt.Sprintf("Item %d", i)).Height(28).Background(RGB(230, uint8(i*5), 200)))
				}
				input := TextInput("edit me")
				doc := View(Column(Row(input, Button("Go", nil)), rows, ScrollView(content).Height(160).Background(RGB(250, 250, 250))).Padding(7))
				h := &regionScreen{scrollScreen: &scrollScreen{Tester: &Tester{Document: doc, Width: 640, Height: 700, Scale: scale}, supported: native}}
				doc.Mount(h)
				h.Step()
				h.Document.live.frame(false)
				checkRegionPixels(t, h)
				for _, dy := range []float32{8, 8, 48, -16, 1, -9, 700, -700, .25, 8} {
					h.Send(fego.Event{Kind: fego.Scroll, X: 60 * scale, Y: 120 * scale, DY: dy * scale})
					checkRegionPixels(t, h)
					h.Send(fego.Event{Kind: fego.Scroll, X: 60 * scale, Y: 400 * scale, DY: dy * scale})
					checkRegionPixels(t, h)
				}
				if native && h.copies == 0 {
					t.Fatal("native copy path never used")
				}
				h.Click(30, 25)
				h.Type("中文 text")
				checkRegionPixels(t, h)
				doc.ShowToast("saved")
				h.Step()
				checkRegionPixels(t, h)
				doc.ShowDialog(Dialog("modal", Text("content")))
				h.Step()
				checkRegionPixels(t, h)
				doc.CloseDialog()
				h.Step()
				checkRegionPixels(t, h)
				// A window that lost pixels asks for Paint; everything is repainted.
				clear(h.screen.Pix)
				before := h.regions
				h.Send(fego.Event{Kind: fego.Paint})
				checkRegionPixels(t, h)
				if h.regions-before < 2 && scale > 1 {
					t.Fatal("full repaint was not split into bounded bands")
				}
				// A Paint carrying the lost area repaints only that area.
				doc.ShowToast("")
				h.Step()
				lost := image.Rect(37, 91, 245, 133)
				draw.Draw(h.screen, lost, image.Black, image.Point{}, draw.Src)
				h.uploaded = 0
				h.Send(fego.Event{Kind: fego.Paint, X: float32(lost.Min.X), Y: float32(lost.Min.Y), Width: lost.Dx(), Height: lost.Dy()})
				checkRegionPixels(t, h)
				if h.uploaded != lost.Dx()*lost.Dy() {
					t.Fatal("exposure repainted", h.uploaded, "pixels")
				}
				// An exposure before a queued scroll is presented cannot reuse copied pixels.
				draw.Draw(h.screen, lost, image.Black, image.Point{}, draw.Src)
				h.Document.live.input(fego.Event{Kind: fego.Scroll, X: 60 * scale, Y: 120 * scale, DY: 8 * scale})
				if native && h.Document.live.scrolling == nil {
					t.Fatal("queued scroll did not plan a pixel copy")
				}
				h.Send(fego.Event{Kind: fego.Paint, X: float32(lost.Min.X), Y: float32(lost.Min.Y), Width: lost.Dx(), Height: lost.Dy()})
				checkRegionPixels(t, h)
			})
		}
	}
}

func TestRegionBandReleasedAfterLargeFrames(t *testing.T) {
	rows := List(1000, func(i int) string { return fmt.Sprint("Row ", i) }).Fill()
	doc := View(rows)
	h := &regionScreen{scrollScreen: &scrollScreen{Tester: &Tester{Document: doc, Width: 640, Height: 460, Scale: 1}, supported: true}}
	doc.Mount(h)
	h.Step()
	v := h.Document.live
	v.frame(false)
	if cap(v.bandMem) <= smallBandBytes || v.bandTimer == nil {
		t.Fatal("full repaint did not keep a large band under an idle timer", cap(v.bandMem))
	}
	v.trimBand()
	if v.bandMem != nil {
		t.Fatal("idle trim kept the large band")
	}
	h.Send(fego.Event{Kind: fego.Paint})
	h.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 50, DY: 8})
	if cap(v.bandMem) > smallBandBytes {
		t.Fatal("a frame of small strips kept the large band", cap(v.bandMem))
	}
	h.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 50, DY: 8})
	if cap(v.bandMem) == 0 || cap(v.bandMem) > smallBandBytes {
		t.Fatal("small strips did not reuse a small band", cap(v.bandMem))
	}
	checkRegionPixels(t, h)
	h.Close()
	if v.bandMem != nil {
		t.Fatal("closed view kept its band")
	}
}
