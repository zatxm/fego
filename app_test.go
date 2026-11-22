package fego

import (
	"image"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zatxm/fego/v26/internal/platform"
)

type testBackend struct {
	wakes   atomic.Int64
	quits   int
	windows []*testWindow
}

func (b *testBackend) Open(o platform.Options, fn func(Event)) (platform.Window, error) {
	w := &testWindow{event: fn}
	b.windows = append(b.windows, w)
	fn(Event{Kind: Resize, Width: o.Width, Height: o.Height, Scale: 2})
	return w, nil
}
func (b *testBackend) Run() error { return nil }
func (b *testBackend) Wake()      { b.wakes.Add(1) }
func (b *testBackend) Quit()      { b.quits++ }

type testWindow struct {
	event      func(Event)
	closed     int
	inputCalls int
}

func (w *testWindow) Present(*image.RGBA, []image.Rectangle) {}
func (w *testWindow) SetTitle(string)                        {}
func (w *testWindow) SetSize(int, int)                       {}
func (w *testWindow) SetCursor(string)                       {}
func (w *testWindow) SetClipboard(string)                    {}
func (w *testWindow) Clipboard() string                      { return "" }
func (w *testWindow) TextInput(bool, image.Rectangle)        { w.inputCalls++ }
func (w *testWindow) Close()                                 { w.closed++ }

type testContent struct {
	host           Host
	closed, paints int
}

func (c *testContent) Mount(h Host) View { c.host = h; return c }
func (c *testContent) Event(e Event) {
	if e.Kind == Paint {
		c.paints++
	}
}
func (c *testContent) Close() { c.closed++ }

type retainedTestContent struct {
	testContent
	requests int
}

func (c *retainedTestContent) Mount(h Host) View { c.host = h; return c }
func (c *retainedTestContent) PaintIfDirty()     { c.requests++ }

