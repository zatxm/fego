//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"image"
	"math"
	"math/bits"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	xKeyPress         = 2
	xKeyRelease       = 3
	xButtonPress      = 4
	xButtonRelease    = 5
	xMotion           = 6
	xFocusIn          = 9
	xFocusOut         = 10
	xExpose           = 12
	xGraphicsExpose   = 13
	xDestroy          = 17
	xMap              = 19
	xReparent         = 21
	xConfigure        = 22
	xProperty         = 28
	xSelectionClear   = 29
	xSelectionRequest = 30
	xSelectionNotify  = 31
	xClientMessage    = 33
	xPropertyMask     = 1 << 22
	xWindowMask       = 1 | 2 | 4 | 8 | 1<<6 | 1<<15 | 1<<17 | 1<<21 | xPropertyMask
	xMaxClipboard     = 16 << 20
	xMaxPixels        = 64 << 20
	xUploadBytes      = (384 << 10) - 64 // Bound staging memory without excessive Xwayland damage requests.
	xUploadIdleBytes  = 128 << 10
	xUploadIdleTime   = 250 * time.Millisecond
)

type xEvent [24]uint64
type xAny struct {
	Kind            int32
	_               int32
	Serial          uint64
	Sent            int32
	_               int32
	Display, Window uintptr
}
type xKeyEvent struct {
	xAny
	Root, Child, Time  uintptr
	X, Y, RootX, RootY int32
	State, Code        uint32
	SameScreen         int32
}
type xConfigureEvent struct {
	xAny
	Window                      uintptr
	X, Y, Width, Height, Border int32
	_                           int32
	Above                       uintptr
	Override                    int32
}
type xClientEvent struct {
	xAny
	Message uintptr
	Format  int32
	_       int32
	Data    [5]int64
}
type xSelectionEvent struct {
	xAny
	Selection, Target, Property, Time uintptr
}
type xSelectionRequestEvent struct {
	xAny
	Requestor, Selection, Target, Property, Time uintptr
}
type xPropertyEvent struct {
	xAny
	Atom, Time uintptr
	State      int32
}
type xErrorEvent struct {
	Kind                      int32
	_                         int32
	Display, Resource, Serial uintptr
	Code, Request, Minor      uint8
}
type xPoint struct{ X, Y int16 }
type xImage struct {
	Width, Height, XOffset, Format                                                      int32
	Data                                                                                *byte
	ByteOrder, BitmapUnit, BitmapBitOrder, BitmapPad, Depth, BytesPerLine, BitsPerPixel int32
	_                                                                                   int32
	Red, Green, Blue                                                                    uintptr
	Object                                                                              uintptr
	Functions                                                                           [6]uintptr
}

var xl struct {
	openDisplay          func(string) uintptr
	closeDisplay         func(uintptr) int32
	defaultScreen        func(uintptr) int32
	rootWindow           func(uintptr, int32) uintptr
	defaultVisual        func(uintptr, int32) uintptr
	defaultDepth         func(uintptr, int32) int32
	connection           func(uintptr) int32
	translateCoordinates func(uintptr, uintptr, uintptr, int32, int32, *int32, *int32, *uintptr) int32
	resources            func(uintptr) *byte
	createWindow         func(uintptr, uintptr, int32, int32, uint32, uint32, uint32, uintptr, uintptr) uintptr
	destroyWindow        func(uintptr, uintptr) int32
	mapWindow            func(uintptr, uintptr) int32
	unmapWindow          func(uintptr, uintptr) int32
	selectInput          func(uintptr, uintptr, int64) int32
	storeName            func(uintptr, uintptr, string) int32
	resizeWindow         func(uintptr, uintptr, uint32, uint32) int32
	createGC             func(uintptr, uintptr, uintptr, uintptr) uintptr
	freeGC               func(uintptr, uintptr) int32
	createImage          func(uintptr, uintptr, uint32, int32, int32, *byte, uint32, uint32, int32, int32) *xImage
	destroyImage         func(*xImage) int32
	putImage             uintptr
	flushImage           uintptr
	copyArea             func(uintptr, uintptr, uintptr, uintptr, int32, int32, uint32, uint32, int32, int32) int32
	flush                func(uintptr) int32
	pending              func(uintptr) int32
	nextEvent            func(uintptr, *xEvent) int32
	filterEvent          func(*xEvent, uintptr) int32
	internAtom           func(uintptr, string, int32) uintptr
	internAtoms          func(uintptr, **byte, int32, int32, *uintptr) int32
	setProtocols         func(uintptr, uintptr, *uintptr, int32) int32
	changeProperty       func(uintptr, uintptr, uintptr, uintptr, int32, int32, unsafe.Pointer, int32) int32
	getProperty          func(uintptr, uintptr, uintptr, int64, int64, int32, uintptr, *uintptr, *int32, *uint64, *uint64, **byte) int32
	deleteProperty       func(uintptr, uintptr, uintptr) int32
	selectionOwner       func(uintptr, uintptr) uintptr
	setSelectionOwner    func(uintptr, uintptr, uintptr, uintptr) int32
	convertSelection     func(uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) int32
	sendEvent            func(uintptr, uintptr, int32, int64, *xEvent) int32
	free                 func(unsafe.Pointer) int32
	createCursor         func(uintptr, uint32) uintptr
	defineCursor         func(uintptr, uintptr, uintptr) int32
	freeCursor           func(uintptr, uintptr) int32
	lookupKeysym         func(*xKeyEvent, int32) uintptr
	lookupString         func(*xKeyEvent, *byte, int32, *uintptr, uintptr) int32
	setLocaleModifiers   func(string) *byte
	openIM               func(uintptr, uintptr, uintptr, uintptr) uintptr
	closeIM              func(uintptr) int32
	createIC             func(uintptr, string, uintptr, string, uintptr, string, uintptr, uintptr) uintptr
	destroyIC            func(uintptr)
	setICFocus           func(uintptr)
	unsetICFocus         func(uintptr)
	utf8Lookup           func(uintptr, *xKeyEvent, *byte, int32, *uintptr, *int32) int32
	nestedList           func(int32, string, *xPoint, uintptr) unsafe.Pointer
	setICValues          func(uintptr, string, unsafe.Pointer, uintptr) uintptr
	setErrorHandler      func(uintptr) uintptr
	detectRepeat         func(uintptr, int32, *int32) int32
	calloc               func(uintptr, uintptr) *byte
	setLocale            func(int32, string) *byte
}

// Raw symbols for per-frame calls; purego's reflective wrappers allocate per call.
var xRaw struct{ pending, nextEvent, filterEvent, copyArea uintptr }

var xLoad struct {
	sync.Once
	err error
}
var xBackends = struct {
	sync.Mutex
	byDisplay map[uintptr]*xBackend
}{byDisplay: make(map[uintptr]*xBackend)}

