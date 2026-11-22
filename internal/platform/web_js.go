package platform

import (
	"errors"
	"image"
	"math"
	"strconv"
	"strings"
	"syscall/js"
	"unicode/utf8"
)

type webBackend struct {
	dispatch func()
	done     chan struct{}
	windows  map[*webWindow]struct{}
	raf      js.Func
	rafID    int
	stopped  bool
}

func New(dispatch func()) (Backend, error) {
	if js.Global().Get("document").IsUndefined() {
		return nil, errors.New("fego: browser WASM needs a document")
	}
	b := &webBackend{dispatch: dispatch, done: make(chan struct{}), windows: make(map[*webWindow]struct{})}
	b.raf = js.FuncOf(func(js.Value, []js.Value) any {
		b.rafID = 0
		b.dispatch()
		for w := range b.windows {
			w.flushGPU()
		}
		return nil
	})
	return b, nil
}

func (b *webBackend) Wake() {
	// Go's browser target runs on one JavaScript thread. Set the flag before
	// entering JavaScript so work posted during dispatch can request a frame.
	if b.rafID == 0 && !b.stopped {
		b.rafID = js.Global().Call("requestAnimationFrame", b.raf).Int()
	}
}

func (b *webBackend) Run() error { <-b.done; b.raf.Release(); return nil }
func (b *webBackend) Quit() {
	if !b.stopped {
		b.stopped = true
		if b.rafID != 0 {
			js.Global().Call("cancelAnimationFrame", b.rafID)
		}
		close(b.done)
	}
}

type webListener struct {
	node js.Value
	name string
	fn   js.Func
}

type webWindow struct {
	backend                    *webBackend
	send                       func(Event)
	root, canvas, input, ctx   js.Value
	listeners                  []webListener
	pixels, data               js.Value
	dataWidth, dataHeight      int
	width, height              int
	scale                      float64
	clipboard                  string
	resizeObserver             js.Value
	resizeCallback             js.Func
	accessRoot                 js.Value
	accessNodes                map[uint64]webAccessNode
	dragOrigin, dragLast       [2]float32
	dragScrolling              bool
	composing, closed, editing bool
	gpu                        *webGL
	gpuRequested               bool
}

func (b *webBackend) Open(o Options, send func(Event)) (Window, error) {
	d := js.Global().Get("document")
	mount := d.Call("getElementById", "fego")
	if mount.IsNull() {
		mount = d.Get("body")
	}
	if mount.IsNull() {
		return nil, errors.New("fego: browser document body is unavailable")
	}
	root, canvas, input := d.Call("createElement", "div"), d.Call("createElement", "canvas"), d.Call("createElement", "textarea")
	root.Get("style").Set("cssText", "position:relative;display:block;max-width:100%;overflow:hidden;")
	canvas.Get("style").Set("cssText", "display:block;width:100%;height:100%;touch-action:none;outline:none;")
	canvas.Set("tabIndex", 0)
	canvas.Call("setAttribute", "role", "application")
	input.Get("style").Set("cssText", "position:absolute;opacity:0;width:1px;height:1px;padding:0;border:0;resize:none;pointer-events:none;")
	input.Set("tabIndex", -1)
	input.Call("setAttribute", "aria-label", "Text input")
	input.Call("setAttribute", "autocapitalize", "off")
	input.Call("setAttribute", "autocomplete", "off")
	input.Set("spellcheck", false)
	ctx := canvas.Call("getContext", "2d", map[string]any{"alpha": false})
	if ctx.IsNull() {
		return nil, errors.New("fego: Canvas 2D is unavailable")
	}
	root.Call("append", canvas, input)
	mount.Call("appendChild", root)
	w := &webWindow{backend: b, send: send, root: root, canvas: canvas, input: input, ctx: ctx, scale: 1}
	b.windows[w] = struct{}{}
	w.SetTitle(o.Title)
	if mount.Call("hasAttribute", "data-fego-fill").Bool() {
		root.Get("style").Set("width", "100%")
		root.Get("style").Set("height", "100%")
	} else {
		w.setCSSSize(o.Width, o.Height)
	}
	w.bind()
	w.resize()
	if ctor := js.Global().Get("ResizeObserver"); !ctor.IsUndefined() {
		w.resizeCallback = js.FuncOf(func(js.Value, []js.Value) any { w.resize(); return nil })
		w.resizeObserver = ctor.New(w.resizeCallback)
		w.resizeObserver.Call("observe", w.root)
	}
	return w, nil
}

