package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zatxm/fego/v26"
	fonttext "github.com/zatxm/fego/v26/internal/text"
)

func TestFillDimensionsAndScrollClipping(t *testing.T) {
	sidebar := Column(Text("nav")).Width(190).FillHeight()
	var last *Element
	content := Column()
	for i := 0; i < 30; i++ {
		last = Button(fmt.Sprint(i), nil)
		content.Append(last)
	}
	scroll := ScrollView(content).Fill()
	view := NewTester(Row(sidebar, scroll).Gap(0), 700, 300)
	defer view.Close()
	if sidebar.rect.W != 190 || sidebar.rect.H != 300 || scroll.rect.W != 510 {
		t.Fatalf("incorrect fill geometry: %v %v", sidebar.rect, scroll.rect)
	}
	if hit(view.Document.root, last.rect.X+10, last.rect.Y+10) != nil {
		t.Fatal("offscreen row receives input")
	}
	view.Document.live.focus(last)
	view.Step()
	if last.rect.Y+last.rect.H > 300.1 {
		t.Fatal("focused item was not revealed")
	}
	scroll.scroll = 0
	scroll.touch(true)
	view.Step()
	view.Send(fego.Event{Kind: fego.Scroll, X: 400, Y: 280, DY: 100000})
	if scroll.scroll != scroll.maxScroll() || last.rect.Y+last.rect.H > 300.1 {
		t.Fatal("scroll did not reveal last item", last.rect, scroll.scroll)
	}
}

func TestLongPopupMouseSelectsPaintedItem(t *testing.T) {
	options := make([]string, 40)
	for i := range options {
		options[i] = fmt.Sprint(i)
	}
	control := Select(options, 30)
	view := NewTester(Column(control), 400, 500)
	defer view.Close()
	clickElement(view, control)
	p := view.Document.live.popup
	if p.first == 0 {
		t.Fatal("selected option not revealed")
	}
	expected := p.first + 1
	view.Click(p.rect.X+20, p.rect.Y+4+34+17)
	if control.Selected() != expected {
		t.Fatal("clicked a different option", control.Selected(), expected)
	}
}

func TestInputScrollAndEmptyWordNavigation(t *testing.T) {
	input := TextInput("").Width(130)
	view := NewTester(Column(input), 250, 100)
	defer view.Close()
	clickElement(view, input)
	view.Key("ArrowLeft", false, true)
	view.Type(strings.Repeat("abc", 50))
	inner := view.Document.live.inputInner(input)
	if input.scrollX == 0 || float32(view.Caret.Min.X) > inner.X+inner.W {
		t.Fatal("caret left viewport", view.Caret)
	}
	view.Key("Home", false, true)
	if input.scrollX != 0 {
		t.Fatal("Home did not reveal start")
	}
}

