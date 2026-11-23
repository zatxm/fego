//go:build darwin && !ios && (amd64 || arm64)

package platform

import (
	"errors"
	"image"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var macOpenGL = sync.OnceValues(func() (uintptr, error) {
	return purego.Dlopen("/System/Library/Frameworks/OpenGL.framework/OpenGL", purego.RTLD_NOW|purego.RTLD_LOCAL)
})

type cglContext struct {
	window  *cocoaWindow
	context objc.ID
	library uintptr
	size    [3]float64
}

func (w *cocoaWindow) SetGPUEnabled(enabled bool) bool {
	changed := w.graphics.set(enabled, w.newGPUContext)
	if changed && w.GPUActive() {
		if w.image != 0 {
			mac.imageRelease(w.image)
			w.image = 0
		}
	}
	return changed
}
func (w *cocoaWindow) PresentGPURegions(size image.Point, paint func(func(*image.RGBA))) bool {
	return w.graphics.presentRegions(size, paint)
}
func (w *cocoaWindow) GPUActive() bool                       { return w.graphics.renderer != nil }
func (w *cocoaWindow) SetFragmentShader(source string) error { return w.graphics.shader(source) }

func (w *cocoaWindow) newGPUContext() (glContext, error) {
	lib, err := macOpenGL()
	if err != nil {
		return nil, err
	}
	attributes := [...]uint32{99, 0x4100, 5, 73, 8, 24, 11, 8, 0}
	format := macSend(macSend(macClass("NSOpenGLPixelFormat"), "alloc"), "initWithAttributes:", uintptr(unsafe.Pointer(&attributes[0])))
	runtime.KeepAlive(attributes)
	if format == 0 {
		return nil, errors.New("hardware NSOpenGL pixel format unavailable")
	}
	context := macSend(macSend(macClass("NSOpenGLContext"), "alloc"), "initWithFormat:shareContext:", uintptr(format), 0)
	macSend(format, "release")
	if context == 0 {
		return nil, errors.New("create NSOpenGL 4.1 context")
	}
	macSend(w.view, "setWantsBestResolutionOpenGLSurface:", 1)
	macSend(context, "setView:", uintptr(w.view))
	interval := int32(0)
	macSend(context, "setValues:forParameter:", uintptr(unsafe.Pointer(&interval)), 222)
	return &cglContext{window: w, context: context, library: lib}, nil
}

func (c *cglContext) MakeCurrent() bool {
	if c.context == 0 || c.window.closed {
		return false
	}
	bounds := mac.rect(c.window.view, macSel("bounds"))
	size := [3]float64{bounds.Size.Width, bounds.Size.Height, c.window.scale()}
	if size != c.size {
		macSend(c.context, "update")
		c.size = size
	}
	macSend(c.context, "makeCurrentContext")
	return macSend(macClass("NSOpenGLContext"), "currentContext") == c.context
}
func (c *cglContext) Swap() bool {
	if c.context == 0 || c.window.closed {
		return false
	}
	macSend(c.context, "flushBuffer")
	return true
}
func (c *cglContext) Proc(name string) uintptr {
	address, _ := purego.Dlsym(c.library, name)
	return address
}
func (c *cglContext) Close() {
	if c.context == 0 {
		return
	}
	macSend(c.context, "clearDrawable")
	if macSend(macClass("NSOpenGLContext"), "currentContext") == c.context {
		macSend(macClass("NSOpenGLContext"), "clearCurrentContext")
	}
	macSend(c.context, "release")
	c.context = 0
}