func loadX11() error {
	xLoad.Do(func() {
		lib, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			xLoad.err = err
			return
		}
		libc, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			xLoad.err = err
			return
		}
		// EGL starts on its own connection in another thread. libX11 1.8+ already
		// does this; older versions require it before any other Xlib call.
		if initThreads, err := purego.Dlsym(lib, "XInitThreads"); err == nil {
			purego.SyscallN(initThreads)
		}
		for _, f := range []struct {
			name string
			dest any
		}{
			{"XTranslateCoordinates", &xl.translateCoordinates}, {"XOpenDisplay", &xl.openDisplay}, {"XCloseDisplay", &xl.closeDisplay}, {"XDefaultScreen", &xl.defaultScreen}, {"XRootWindow", &xl.rootWindow}, {"XDefaultVisual", &xl.defaultVisual}, {"XDefaultDepth", &xl.defaultDepth}, {"XConnectionNumber", &xl.connection}, {"XResourceManagerString", &xl.resources},
			{"XCreateSimpleWindow", &xl.createWindow}, {"XDestroyWindow", &xl.destroyWindow}, {"XMapWindow", &xl.mapWindow}, {"XUnmapWindow", &xl.unmapWindow}, {"XSelectInput", &xl.selectInput}, {"XStoreName", &xl.storeName}, {"XResizeWindow", &xl.resizeWindow}, {"XCreateGC", &xl.createGC}, {"XFreeGC", &xl.freeGC}, {"XCreateImage", &xl.createImage}, {"XDestroyImage", &xl.destroyImage}, {"XCopyArea", &xl.copyArea},
			{"XFlush", &xl.flush}, {"XPending", &xl.pending}, {"XNextEvent", &xl.nextEvent}, {"XFilterEvent", &xl.filterEvent}, {"XInternAtom", &xl.internAtom}, {"XInternAtoms", &xl.internAtoms}, {"XSetWMProtocols", &xl.setProtocols}, {"XChangeProperty", &xl.changeProperty}, {"XGetWindowProperty", &xl.getProperty}, {"XDeleteProperty", &xl.deleteProperty}, {"XGetSelectionOwner", &xl.selectionOwner}, {"XSetSelectionOwner", &xl.setSelectionOwner}, {"XConvertSelection", &xl.convertSelection}, {"XSendEvent", &xl.sendEvent}, {"XFree", &xl.free},
			{"XCreateFontCursor", &xl.createCursor}, {"XDefineCursor", &xl.defineCursor}, {"XFreeCursor", &xl.freeCursor}, {"XLookupKeysym", &xl.lookupKeysym}, {"XLookupString", &xl.lookupString}, {"XSetLocaleModifiers", &xl.setLocaleModifiers}, {"XOpenIM", &xl.openIM}, {"XCloseIM", &xl.closeIM}, {"XCreateIC", &xl.createIC}, {"XDestroyIC", &xl.destroyIC}, {"XSetICFocus", &xl.setICFocus}, {"XUnsetICFocus", &xl.unsetICFocus}, {"Xutf8LookupString", &xl.utf8Lookup}, {"XVaCreateNestedList", &xl.nestedList}, {"XSetICValues", &xl.setICValues}, {"XSetErrorHandler", &xl.setErrorHandler}, {"XkbSetDetectableAutoRepeat", &xl.detectRepeat},
		} {
			p, err := purego.Dlsym(lib, f.name)
			if err != nil {
				xLoad.err = err
				return
			}
			purego.RegisterFunc(f.dest, p)
		}
		xl.putImage, err = purego.Dlsym(lib, "XPutImage")
		if err != nil {
			xLoad.err = err
			return
		}
		xl.flushImage, err = purego.Dlsym(lib, "XFlush")
		if err != nil {
			xLoad.err = err
			return
		}
		for _, f := range []struct {
			name string
			dest *uintptr
		}{{"XPending", &xRaw.pending}, {"XNextEvent", &xRaw.nextEvent}, {"XFilterEvent", &xRaw.filterEvent}, {"XCopyArea", &xRaw.copyArea}} {
			if *f.dest, err = purego.Dlsym(lib, f.name); err != nil {
				xLoad.err = err
				return
			}
		}
		purego.RegisterLibFunc(&xl.calloc, libc, "calloc")
		purego.RegisterLibFunc(&xl.setLocale, libc, "setlocale")
		xl.setErrorHandler(purego.NewCallback(func(display uintptr, e *xErrorEvent) int32 {
			// A selection requester may disappear before its reply reaches X.
			if e.Code == 3 || e.Code == 9 {
				return 0
			}
			xBackends.Lock()
			if b := xBackends.byDisplay[display]; b != nil && b.shmProbing && e.Request == b.shmMajor {
				b.shmFailed = true
			} else if b != nil && b.err == nil {
				b.err = fmt.Errorf("X11 error %d, request %d, resource %#x", e.Code, e.Request, e.Resource)
				b.quit = true
			}
			xBackends.Unlock()
			return 0
		}))
	})
	return xLoad.err
}

type xBackend struct {
	eglDisplay, eglXDisplay                                                                      uintptr
	eglContexts                                                                                  int
	eglPending                                                                                   bool
	eglDone                                                                                      chan eglStart
	display, root, visual, im                                                                    uintptr
	depth                                                                                        int32
	fd                                                                                           int
	scale                                                                                        float32
	dispatch                                                                                     func()
	windows                                                                                      map[uintptr]*xWindow
	cursors                                                                                      map[uint32]uintptr
	wmDelete, wmProtocols, utf8, clipboardAtom, targets, textAtom, incr, clipProperty, titleAtom uintptr
	clipboardOwner                                                                               *xWindow
	clipboard                                                                                    string
	transfer                                                                                     *xClipboardRead
	wakeMu                                                                                       sync.Mutex
	conn                                                                                         *os.File
	rawConn                                                                                      syscall.RawConn
	woken, waiting, kicked                                                                       atomic.Bool
	deadline, readChecked                                                                        bool
	readReady                                                                                    func(uintptr) bool
	runEvent                                                                                     xEvent
	callArgs                                                                                     [2]uintptr
	shmState                                                                                     int8 // 0 unknown, 1 usable, -1 off
	shmMajor                                                                                     uint8
	shmCompletion                                                                                int32
	shmProbing, shmFailed                                                                        bool
	resizes                                                                                      bool
	quit, ran                                                                                    bool
	err                                                                                          error
}

