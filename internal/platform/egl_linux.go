//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var egl struct {
	once           sync.Once
	err            error
	getDisplay     func(uint32, uintptr, *uintptr) uintptr
	initialize     func(uintptr, *int32, *int32) uint32
	terminate      func(uintptr) uint32
	bindAPI        func(uint32) uint32
	chooseConfig   func(uintptr, *int32, *uintptr, int32, *int32) uint32
	configAttrib   func(uintptr, uintptr, int32, *int32) uint32
	createContext  func(uintptr, uintptr, uintptr, *int32) uintptr
	createSurface  func(uintptr, uintptr, uintptr, *int32) uintptr
	makeCurrent    func(uintptr, uintptr, uintptr, uintptr) uint32
	swapBuffers    func(uintptr, uintptr) uint32
	swapInterval   func(uintptr, int32) uint32
	destroyContext func(uintptr, uintptr) uint32
	destroySurface func(uintptr, uintptr) uint32
	getError       func() uint32
	getProc        func(string) uintptr
	queryString    func(uintptr, int32) *byte
}

var gpuX11Query func(uintptr, string, *int32, *int32, *int32) int32
var gpuX11Visual func(uintptr) uintptr
var gpuX11Sync func(uintptr, int32) int32

func loadEGL() error {
	egl.once.Do(func() {
		x11, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			egl.err = err
			return
		}
		purego.RegisterLibFunc(&gpuX11Query, x11, "XQueryExtension")
		purego.RegisterLibFunc(&gpuX11Visual, x11, "XVisualIDFromVisual")
		purego.RegisterLibFunc(&gpuX11Sync, x11, "XSync")
		lib, err := purego.Dlopen("libEGL.so.1", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			egl.err = err
			return
		}
		for _, symbol := range []struct {
			name string
			fn   any
		}{
			{"eglGetPlatformDisplay", &egl.getDisplay}, {"eglInitialize", &egl.initialize}, {"eglTerminate", &egl.terminate},
			{"eglBindAPI", &egl.bindAPI}, {"eglChooseConfig", &egl.chooseConfig}, {"eglGetConfigAttrib", &egl.configAttrib},
			{"eglCreateContext", &egl.createContext}, {"eglCreateWindowSurface", &egl.createSurface}, {"eglMakeCurrent", &egl.makeCurrent},
			{"eglSwapBuffers", &egl.swapBuffers}, {"eglSwapInterval", &egl.swapInterval}, {"eglDestroyContext", &egl.destroyContext},
			{"eglDestroySurface", &egl.destroySurface}, {"eglGetError", &egl.getError}, {"eglGetProcAddress", &egl.getProc},
			{"eglQueryString", &egl.queryString},
		} {
			address, err := purego.Dlsym(lib, symbol.name)
			if err != nil {
				egl.err = err
				return
			}
			purego.RegisterFunc(symbol.fn, address)
		}
	})
	return egl.err
}

type eglContext struct {
	b                         *xBackend
	display, surface, context uintptr
	swapDamage                uintptr
	args                      [4]uintptr
}

func (w *xWindow) SetGPUEnabled(enabled bool) bool {
	if w.preferXRender {
		if enabled == (w.render.dst != 0) {
			return false
		}
		if enabled && !w.render.open(w) {
			return false
		}
		if !enabled {
			w.render.close(w.b.display)
		}
		w.releaseUploads()
		w.surfaceSize = image.Point{}
		return true
	}
	b := w.b
	if enabled && !w.graphics.requested {
		w.gpuFailed = false
		if b.eglDisplay == 0 {
			// Loading the driver takes tens of milliseconds. StartGPU or AwaitGPU
			// begins it; CPU frames continue until the event loop sees the result.
			w.graphics.requested = true
			return false
		}
	}
	changed := w.graphics.set(enabled, w.newGPUContext)
	if changed && w.graphics.renderer != nil {
		w.releaseUploads()
	}
	b.releaseEGL()
	w.surfaceSize = image.Point{}
	return changed
}

func (w *xWindow) gpuWaiting() bool {
	return !w.closed && !w.preferXRender && w.graphics.requested && w.graphics.renderer == nil && !w.gpuFailed
}

