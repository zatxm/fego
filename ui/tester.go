package ui

import (
	"image"

	"github.com/zatxm/fego/v26"
)

// Tester drives the same layout, paint and input code as a native window.
// It never starts a native backend or a periodic frame loop.
type Tester struct {
	Document      *Document
	Width, Height int
	Scale         float32
	clipboard     string
	wanted        bool
	img           *image.RGBA
	damage        []image.Rectangle
	Caret         image.Rectangle
	Editing       bool
}

func NewTester(root *Element, width, height int) *Tester {
	return NewDocumentTester(View(root), width, height)
}
func NewDocumentTester(doc *Document, width, height int) *Tester {
	t := &Tester{Document: doc, Width: width, Height: height, Scale: 1}
	t.Document.Mount(t)
	t.Document.live.frame(false)
	return t
}
func (t *Tester) Close() {
	if t.Document.live != nil {
		t.Document.live.Close()
	}
}
func (t *Tester) Size() (int, int, float32) { return t.Width, t.Height, t.Scale }
func (t *Tester) Present(img *image.RGBA, damage []image.Rectangle) {
	t.img = img
	t.damage = append(t.damage[:0], damage...)
}
func (t *Tester) Invalidate()                                  { t.wanted = true }
func (t *Tester) SetCursor(string)                             {}
func (t *Tester) SetClipboard(s string)                        { t.clipboard = s }
func (t *Tester) Clipboard() string                            { return t.clipboard }
func (t *Tester) TextInput(active bool, caret image.Rectangle) { t.Editing, t.Caret = active, caret }
func (t *Tester) Step() {
	if t.wanted {
		t.wanted = false
		t.Document.live.frame(false)
	}
}
func (t *Tester) Send(e fego.Event) { t.Document.live.Event(e); t.Step() }
func (t *Tester) Click(x, y float32) {
	t.Send(fego.Event{Kind: fego.PointerDown, X: x * t.Scale, Y: y * t.Scale, Button: 1})
	t.Send(fego.Event{Kind: fego.PointerUp, X: x * t.Scale, Y: y * t.Scale, Button: 1})
}
func (t *Tester) Key(key string, shift, control bool) {
	t.Send(fego.Event{Kind: fego.KeyDown, Key: key, Shift: shift, Control: control})
}
func (t *Tester) Type(text string) { t.Send(fego.Event{Kind: fego.Text, Text: text}) }
func (t *Tester) Snapshot() *image.RGBA {
	t.Step()
	copy := image.NewRGBA(t.img.Rect)
	copy.Pix = append(copy.Pix[:0], t.img.Pix...)
	return copy
}
func (t *Tester) Damage() []image.Rectangle { return append([]image.Rectangle(nil), t.damage...) }