type xWindow struct {
	graphics                               gpuState
	preferXRender, gpuFailed               bool
	render                                 xRenderPresenter
	b                                      *xBackend
	id, gc, ic                             uintptr
	emit                                   func(Event)
	img                                    *xImage
	putArgs                                [10]uintptr
	imageBytes                             int
	smallUploads                           int
	uploadIdleAt                           time.Time
	surfaceSize, accessOrigin              image.Point
	exposed                                image.Rectangle
	shm                                    *xShm
	presented                              bool
	originKnown                            bool
	width, height                          int
	focused, textInput, closed, configured bool
	resizePending, restoring               bool
	// lost collects exposures folded into a pending Resize; unrestored is
	// what the Resize handling has not presented again yet.
	lost                    image.Rectangle
	unrestored, restScratch []image.Rectangle
	preeditSpot             xPoint
	title                   string
	access                  *atspiBridge
	accessTried             bool
}

type xClipboardRead struct {
	window                    *xWindow
	target                    uintptr
	data                      []byte
	done, incremental, failed bool
}

// New uses X11, including XWayland on Wayland desktops.
func New(dispatch func()) (Backend, error) {
	if err := loadX11(); err != nil {
		return nil, fmt.Errorf("load X11: %w", err)
	}
	xl.setLocale(0, "")
	xl.setLocaleModifiers("")
	d := xl.openDisplay("")
	if d == 0 {
		return nil, errors.New("cannot open X11 display (DISPLAY/XWayland is required)")
	}
	b := &xBackend{display: d, dispatch: dispatch, windows: make(map[uintptr]*xWindow), cursors: make(map[uint32]uintptr), fd: int(xl.connection(d)), scale: 1}
	if err := b.openConn(); err != nil {
		b.release()
		return nil, err
	}
	screen := xl.defaultScreen(d)
	b.root, b.visual, b.depth = xl.rootWindow(d, screen), xl.defaultVisual(d, screen), xl.defaultDepth(d, screen)
	if b.depth != 24 && b.depth != 32 {
		b.release()
		return nil, fmt.Errorf("X11 depth %d is unsupported; use a true-color display", b.depth)
	}
	b.scale = xScale(xString(xl.resources(d)), os.Getenv("GDK_SCALE"))
	if !b.internWindowAtoms() {
		b.release()
		return nil, errors.New("cannot intern X11 window atoms")
	}
	b.im = xl.openIM(d, 0, 0, 0)
	var supported int32
	xl.detectRepeat(d, 1, &supported)
	xBackends.Lock()
	xBackends.byDisplay[d] = b
	xBackends.Unlock()
	return b, nil
}

// Submit the startup atom requests together, avoiding a round trip per name.
func (b *xBackend) internWindowAtoms() bool {
	atoms := [...]struct {
		name string
		id   *uintptr
	}{
		{"WM_DELETE_WINDOW\x00", &b.wmDelete}, {"WM_PROTOCOLS\x00", &b.wmProtocols},
		{"UTF8_STRING\x00", &b.utf8}, {"CLIPBOARD\x00", &b.clipboardAtom},
		{"TARGETS\x00", &b.targets}, {"TEXT\x00", &b.textAtom}, {"INCR\x00", &b.incr},
		{"_FEGO_CLIPBOARD\x00", &b.clipProperty}, {"_NET_WM_NAME\x00", &b.titleAtom},
	}
	var names [len(atoms)]*byte
	var ids [len(atoms)]uintptr
	for i, atom := range atoms {
		names[i] = unsafe.StringData(atom.name)
	}
	if xl.internAtoms(b.display, &names[0], int32(len(names)), 0, &ids[0]) == 0 {
		return false
	}
	for i, atom := range atoms {
		*atom.id = ids[i]
	}
	return true
}

func xScale(resources, override string) float32 {
	if n, err := strconv.ParseFloat(override, 32); err == nil && n >= 0.5 && n <= 4 {
		return float32(n)
	}
	for _, line := range strings.Split(resources, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "Xft.dpi" {
			if n, err := strconv.ParseFloat(strings.TrimSpace(value), 32); err == nil && n >= 48 && n <= 384 {
				return float32(n / 96)
			}
		}
	}
	return 1
}

func xString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

func xDimensions(w, h int) bool {
	return w > 0 && h > 0 && w <= 32767 && h <= 32767 && w <= xMaxPixels/h
}

func (b *xBackend) Open(o Options, emit func(Event)) (Window, error) {
	w, h := int(math.Round(float64(o.Width)*float64(b.scale))), int(math.Round(float64(o.Height)*float64(b.scale)))
	if b.display == 0 || b.quit {
		return nil, errors.New("X11 backend is closed")
	}
	if !xDimensions(w, h) {
		return nil, errors.New("invalid window size (maximum 64 million pixels)")
	}
	id := xl.createWindow(b.display, b.root, 0, 0, uint32(w), uint32(h), 0, 0, 0xffffff)
	if id == 0 {
		return nil, errors.New("XCreateSimpleWindow failed")
	}
	x := &xWindow{preferXRender: o.X11Render, b: b, id: id, width: w, height: h, emit: emit}
	x.gc = xl.createGC(b.display, id, 0, 0)
	if x.gc == 0 {
		xl.destroyWindow(b.display, id)
		return nil, errors.New("XCreateGC failed")
	}
	b.windows[id] = x
	xl.selectInput(b.display, id, xWindowMask)
	xl.setProtocols(b.display, id, &b.wmDelete, 1)
	x.SetTitle(o.Title)
	x.setIdentity(o)
	xl.mapWindow(b.display, id)
	xl.flush(b.display)
	emit(Event{Kind: Resize, Width: w, Height: h, Scale: b.scale})
	return x, nil
}

func (b *xBackend) Run() error {
	if b.ran || b.display == 0 {
		return errors.New("X11 backend may run only once")
	}
	b.ran = true
	defer b.release()
	for !b.quit {
		for !b.quit && b.pending() {
			// Run is not reentrant; nested clipboard loops use their own event storage.
			e := &b.runEvent
			b.callArgs = [2]uintptr{b.display, uintptr(unsafe.Pointer(e))}
			purego.SyscallN(xRaw.nextEvent, b.callArgs[:]...)
			b.event(e)
		}
		b.emitResizes()
		if !b.quit {
			if err := b.wait(b.reclaimUploads(time.Now())); err != nil {
				return err
			}
		}
	}
	return b.err
}

// openConn lets the UI thread park in Go's netpoller on a duplicate X
// descriptor. A raw blocking select kept the runtime's sysmon polling each frame.
func (b *xBackend) openConn() error {
	fd, err := syscall.Dup(b.fd)
	if err != nil {
		return err
	}
	syscall.CloseOnExec(fd)
	// Xlib's connection is already nonblocking; the duplicate shares that status.
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return err
	}
	f := os.NewFile(uintptr(fd), "x11")
	rc, err := f.SyscallConn()
	if err == nil {
		err = f.SetReadDeadline(time.Time{})
	}
	if err != nil {
		f.Close()
		return fmt.Errorf("X11 connection is not pollable: %w", err)
	}
	b.conn, b.rawConn = f, rc
	return nil
}