// StartGPU begins a requested EGL start in the background.
func (w *xWindow) StartGPU() {
	if w.gpuWaiting() && w.b.eglDisplay == 0 {
		w.b.startEGL()
	}
}

// AwaitGPU completes a requested start and reports whether w became active.
// Other windows that became active receive a Paint event.
func (w *xWindow) AwaitGPU() bool {
	w.StartGPU()
	if !w.b.eglPending {
		return false
	}
	return w.b.finishEGL(<-w.b.eglDone, w)
}

type eglStart struct {
	xdisplay, display uintptr
	err               error
}

func (b *xBackend) startEGL() {
	if b.eglPending {
		return
	}
	if b.eglDone == nil {
		b.eglDone = make(chan eglStart, 1)
	}
	b.eglPending = true
	// Load what the CPU frames need first: dlopen and dlsym wait while the
	// driver is being loaded on the other thread.
	loadXShm()
	xdisplay := b.eglXDisplay
	go func() {
		b.eglDone <- openEGL(xdisplay)
		b.Wake()
	}()
}

// openEGL initializes EGL on a private X connection, so it can run beside the
// UI thread. Window surfaces still use the windows' server-side IDs.
func openEGL(xdisplay uintptr) (r eglStart) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r.xdisplay = xdisplay
	if r.err = loadEGL(); r.err != nil {
		return r
	}
	if r.xdisplay == 0 {
		if r.xdisplay = xl.openDisplay(""); r.xdisplay == 0 {
			r.err = errors.New("open X11 display for EGL")
			return r
		}
	}
	var opcode, event, errorBase int32
	if gpuX11Query(r.xdisplay, "DRI3", &opcode, &event, &errorBase) == 0 {
		r.err = errors.New("X11 DRI3 unavailable")
		return r
	}
	display := egl.getDisplay(0x31d5, r.xdisplay, nil)
	var major, minor int32
	if display == 0 || egl.initialize(display, &major, &minor) == 0 {
		r.err = fmt.Errorf("initialize EGL: %#x", egl.getError())
		return r
	}
	r.display = display
	return r
}

// finishEGL runs on the UI thread and creates contexts for waiting windows.
// It reports whether caller became active; caller repaints itself.
func (b *xBackend) finishEGL(r eglStart, caller *xWindow) (activated bool) {
	b.eglPending = false
	b.eglXDisplay = r.xdisplay
	if r.err == nil {
		b.eglDisplay = r.display
	}
	for _, w := range b.windows {
		if !w.gpuWaiting() {
			continue
		}
		if r.err != nil || !w.graphics.open(w.newGPUContext) {
			// Like a failed synchronous start, stay on the CPU until re-enabled.
			w.gpuFailed = true
			continue
		}
		w.releaseUploads()
		w.surfaceSize = image.Point{}
		if w == caller {
			activated = true
		} else {
			w.emit(Event{Kind: Paint})
		}
	}
	b.releaseEGL()
	return activated
}

// releaseEGL terminates an initialized display once no context uses it.
func (b *xBackend) releaseEGL() {
	if b.eglContexts == 0 && b.eglDisplay != 0 {
		egl.terminate(b.eglDisplay)
		b.eglDisplay = 0
	}
}

// closeEGL runs when the backend is released, after its windows closed.
func (b *xBackend) closeEGL() {
	if b.eglPending {
		b.finishEGL(<-b.eglDone, nil)
	}
	if b.eglXDisplay != 0 {
		xl.closeDisplay(b.eglXDisplay)
		b.eglXDisplay = 0
	}
}
func (w *xWindow) PresentGPURegions(size image.Point, paint func(func(*image.RGBA))) bool {
	if w.render.dst != 0 {
		paint(w.PresentRegion)
		return true
	}
	if w.restoring {
		frame := paint
		paint = func(upload func(*image.RGBA)) {
			frame(func(img *image.RGBA) {
				upload(img)
				w.restore(img.Rect)
			})
		}
	}
	return w.graphics.presentRegions(size, paint)
}
func (w *xWindow) GPUActive() bool { return w.render.dst != 0 || w.graphics.renderer != nil }
func (w *xWindow) SetFragmentShader(source string) error {
	if w.render.dst != 0 {
		if source == "" {
			return nil
		}
		return errors.New("fego: custom fragment shaders require OpenGL; disable X11Render for this window")
	}
	return w.graphics.shader(source)
}

