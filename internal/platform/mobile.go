//go:build android || ios

package platform

import (
	"errors"
	"image"
	"image/draw"
	"io"
	"math"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	gio "gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

type mobileBackend struct {
	notify func()
	native gio.Window
	window *mobileWindow // initialized before Run starts handing control to the event goroutine
	quit   atomic.Bool
}

func New(dispatch func()) (Backend, error) {
	return &mobileBackend{notify: dispatch}, nil
}

func (b *mobileBackend) Open(o Options, send func(Event)) (Window, error) {
	if b.window != nil {
		return nil, errors.New("fego: a mobile app owns one native window")
	}
	b.native.Option(gio.Title(o.Title))
	b.window = &mobileWindow{backend: b, send: send}
	return b.window, nil
}

func (b *mobileBackend) Wake() { b.native.Invalidate() }

func (b *mobileBackend) Quit() {
	if b.quit.CompareAndSwap(false, true) {
		b.native.Perform(system.ActionClose)
	}
}

func (b *mobileBackend) Run() error {
	if b.window == nil {
		return errors.New("fego: a mobile app needs a window")
	}
	if b.quit.Load() {
		return nil
	}
	go b.window.run()
	gio.Main()
	return nil
}

type mobileTile struct {
	pos   image.Point
	image paint.ImageOp
}

type mobileWindow struct {
	backend                       *mobileBackend
	send                          func(Event)
	size                          image.Point
	scale                         float32
	tiles                         []mobileTile
	surface                       image.Point
	access                        []AccessNode
	textActive, keyboard, closing bool
	caret                         image.Rectangle
	text                          string
	start, end                    int
	hint                          key.InputHint
	touchID                       pointer.ID
	touchDown, touchScroll        bool
	origin, last                  f32.Point
	clipboardText                 string
	writeClipboard, readClipboard bool
}

const mobileTileSize = 256

func (w *mobileWindow) Present(img *image.RGBA, damage []image.Rectangle) {
	if len(damage) == 0 {
		return
	}
	cols := (img.Rect.Dx() + mobileTileSize - 1) / mobileTileSize
	rows := (img.Rect.Dy() + mobileTileSize - 1) / mobileTileSize
	if w.surface != img.Rect.Size() {
		w.surface = img.Rect.Size()
		w.tiles = make([]mobileTile, cols*rows)
		damage = []image.Rectangle{img.Rect}
	}
	for _, r := range damage {
		r = r.Intersect(img.Rect).Sub(img.Rect.Min)
		if r.Empty() {
			continue
		}
		for y := r.Min.Y / mobileTileSize; y <= (r.Max.Y-1)/mobileTileSize; y++ {
			for x := r.Min.X / mobileTileSize; x <= (r.Max.X-1)/mobileTileSize; x++ {
				pos := image.Pt(x*mobileTileSize, y*mobileTileSize)
				bounds := image.Rectangle{Min: pos, Max: pos.Add(image.Pt(mobileTileSize, mobileTileSize))}.Add(img.Rect.Min).Intersect(img.Rect)
				cp := image.NewRGBA(image.Rectangle{Max: bounds.Size()})
				draw.Draw(cp, cp.Rect, img, bounds.Min, draw.Src)
				w.tiles[y*cols+x] = mobileTile{pos: pos, image: paint.NewImageOp(cp)}
			}
		}
	}
}

func (w *mobileWindow) SetTitle(title string) { w.backend.native.Option(gio.Title(title)) }
func (w *mobileWindow) SetSize(int, int)      {}
func (w *mobileWindow) SetCursor(string)      {}
func (w *mobileWindow) SetClipboard(s string) { w.clipboardText = s; w.writeClipboard = true }
func (w *mobileWindow) Clipboard() string     { w.readClipboard = true; return "" }
func (w *mobileWindow) TextInput(active bool, caret image.Rectangle) {
	w.textActive, w.caret = active, caret
	if !active {
		w.text = ""
	}
}
func (w *mobileWindow) SetTextInputState(text string, start, end int, password, numeric bool) {
	w.hint = key.HintText
	switch {
	case password:
		w.hint = key.HintPassword
		text = strings.Repeat("•", utf8.RuneCountInString(text))
	case numeric:
		w.hint = key.HintNumeric
	}
	w.text, w.start, w.end = text, start, end
}
func (w *mobileWindow) SetAccessibility(nodes []AccessNode) {
	w.access = append(w.access[:0], nodes...)
}
func (w *mobileWindow) Close() {
	if !w.closing {
		w.closing = true
		w.backend.Quit()
	}
}

func (w *mobileWindow) run() {
	var ops op.Ops
	for {
		switch e := w.backend.native.Event().(type) {
		case gio.FrameEvent:
			w.frame(&ops, e)
		case *gio.ClosingEvent:
			w.send(Event{Kind: Close})
			if !w.closing {
				e.Abort()
			}
		case gio.DestroyEvent:
			w.closing = true
			w.send(Event{Kind: Close, Destroyed: true})
			return
		}
	}
}

const mobileClipboard = "application/text"

func (w *mobileWindow) frame(ops *op.Ops, frame gio.FrameEvent) {
	context := gio.NewContext(ops, frame)
	size := context.Constraints.Max
	if size.X > 0 && size.Y > 0 && (w.size != size || w.scale != frame.Metric.PxPerDp) {
		w.size, w.scale = size, frame.Metric.PxPerDp
		w.send(Event{Kind: Resize, Width: size.X, Height: size.Y, Scale: w.scale})
	}
	w.backend.notify()
	area := clip.Rect{Max: size}.Push(ops)
	event.Op(ops, w)
	src := frame.Source
	pointers := pointer.Filter{Target: w, Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll | pointer.Cancel,
		ScrollX: pointer.ScrollRange{Min: -4096, Max: 4096}, ScrollY: pointer.ScrollRange{Min: -4096, Max: 4096}}
	focus := key.FocusFilter{Target: w}
	allKeys := key.Filter{Optional: key.ModCtrl | key.ModCommand | key.ModShift | key.ModAlt | key.ModSuper}
	tab := allKeys
	tab.Name = key.NameTab
	paste := transfer.TargetFilter{Target: w, Type: mobileClipboard}
	for {
		ev, ok := src.Event(pointers, focus, allKeys, tab, paste)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			w.pointer(e)
		case key.Event:
			kind := KeyDown
			if e.State == key.Release {
				kind = KeyUp
			}
			w.send(mobileKey(kind, keyName(e.Name), e.Modifiers))
		case key.EditEvent:
			if w.textActive {
				w.send(Event{Kind: TextEdit, Start: e.Range.Start, End: e.Range.End, Text: e.Text})
			}
		case key.SelectionEvent:
			if w.textActive {
				w.send(Event{Kind: TextSelection, Start: e.Start, End: e.End})
			}
		case transfer.DataEvent:
			reader := e.Open()
			data, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
			_ = reader.Close()
			if err == nil && len(data) <= 4<<20 && w.textActive {
				w.send(Event{Kind: Text, Text: string(data)})
			}
		}
	}
	w.backend.notify()
	if w.textActive != w.keyboard {
		w.keyboard = w.textActive
		if w.keyboard {
			src.Execute(key.FocusCmd{Tag: w})
		}
		src.Execute(key.SoftKeyboardCmd{Show: w.keyboard})
	}
	if w.textActive {
		key.InputHintOp{Tag: w, Hint: w.hint}.Add(ops)
		src.Execute(key.SelectionCmd{Tag: w, Range: key.Range{Start: w.start, End: w.end},
			Caret: key.Caret{Pos: f32.Pt(float32(w.caret.Min.X), float32(w.caret.Max.Y)), Ascent: float32(w.caret.Dy())}})
		src.Execute(key.SnippetCmd{Tag: w, Snippet: key.Snippet{Range: key.Range{End: utf8.RuneCountInString(w.text)}, Text: w.text}})
	}
	if w.writeClipboard {
		src.Execute(clipboard.WriteCmd{Type: mobileClipboard, Data: io.NopCloser(strings.NewReader(w.clipboardText))})
		w.writeClipboard = false
	}
	if w.readClipboard {
		src.Execute(clipboard.ReadCmd{Tag: w})
		w.readClipboard = false
	}
	for _, tile := range w.tiles {
		translate := op.Offset(tile.pos).Push(ops)
		tile.image.Add(ops)
		paint.PaintOp{}.Add(ops)
		translate.Pop()
	}
	for _, n := range w.access {
		node := clip.Rect(n.Bounds).Push(ops)
		semantic.LabelOp(n.Label).Add(ops)
		semantic.DescriptionOp(n.Value).Add(ops)
		semantic.SelectedOp(n.Selected || n.Checked).Add(ops)
		semantic.EnabledOp(!n.Disabled).Add(ops)
		switch n.Role {
		case "button":
			semantic.Button.Add(ops)
		case "checkbox":
			semantic.CheckBox.Add(ops)
		case "input":
			semantic.Editor.Add(ops)
		case "radio":
			semantic.RadioButton.Add(ops)
		case "switch":
			semantic.Switch.Add(ops)
		}
		node.Pop()
	}
	area.Pop()
	frame.Frame(ops)
}

