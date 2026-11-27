// Package fego runs native desktop windows with UI implemented in Go.
package fego

import (
	"errors"
	"image"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zatxm/fego/v26/internal/platform"
)

func init() { runtime.LockOSThread() }

type Event = platform.Event
type EventKind = platform.EventKind
type AccessNode = platform.AccessNode

const (
	Paint         = platform.Paint
	Resize        = platform.Resize
	PointerMove   = platform.PointerMove
	PointerDown   = platform.PointerDown
	PointerUp     = platform.PointerUp
	Scroll        = platform.Scroll
	KeyDown       = platform.KeyDown
	KeyUp         = platform.KeyUp
	Text          = platform.Text
	Focus         = platform.Focus
	Close         = platform.Close
	AccessAction  = platform.AccessAction
	PointerCancel = platform.PointerCancel
	TextEdit      = platform.TextEdit
	TextSelection = platform.TextSelection
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
	GPU           bool        // opt in to hardware presentation when available
	X11Render     bool        // use the X server for GPU presentation on Linux; custom GLSL requires OpenGL
	Icon          image.Image // overrides the application icon
	TitleBarIcon  image.Image // optional title-bar accessory on macOS
}

// AppOptions sets application identity and lifetime before Run.
type AppOptions struct {
	ID        string
	Name      string
	Icon      image.Image
	KeepAlive bool
}

type Application struct {
	mu               sync.Mutex
	queue            []func()
	spareQueue       []func()
	backend          platform.Backend
	options          AppOptions
	icon             *image.NRGBA
	trays            map[*Tray]struct{}
	ready            []func()
	windows          map[*Window]struct{}
	running, stopped bool
	quitting         bool
	err              error
}

func NewApp() *Application { return NewAppWithOptions(AppOptions{}) }

func NewAppWithOptions(o AppOptions) *Application {
	return &Application{options: o, windows: make(map[*Window]struct{})}
}