func (w *xWindow) newGPUContext() (glContext, error) {
	b := w.b
	if b.eglDisplay == 0 {
		return nil, errors.New("EGL is not initialized")
	}
	display := b.eglDisplay
	if egl.bindAPI(0x30a2) == 0 {
		return nil, errors.New("EGL OpenGL API unavailable")
	}
	attributes := [...]int32{0x3033, 4, 0x3040, 8, 0x3024, 8, 0x3023, 8, 0x3022, 8, 0x3038}
	var configs [64]uintptr
	var count int32
	if egl.chooseConfig(display, &attributes[0], &configs[0], int32(len(configs)), &count) == 0 {
		return nil, fmt.Errorf("choose EGL config: %#x", egl.getError())
	}
	visual := gpuX11Visual(b.visual)
	var selected uintptr
	for _, config := range configs[:min(max(0, count), int32(len(configs)))] {
		var id int32
		if egl.configAttrib(display, config, 0x302e, &id) != 0 && uintptr(id) == visual {
			selected = config
			break
		}
	}
	if selected == 0 {
		return nil, errors.New("no EGL config matches the X11 visual")
	}
	contextAttributes := [...]int32{0x3098, 3, 0x30fb, 3, 0x30fd, 1, 0x3038}
	ctx := egl.createContext(display, selected, 0, &contextAttributes[0])
	if ctx == 0 {
		return nil, fmt.Errorf("create EGL 3.3 context: %#x", egl.getError())
	}
	surface := egl.createSurface(display, selected, w.id, nil)
	if surface == 0 {
		egl.destroyContext(display, ctx)
		return nil, fmt.Errorf("create EGL window surface: %#x", egl.getError())
	}
	b.eglContexts++
	c := &eglContext{b: b, display: display, surface: surface, context: ctx}
	extensions := " " + xString(egl.queryString(display, 0x3055)) + " "
	for _, name := range [...]string{"KHR", "EXT"} {
		if strings.Contains(extensions, " EGL_"+name+"_swap_buffers_with_damage ") {
			c.swapDamage = egl.getProc("eglSwapBuffersWithDamage" + name)
			break
		}
	}
	if !c.MakeCurrent() {
		c.Close()
		return nil, errors.New("activate EGL window context")
	}
	egl.swapInterval(display, 0)
	return c, nil
}

func (c *eglContext) MakeCurrent() bool {
	return c.context != 0 && egl.makeCurrent(c.display, c.surface, c.surface, c.context) != 0
}
func (c *eglContext) Swap() bool { return c.surface != 0 && egl.swapBuffers(c.display, c.surface) != 0 }

// SwapDamage swaps but tells the display server that only rects changed.
// rects hold x, y, width, height with a bottom-left origin.
func (c *eglContext) SwapDamage(rects []int32) bool {
	if c.swapDamage == 0 || len(rects) == 0 {
		return c.Swap()
	}
	c.args = [4]uintptr{c.display, c.surface, uintptr(unsafe.Pointer(&rects[0])), uintptr(len(rects) / 4)}
	ok, _, _ := purego.SyscallN(c.swapDamage, c.args[:]...)
	runtime.KeepAlive(rects)
	return c.surface != 0 && uint32(ok) != 0
}
func (c *eglContext) Proc(name string) uintptr { return egl.getProc(name) }
func (c *eglContext) Close() {
	if c.context == 0 {
		return
	}
	// Presents travel on the EGL connection, unordered with the window's own
	// requests: let them land before CPU uploads replace this surface.
	gpuX11Sync(c.b.eglXDisplay, 0)
	egl.makeCurrent(c.display, 0, 0, 0)
	egl.destroySurface(c.display, c.surface)
	egl.destroyContext(c.display, c.context)
	c.surface, c.context = 0, 0
	c.b.eglContexts--
	c.b.releaseEGL()
}