func TestQueuedRetainedPaintKeepsNativeExpose(t *testing.T) {
	a := NewApp()
	backend := &testBackend{}
	a.backend = backend
	content := &retainedTestContent{}
	w, err := a.NewWindow(WindowOptions{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	a.dispatch()
	if content.requests != 1 || content.paints != 0 {
		t.Fatal("queued repaint bypassed retained view", content.requests, content.paints)
	}
	backend.windows[0].event(Event{Kind: Paint})
	if content.paints != 1 {
		t.Fatal("native exposure was suppressed")
	}
	w.Update(func() { w.Invalidate(); w.Invalidate() })
	a.dispatch()
	a.dispatch()
	if content.requests != 2 || content.paints != 1 {
		t.Fatal("queued updates were not coalesced", content.requests, content.paints)
	}
	w.Destroy()
	a.dispatch()
}

func TestConcurrentPostAndShutdown(t *testing.T) {
	a := NewApp()
	backend := &testBackend{}
	a.backend = backend
	var workers sync.WaitGroup
	calls := 0
	for range 8 {
		workers.Go(func() {
			for range 100 {
				if !a.Post(func() { calls++ }) {
					t.Error("live post rejected")
				}
			}
		})
	}
	workers.Wait()
	a.dispatch()
	if calls != 800 || backend.wakes.Load() != 800 {
		t.Fatal("queued work lost", calls, backend.wakes.Load())
	}
	a.mu.Lock()
	a.stopped = true
	a.mu.Unlock()
	if a.Post(func() { t.Fatal("shutdown callback ran") }) {
		t.Fatal("post accepted after shutdown")
	}
	a.dispatch()
}

func TestDispatchReusesQueueWithoutRetainingCallbacks(t *testing.T) {
	a := NewApp()
	noop := func() {}
	for range 3 {
		a.Post(noop)
		a.dispatch()
	}
	if allocations := testing.AllocsPerRun(100, func() { a.Post(noop); a.dispatch() }); allocations != 0 {
		t.Fatal("dispatch still allocates a queue", allocations)
	}
	for _, queue := range [][]func(){a.queue, a.spareQueue} {
		for _, callback := range queue[:cap(queue)] {
			if callback != nil {
				t.Fatal("queue retains a callback")
			}
		}
	}
	calls := 0
	a.Post(func() { calls++; a.Post(func() { calls++ }) })
	a.dispatch()
	if calls != 1 {
		t.Fatal("new work ran in the wrong batch", calls)
	}
	a.dispatch()
	if calls != 2 {
		t.Fatal("work posted during dispatch was lost", calls)
	}
}

func TestWindowLifetimeAndQueuedUpdates(t *testing.T) {
	a := NewApp()
	backend := &testBackend{}
	a.backend = backend
	first, second := &testContent{}, &testContent{}
	w, err := a.NewWindow(WindowOptions{Content: first, Width: 400, Height: 300})
	if err != nil {
		t.Fatal(err)
	}
	v, err := a.NewWindow(WindowOptions{Content: second})
	if err != nil {
		t.Fatal(err)
	}
	a.dispatch()
	if width, height, scale := w.Size(); width != 400 || height != 300 || scale != 2 {
		t.Fatal(width, height, scale)
	}
	if first.paints != 1 || second.paints != 1 {
		t.Fatal("initial frame missing")
	}
	w.Destroy()
	w.Update(func() { t.Fatal("closed update ran") })
	w.Destroy()
	a.dispatch()
	if first.closed != 1 || backend.windows[0].closed != 1 || backend.quits != 0 {
		t.Fatal("first window close lifecycle")
	}
	v.Destroy()
	a.dispatch()
	if second.closed != 1 || backend.quits != 1 || len(a.windows) != 0 {
		t.Fatal("last window did not stop loop")
	}
	backend.windows[0].event(Event{Kind: Paint})
	if first.paints != 1 {
		t.Fatal("late native paint reached closed view")
	}
}

func TestTextInputSkipsOnlyRepeatedDisabledState(t *testing.T) {
	native := &testWindow{}
	w := &Window{native: native}
	w.TextInput(false, image.Rectangle{})
	w.TextInput(false, image.Rect(2, 3, 4, 5))
	if native.inputCalls != 1 {
		t.Fatal("disabled input repeatedly sent", native.inputCalls)
	}
	w.TextInput(true, image.Rect(2, 3, 4, 5))
	w.TextInput(true, image.Rect(2, 3, 4, 5))
	w.TextInput(false, image.Rectangle{})
	w.TextInput(false, image.Rectangle{})
	if native.inputCalls != 4 {
		t.Fatal("activation/active geometry was suppressed", native.inputCalls)
	}
	w.closed = true
	w.TextInput(true, image.Rect(4, 5, 6, 7))
	if native.inputCalls != 4 {
		t.Fatal("closed window received input changes")
	}
}

func TestKeepAliveAndCloseVeto(t *testing.T) {
	app := NewAppWithOptions(AppOptions{KeepAlive: true})
	backend := &testBackend{}
	app.backend = backend

	open := func() *Window {
		w, err := app.NewWindow(WindowOptions{Content: &testContent{}})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	window := open()
	window.OnClose(func(e *CloseEvent) { e.PreventDefault() })
	backend.windows[0].event(Event{Kind: Close})
	if window.closed {
		t.Fatal("OS close ignored the application's veto")
	}
	window.Destroy()
	app.dispatch()
	if !window.closed || len(app.windows) != 0 || backend.quits != 0 {
		t.Fatal("keep-alive stopped after the last window was destroyed")
	}
	window = open()
	window.OnClose(func(e *CloseEvent) { e.PreventDefault() })
	backend.windows[1].event(Event{Kind: Close, Destroyed: true})
	if !window.closed {
		t.Fatal("the OS already destroyed the native window")
	}
	app.Quit()
	app.dispatch()
	if backend.quits != 1 {
		t.Fatal("explicit quit must exit keep-alive mode")
	}
}

type fakeTrayBackend struct {
	testBackend
	tray *fakeTray
}

func (*fakeTrayBackend) Configure(string, string, *image.NRGBA) error { return nil }
func (b *fakeTrayBackend) NewTray(s platform.TrayState, click func(int32)) (platform.Tray, error) {
	b.tray = &fakeTray{state: s, click: click}
	return b.tray, nil
}

type fakeTray struct {
	state  platform.TrayState
	click  func(int32)
	closed bool
}

func (t *fakeTray) Update(s platform.TrayState) error { t.state = s; return nil }
func (t *fakeTray) Available() bool                   { return !t.closed }
func (t *fakeTray) Close()                            { t.closed = true }

func TestTrayMenusIgnoreStaleAndDisabledClicks(t *testing.T) {
	app := NewAppWithOptions(AppOptions{KeepAlive: true})
	backend := &fakeTrayBackend{}
	app.backend = backend
	icon := image.NewRGBA(image.Rect(0, 0, 16, 16))
	oldCalls, newCalls := 0, 0
	tray, err := app.NewTray(TrayOptions{Icon: icon, Menu: Menu{
		{Label: "Old", OnClick: func() { oldCalls++ }},
		{Label: "Disabled", Disabled: true, Submenu: Menu{{Label: "Child", OnClick: func() { oldCalls++ }}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	old, disabled := backend.tray.state.Menu[0].ID, backend.tray.state.Menu[1].Submenu[0].ID
	backend.tray.click(disabled)
	backend.tray.click(old)
	app.dispatch()
	if oldCalls != 1 {
		t.Fatalf("old action count = %d", oldCalls)
	}
	backend.tray.click(old)
	if err := tray.SetMenu(Menu{{Label: "New", Checkable: true, Checked: true, OnClick: func() { newCalls++ }}}); err != nil {
		t.Fatal(err)
	}
	backend.tray.click(backend.tray.state.Menu[0].ID)
	app.dispatch()
	if oldCalls != 1 || newCalls != 1 {
		t.Fatalf("stale, disabled or current action mismatch: old=%d new=%d", oldCalls, newCalls)
	}
	app.Quit()
	app.dispatch()
	if tray.Available() || !backend.tray.closed {
		t.Fatal("quit leaked a tray")
	}
}