// SetOptions configures an application, including the default App, before Run.
func (a *Application) SetOptions(o AppOptions) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return errors.New("fego: set app options before Run")
	}
	a.options = o
	return nil
}

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
		for tray := range a.trays {
			tray.Close()
		}
		for w := range a.windows {
			w.destroy()
		}
	}()
	appIcon, err := platform.CopyIcon(a.options.Icon)
	if err != nil {
		return err
	}
	a.options.Icon = nil
	a.icon = appIcon
	b, err := platform.New(a.dispatch)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.backend = b
	a.mu.Unlock()
	if desktop, ok := b.(platform.DesktopBackend); ok {
		a.err = desktop.Configure(a.options.ID, a.options.Name, a.icon)
	}
	if a.err == nil {
		for _, fn := range a.ready {
			fn()
		}
	}
	a.ready = nil
	if a.err == nil && len(a.windows) == 0 && !a.options.KeepAlive {
		a.err = errors.New("fego: no window opened in WhenReady")
	}
	a.dispatch()
	if a.err != nil {
		a.quit()
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

func (a *Application) Quit() { a.Post(a.quit) }

func (a *Application) quit() {
	if a.quitting {
		return
	}
	a.quitting = true
	for tray := range a.trays {
		tray.Close()
	}
	a.backend.Quit()
}

// NewWindow is called on the UI thread, normally from WhenReady or a callback.
func (a *Application) NewWindow(o WindowOptions) (*Window, error) {
	if a.backend == nil || a.stopped || a.quitting {
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
	icon := a.icon
	if o.Icon != nil {
		var err error
		icon, err = platform.CopyIcon(o.Icon)
		if err != nil {
			return nil, err
		}
	}
	titleBarIcon, err := platform.CopyIcon(o.TitleBarIcon)
	if err != nil {
		return nil, err
	}
	if o.Title == "" {
		o.Title = a.options.Name
	}
	w := &Window{app: a, width: o.Width, height: o.Height, scale: 1}
	n, err := a.backend.Open(platform.Options{
		Title: o.Title, Width: o.Width, Height: o.Height, AppID: a.options.ID,
		Icon: icon, TitleBarIcon: titleBarIcon, X11Render: o.X11Render,
	}, w.event)
	if err != nil {
		return nil, err
	}
	w.native = n
	if graphics, ok := n.(platform.Graphics); ok && o.GPU {
		graphics.SetGPUEnabled(true)
	}
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
	paintPending           func()
	onReady                []func()
	onClose                func(*CloseEvent)
	done                   chan struct{}
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
		if e.Destroyed {
			w.destroy()
		} else {
			w.requestClose()
		}
		return
	}
	if w.view != nil {
		w.view.Event(e)
	}
}

// SetGPUEnabled switches hardware presentation on or off on the UI thread.
func (w *Window) SetGPUEnabled(enabled bool) {
	native, ok := w.native.(platform.Graphics)
	if !ok || w.closed {
		return
	}
	changed := w.awaitGPU()
	changed = native.SetGPUEnabled(enabled) || changed
	if w.awaitGPU() || changed {
		w.resetPresenter()
		w.event(Event{Kind: Paint})
	}
}

// GPUActive reports whether this window is currently presenting through a GPU.
func (w *Window) GPUActive() bool {
	native, ok := w.native.(platform.Graphics)
	if !ok || w.closed {
		return false
	}
	if w.awaitGPU() {
		// Repaint after the caller returns; the reset keeps frames until then full.
		w.resetPresenter()
		w.Invalidate()
	}
	return native.GPUActive()
}

// awaitGPU completes hardware setup that NewWindow started in the background,
// so explicit GPU calls observe the same state as a synchronous start.
func (w *Window) awaitGPU() bool {
	native, ok := w.native.(interface{ AwaitGPU() bool })
	return ok && native.AwaitGPU()
}

func (w *Window) resetPresenter() {
	if view, ok := w.view.(interface{ ResetPresenter() }); ok {
		view.ResetPresenter()
	}
}

// SetFragmentShader compiles a GLSL fragment body on the UI thread. The body
// defines main and may sample frameTexture at frameUV. An empty body resets it.
func (w *Window) SetFragmentShader(source string) error {
	if len(source) > 32<<10 || strings.ContainsRune(source, 0) {
		return errors.New("fego: invalid fragment shader source")
	}
	if native, ok := w.native.(platform.ShaderPresenter); ok && !w.closed {
		if w.awaitGPU() {
			w.resetPresenter()
			w.Invalidate()
		}
		if err := native.SetFragmentShader(source); err != nil {
			return err
		}
		w.event(Event{Kind: Paint})
		return nil
	}
	return platform.ErrGPUInactive
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
	if native, ok := w.native.(platform.GPURegionPresenter); ok && native.GPUActive() {
		return true
	}
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
	if native, ok := w.native.(platform.GPURegionPresenter); ok && native.GPUActive() {
		w.PresentRegions(func(upload func(*image.RGBA)) { upload(img) })
		return
	}
	if native, ok := w.native.(platform.RegionPresenter); ok {
		native.PresentRegion(img)
		w.presented()
	}
}

// PresentRegions groups borrowed device-pixel regions into one displayed frame.
func (w *Window) PresentRegions(paint func(func(*image.RGBA))) {
	if w.closed {
		return
	}
	if native, ok := w.native.(platform.GPURegionPresenter); ok && native.GPUActive() {
		if native.PresentGPURegions(image.Pt(w.width, w.height), paint) {
			w.presented()
		} else {
			// A lost texture needs a full CPU repaint after the current frame unwinds.
			w.app.Post(func() {
				if !w.closed {
					w.resetPresenter()
					w.event(Event{Kind: Paint})
				}
			})
		}
		return
	}
	if native, ok := w.native.(interface{ PresentRegions(func(func(*image.RGBA))) }); ok {
		native.PresentRegions(paint)
		w.presented()
	} else if native, ok := w.native.(platform.RegionPresenter); ok {
		paint(native.PresentRegion)
		w.presented()
	}
}

func (w *Window) presented() {
	if !w.ready {
		w.ready = true
		if native, ok := w.native.(interface{ StartGPU() }); ok {
			// Start the driver after the first frame, so it competes with neither
			// that frame's work nor its display server round trips.
			w.app.Post(func() {
				if !w.closed {
					native.StartGPU()
				}
			})
		}
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
	if w.paintPending == nil {
		w.paintPending = func() {
			w.invalid = false
			if w.closed {
				return
			}
			if view, ok := w.view.(interface{ PaintIfDirty() }); ok {
				view.PaintIfDirty()
			} else {
				w.view.Event(Event{Kind: Paint})
			}
		}
	}
	w.app.Post(w.paintPending)
}

// After runs fn on the UI thread once d has elapsed, unless the window has
// closed or the returned cancel function ran first. Call cancel on the UI thread.
func (w *Window) After(d time.Duration, fn func()) (cancel func()) {
	cancelled := false
	timer := time.AfterFunc(d, func() {
		w.app.Post(func() {
			if !cancelled && !w.closed {
				fn()
			}
		})
	})
	return func() { cancelled = true; timer.Stop() }
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

// SetTextInputState supplies composition context to a host that uses it.
func (w *Window) SetTextInputState(text string, start, end int, password, numeric bool) {
	if host, ok := w.native.(interface {
		SetTextInputState(string, int, int, bool, bool)
	}); ok {
		host.SetTextInputState(text, start, end, password, numeric)
	}
}

func (w *Window) SetAccessibility(nodes []AccessNode) {
	if native, ok := w.native.(platform.Accessible); ok {
		native.SetAccessibility(nodes)
	}
}

// CloseEvent allows a user-initiated close to be canceled.
type CloseEvent struct{ prevented bool }

func (e *CloseEvent) PreventDefault() { e.prevented = true }

// OnClose sets the close handler. Set it on the UI thread.
func (w *Window) OnClose(fn func(*CloseEvent)) { w.onClose = fn }

// Close requests a normal close through the UI event loop.
func (w *Window) Close() { w.app.Post(w.requestClose) }

func (w *Window) requestClose() {
	if w.closed {
		return
	}
	var e CloseEvent
	if w.onClose != nil {
		w.onClose(&e)
	}
	if !e.prevented {
		w.destroy()
	}
}

func (w *Window) Show() { w.control(platform.WindowControls.Show) }
func (w *Window) Hide() { w.control(platform.WindowControls.Hide) }

// Activate shows the window, restores it from minimization and requests focus.
func (w *Window) Activate() { w.control(platform.WindowControls.Activate) }

func (w *Window) control(fn func(platform.WindowControls)) {
	w.app.Post(func() {
		if !w.closed {
			if native, ok := w.native.(platform.WindowControls); ok {
				fn(native)
			}
		}
	})
}

// Done returns a channel closed when the window is destroyed. Get the channel
// on the UI thread before passing it to a background task.
func (w *Window) Done() <-chan struct{} {
	if w.done == nil {
		w.done = make(chan struct{})
		if w.closed {
			close(w.done)
		}
	}
	return w.done
}

// Destroy closes the window without calling its close handler.
func (w *Window) Destroy() { w.app.Post(w.destroy) }

func (w *Window) destroy() {
	if w.closed {
		return
	}
	w.closed = true
	if w.done != nil {
		close(w.done)
	}
	w.onReady = nil
	w.onClose = nil
	if w.view != nil {
		w.view.Close()
		w.view = nil
	}
	if w.native != nil {
		w.native.Close()
	}
	delete(w.app.windows, w)
	if len(w.app.windows) == 0 && !w.app.stopped && !w.app.options.KeepAlive {
		w.app.quit()
	}
}

// SetMaximized uses the desktop work area, leaving system panels available.
func (w *Window) SetMaximized(on bool) {
	if n, ok := w.native.(interface{ SetMaximized(bool) }); ok {
		n.SetMaximized(on)
	}
}
func (w *Window) Maximized() bool {
	if n, ok := w.native.(interface{ Maximized() bool }); ok {
		return n.Maximized()
	}
	return false
}