func TestInputCaretUsesPaintedCoordinates(t *testing.T) {
	for _, scale := range []float32{1, 4.0 / 3, 2} {
		for _, tc := range []struct {
			name, text, visibleLine string
			element                 *Element
			row                     int
		}{
			{"text", "Wii 自用 WWW", "Wii 自用 WWW", TextInput(""), 0},
			{"password", "Wii 自用", "••••••", TextInput("").Password(true), 0},
			{"masked-multiline", "私钥\nWWi", "•••", TextArea("").Password(true), 1},
			{"editor", "首行\nWii 自用", "Wii 自用", CodeEditor(""), 1},
		} {
			t.Run(fmt.Sprintf("%s/%g", tc.name, scale), func(t *testing.T) {
				field := tc.element.Width(260)
				tester := NewTester(Column(field), 700, 420)
				defer tester.Close()
				tester.Scale = scale
				v := tester.Document.live
				v.frame(false)
				clickElement(tester, field)
				tester.Type(tc.text)
				inner := v.inputInner(field)
				font := v.font(field)
				width, _ := v.text.Measure(tc.visibleLine, font*scale, false)
				wantX := (inner.X-field.scrollX)*scale + width
				lineHeight := v.text.LineHeight(font*scale, false)
				wantY := (inner.Y-field.scroll)*scale + (float32(tc.row)+.5)*lineHeight
				got := tester.Caret
				if !tester.Editing || math.Abs(float64(float32(got.Min.X)-wantX)) > 1 ||
					math.Abs(float64(float32(got.Min.Y+got.Max.Y)/2-wantY)) > 1 {
					t.Fatalf("caret %v, want x %.2f and center y %.2f", got, wantX, wantY)
				}
				img := tester.Snapshot()
				accent := v.theme.Accent
				if pixel := img.RGBAAt(got.Min.X, (got.Min.Y+got.Max.Y)/2); pixel != (color.RGBA{accent.R, accent.G, accent.B, accent.A}) {
					t.Fatalf("painted caret and IME caret disagree at %v: %v", got, pixel)
				}
				tester.Click(wantX/scale, wantY/scale)
				if field.cursor != utf8.RuneCountInString(tc.text) {
					t.Fatalf("click at painted end went to rune %d", field.cursor)
				}
				if tc.name == "masked-multiline" {
					ink := v.theme.Text
					first := image.Rect(int(inner.X*scale), int(inner.Y*scale), int(inner.X*scale+width), int(inner.Y*scale+lineHeight))
					second := first.Add(image.Pt(0, int(lineHeight)))
					for row, rect := range []image.Rectangle{first, second} {
						found := false
						for y := rect.Min.Y; y < rect.Max.Y && !found; y++ {
							for x := rect.Min.X; x < rect.Max.X; x++ {
								pixel := img.RGBAAt(x, y)
								if math.Abs(float64(int(pixel.R)-int(ink.R))) < 20 {
									found = true
									break
								}
							}
						}
						if !found {
							t.Fatalf("missing password mask on row %d", row+1)
						}
					}
				}
			})
		}
	}
}

func TestKeyboardActivationKeepsSelectedRow(t *testing.T) {
	list := List(100, func(i int) string { return fmt.Sprint(i) })
	selected := -1
	list.OnSelect(func(i int) { selected = i })
	view := NewTester(Column(list), 300, 280)
	defer view.Close()
	view.Key("Tab", false, false)
	view.Key("End", false, false)
	view.Key("Enter", false, false)
	if selected != 99 || list.Selected() != 99 {
		t.Fatal("keyboard activated another row", selected, list.Selected())
	}
}

func TestScrollbarDragDoesNotSelectRows(t *testing.T) {
	list := List(1000, func(i int) string { return fmt.Sprint(i) })
	view := NewTester(Column(list.Fill()), 300, 300)
	defer view.Close()
	view.Send(fego.Event{Kind: fego.PointerDown, X: 297, Y: 10, Button: 1})
	view.Send(fego.Event{Kind: fego.PointerMove, X: 297, Y: 295})
	view.Send(fego.Event{Kind: fego.PointerUp, X: 297, Y: 295, Button: 1})
	if list.scroll < list.maxScroll()*.95 || list.Selected() != -1 {
		t.Fatal("scrollbar drag selected data or did not scroll", list.scroll, list.Selected())
	}
}

func TestDocumentCannotStealMountedRoot(t *testing.T) {
	root := Column(Text("owned"))
	first, second := View(root), View(root)
	view := NewDocumentTester(first, 200, 100)
	defer view.Close()
	defer func() {
		if recover() == nil {
			t.Fatal("mounted shared root accepted")
		}
		if root.view != first.live {
			t.Fatal("live root was stolen")
		}
	}()
	NewDocumentTester(second, 200, 100)
}

func TestTreeRejectsExpandedCycle(t *testing.T) {
	node := &TreeNode{Label: "cycle"}
	node.Children = []*TreeNode{node}
	tree := Tree(node)
	tree.expanded[node] = true
	defer func() {
		if recover() == nil {
			t.Fatal("expanded cycle accepted")
		}
	}()
	tree.rebuildTree()
}

