package ui

import (
	"bytes"
	"image"
	"testing"

	"github.com/zatxm/fego/v26"
)

func clickElement(t *Tester, e *Element) { r := e.Bounds(); t.Click(r.X+r.W/2, r.Y+r.H/2) }

func TestControlsKeyboardAndDisabled(t *testing.T) {
	clicks := 0
	button := Button("Run", func() { clicks++ })
	check := Checkbox("Enabled", false)
	input := TextInput("")
	view := NewTester(Column(button, check, input).Padding(12), 400, 240)
	defer view.Close()
	clickElement(view, button)
	if clicks != 1 {
		t.Fatal(clicks)
	}
	view.Key("Tab", false, false)
	view.Key("Space", false, false)
	if !check.Checked() {
		t.Fatal("keyboard toggle")
	}
	view.Key("Tab", false, false)
	view.Type("你好 Go")
	if input.Text() != "你好 Go" || !view.Editing {
		t.Fatal(input.Text(), view.Editing)
	}
	view.Key("a", false, true)
	view.Type("替换")
	view.Key("z", false, true)
	if input.Text() != "你好 Go" {
		t.Fatal(input.Text())
	}
	button.Disabled(true)
	view.Step()
	clickElement(view, button)
	if clicks != 1 {
		t.Fatal("disabled button clicked")
	}
}

func TestTranslucentTextUsesStraightWidgetColor(t *testing.T) {
	view := NewTester(Canvas(120, 50, func(p *Painter) {
		p.Rect(p.Bounds(), RGB(255, 255, 255))
		p.Label("Alpha", 8, 8, 24, RGBA(255, 0, 0, 128), false)
	}).Fill(), 120, 50)
	defer view.Close()
	img := view.Snapshot()
	changed := 0
	for y := range img.Rect.Dy() {
		for x := range img.Rect.Dx() {
			c := img.RGBAAt(x, y)
			if c.G < 255 {
				changed++
				if c.R != 255 || c.G != c.B || c.G < 127 {
					t.Fatalf("translucent red text blended incorrectly: %v", c)
				}
			}
		}
	}
	if changed < 20 {
		t.Fatal("text did not render")
	}
}

func TestPartialPaintMatchesFullAndIdleSleeps(t *testing.T) {
	label := Text("0").Width(100)
	button := PrimaryButton("Add", nil)
	root := Column(label, button, Card(Text("unchanged"))).Padding(20)
	view := NewTester(root, 420, 250)
	defer view.Close()
	before := view.Document.Stats()
	view.Step()
	view.Step()
	if view.Document.Stats() != before {
		t.Fatal("idle frames")
	}
	label.SetText("12345")
	view.Step()
	partial := bytes.Clone(view.img.Pix)
	if len(view.damage) != 1 || view.damage[0] == view.img.Rect {
		t.Fatalf("not partial: %v", view.damage)
	}
	view.Document.live.needsLayout = true
	view.Document.live.frame(false)
	if !bytes.Equal(partial, view.img.Pix) {
		t.Fatal("partial redraw differs from full redraw")
	}
}

func TestVirtualListAndSelection(t *testing.T) {
	calls := 0
	list := List(100000, func(i int) string { calls++; return "row" })
	view := NewTester(Column(list.Fill()), 400, 300)
	defer view.Close()
	if calls > 12 {
		t.Fatalf("painted %d rows", calls)
	}
	view.Click(20, 18)
	if list.Selected() != 0 {
		t.Fatal(list.Selected())
	}
	view.Key("End", false, false)
	if list.Selected() != 99999 || list.scroll == 0 {
		t.Fatal("keyboard did not reveal final row")
	}
	calls = 0
	view.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 50, DY: -72})
	if calls > 24 {
		t.Fatalf("painted %d rows after scrolling", calls)
	}
}

func TestInputSelectionClipboardAndPassword(t *testing.T) {
	input := TextInput("a中😀b")
	view := NewTester(Column(input), 400, 100)
	defer view.Close()
	clickElement(view, input)
	view.Key("a", false, true)
	view.Key("c", false, true)
	if view.Clipboard() != "a中😀b" {
		t.Fatal(view.Clipboard())
	}
	view.Key("Backspace", false, false)
	view.Key("v", false, true)
	if input.Text() != "a中😀b" {
		t.Fatal(input.Text())
	}
	input.Password(true)
	view.SetClipboard("safe")
	view.Key("a", false, true)
	view.Key("c", false, true)
	if view.Clipboard() != "safe" {
		t.Fatal("password copied")
	}
	view.Key("x", false, true)
	if input.Text() != "a中😀b" {
		t.Fatal("password cut")
	}
}

func TestPopupAndDialogFocus(t *testing.T) {
	selectBox := Select([]string{"one", "two", "three"}, 0)
	view := NewTester(Column(selectBox), 400, 240)
	defer view.Close()
	clickElement(view, selectBox)
	view.Key("ArrowDown", false, false)
	view.Key("Enter", false, false)
	if selectBox.Selected() != 1 {
		t.Fatal(selectBox.Selected())
	}
	button := Button("Inside", nil)
	view.Document.ShowDialog(Card(button).Width(200))
	view.Step()
	view.Key("Tab", false, false)
	if view.Document.live.focused != button {
		t.Fatal("focus escaped dialog")
	}
	view.Key("Escape", false, false)
	if view.Document.live.dialog != nil {
		t.Fatal("escape did not dismiss")
	}
}

func TestPainterClipAndAlpha(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	p := &Painter{dst: img, clip: image.Rect(8, 8, 24, 24), scale: 1}
	p.RoundRect(Rect{0, 0, 32, 32}, 8, RGBA(240, 120, 40, 128))
	p.Line(0, 0, 32, 32, 2, RGB(10, 20, 30))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			if !(image.Pt(x, y).In(p.clip)) && img.RGBAAt(x, y) != (Color{}) {
				t.Fatal("paint escaped clipping", x, y)
			}
		}
	}
}

func TestTreeOwnershipRejectsCycles(t *testing.T) {
	child := Text("child")
	parent := Column(child)
	defer func() {
		if recover() == nil {
			t.Fatal("accepted a cycle")
		}
	}()
	child.Children(parent)
}