// Retained X11 pixels stay on the server when a large client upload buffer
// is discarded. Only pending large buffers set a deadline for the event loop.
func (b *xBackend) reclaimUploads(now time.Time) time.Duration {
	next := time.Duration(-1)
	for _, w := range b.windows {
		if w.uploadIdleAt.IsZero() {
			continue
		}
		wait := w.uploadIdleAt.Sub(now)
		if wait <= 0 {
			w.releaseUploads()
		} else if next < 0 || wait < next {
			next = wait
		}
	}
	return next
}

func (b *xBackend) pending() bool {
	b.callArgs[0] = b.display
	n, _, _ := purego.SyscallN(xRaw.pending, b.callArgs[:1]...)
	return int32(n) > 0
}

func (b *xBackend) wait(timeout time.Duration) error {
	b.callArgs[0] = b.display
	purego.SyscallN(xl.flushImage, b.callArgs[:1]...)
	if timeout >= 0 {
		b.conn.SetReadDeadline(time.Now().Add(timeout))
		b.deadline = true
	} else if b.deadline || b.kicked.Swap(false) {
		b.conn.SetReadDeadline(time.Time{})
		b.deadline = false
	}
	// Wake stores woken before checking waiting; this order prevents a lost wakeup.
	b.waiting.Store(true)
	var err error
	if !b.woken.Load() {
		if b.readReady == nil {
			b.readReady = func(uintptr) bool {
				if !b.readChecked {
					// Readiness is reset before this call; events read earlier stay queued in Xlib.
					b.readChecked = true
					return b.pending()
				}
				return true
			}
		}
		b.readChecked = false
		err = b.rawConn.Read(b.readReady)
	}
	b.waiting.Store(false)
	if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	if b.woken.Swap(false) {
		if b.eglPending {
			select {
			case r := <-b.eglDone:
				b.finishEGL(r, nil)
			default:
			}
		}
		for _, w := range b.windows {
			if w.access != nil {
				w.access.drain(func(e Event) {
					if !w.closed {
						w.emit(e)
					}
				})
			}
		}
		if b.dispatch != nil {
			b.dispatch()
		}
	}
	return nil
}

// resized defers Resize until the queued events are handled: during a drag
// only the latest size is laid out, and its full repaint covers any Expose.
func (w *xWindow) resized() {
	w.resizePending = true
	w.b.resizes = true
}

func (b *xBackend) emitResizes() {
	if !b.resizes {
		return
	}
	b.resizes = false
	for _, w := range b.windows {
		if !w.resizePending || w.closed {
			continue
		}
		w.resizePending = false
		lost := w.lost.Intersect(image.Rect(0, 0, w.width, w.height))
		w.lost = image.Rectangle{}
		w.unrestored = append(w.unrestored[:0], lost)
		w.restoring = !lost.Empty()
		w.emit(Event{Kind: Resize, Width: w.width, Height: w.height, Scale: b.scale})
		w.restoring = false
		// Content that repaints on Resize already restored the exposed area.
		var left image.Rectangle
		for _, r := range w.unrestored {
			left = left.Union(r)
		}
		if !left.Empty() && !w.closed {
			w.emit(Event{Kind: Paint, X: float32(left.Min.X), Y: float32(left.Min.Y), Width: left.Dx(), Height: left.Dy()})
		}
	}
}

// restore notes pixels presented while a deferred Resize is handled.
func (w *xWindow) restore(r image.Rectangle) {
	if !w.restoring {
		return
	}
	left := w.restScratch[:0]
	for _, u := range w.unrestored {
		left = subtractRect(left, u, r)
	}
	w.restScratch, w.unrestored = w.unrestored[:0], left
}

// subtractRect appends the parts of a outside b, as at most four rects.
func subtractRect(dst []image.Rectangle, a, b image.Rectangle) []image.Rectangle {
	i := a.Intersect(b)
	if i.Empty() {
		return append(dst, a)
	}
	for _, r := range [...]image.Rectangle{
		image.Rect(a.Min.X, a.Min.Y, a.Max.X, i.Min.Y),
		image.Rect(a.Min.X, i.Max.Y, a.Max.X, a.Max.Y),
		image.Rect(a.Min.X, i.Min.Y, i.Min.X, i.Max.Y),
		image.Rect(i.Max.X, i.Min.Y, a.Max.X, i.Max.Y),
	} {
		if !r.Empty() {
			dst = append(dst, r)
		}
	}
	return dst
}

func (b *xBackend) Wake() {
	b.woken.Store(true)
	if !b.waiting.Load() {
		return
	}
	b.wakeMu.Lock()
	if b.conn != nil {
		b.kicked.Store(true)
		b.conn.SetReadDeadline(time.Unix(1, 0))
	}
	b.wakeMu.Unlock()
}

func (b *xBackend) Quit() { b.quit = true; b.Wake() }

func (b *xBackend) release() {
	for _, w := range b.windows {
		w.Close()
	}
	b.closeEGL()
	if b.display != 0 {
		for _, c := range b.cursors {
			xl.freeCursor(b.display, c)
		}
		if b.im != 0 {
			xl.closeIM(b.im)
			b.im = 0
		}
		d := b.display
		xl.closeDisplay(d)
		b.display = 0
		xBackends.Lock()
		delete(xBackends.byDisplay, d)
		xBackends.Unlock()
	}
	b.wakeMu.Lock()
	if b.conn != nil {
		b.conn.Close()
		b.conn, b.rawConn = nil, nil
	}
	b.wakeMu.Unlock()
}

