// Package fego runs native desktop windows with UI implemented in Go.
package fego

import (
	"errors"
	"image"
	"runtime"
	"sync"

	"github.com/zatxm/fego/internal/platform"
)

func init() { runtime.LockOSThread() }

type Event = platform.Event
type EventKind = platform.EventKind
type AccessNode = platform.AccessNode

const (
	Paint        = platform.Paint
	Resize       = platform.Resize
	PointerMove  = platform.PointerMove
	PointerDown  = platform.PointerDown
	PointerUp    = platform.PointerUp
	Scroll       = platform.Scroll
	KeyDown      = platform.KeyDown
	KeyUp        = platform.KeyUp
	Text         = platform.Text
	Focus        = platform.Focus
	Close        = platform.Close
	AccessAction = platform.AccessAction
)

// Host methods are called on the UI thread. Sizes and events use device pixels.
type Host interface {
	Size() (width, height int, scale float32)
	Present(*image.RGBA, []image.Rectangle)
	Invalidate()
	SetCursor(string)
	SetClipboard(string)
	Clipboard() string
	TextInput(bool, image.Rectangle)
}

type Content interface{ Mount(Host) View }
type View interface {
	Event(Event)
	Close()
}

type WindowOptions struct {
	Title         string
	Width, Height int // initial logical pixels
	Content       Content
}

type Application struct {
	mu               sync.Mutex
	queue            []func()
	spareQueue       []func()
	backend          platform.Backend
	ready            []func()
	windows          map[*Window]struct{}
	running, stopped bool
	err              error
}

func NewApp() *Application { return &Application{windows: make(map[*Window]struct{})} }

// App is the default application. Run must be called from main.
var App = NewApp()

func (a *Application) WhenReady(fn func()) { a.ready = append(a.ready, fn) }

func (a *Application) Run() error {
	a.mu.Lock()
	if a.running || a.stopped {
		a.mu.Unlock()
		return errors.New("fego: an application runs once")
	}
	a.running = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.stopped = true
		a.queue = nil
		a.spareQueue = nil
		a.mu.Unlock()
		for w := range a.windows {
			w.destroy()
		}
	}()
	b, err := platform.New(a.dispatch)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.backend = b
	a.mu.Unlock()
	for _, fn := range a.ready {
		fn()
	}
	a.ready = nil
	if a.err == nil && len(a.windows) == 0 {
		a.err = errors.New("fego: no window opened in WhenReady")
	}
	a.dispatch()
	if a.err != nil {
		b.Quit()
	}
	return errors.Join(a.err, b.Run())
}

// Post queues work from any goroutine without blocking the UI thread.
// It returns false after shutdown.
func (a *Application) Post(fn func()) bool {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return false
	}
	a.queue = append(a.queue, fn)
	b := a.backend
	a.mu.Unlock()
	if b != nil {
		b.Wake()
	}
	return true
}

func (a *Application) dispatch() {
	a.mu.Lock()
	q := a.queue
	a.queue, a.spareQueue = a.spareQueue, nil
	a.mu.Unlock()
	for _, fn := range q {
		fn()
	}
	clear(q)
	a.mu.Lock()
	if !a.stopped {
		a.spareQueue = q[:0]
	}
	a.mu.Unlock()
}

func (a *Application) Quit() { a.Post(func() { a.backend.Quit() }) }

// NewWindow is called on the UI thread, normally from WhenReady or a callback.
func (a *Application) NewWindow(o WindowOptions) (*Window, error) {
	if a.backend == nil {
		return nil, errors.New("fego: open windows from WhenReady")
	}
	if o.Content == nil {
		return nil, errors.New("fego: window content is required")
	}
	if o.Width <= 0 {
		o.Width = 960
	}
	if o.Height <= 0 {
		o.Height = 640
	}
	w := &Window{app: a, width: o.Width, height: o.Height, scale: 1}
	n, err := a.backend.Open(platform.Options{Title: o.Title, Width: o.Width, Height: o.Height}, w.event)
	if err != nil {
		return nil, err
	}
	w.native = n
	w.view = o.Content.Mount(w)
	a.windows[w] = struct{}{}
	w.Invalidate()
	return w, nil
}

// Run opens one window and runs its event loop.
func Run(o WindowOptions) error {
	a := NewApp()
	a.WhenReady(func() { _, a.err = a.NewWindow(o) })
	return a.Run()
}