func TestSliderExtremeFiniteRange(t *testing.T) {
	slider := Slider(0, -math.MaxFloat64, math.MaxFloat64)
	view := NewTester(Column(slider), 300, 100)
	defer view.Close()
	view.Click(slider.rect.X+slider.rect.W/2, slider.rect.Y+slider.rect.H/2)
	if math.IsNaN(slider.Value()) || math.IsInf(slider.Value(), 0) {
		t.Fatal("invalid midpoint")
	}
	view.Key("End", false, false)
	view.Key("ArrowRight", false, false)
	if slider.Value() != math.MaxFloat64 {
		t.Fatal("range end", slider.Value())
	}
}

func TestTreeEditsPreserveFocusAndClampScroll(t *testing.T) {
	input := TextInput("a")
	root := Column(input)
	view := NewTester(root, 300, 200)
	defer view.Close()
	clickElement(view, input)
	root.Append(Text("new"))
	view.Step()
	view.Type("b")
	if input.Text() != "ab" {
		t.Fatal("append lost existing focus", input.Text())
	}
	label := Text("line").Width(100)
	root.Children(label)
	view.Step()
	height := label.rect.H
	label.SetText("line\nline")
	view.Step()
	if label.rect.H <= height {
		t.Fatal("fixed width prevented height update")
	}
	area := TextArea(strings.Repeat("line\n", 100))
	root.Children(area)
	view.Step()
	view.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 20, DY: 10000})
	area.SetText("short")
	view.Step()
	if area.scroll != 0 {
		t.Fatal("text scroll remained beyond content")
	}
	list := List(100, func(i int) string { return fmt.Sprint(i) })
	root.Children(list)
	view.Step()
	clickElement(view, list)
	view.Key("End", false, false)
	list.SetCount(2)
	view.Step()
	if list.scroll != 0 {
		t.Fatal("list scroll remained beyond content")
	}
}

func TestModalDoesNotRestoreHiddenOrDisabledFocus(t *testing.T) {
	for _, disableParent := range []bool{false, true} {
		called := false
		button := Button("background", func() { called = true })
		root := Column(button)
		view := NewTester(root, 300, 200)
		view.Key("Tab", false, false)
		view.Document.ShowDialog(Dialog("Modal", Text("body")))
		if disableParent {
			root.Disabled(true)
		} else {
			button.Hidden(true)
		}
		view.Document.CloseDialog()
		view.Step()
		view.Key("Enter", false, false)
		if called {
			t.Fatal("invalid focus restored")
		}
		view.Close()
	}
}

type reentrantClipboardHost struct {
	*Tester
	read func() string
}

func (h *reentrantClipboardHost) Clipboard() string { return h.read() }

func TestClipboardReadDoesNotMutateStaleEditor(t *testing.T) {
	for _, action := range []string{"empty", "hide", "modal", "close"} {
		t.Run(action, func(t *testing.T) {
			changes := 0
			input := TextInput("keep").OnChange(func(string) { changes++ })
			doc := View(Column(input))
			host := &reentrantClipboardHost{Tester: &Tester{Document: doc, Width: 300, Height: 200, Scale: 1}}
			doc.Mount(host)
			doc.live.frame(false)
			doc.live.focus(input)
			input.anchor = 0
			input.cursor = 4
			host.read = func() string {
				switch action {
				case "empty":
					return ""
				case "hide":
					input.Hidden(true)
				case "modal":
					doc.ShowDialog(Dialog("Modal", Text("body")))
				case "close":
					doc.live.Close()
				}
				return "replace"
			}
			live := doc.live
			live.Event(fego.Event{Kind: fego.KeyDown, Key: "v", Control: true})
			if input.Text() != "keep" || changes != 0 {
				t.Fatal("stale clipboard changed input", input.Text(), changes)
			}
			host.Close()
		})
	}
}