func (b *xBackend) event(raw *xEvent) {
	a := (*xAny)(unsafe.Pointer(raw))
	if a.Kind == xSelectionRequest {
		b.selectionRequest((*xSelectionRequestEvent)(unsafe.Pointer(raw)))
		return
	}
	if a.Kind == xSelectionNotify {
		b.selectionNotify((*xSelectionEvent)(unsafe.Pointer(raw)))
		return
	}
	if a.Kind == xProperty {
		p := (*xPropertyEvent)(unsafe.Pointer(raw))
		if t := b.transfer; t != nil && t.incremental && p.Window == t.window.id && p.Atom == b.clipProperty && p.State == 0 {
			b.readClipboardProperty()
		}
		return
	}
	if a.Kind == xSelectionClear {
		if b.clipboardOwner != nil && b.clipboardOwner.id == a.Window {
			b.clipboardOwner = nil
			b.clipboard = ""
		}
		return
	}
	if b.shmCompletion != 0 && a.Kind == b.shmCompletion {
		b.shmEvent(raw)
		return
	}
	b.callArgs = [2]uintptr{uintptr(unsafe.Pointer(raw)), 0}
	if r, _, _ := purego.SyscallN(xRaw.filterEvent, b.callArgs[:]...); int32(r) != 0 {
		return
	}
	w := b.windows[a.Window]
	if a.Kind == xDestroy {
		w = b.windows[uintptr(raw[5])]
	}
	if w == nil || w.closed {
		return
	}
	switch a.Kind {
	case xExpose, xGraphicsExpose:
		// Collect a series of exposed rectangles; count is the number still to come.
		x, y := int(int32(raw[5])), int(int32(raw[5]>>32))
		lost := image.Rect(x, y, x+int(int32(raw[6])), y+int(int32(raw[6]>>32)))
		w.exposed = w.exposed.Union(lost)
		if int32(raw[7]) != 0 {
			return
		}
		lost, w.exposed = w.exposed, image.Rectangle{}
		if !w.configured {
			w.configured = true
			lost = image.Rect(0, 0, w.width, w.height)
			w.resized()
		}
		if w.resizePending {
			w.lost = w.lost.Union(lost)
		} else {
			w.emit(Event{Kind: Paint, X: float32(lost.Min.X), Y: float32(lost.Min.Y), Width: lost.Dx(), Height: lost.Dy()})
		}
	case xMap:
		// A remapped window can have lost every server/backbuffer pixel,
		// even if the window manager supplies no new Configure or Expose.
		w.refreshAccessibility()
		w.resized()
	case xReparent:
		w.refreshAccessibility()
	case xConfigure:
		e := (*xConfigureEvent)(unsafe.Pointer(raw))
		if !xDimensions(int(e.Width), int(e.Height)) {
			return
		}
		if !w.configured || int(e.Width) != w.width || int(e.Height) != w.height {
			w.configured = true
			w.width, w.height = int(e.Width), int(e.Height)
			w.resized()
		}
		w.refreshAccessibility()
	case xClientMessage:
		e := (*xClientEvent)(unsafe.Pointer(raw))
		if e.Message == b.wmProtocols && e.Format == 32 && uintptr(e.Data[0]) == b.wmDelete {
			w.emit(Event{Kind: Close})
		}
	case xDestroy:
		w.dispose(false)
		w.emit(Event{Kind: Close, Destroyed: true})
	case xFocusIn, xFocusOut:
		w.focused = a.Kind == xFocusIn
		if w.ic != 0 {
			if w.focused && w.textInput {
				xl.setICFocus(w.ic)
			} else {
				xl.unsetICFocus(w.ic)
			}
		}
		w.emit(Event{Kind: Focus, Focused: w.focused})
	case xMotion, xButtonPress, xButtonRelease:
		e := (*xKeyEvent)(unsafe.Pointer(raw))
		v := xModifiers(e.State)
		v.X, v.Y = float32(e.X), float32(e.Y)
		if a.Kind == xMotion {
			v.Kind = PointerMove
		} else if e.Code >= 4 && e.Code <= 7 {
			if a.Kind == xButtonRelease {
				return
			}
			v.Kind = Scroll
			switch e.Code {
			case 4:
				v.DY = -40
			case 5:
				v.DY = 40
			case 6:
				v.DX = -40
			case 7:
				v.DX = 40
			}
		} else {
			v.Kind = PointerDown
			if a.Kind == xButtonRelease {
				v.Kind = PointerUp
			}
			v.Button = int(e.Code)
		}
		w.emit(v)
	case xKeyPress, xKeyRelease:
		w.key((*xKeyEvent)(unsafe.Pointer(raw)), a.Kind == xKeyPress)
	}
}

func xModifiers(s uint32) Event {
	return Event{Shift: s&1 != 0, Control: s&4 != 0, Alt: s&8 != 0, Super: s&64 != 0}
}

func xKeyName(k uintptr) string {
	switch k {
	case 0xff08:
		return "Backspace"
	case 0xff09, 0xfe20:
		return "Tab"
	case 0xff0d, 0xff8d:
		return "Enter"
	case 0xff1b:
		return "Escape"
	case 0x20:
		return "Space"
	case 0xffff:
		return "Delete"
	case 0xff50, 0xff95:
		return "Home"
	case 0xff57, 0xff9c:
		return "End"
	case 0xff55, 0xff9a:
		return "PageUp"
	case 0xff56, 0xff9b:
		return "PageDown"
	case 0xff51, 0xff96:
		return "ArrowLeft"
	case 0xff52, 0xff97:
		return "ArrowUp"
	case 0xff53, 0xff98:
		return "ArrowRight"
	case 0xff54, 0xff99:
		return "ArrowDown"
	case 0xff63, 0xff9e:
		return "Insert"
	case 0xffe1, 0xffe2:
		return "Shift"
	case 0xffe3, 0xffe4:
		return "Control"
	case 0xffe9, 0xffea:
		return "Alt"
	case 0xffeb, 0xffec:
		return "Super"
	}
	if k >= 0xffbe && k <= 0xffd5 {
		return "F" + strconv.Itoa(int(k-0xffbe+1))
	}
	if k >= 0xffb0 && k <= 0xffb9 {
		return string(rune('0' + k - 0xffb0))
	}
	if k&0xff000000 == 0x01000000 {
		k &= 0xffffff
	} else if k > 255 {
		return ""
	}
	if k >= 32 && k <= utf8.MaxRune && unicode.IsPrint(rune(k)) {
		return strings.ToLower(string(rune(k)))
	}
	return ""
}

func (w *xWindow) key(e *xKeyEvent, down bool) {
	v := xModifiers(e.State)
	v.Key = xKeyName(xl.lookupKeysym(e, 0))
	v.Kind = KeyUp
	text := ""
	if down {
		v.Kind = KeyDown
		var buf [128]byte
		var sym uintptr
		if w.ic != 0 && w.textInput {
			var status int32
			n := xl.utf8Lookup(w.ic, e, &buf[0], int32(len(buf)), &sym, &status)
			if status == -1 && n > 0 && n <= 1<<20 {
				large := make([]byte, n)
				n = xl.utf8Lookup(w.ic, e, &large[0], int32(len(large)), &sym, &status)
				if n > 0 && n <= int32(len(large)) && (status == 2 || status == 4) {
					text = string(large[:n])
				}
			} else if n > 0 && n <= int32(len(buf)) && (status == 2 || status == 4) {
				text = string(buf[:n])
			}
		} else {
			n := xl.lookupString(e, &buf[0], int32(len(buf)), &sym, 0)
			if n > 0 && n <= int32(len(buf)) {
				text = latin1(buf[:n])
			}
		}
		if sym != 0 {
			v.Key = xKeyName(sym)
		}
	}
	w.emit(v)
	if !w.closed && down && !v.Control && !v.Alt && !v.Super && text != "" {
		text = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, text)
		if text != "" {
			w.emit(Event{Kind: Text, Text: text})
		}
	}
}

