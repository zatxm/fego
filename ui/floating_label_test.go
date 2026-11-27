package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"strings"
	"testing"

	"github.com/zatxm/fego/v26"
)

func TestFloatingLabelEditingAndSelection(t *testing.T) {
	for _, theme := range []Theme{Light, Dark} {
		for _, scale := range []float32{1, 1.25, 2} {
			t.Run(fmt.Sprint(theme.Background, scale), func(t *testing.T) {
				input := TextInput("").Password(true).Placeholder("Old hint").Label("Old name").FloatingLabel("密码")
				selectBox := Select([]string{"", "上海", "Tokyo"}, -1).FloatingLabel("城市")
				doc := View(Column(input, selectBox).Width(200).Padding(7)).Theme(theme)
				h := newScrollScreen(doc, scale, true)
				defer h.Close()
				v := doc.live
				check := func(e *Element, raised bool) {
					t.Helper()
					b := v.floatingTitleBounds(&Painter{clip: v.size}, e, floatingFrame(e.rect))
					if (b.W > 0) != raised {
						t.Fatalf("title bounds %v, want raised %v", b, raised)
					}
					checkScrollPixels(t, h)
				}
				geometry := input.Bounds()
				h.Click(30, 30)
				check(input, true)
				h.Type("密")
				check(input, true)
				h.Type("码")
				inner := v.inputInner(input).pixels(scale)
				for _, d := range h.Damage() {
					if !d.In(inner) {
						t.Fatalf("normal typing repainted outline: %v", d)
					}
				}
				check(input, true)
				for _, n := range h.nodes {
					if n.ID == input.id && (n.Label != "密码" || n.Value != "••") {
						t.Fatalf("password accessibility: %+v", n)
					}
				}
				h.Key("a", false, true)
				h.Key("Backspace", false, false)
				check(input, true)
				h.Key("z", false, true)
				check(input, true)
				input.SetText("")
				h.Step()
				check(input, true)
				if input.Bounds() != geometry {
					t.Fatal("entering and clearing text moved the field")
				}
				h.Key("Tab", false, false)
				h.Key("ArrowDown", false, false) // An option with an empty display value.
				check(selectBox, false)
				h.Key("ArrowDown", false, false)
				check(selectBox, true)
				selectBox.SetSelected(-1)
				h.Step()
				check(selectBox, false)

				input.FloatingLabel("")
				h.Step()
				if input.emptyFieldText() != "Old hint" {
					t.Fatal("original placeholder lost")
				}
				for _, n := range h.nodes {
					if n.ID == input.id && n.Label != "Old name" {
						t.Fatal("original accessible name lost")
					}
				}
			})
		}
	}
}

func TestFloatingLabelGroupAndLongTitle(t *testing.T) {
	combo := ComboBox([]string{"One", "Two"}, "One").FloatingLabel(strings.Repeat("标题", 50))
	h := newScrollScreen(View(Column(combo.Element).Padding(9).Background(RGB(67, 52, 48))).Theme(Dark), 1.25, true)
	defer h.Close()
	group := combo.anchor
	outer := group.rect
	frame := floatingFrame(outer)
	if group.captionControl() != combo.Input || combo.Input.rect.Y < frame.Y {
		t.Fatal("compound editor is not inside the shared outline")
	}
	h.Click(outer.X+18, outer.Y+5)
	if h.Document.live.focused != combo.Input {
		t.Fatal("clicking the caption did not focus the editor")
	}
	checkScrollPixels(t, h)
	snapshot := func(r image.Rectangle) []byte {
		img := image.NewRGBA(r)
		draw.Draw(img, r, h.screen, r.Min, draw.Src)
		return img.Pix
	}
	head := (Rect{outer.X, outer.Y, outer.W, frame.Y - outer.Y + 1}).pixels(h.Scale)
	before := snapshot(head)
	combo.Input.SetText("")
	h.Step()
	checkScrollPixels(t, h)
	if bytes.Equal(before, snapshot(head)) {
		t.Fatal("cleared value left the top caption unchanged")
	}
	combo.Input.SetText("One")
	h.Step()
	checkScrollPixels(t, h)
	if !bytes.Equal(before, snapshot(head)) {
		t.Fatal("filled caption did not return to its original pixels")
	}
}

func TestFloatingLabelRestoresPlainAppearance(t *testing.T) {
	for _, control := range []func() *Element{
		func() *Element { return TextInput("").Placeholder("Hint") },
		func() *Element { return TextArea("Line one\nLine two") },
		func() *Element { return Select([]string{"First", "Second"}, 1) },
	} {
		field := control()
		h := NewTester(Column(field).Padding(10), 260, 180)
		before := h.Snapshot()
		bounds := field.Bounds()
		field.FloatingLabel("Title")
		h.Step()
		if bytes.Equal(before.Pix, h.Snapshot().Pix) {
			t.Fatal("floating mode did not change appearance")
		}
		field.FloatingLabel("")
		h.Step()
		if field.Bounds() != bounds || !bytes.Equal(before.Pix, h.Snapshot().Pix) {
			t.Fatal("turning off floating mode changed the original control")
		}
		h.Close()
	}
}

func TestFloatingLabelSelectPopupAndGroupedCaret(t *testing.T) {
	selected := -1
	field := Select([]string{"A", "B"}, -1).OnSelect(func(i int) { selected = i }).FloatingLabel("Choose")
	grouped := TextInput("x")
	group := InputGroup(grouped, Text("$"), Button("Edit", nil)).FloatingLabel("Amount")
	h := newScrollScreen(View(Column(field, group).Padding(8)), 2, true)
	defer h.Close()
	h.Click(field.rect.X+18, field.rect.Y+22)
	if h.Document.live.popup == nil {
		t.Fatal("select did not open")
	}
	option := h.Document.live.popup.row(1)
	h.Click(option.X+8, option.Y+option.H/2)
	if selected != 1 || field.Selected() != 1 {
		t.Fatal("popup did not select the second value")
	}
	checkScrollPixels(t, h)
	h.Click(grouped.rect.X+12, grouped.rect.Y+18)
	h.Type("2")
	v := h.Document.live
	x, y := v.caretPosition(grouped, v.inputInner(grouped))
	if h.Caret != v.inputCaretRect(grouped, x, y).pixels(h.Scale) || !h.Caret.In(grouped.clip.pixels(h.Scale)) {
		t.Fatal("IME caret outside group editor")
	}
	h.Send(fego.Event{Kind: fego.AccessAction, AccessID: grouped.id, Action: "setValue", Text: ""})
	if grouped.Text() != "" {
		t.Fatal("accessible clear failed")
	}
	checkScrollPixels(t, h)
}
