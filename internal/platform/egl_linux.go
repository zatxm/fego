//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"image"
	"sync"

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
}

var gpuX11Query func(uintptr, string, *int32, *int32, *int32) int32
var gpuX11Visual func(uintptr) uintptr

func loadEGL() error {
	egl.once.Do(func() {
		x11, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			egl.err = err
			return
		}
		purego.RegisterLibFunc(&gpuX11Query, x11, "XQueryExtension")
		purego.RegisterLibFunc(&gpuX11Visual, x11, "XVisualIDFromVisual")
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
}

func (w *xWindow) SetGPUEnabled(enabled bool) bool {
	changed := w.graphics.set(enabled, w.newGPUContext)
	if w.b.eglContexts == 0 && w.b.eglDisplay != 0 {
		egl.terminate(w.b.eglDisplay)
		w.b.eglDisplay = 0
	}
	w.surfaceSize = image.Point{}
	return changed
}
func (w *xWindow) UsesFullSurface() bool                 { return w.GPUActive() }
func (w *xWindow) GPUActive() bool                       { return w.graphics.renderer != nil }
func (w *xWindow) SetFragmentShader(source string) error { return w.graphics.shader(source) }

func (w *xWindow) newGPUContext() (glContext, error) {
	b := w.b
	if err := loadEGL(); err != nil {
		return nil, err
	}
	var opcode, event, errorBase int32
	if gpuX11Query(b.display, "DRI3", &opcode, &event, &errorBase) == 0 {
		return nil, errors.New("X11 DRI3 unavailable")
	}
	if b.eglDisplay == 0 {
		display := egl.getDisplay(0x31d5, b.display, nil)
		var major, minor int32
		if display == 0 || egl.initialize(display, &major, &minor) == 0 {
			return nil, fmt.Errorf("initialize EGL: %#x", egl.getError())
		}
		b.eglDisplay = display
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
func (c *eglContext) Swap() bool               { return c.surface != 0 && egl.swapBuffers(c.display, c.surface) != 0 }
func (c *eglContext) Proc(name string) uintptr { return egl.getProc(name) }
func (c *eglContext) Close() {
	if c.context == 0 {
		return
	}
	egl.makeCurrent(c.display, 0, 0, 0)
	egl.destroySurface(c.display, c.surface)
	egl.destroyContext(c.display, c.context)
	c.surface, c.context = 0, 0
	c.b.eglContexts--
	if c.b.eglContexts == 0 {
		egl.terminate(c.display)
		c.b.eglDisplay = 0
	}
}
