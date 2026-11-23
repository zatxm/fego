//go:build android || ios

package platform

/*
#include <stdlib.h>
void fe_wake(void);
void fe_upload(int x,int y,int width,int height,int stride,const void *pixels);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"image"
	"math"
	"runtime"
	"slices"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

// After Run publishes the backend, the system UI thread owns the window. Wake
// is the only operation called from worker goroutines.
var mobileActive atomic.Pointer[mobileBackend]

type mobileBackend struct {
	dispatch func()
	window   *mobileWindow
	done     chan struct{}
	quit     bool
}

func New(dispatch func()) (Backend, error) {
	return &mobileBackend{dispatch: dispatch, done: make(chan struct{})}, nil
}
func (b *mobileBackend) Open(o Options, send func(Event)) (Window, error) {
	if b.window != nil {
		return nil, errors.New("fego: a mobile app owns one native window")
	}
	w := &mobileWindow{send: send, backend: b, title: o.Title}
	b.window = w
	return w, nil
}
func (b *mobileBackend) Wake() { C.fe_wake() }
func (b *mobileBackend) Run() error {
	if b.quit {
		return nil
	}
	if b.window == nil || !mobileActive.CompareAndSwap(nil, b) {
		return errors.New("fego: expected one active mobile window")
	}
	b.Wake()
	<-b.done
	return nil
}
func (b *mobileBackend) Quit() { b.quit = true; b.Wake() }

type mobileInput struct {
	Active, Password, Numeric bool
	Text                      string
	Start, End                int
	Caret                     image.Rectangle
}
type mobileState struct {
	Title         *string       `json:",omitempty"`
	Input         *mobileInput  `json:",omitempty"`
	Access        *[]AccessNode `json:",omitempty"`
	Copy          *string       `json:",omitempty"`
	Paste, Finish bool          `json:",omitempty"`
}
type mobileWindow struct {
	send                         func(Event)
	backend                      *mobileBackend
	size                         image.Point
	scale                        float32
	title                        string
	input                        mobileInput
	access                       []AccessNode
	inFrame, accessibility       bool
	pending                      mobileState
	touch, scroll                bool
	startX, startY, lastX, lastY float32
}

func (w *mobileWindow) stateChanged() {
	if !w.inFrame {
		w.backend.Wake()
	}
}

func (w *mobileWindow) SetTitle(title string) {
	w.title = title
	w.pending.Title = &w.title
	w.stateChanged()
}
func (w *mobileWindow) SetSize(int, int)      {}
func (w *mobileWindow) SetCursor(string)      {}
func (w *mobileWindow) Close()                { w.backend.Quit() }
func (w *mobileWindow) SetClipboard(s string) { w.pending.Copy = &s; w.stateChanged() }
func (w *mobileWindow) Clipboard() string     { w.pending.Paste = true; w.stateChanged(); return "" }
func (w *mobileWindow) TextInput(active bool, caret image.Rectangle) {
	if w.input.Active == active && w.input.Caret == caret {
		return
	}
	w.input.Active, w.input.Caret = active, caret
	if !active {
		w.input.Text = ""
		w.input.Start, w.input.End = 0, 0
	}
	w.pending.Input = &w.input
	w.stateChanged()
}
func (w *mobileWindow) SetTextInputState(text string, start, end int, password, numeric bool) {
	s := w.input
	s.Text, s.Start, s.End, s.Password, s.Numeric = text, start, end, password, numeric
	if s != w.input {
		w.input = s
		w.pending.Input = &w.input
		w.stateChanged()
	}
}
func (w *mobileWindow) SetAccessibility(nodes []AccessNode) {
	if slices.Equal(w.access, nodes) {
		return
	}
	w.access = append(w.access[:0], nodes...)
	if w.accessibility {
		w.pending.Access = &w.access
		w.stateChanged()
	}
}
func (w *mobileWindow) PresentRegion(img *image.RGBA) {
	if img == nil {
		return
	}
	r := img.Rect.Intersect(image.Rectangle{Max: w.size})
	if r.Empty() {
		return
	}
	offset := img.PixOffset(r.Min.X, r.Min.Y)
	C.fe_upload(C.int(r.Min.X), C.int(r.Min.Y), C.int(r.Dx()), C.int(r.Dy()), C.int(img.Stride), unsafe.Pointer(&img.Pix[offset]))
	runtime.KeepAlive(img)
}
func (w *mobileWindow) Present(img *image.RGBA, damage []image.Rectangle) {
	if img == nil {
		return
	}
	if len(damage) == 0 {
		w.PresentRegion(img)
		return
	}
	for _, r := range damage {
		if r = r.Intersect(img.Rect); !r.Empty() {
			w.PresentRegion(img.SubImage(r).(*image.RGBA))
		}
	}
}

//export fe_frame
func fe_frame(width, height C.int, scale C.float, reset, accessible C.int) *C.char {
	b := mobileActive.Load()
	if b == nil {
		return nil
	}
	w := b.window
	size := image.Pt(int(width), int(height))
	if size.X <= 0 || size.Y <= 0 {
		return nil
	}
	if w.size == (image.Point{}) {
		reset = 1
	}
	w.inFrame = true
	defer func() { w.inFrame = false }()
	if reset != 0 || accessible == 2 || w.accessibility != (accessible != 0) {
		w.accessibility = accessible != 0
		if w.accessibility {
			w.pending.Access = &w.access
		} else {
			w.pending.Access = nil
		}
	}
	if reset != 0 {
		w.pending.Title = &w.title
		w.pending.Input = &w.input
		w.touch, w.scroll = false, false
	}
	if size != w.size || w.scale != float32(scale) || reset != 0 {
		w.size, w.scale = size, float32(scale)
		w.send(Event{Kind: Resize, Width: size.X, Height: size.Y, Scale: w.scale})
	}
	b.dispatch()
	if b.quit {
		w.pending.Finish = true
	}
	var result *C.char
	if w.pending != (mobileState{}) {
		data, _ := json.Marshal(w.pending)
		result = C.CString(string(data))
		w.pending = mobileState{}
	}
	if b.quit && mobileActive.CompareAndSwap(b, nil) {
		close(b.done)
	}
	return result
}

//export fe_touch
func fe_touch(kind C.int, px, py C.float) {
	b := mobileActive.Load()
	if b == nil {
		return
	}
	w := b.window
	x, y := float32(px), float32(py)
	e := Event{X: x, Y: y, Button: 1}
	switch kind {
	case 0:
		w.touch, w.scroll = true, false
		w.startX, w.startY = x, y
		e.Kind = PointerDown
	case 1:
		if !w.touch {
			return
		}
		dx, dy := math.Abs(float64(x-w.startX)), math.Abs(float64(y-w.startY))
		if !w.scroll && dy > float64(max(1, w.scale)*8) && dy > dx {
			w.send(Event{Kind: PointerCancel})
			w.scroll = true
		}
		e.Kind = PointerMove
		if w.scroll {
			e.Kind = Scroll
			e.DX, e.DY = w.lastX-x, w.lastY-y
			e.X, e.Y = w.startX, w.startY
		}
	case 2, 3:
		if !w.touch {
			return
		}
		w.touch = false
		if w.scroll {
			w.scroll = false
			return
		}
		e.Kind = PointerUp
		if kind == 3 {
			e.Kind = PointerCancel
		}
	default:
		return
	}
	w.lastX, w.lastY = x, y
	w.send(e)
}

func mobileText(ptr *C.char, n C.int) (string, bool) {
	if n < 0 || n > nativeTextMaxBytes || n != 0 && ptr == nil {
		return "", false
	}
	return nativeUTF8Text(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(n)))
}

//export fe_edit
func fe_edit(ptr *C.char, n, start, end C.int) {
	b := mobileActive.Load()
	if b == nil || !b.window.input.Active {
		return
	}
	value, ok := mobileText(ptr, n)
	if !ok {
		return
	}
	w := b.window
	old := w.input
	count := utf8.RuneCountInString(value)
	a, z := max(0, min(count, int(start))), max(0, min(count, int(end)))
	if old.Text == value && old.Start == a && old.End == z {
		return
	}
	// Another IME edit can arrive before the next paint. Compare it to the
	// native editor's latest state. A filtered value is reconciled by the widget.
	w.input.Text, w.input.Start, w.input.End = value, a, z
	if old.Text != value {
		start, end, inserted := nativeTextChange(old.Text, value)
		w.send(Event{Kind: TextEdit, Start: start, End: end, Text: inserted})
	}
	w.send(Event{Kind: TextSelection, Start: a, End: z})
}

//export fe_pointer
func fe_pointer(kind C.int, x, y, dx, dy C.float, flags C.int) {
	b := mobileActive.Load()
	if b == nil {
		return
	}
	kinds := [...]EventKind{PointerDown, PointerMove, PointerUp, PointerCancel, Scroll}
	if kind < 0 || int(kind) >= len(kinds) {
		return
	}
	b.window.send(Event{Kind: kinds[kind], X: float32(x), Y: float32(y), DX: float32(dx), DY: float32(dy), Button: 1, Shift: flags&1 != 0, Control: flags&2 != 0, Alt: flags&4 != 0, Super: flags&8 != 0})
}

//export fe_event
func fe_event(kind C.int, ptr *C.char, n, flags C.int, id C.longlong) {
	b := mobileActive.Load()
	if b == nil {
		return
	}
	s, ok := mobileText(ptr, n)
	if !ok {
		return
	}
	e := Event{Shift: flags&1 != 0, Control: flags&2 != 0, Alt: flags&4 != 0, Super: flags&8 != 0}
	switch kind {
	case 0, 1:
		e.Kind = KeyDown
		if kind == 1 {
			e.Kind = KeyUp
		}
		e.Key = s
	case 2:
		e.Kind = Text
		e.Text = s
	case 3:
		e.Kind = Focus
		e.Focused = flags != 0
	case 4:
		e.Kind = AccessAction
		e.AccessID = uint64(id)
		e.Action = s
	case 5:
		e.Kind = Close
	case 6:
		e.Kind = Close
		e.Destroyed = true
	default:
		return
	}
	b.window.send(e)
	if kind == 6 && mobileActive.CompareAndSwap(b, nil) {
		close(b.done)
	}
}