func TestOversizedInputRejectedBeforeRuneAllocation(t *testing.T) {
	input := TextInput("keep")
	view := NewTester(Column(input), 300, 100)
	defer view.Close()
	clickElement(view, input)
	text := strings.Repeat("a", 8<<20)
	if n := testing.AllocsPerRun(10, func() { view.Document.live.replaceSelection(input, text) }); n != 0 {
		t.Fatal("oversized text allocated", n)
	}
	if input.Text() != "keep" {
		t.Fatal("oversized input accepted")
	}
}

type accessibleHost struct {
	*Tester
	nodes   []fego.AccessNode
	updates int
}

func (h *accessibleHost) SetAccessibility(nodes []fego.AccessNode) {
	h.nodes = nodes
	h.updates++
}

func TestHoverPaintDoesNotRepublishAccessibility(t *testing.T) {
	button := Button("Hover", nil)
	slider := Slider(10, 0, 100)
	doc := View(Column(button, slider))
	host := &accessibleHost{Tester: &Tester{Document: doc, Width: 380, Height: 150, Scale: 1}}
	doc.Mount(host)
	doc.live.frame(false)
	defer host.Close()
	updates, frames := host.updates, doc.Stats().Frames
	host.Send(fego.Event{Kind: fego.PointerMove, X: 20, Y: 12})
	if doc.Stats().Frames == frames || host.updates != updates {
		t.Fatal("visual hover failed to paint without publishing the accessibility tree")
	}
	slider.SetValue(42)
	host.Step()
	if host.updates != updates+1 {
		t.Fatal("partial slider repaint did not publish the new value")
	}
	for _, node := range host.nodes {
		if node.ID == slider.id && node.Number == 42 {
			return
		}
	}
	t.Fatal("accessibility slider value stayed stale")
}

func TestAccessibilityActionsPrivacyAndModalBoundary(t *testing.T) {
	clicks := 0
	button := Button("Run", func() { clicks++ })
	secret := TextInput("private-token").Password(true).Label("Password")
	check := Checkbox("Enabled", false)
	doc := View(Column(button, secret, check))
	host := &accessibleHost{Tester: &Tester{Document: doc, Width: 400, Height: 300, Scale: 1}}
	doc.Mount(host)
	doc.live.frame(false)
	defer host.Close()
	for _, n := range host.nodes {
		if strings.Contains(n.Value, "private") || strings.Contains(n.Label, "private") {
			t.Fatal("password leaked into accessibility")
		}
	}
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: button.id, Action: "press"})
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: check.id, Action: "press"})
	if clicks != 1 || !check.Checked() {
		t.Fatal("accessible action not routed")
	}
	button.Disabled(true)
	host.Step()
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: button.id, Action: "press"})
	if clicks != 1 {
		t.Fatal("disabled accessibility action executed")
	}
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: secret.id, Action: "setValue", Text: "new中😀"})
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: secret.id, Action: "setSelection", Text: "1:3"})
	if secret.Text() != "new中😀" || secret.anchor != 1 || secret.cursor != 3 {
		t.Fatal("text action failed")
	}
	doc.ShowDialog(Dialog("Confirm", Text("Modal"), Button("OK", nil)))
	host.Step()
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: check.id, Action: "press"})
	if !check.Checked() {
		t.Fatal("background control activated through modal")
	}
	doc.CloseDialog()
	host.Step()
	check.Hidden(true)
	host.Step()
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: check.id, Action: "press"})
	if !check.Checked() {
		t.Fatal("hidden control activated")
	}
}