func (w *webWindow) resize() {
	if w.closed {
		return
	}
	dpr := max(1, js.Global().Get("devicePixelRatio").Float())
	bw, bh := w.root.Get("clientWidth").Float(), w.root.Get("clientHeight").Float()
	if bw == 0 || bh == 0 {
		return
	}
	dpr = min(dpr, 16384/max(bw, bh), math.Sqrt(float64(64<<20)/(bw*bh)))
	width, height := int(bw*dpr), int(bh*dpr)
	if width == w.width && height == w.height && dpr == w.scale {
		return
	}
	w.width, w.height, w.scale = width, height, dpr
	w.canvas.Set("width", width)
	w.canvas.Set("height", height)
	if w.gpu != nil {
		if !w.gpu.resize(width, height) {
			w.closeGPU()
		}
	}
	w.send(Event{Kind: Resize, Width: width, Height: height, Scale: float32(dpr)})
}

func (w *webWindow) listen(node js.Value, name string, fn func(js.Value)) {
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		fn(args[0])
		return nil
	})
	node.Call("addEventListener", name, callback, map[string]any{"passive": false})
	w.listeners = append(w.listeners, webListener{node, name, callback})
}

func (w *webWindow) bind() {
	w.listen(js.Global(), "resize", func(js.Value) { w.resize() })
	for name, kind := range map[string]EventKind{
		"pointermove": PointerMove, "pointerdown": PointerDown, "pointerup": PointerUp,
		"pointercancel": PointerCancel, "wheel": Scroll,
	} {
		w.listen(w.root, name, func(e js.Value) {
			if e.Get("isPrimary").Type() == js.TypeBoolean && !e.Get("isPrimary").Bool() {
				return
			}
			r := w.root.Call("getBoundingClientRect")
			ev := webModifiers(e)
			ev.Kind = kind
			ev.X = float32((e.Get("clientX").Float() - r.Get("left").Float()) * w.scale)
			ev.Y = float32((e.Get("clientY").Float() - r.Get("top").Float()) * w.scale)
			if kind == Scroll {
				scale := w.scale
				switch e.Get("deltaMode").Int() {
				case 1:
					scale *= 32
				case 2:
					scale = float64(w.height)
				}
				ev.DX, ev.DY = float32(e.Get("deltaX").Float()*scale), float32(e.Get("deltaY").Float()*scale)
			} else {
				ev.Button = e.Get("button").Int() + 1
				if ev.Button == 2 {
					ev.Button = 3
				} else if ev.Button == 3 {
					ev.Button = 2
				}
				if kind == PointerDown {
					if !w.editing {
						w.focus(w.canvas)
					}
					if e.Get("isTrusted").Bool() {
						w.root.Call("setPointerCapture", e.Get("pointerId"))
					}
				}
			}
			e.Call("preventDefault")
			if e.Get("pointerType").String() == "touch" && kind != Scroll {
				switch kind {
				case PointerDown:
					w.dragOrigin = [2]float32{ev.X, ev.Y}
					w.dragLast = w.dragOrigin
					w.dragScrolling = false
				case PointerMove:
					dx, dy := float64(ev.X-w.dragOrigin[0]), float64(ev.Y-w.dragOrigin[1])
					if !w.dragScrolling && math.Hypot(dx, dy) > 8*w.scale {
						w.dragScrolling = true
						w.send(Event{Kind: PointerCancel})
					}
					if w.dragScrolling {
						x, y := ev.X, ev.Y
						ev.Kind, ev.X, ev.Y = Scroll, w.dragOrigin[0], w.dragOrigin[1]
						ev.DX, ev.DY = w.dragLast[0]-x, w.dragLast[1]-y
						w.dragLast = [2]float32{x, y}
					} else {
						w.dragLast = [2]float32{ev.X, ev.Y}
					}
				case PointerUp, PointerCancel:
					if w.dragScrolling {
						w.dragScrolling = false
						return
					}
				}
			}
			w.send(ev)
		})
	}
	w.listen(w.root, "pointerleave", func(e js.Value) {
		if e.Get("buttons").Int() == 0 {
			w.send(Event{Kind: PointerMove, X: -1, Y: -1})
		}
	})
	w.listen(w.root, "contextmenu", func(e js.Value) { e.Call("preventDefault") })
	w.listen(w.root, "focusin", func(e js.Value) {
		if p := e.Get("relatedTarget"); p.IsNull() || !w.root.Call("contains", p).Bool() {
			w.send(Event{Kind: Focus, Focused: true})
		}
	})
	w.listen(w.root, "focusout", func(e js.Value) {
		if p := e.Get("relatedTarget"); p.IsNull() || !w.root.Call("contains", p).Bool() {
			w.send(Event{Kind: Focus})
		}
	})
	w.listen(w.input, "compositionstart", func(js.Value) { w.composing = true })
	w.listen(w.input, "compositionend", func(e js.Value) {
		w.composing = false
		w.input.Set("value", "")
		w.send(Event{Kind: Text, Text: e.Get("data").String()})
	})
	w.listen(w.input, "beforeinput", func(e js.Value) {
		if w.composing {
			return
		}
		key := ""
		switch e.Get("inputType").String() {
		case "deleteContentBackward":
			key = "Backspace"
		case "deleteContentForward":
			key = "Delete"
		case "insertLineBreak", "insertParagraph":
			key = "Enter"
		}
		if key != "" {
			e.Call("preventDefault")
			w.send(Event{Kind: KeyDown, Key: key})
		}
	})
	w.listen(w.input, "input", func(e js.Value) {
		if w.composing {
			return
		}
		if e.Get("inputType").String() == "insertFromComposition" {
			w.input.Set("value", "")
			return
		}
		value := w.input.Get("value").String()
		w.input.Set("value", "")
		if value != "" {
			w.send(Event{Kind: Text, Text: value})
		}
	})
	w.listen(w.root, "paste", func(e js.Value) {
		e.Call("preventDefault")
		value := e.Get("clipboardData").Call("getData", "text/plain").String()
		w.send(Event{Kind: Text, Text: value})
	})
	for name, kind := range map[string]EventKind{"keydown": KeyDown, "keyup": KeyUp} {
		w.listen(w.root, name, func(e js.Value) {
			if w.composing || e.Get("isComposing").Truthy() {
				return
			}
			ev := webModifiers(e)
			if e.Call("getModifierState", "AltGraph").Bool() {
				ev.Alt, ev.Control = false, false
			}
			ev.Kind, ev.Key = kind, e.Get("key").String()
			if (ev.Control || ev.Super) && strings.EqualFold(ev.Key, "v") {
				return
			}
			switch ev.Key {
			case " ":
				ev.Key = "Space"
			case "Esc":
				ev.Key = "Escape"
			}
			if len(ev.Key) == 1 && (ev.Control || ev.Super) {
				ev.Key = strings.ToLower(ev.Key)
			}
			if !w.editing || ev.Control || ev.Super || (utf8.RuneCountInString(e.Get("key").String()) != 1 && ev.Key != "Dead") {
				e.Call("preventDefault")
			}
			w.send(ev)
		})
	}
}