type Window struct {
	app                    *Application
	native                 platform.Window
	view                   View
	width, height          int
	scale                  float32
	closed, invalid, ready bool
	onReady                []func()
	textInputDisabled      bool
}

func (w *Window) event(e Event) {
	if w.closed {
		return
	}
	if e.Kind == Resize && e.Width > 0 && e.Height > 0 {
		w.width, w.height = e.Width, e.Height
		if e.Scale > 0 {
			w.scale = e.Scale
		}
	}
	if e.Kind == Close {
		w.destroy()
		return
	}
	if w.view != nil {
		w.view.Event(e)
	}
}

func (w *Window) Size() (int, int, float32) { return w.width, w.height, w.scale }
func (w *Window) Present(img *image.RGBA, damage []image.Rectangle) {
	if w.closed {
		return
	}
	w.native.Present(img, damage)
	w.presented()
}

// RetainsPixels reports whether PresentRegion is available. Such windows keep
// presented pixels and send a Paint event after losing them, so content may
// present only damaged regions instead of retaining a full-window image.
func (w *Window) RetainsPixels() bool {
	_, ok := w.native.(platform.RegionPresenter)
	return ok
}

// PresentRegion uploads img at its bounds, in window device pixels. It is an
// optional host method, called on the UI thread when RetainsPixels is true;
// img is borrowed for the call and must not be retained.
func (w *Window) PresentRegion(img *image.RGBA) {
	if w.closed {
		return
	}
	if native, ok := w.native.(platform.RegionPresenter); ok {
		native.PresentRegion(img)
		w.presented()
	}
}

func (w *Window) presented() {
	if !w.ready {
		w.ready = true
		for _, fn := range w.onReady {
			fn()
		}
		w.onReady = nil
	}
}

// ScrollPixels is an optional host acceleration, called on the UI thread.
func (w *Window) ScrollPixels(rect image.Rectangle, delta image.Point) bool {
	if w.closed {
		return false
	}
	if native, ok := w.native.(platform.PixelScroller); ok {
		return native.ScrollPixels(rect, delta)
	}
	return false
}

func (w *Window) Invalidate() {
	if w.invalid || w.closed {
		return
	}
	w.invalid = true
	w.app.Post(func() {
		w.invalid = false
		if !w.closed {
			if view, ok := w.view.(interface{ PaintIfDirty() }); ok {
				view.PaintIfDirty()
			} else {
				w.view.Event(Event{Kind: Paint})
			}
		}
	})
}

func (w *Window) OnReady(fn func()) {
	w.app.Post(func() {
		if w.closed {
			return
		}
		if w.ready {
			fn()
		} else {
			w.onReady = append(w.onReady, fn)
		}
	})
}

// Update is the boundary for changing widgets from a worker goroutine.
func (w *Window) Update(fn func()) {
	w.app.Post(func() {
		if !w.closed {
			fn()
			w.Invalidate()
		}
	})
}
func (w *Window) SetTitle(title string) {
	w.app.Post(func() {
		if !w.closed {
			w.native.SetTitle(title)
		}
	})
}
func (w *Window) SetSize(width, height int) {
	w.app.Post(func() {
		if !w.closed {
			w.native.SetSize(width, height)
		}
	})
}
func (w *Window) SetCursor(cursor string) { w.native.SetCursor(cursor) }
func (w *Window) SetClipboard(s string)   { w.native.SetClipboard(s) }
func (w *Window) Clipboard() string       { return w.native.Clipboard() }
func (w *Window) TextInput(active bool, caret image.Rectangle) {
	if w.closed || (!active && w.textInputDisabled) {
		return
	}
	w.native.TextInput(active, caret)
	w.textInputDisabled = !active
}
func (w *Window) SetAccessibility(nodes []AccessNode) {
	if native, ok := w.native.(platform.Accessible); ok {
		native.SetAccessibility(nodes)
	}
}
func (w *Window) Destroy() { w.app.Post(w.destroy) }

func (w *Window) destroy() {
	if w.closed {
		return
	}
	w.closed = true
	w.onReady = nil
	if w.view != nil {
		w.view.Close()
		w.view = nil
	}
	if w.native != nil {
		w.native.Close()
	}
	delete(w.app.windows, w)
	if len(w.app.windows) == 0 && !w.app.stopped {
		w.app.backend.Quit()
	}
}