func TestOpaqueImageSamplingIgnoresClip(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 7, 10, 12))
	for i := range src.Pix {
		src.Pix[i] = uint8(i*37 + 11)
	}
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = 255
	}
	r := Rect{4, 6, 37, 17}
	dst := r.pixels(1)
	for _, clip := range []image.Rectangle{dst, image.Rect(0, 8, 64, 11), image.Rect(9, 13, 30, 40), image.Rect(0, 22, 64, 23)} {
		out := image.NewRGBA(image.Rect(0, 0, 64, 32))
		p := &Painter{dst: out, clip: clip, scale: 1}
		p.Image(src, r)
		for y := 0; y < 32; y++ {
			for x := 0; x < 64; x++ {
				want := color.RGBA{}
				if image.Pt(x, y).In(dst.Intersect(clip)) {
					want = src.RGBAAt(src.Rect.Min.X+(x-dst.Min.X)*src.Rect.Dx()/dst.Dx(), src.Rect.Min.Y+(y-dst.Min.Y)*src.Rect.Dy()/dst.Dy())
				}
				if got := out.RGBAAt(x, y); got != want {
					t.Fatalf("clip %v pixel %d,%d=%v want %v", clip, x, y, got, want)
				}
			}
		}
	}
}

func TestTranslucentFillMatchesPixelBlend(t *testing.T) {
	for _, c := range []Color{RGBA(22, 40, 76, 3), RGBA(8, 16, 32, 96), RGBA(255, 255, 255, 1), RGBA(0, 0, 0, 254), RGBA(200, 100, 50, 128)} {
		for _, size := range []image.Point{{16, 16}, {300, 220}} {
			got := image.NewRGBA(image.Rect(3, 5, 3+size.X, 5+size.Y))
			for i := range got.Pix {
				got.Pix[i] = uint8(i*131 + i/4)
			}
			want := image.NewRGBA(got.Rect)
			copy(want.Pix, got.Pix)
			(&Painter{dst: got, clip: got.Rect}).fillPixels(got.Rect, c)
			reference := &Painter{dst: want, clip: want.Rect}
			for y := want.Rect.Min.Y; y < want.Rect.Max.Y; y++ {
				for x := want.Rect.Min.X; x < want.Rect.Max.X; x++ {
					reference.pixel(x, y, c)
				}
			}
			if !bytes.Equal(got.Pix, want.Pix) {
				t.Fatalf("translucent fill %v size %v differs from per-pixel blend", c, size)
			}
		}
	}
}

func TestTextOnSolidFillMatchesBlending(t *testing.T) {
	text := fonttext.New()
	defer text.Close()
	icon := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for i := range icon.Pix {
		icon.Pix[i] = uint8(i*29) | uint8(255*(i%4/3)) // opaque: direct row copies
	}
	rng := rand.New(rand.NewPCG(9, 10))
	for range 2000 {
		fast := &Painter{dst: image.NewRGBA(image.Rect(0, 0, 120, 60)), clip: image.Rect(0, 0, 120, 60), scale: 1, text: text}
		fast.clip.Max.Y -= rng.IntN(20)
		slow := &Painter{dst: image.NewRGBA(fast.dst.Rect), clip: fast.clip, scale: 1, text: text}
		// Operations cluster so overlays often land under a later label.
		cx, cy := float32(20+rng.IntN(60)), float32(10+rng.IntN(30))
		for range 1 + rng.IntN(6) {
			kind, x, y := rng.IntN(6), cx+float32(rng.IntN(24)-4), cy+float32(rng.IntN(12)-2)
			c := RGB(uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)))
			ink, size := RGBA(20, 30, 40, 255-uint8(rng.IntN(2))*100), 11+float32(rng.IntN(3))
			for _, p := range []*Painter{fast, slow} {
				switch kind {
				case 0:
					p.Rect(Rect{x - 20, y - 10, 70, 30}, c)
				case 1:
					p.Line(x, y, x+9, y+3, 1.5, c)
				case 2:
					p.Circle(x, y, 2.5, c)
				case 3:
					p.Rect(Rect{x, y, 6, 6}, RGBA(c.R, c.G, c.B, 90))
				case 4:
					p.Image(icon, Rect{x, y, 6, 6})
				default:
					if p == slow {
						p.solid = image.Rectangle{} // reference: always blend
					}
					p.Label("Ag 1.5", cx-2, cy-2, size, ink, false)
				}
			}
		}
		if !bytes.Equal(fast.dst.Pix, slow.dst.Pix) {
			t.Fatal("text over a solid fill differs from blending")
		}
	}
}