func webModifiers(e js.Value) Event {
	return Event{Shift: e.Get("shiftKey").Truthy(), Alt: e.Get("altKey").Truthy(),
		Control: e.Get("ctrlKey").Truthy(), Super: e.Get("metaKey").Truthy()}
}

func (w *webWindow) focus(node js.Value) { node.Call("focus", map[string]any{"preventScroll": true}) }

func (w *webWindow) ScrollPixels(r image.Rectangle, delta image.Point) bool {
	if w.closed || w.GPUActive() {
		return false
	}
	from := r.Intersect(r.Sub(delta))
	if from.Empty() {
		return false
	}
	dst := from.Add(delta)
	w.ctx.Call("drawImage", w.canvas, from.Min.X, from.Min.Y, from.Dx(), from.Dy(), dst.Min.X, dst.Min.Y, dst.Dx(), dst.Dy())
	return true
}

func (w *webWindow) Present(img *image.RGBA, damage []image.Rectangle) {
	if len(damage) == 0 {
		w.PresentRegion(img)
		return
	}
	for _, r := range damage {
		r = r.Intersect(img.Bounds())
		if !r.Empty() {
			w.PresentRegion(img.SubImage(r).(*image.RGBA))
		}
	}
}

func (w *webWindow) PresentRegion(img *image.RGBA) {
	r := img.Bounds()
	if w.closed || r.Empty() {
		return
	}
	if w.gpu != nil && w.gpu.lost() {
		w.closeGPU()
		// Repaint pixels that were owned by the lost context.
		w.backend.dispatchResizePaint(w)
	}
	rows := max(1, (512<<10)/(4*r.Dx()))
	for y := r.Min.Y; y < r.Max.Y; y += rows {
		h := min(rows, r.Max.Y-y)
		bytes := 4 * r.Dx() * h
		if bytes > w.pixelsCapacity() {
			w.pixels = js.Global().Get("Uint8ClampedArray").New(bytes)
			w.data = js.Undefined()
		}
		pixels := w.pixels.Call("subarray", 0, bytes)
		start := img.PixOffset(r.Min.X, y)
		if img.Stride == 4*r.Dx() {
			js.CopyBytesToJS(pixels, img.Pix[start:start+bytes])
		} else {
			stride := 4 * r.Dx()
			for row := 0; row < h; row++ {
				js.CopyBytesToJS(pixels.Call("subarray", row*stride, (row+1)*stride),
					img.Pix[start:start+stride])
				start += img.Stride
			}
		}
		if w.gpu != nil {
			w.gpu.upload(pixels, r.Min.X, y, r.Dx(), h)
			w.backend.Wake()
		} else {
			if w.data.IsUndefined() || w.dataWidth != r.Dx() || w.dataHeight != h {
				w.data = js.Global().Get("ImageData").New(pixels, r.Dx(), h)
				w.dataWidth, w.dataHeight = r.Dx(), h
			}
			w.ctx.Call("putImageData", w.data, r.Min.X, y)
		}
	}
}