func (w *mobileWindow) pointer(p pointer.Event) {
	e := mobileKey(PointerMove, "", p.Modifiers)
	e.X, e.Y, e.Button = p.Position.X, p.Position.Y, 1
	if p.Source == pointer.Touch {
		if p.Kind == pointer.Press {
			if w.touchDown {
				return
			}
			w.touchID, w.touchDown, w.touchScroll = p.PointerID, true, false
			w.origin, w.last = p.Position, p.Position
		} else if !w.touchDown || p.PointerID != w.touchID {
			return
		}
		if p.Kind == pointer.Drag {
			dx := float64(p.Position.X - w.origin.X)
			dy := float64(p.Position.Y - w.origin.Y)
			if !w.touchScroll && math.Abs(dy) > float64(max(1, w.scale)*8) && math.Abs(dy) > math.Abs(dx) {
				w.touchScroll = true
				w.send(Event{Kind: PointerCancel})
			}
			if w.touchScroll {
				e.Kind, e.DY = Scroll, w.last.Y-p.Position.Y
				e.X, e.Y = w.origin.X, w.origin.Y
				w.last = p.Position
				w.send(e)
				return
			}
			w.last = p.Position
		}
		if p.Kind == pointer.Release || p.Kind == pointer.Cancel {
			w.touchDown = false
			if w.touchScroll {
				return
			}
		}
	}
	switch p.Kind {
	case pointer.Press:
		e.Kind = PointerDown
	case pointer.Release:
		e.Kind = PointerUp
	case pointer.Cancel:
		e.Kind = PointerCancel
	case pointer.Scroll:
		e.Kind, e.DX, e.DY = Scroll, p.Scroll.X, p.Scroll.Y
	}
	w.send(e)
}

func mobileKey(kind EventKind, name string, mods key.Modifiers) Event {
	return Event{Kind: kind, Key: name, Shift: mods.Contain(key.ModShift), Control: mods.Contain(key.ModCtrl),
		Alt: mods.Contain(key.ModAlt), Super: mods.Contain(key.ModCommand) || mods.Contain(key.ModSuper)}
}

func keyName(n key.Name) string {
	switch n {
	case key.NameLeftArrow:
		return "ArrowLeft"
	case key.NameRightArrow:
		return "ArrowRight"
	case key.NameUpArrow:
		return "ArrowUp"
	case key.NameDownArrow:
		return "ArrowDown"
	case key.NameReturn, key.NameEnter:
		return "Enter"
	case key.NameEscape, key.NameBack:
		return "Escape"
	case key.NameDeleteBackward:
		return "Backspace"
	case key.NameDeleteForward:
		return "Delete"
	case key.NamePageUp:
		return "PageUp"
	case key.NamePageDown:
		return "PageDown"
	case key.NameHome:
		return "Home"
	case key.NameEnd:
		return "End"
	default:
		return string(n)
	}
}