func (w *xWindow) Present(src *image.RGBA, damage []image.Rectangle) {
	if w.closed || src == nil || len(damage) == 0 {
		return
	}
	bounds := src.Bounds()
	if w.graphics.present(src, damage) {
		for _, r := range damage {
			w.restore(r.Intersect(bounds).Sub(bounds.Min))
		}
		return
	}
	if !w.presented || w.surfaceSize != bounds.Size() {
		damage = []image.Rectangle{bounds}
	}
	if w.present(src, damage, bounds.Min) {
		w.flushImage()
		w.surfaceSize = bounds.Size()
		for _, r := range damage {
			w.restore(r.Intersect(bounds).Sub(bounds.Min))
		}
	}
}

// PresentRegion uploads a damaged region; the X server retains the rest and
// reports lost pixels with Expose, which becomes a full Paint event.
func (w *xWindow) PresentRegion(src *image.RGBA) {
	w.queueRegion(src)
	w.flushImage()
}

// All bands belong to one frame. Xlib can queue them until the final flush.
func (w *xWindow) PresentRegions(paint func(func(*image.RGBA))) {
	paint(w.queueRegion)
	w.flushImage()
}

func (w *xWindow) queueRegion(src *image.RGBA) {
	if w.closed || src == nil {
		return
	}
	damage := [1]image.Rectangle{src.Rect}
	if w.present(src, damage[:], image.Point{}) {
		w.surfaceSize = image.Point{}
		w.restore(src.Rect)
	}
}

func (w *xWindow) flushImage() {
	if !w.closed {
		w.putArgs[0] = w.b.display
		purego.SyscallN(xl.flushImage, w.putArgs[:1]...)
	}
}

func (w *xWindow) present(src *image.RGBA, damage []image.Rectangle, origin image.Point) bool {
	bounds := src.Bounds()
	if !xValidRGBA(src) {
		w.b.err = errors.New("invalid RGBA surface")
		w.b.Quit()
		return false
	}
	needed := 0
	for _, rect := range damage {
		r := rect.Intersect(bounds)
		needed = max(needed, min(xUploadBytes, r.Dx()*r.Dy()*4))
	}
	if needed == 0 {
		return false
	}
	capacity := w.uploadCapacity(needed)
	var shm *xShm
	if w.render.dst != 0 {
		w.render.resize(w, max(w.width, bounds.Dx()))
	} else {
		shm = w.shmBuffers(capacity)
	}
	if shm != nil {
		if w.img != nil {
			xl.destroyImage(w.img)
			w.img = nil
		}
		w.imageBytes = capacity
	} else if !w.socketBuffers(capacity) {
		return false
	}
	im := w.img
	if shm != nil {
		im = shm.img
	}
	for _, rect := range damage {
		r := rect.Intersect(bounds)
		if r.Empty() {
			continue
		}
		rows := max(1, w.imageBytes/(r.Dx()*4))
		if w.render.dst != 0 {
			rows = min(rows, w.render.rows)
		}
		for y := r.Min.Y; y < r.Max.Y; y += rows {
			tile := image.Rect(r.Min.X, y, r.Max.X, min(y+rows, r.Max.Y))
			im.Width, im.Height, im.BytesPerLine = int32(tile.Dx()), int32(tile.Dy()), int32(tile.Dx()*4)
			var pix []byte
			if shm != nil {
				pix = w.shmTile(shm)[:int(im.BytesPerLine)*tile.Dy()]
			} else {
				// XPutImage copies each tile before returning, so this buffer is reusable.
				pix = unsafe.Slice(im.Data, int(im.BytesPerLine)*tile.Dy())
			}
			view := *src
			view.Pix = src.Pix[src.PixOffset(tile.Min.X, tile.Min.Y):]
			view.Rect = tile
			xCopyPixels(pix, int(im.BytesPerLine), &view, tile, im.ByteOrder != 0)
			x, dy := tile.Min.X-origin.X, tile.Min.Y-origin.Y
			if shm != nil {
				w.shmPut(shm, int32(x), int32(dy), uint32(tile.Dx()), uint32(tile.Dy()))
			} else {
				w.putImage(int32(x), int32(dy), uint32(tile.Dx()), uint32(tile.Dy()))
			}
		}
	}
	runtime.KeepAlive(src)
	runtime.KeepAlive(w)
	if capacity > xUploadIdleBytes {
		w.uploadIdleAt = time.Now().Add(xUploadIdleTime)
	} else {
		w.uploadIdleAt = time.Time{}
	}
	w.presented = true
	return true
}

func (w *xWindow) releaseUploads() {
	if w.img != nil {
		xl.destroyImage(w.img)
		w.img = nil
	}
	w.releaseShm()
	w.imageBytes = 0
	w.smallUploads = 0
	w.uploadIdleAt = time.Time{}
}

// socketBuffers keeps a client-side staging image for XPutImage.
func (w *xWindow) socketBuffers(capacity int) bool {
	if w.img == nil {
		im := xl.createImage(w.b.display, w.b.visual, uint32(w.b.depth), 2, 0, nil, 1, 1, 32, 0)
		if im == nil {
			w.b.err = errors.New("XCreateImage failed")
			w.b.Quit()
			return false
		}
		if im.BitsPerPixel != 32 || im.BitmapPad != 32 || im.Red != 0xff0000 || im.Green != 0xff00 || im.Blue != 0xff {
			xl.destroyImage(im)
			w.b.err = errors.New("unsupported XImage pixel layout")
			w.b.Quit()
			return false
		}
		w.img = im
	}
	if im := w.img; capacity != w.imageBytes || im.Data == nil {
		data := xl.calloc(uintptr(capacity), 1)
		if data == nil {
			w.b.err = errors.New("XImage allocation failed")
			w.b.Quit()
			return false
		}
		if im.Data != nil {
			xl.free(unsafe.Pointer(im.Data))
		}
		im.Data = data
		w.imageBytes = capacity
	}
	return true
}

func (w *xWindow) putImage(x, y int32, width, height uint32) {
	// Reuse the ABI argument storage instead of allocating reflection/variadic arrays.
	dst, dx, dy := w.id, x, y
	if w.render.dst != 0 {
		dst, dx, dy = w.render.pix, 0, 0
	}
	w.putArgs = [10]uintptr{w.b.display, dst, w.gc, uintptr(unsafe.Pointer(w.img)), 0, 0, uintptr(dx), uintptr(dy), uintptr(width), uintptr(height)}
	purego.SyscallN(xl.putImage, w.putArgs[:]...)
	if w.render.dst != 0 {
		xr.composite(w.b.display, 1, w.render.src, 0, w.render.dst, 0, 0, 0, 0, x, y, width, height)
	}
	runtime.KeepAlive(w)
}

// Keep small damage tiles reusable; release an oversized buffer after a full repaint.
func xUploadCapacity(current, needed int) int {
	needed = max(4096, needed)
	if needed > current || needed < current/2 {
		return needed
	}
	return current
}

func (w *xWindow) uploadCapacity(needed int) int {
	capacity := xUploadCapacity(w.imageBytes, needed)
	if capacity < w.imageBytes {
		w.smallUploads++
		// Alternating full/partial updates keep one buffer instead of reallocating each frame.
		if w.smallUploads < 8 {
			return w.imageBytes
		}
	}
	w.smallUploads = 0
	return capacity
}