func (b *webBackend) dispatchResizePaint(w *webWindow) {
	var repaint js.Func
	repaint = js.FuncOf(func(js.Value, []js.Value) any {
		repaint.Release()
		if !w.closed {
			w.send(Event{Kind: Paint})
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", repaint)
}

func (w *webWindow) pixelsCapacity() int {
	if w.pixels.IsUndefined() {
		return 0
	}
	return w.pixels.Length()
}

func (w *webWindow) SetTitle(title string) {
	w.canvas.Call("setAttribute", "aria-label", title)
	js.Global().Get("document").Set("title", title)
}
func (w *webWindow) setCSSSize(width, height int) {
	w.root.Get("style").Set("width", strconv.Itoa(width)+"px")
	w.root.Get("style").Set("height", strconv.Itoa(height)+"px")
}
func (w *webWindow) SetSize(width, height int) { w.setCSSSize(width, height); w.resize() }
func (w *webWindow) SetCursor(cursor string)   { w.root.Get("style").Set("cursor", cursor) }
func (w *webWindow) Clipboard() string         { return w.clipboard }
func (w *webWindow) SetClipboard(value string) {
	w.clipboard = value
	if clipboard := js.Global().Get("navigator").Get("clipboard"); !clipboard.IsUndefined() {
		promise := clipboard.Call("writeText", value)
		promise.Call("catch", js.Global().Get("Boolean"))
	}
}
func (w *webWindow) TextInput(active bool, caret image.Rectangle) {
	if active {
		w.input.Get("style").Set("left", strconv.Itoa(int(float64(caret.Min.X)/w.scale))+"px")
		w.input.Get("style").Set("top", strconv.Itoa(int(float64(caret.Min.Y)/w.scale))+"px")
		if !w.editing {
			w.focus(w.input)
		}
	} else if w.editing && js.Global().Get("document").Get("activeElement").Equal(w.input) {
		w.focus(w.canvas)
	}
	w.editing = active
}
func (w *webWindow) SetTextInputState(_ string, _, _ int, password, numeric bool) {
	mode := "text"
	if numeric {
		mode = "numeric"
	}
	if w.input.Call("getAttribute", "inputmode").String() != mode {
		w.input.Call("setAttribute", "inputmode", mode)
	}
	correction := "on"
	if password {
		correction = "off"
	}
	if w.input.Call("getAttribute", "autocorrect").String() != correction {
		w.input.Call("setAttribute", "autocorrect", correction)
	}
}

func (w *webWindow) Close() {
	if w.closed {
		return
	}
	w.closed = true
	w.closeGPU()
	if !w.resizeObserver.IsUndefined() {
		w.resizeObserver.Call("disconnect")
		w.resizeCallback.Release()
	}
	for _, l := range w.listeners {
		l.node.Call("removeEventListener", l.name, l.fn)
		l.fn.Release()
	}
	w.listeners = nil
	w.root.Call("remove")
	delete(w.backend.windows, w)
}