func xValidRGBA(src *image.RGBA) bool {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	return xDimensions(w, h) && src.Stride >= 4*w && len(src.Pix) >= 4*w && h-1 <= (len(src.Pix)-4*w)/src.Stride
}

func (w *xWindow) ScrollPixels(r image.Rectangle, delta image.Point) bool {
	if w.graphics.renderer != nil {
		return !w.closed && w.graphics.scroll(r, delta)
	}
	if w.closed || !w.presented || delta.X != 0 || delta.Y == 0 || !r.In(image.Rect(0, 0, w.width, w.height)) {
		return false
	}
	dst := r.Intersect(r.Add(delta))
	if dst.Empty() {
		return false
	}
	src := dst.Sub(delta)
	// The GC keeps GraphicsExpose enabled; obscured source pixels trigger repaint.
	w.putArgs = [10]uintptr{w.b.display, w.id, w.id, w.gc, uintptr(src.Min.X), uintptr(src.Min.Y), uintptr(src.Dx()), uintptr(src.Dy()), uintptr(dst.Min.X), uintptr(dst.Min.Y)}
	purego.SyscallN(xRaw.copyArea, w.putArgs[:]...)
	runtime.KeepAlive(w)
	return true
}

func xCopyPixels(dst []byte, stride int, src *image.RGBA, r image.Rectangle, bigEndian bool) {
	if r.Empty() {
		return
	}
	width := r.Dx() * 4
	dest := (r.Min.Y-src.Rect.Min.Y)*stride + (r.Min.X-src.Rect.Min.X)*4
	if !bigEndian && stride == width && src.Stride == width {
		n := width * r.Dy()
		at := src.PixOffset(r.Min.X, r.Min.Y)
		xCopyBGRX(dst[dest:dest+n], src.Pix[at:at+n])
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		s := src.Pix[src.PixOffset(r.Min.X, y):][:r.Dx()*4]
		d := dst[(y-src.Rect.Min.Y)*stride+(r.Min.X-src.Rect.Min.X)*4:][:len(s)]
		if bigEndian {
			for i := 0; i < len(s); i += 4 {
				d[i], d[i+1], d[i+2], d[i+3] = 255, s[i], s[i+1], s[i+2]
			}
			continue
		}
		xCopyBGRX(d, s)
	}
}

func xCopyWords(d, s []byte) {
	// Both supported Linux architectures permit unaligned little-endian words.
	sw := unsafe.Slice((*uint64)(unsafe.Pointer(unsafe.SliceData(s))), len(s)/8)
	dw := unsafe.Slice((*uint64)(unsafe.Pointer(unsafe.SliceData(d))), len(d)/8)
	for i, v := range sw {
		// Reverse and rotate two RGBA pixels into BGRX, then force opaque alpha.
		dw[i] = bits.RotateLeft64(bits.ReverseBytes64(v), 24) | 0xff000000ff000000
	}
	if len(s)%8 != 0 {
		i := len(s) - 4
		d[i], d[i+1], d[i+2], d[i+3] = s[i+2], s[i+1], s[i], 255
	}
}

func (w *xWindow) SetTitle(title string) {
	if w.closed {
		return
	}
	title = strings.ReplaceAll(title, "\x00", "")
	w.title = title
	xl.storeName(w.b.display, w.id, title)
	w.b.putBytes(w.id, w.b.titleAtom, w.b.utf8, []byte(title))
	w.refreshAccessibility()
}

func (w *xWindow) SetSize(width, height int) {
	if w.closed {
		return
	}
	x, y := int(math.Round(float64(width)*float64(w.b.scale))), int(math.Round(float64(height)*float64(w.b.scale)))
	if xDimensions(x, y) {
		xl.resizeWindow(w.b.display, w.id, uint32(x), uint32(y))
		xl.flush(w.b.display)
	}
}

func (w *xWindow) SetCursor(name string) {
	if w.closed {
		return
	}
	shape := uint32(68)
	switch name {
	case "pointer":
		shape = 60
	case "text":
		shape = 152
	case "move":
		shape = 52
	case "crosshair":
		shape = 34
	case "ew-resize":
		shape = 108
	case "ns-resize":
		shape = 116
	case "wait":
		shape = 150
	}
	c := w.b.cursors[shape]
	if c == 0 {
		c = xl.createCursor(w.b.display, shape)
		w.b.cursors[shape] = c
	}
	xl.defineCursor(w.b.display, w.id, c)
}

func (w *xWindow) TextInput(enabled bool, caret image.Rectangle) {
	if w.closed {
		return
	}
	changed := w.textInput != enabled
	w.textInput = enabled
	// The input method is ready before interaction. Allocate a context only
	// for windows that accept text, and reuse it across focus changes.
	if changed && enabled && w.ic == 0 && w.b.im != 0 {
		w.ic = xl.createIC(w.b.im, "inputStyle", 0x408, "clientWindow", w.id, "focusWindow", w.id, 0)
	}
	if w.ic == 0 {
		return
	}
	if changed {
		if enabled && w.focused {
			xl.setICFocus(w.ic)
		} else {
			xl.unsetICFocus(w.ic)
		}
	}
	if enabled {
		spot := xPoint{int16(min(max(caret.Min.X, 0), 32767)), int16(min(max(caret.Max.Y, 0), 32767))}
		if !changed && w.preeditSpot == spot {
			return
		}
		attrs := xl.nestedList(0, "spotLocation", &spot, 0)
		if attrs != nil {
			xl.setICValues(w.ic, "preeditAttributes", attrs, 0)
			xl.free(attrs)
			w.preeditSpot = spot
		}
	}
}

func (w *xWindow) Close() { w.dispose(true) }

func (w *xWindow) dispose(destroy bool) {
	if w.closed {
		return
	}
	w.graphics.close()
	w.render.close(w.b.display)
	w.closed = true
	if w.access != nil {
		w.access.close()
		w.access = nil
	}
	delete(w.b.windows, w.id)
	if w.b.clipboardOwner == w {
		w.b.clipboardOwner = nil
		w.b.clipboard = ""
	}
	if t := w.b.transfer; t != nil && t.window == w {
		t.done, t.failed = true, true
	}
	if w.ic != 0 {
		xl.destroyIC(w.ic)
		w.ic = 0
	}
	w.releaseUploads()
	if w.gc != 0 {
		xl.freeGC(w.b.display, w.gc)
		w.gc = 0
	}
	if destroy {
		xl.destroyWindow(w.b.display, w.id)
	}
}

func (w *xWindow) SetClipboard(text string) {
	if w.closed {
		return
	}
	w.b.clipboard, w.b.clipboardOwner = text, w
	xl.setSelectionOwner(w.b.display, w.b.clipboardAtom, w.id, 0)
	xl.flush(w.b.display)
}

func (w *xWindow) Clipboard() string {
	if w.closed {
		return ""
	}
	b := w.b
	owner := xl.selectionOwner(b.display, b.clipboardAtom)
	if owner == 0 {
		return ""
	}
	if b.clipboardOwner != nil && b.clipboardOwner.id == owner {
		return b.clipboard
	}
	if b.transfer != nil {
		return ""
	}
	t := &xClipboardRead{window: w, target: b.utf8}
	b.transfer = t
	defer func() { b.transfer = nil }()
	xl.deleteProperty(b.display, w.id, b.clipProperty)
	xl.convertSelection(b.display, b.clipboardAtom, t.target, b.clipProperty, w.id, 0)
	deadline := time.Now().Add(2 * time.Second)
	for !t.done && !b.quit {
		for !t.done && xl.pending(b.display) > 0 {
			var e xEvent
			xl.nextEvent(b.display, &e)
			b.event(&e)
		}
		left := time.Until(deadline)
		if t.done || left <= 0 {
			break
		}
		if err := b.wait(left); err != nil {
			t.failed = true
			break
		}
	}
	if !t.done || t.failed {
		return ""
	}
	if t.target == 31 {
		return latin1(t.data)
	}
	return string(t.data)
}

func latin1(data []byte) string {
	r := make([]rune, len(data))
	for i, c := range data {
		r[i] = rune(c)
	}
	return string(r)
}

func (b *xBackend) putBytes(window, property, kind uintptr, data []byte) {
	mode := int32(0)
	if len(data) == 0 {
		xl.changeProperty(b.display, window, property, kind, 8, mode, nil, 0)
		return
	}
	for len(data) > 0 {
		n := min(len(data), 32<<10)
		xl.changeProperty(b.display, window, property, kind, 8, mode, unsafe.Pointer(&data[0]), int32(n))
		data = data[n:]
		mode = 2
	}
}

func (b *xBackend) selectionRequest(e *xSelectionRequestEvent) {
	property := e.Property
	if property == 0 {
		property = e.Target
	}
	if e.Selection != b.clipboardAtom || b.clipboardOwner == nil || e.Window != b.clipboardOwner.id {
		property = 0
	} else {
		switch e.Target {
		case b.targets:
			atoms := [4]uintptr{b.targets, b.utf8, b.textAtom, 31}
			xl.changeProperty(b.display, e.Requestor, property, 4, 32, 0, unsafe.Pointer(&atoms[0]), int32(len(atoms)))
		case b.utf8, b.textAtom:
			b.putBytes(e.Requestor, property, b.utf8, []byte(b.clipboard))
		case 31:
			data := make([]byte, 0, len(b.clipboard))
			for _, r := range b.clipboard {
				if r > 255 {
					r = '?'
				}
				data = append(data, byte(r))
			}
			b.putBytes(e.Requestor, property, 31, data)
		default:
			property = 0
		}
	}
	var raw xEvent
	n := (*xSelectionEvent)(unsafe.Pointer(&raw))
	*n = xSelectionEvent{xAny: xAny{Kind: xSelectionNotify, Display: b.display, Window: e.Requestor}, Selection: e.Selection, Target: e.Target, Property: property, Time: e.Time}
	xl.sendEvent(b.display, e.Requestor, 0, 0, &raw)
	xl.flush(b.display)
}

func (b *xBackend) selectionNotify(e *xSelectionEvent) {
	t := b.transfer
	if t == nil || e.Window != t.window.id || e.Selection != b.clipboardAtom || e.Target != t.target {
		return
	}
	if e.Property == 0 {
		if t.target == b.utf8 {
			t.target = 31
			xl.convertSelection(b.display, b.clipboardAtom, t.target, b.clipProperty, t.window.id, 0)
		} else {
			t.done, t.failed = true, true
		}
		return
	}
	if e.Property != b.clipProperty {
		return
	}
	b.readClipboardProperty()
}

func (b *xBackend) readClipboardProperty() {
	t := b.transfer
	var kind uintptr
	var format int32
	var count, remaining uint64
	var data *byte
	status := xl.getProperty(b.display, t.window.id, b.clipProperty, 0, xMaxClipboard/4, 1, 0, &kind, &format, &count, &remaining, &data)
	if data != nil {
		defer xl.free(unsafe.Pointer(data))
	}
	if status != 0 || remaining != 0 || count > uint64(xMaxClipboard-len(t.data)) {
		t.done, t.failed = true, true
		return
	}
	if kind == b.incr && !t.incremental {
		// GetProperty already acknowledged INCR; another delete can drop the first chunk.
		t.incremental = true
		return
	}
	if format != 8 || (kind != b.utf8 && kind != 31 && kind != b.textAtom) || count > 0 && data == nil {
		t.done, t.failed = true, true
		return
	}
	if count > 0 {
		t.data = append(t.data, unsafe.Slice(data, int(count))...)
	}
	if !t.incremental || count == 0 {
		t.done = true
	}
}

func (w *xWindow) SetMaximized(on bool) {
	if w.closed {
		return
	}
	b := w.b
	atom := func(name string) uintptr { return xl.internAtom(b.display, name, 0) }
	var raw xEvent
	e := (*xClientEvent)(unsafe.Pointer(&raw))
	e.Kind = xClientMessage
	e.Display = b.display
	e.Window = w.id
	e.Message = atom("_NET_WM_STATE")
	e.Format = 32
	if on {
		e.Data[0] = 1
	}
	e.Data[1] = int64(atom("_NET_WM_STATE_MAXIMIZED_VERT"))
	e.Data[2] = int64(atom("_NET_WM_STATE_MAXIMIZED_HORZ"))
	e.Data[3] = 1
	xl.sendEvent(b.display, b.root, 0, (1<<19)|(1<<20), &raw)
	xl.flush(b.display)
}
func (w *xWindow) Maximized() bool {
	if w.closed {
		return false
	}
	d := w.b.display
	var kind uintptr
	var format int32
	var count, rest uint64
	var data *byte
	status := xl.getProperty(d, w.id, xl.internAtom(d, "_NET_WM_STATE", 0), 0, 128, 0, 4, &kind, &format, &count, &rest, &data)
	if data == nil {
		return false
	}
	defer xl.free(unsafe.Pointer(data))
	if status != 0 || kind != 4 || format != 32 || count > 128 {
		return false
	}
	vert, horz := xl.internAtom(d, "_NET_WM_STATE_MAXIMIZED_VERT", 0), xl.internAtom(d, "_NET_WM_STATE_MAXIMIZED_HORZ", 0)
	var v, h bool
	for _, a := range unsafe.Slice((*uintptr)(unsafe.Pointer(data)), int(count)) {
		v = v || a == vert
		h = h || a == horz
	}
	return v && h
}
