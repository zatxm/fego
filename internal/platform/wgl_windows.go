//go:build windows && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	wglDLL      = syscall.NewLazyDLL("opengl32.dll")
	wglCreate   = wglDLL.NewProc("wglCreateContext")
	wglCurrent  = wglDLL.NewProc("wglMakeCurrent")
	wglDelete   = wglDLL.NewProc("wglDeleteContext")
	wglAddress  = wglDLL.NewProc("wglGetProcAddress")
	wglChoose   = winGDI.NewProc("ChoosePixelFormat")
	wglSetPixel = winGDI.NewProc("SetPixelFormat")
	wglSwap     = winGDI.NewProc("SwapBuffers")
	wglWindows  = make(map[uintptr]*windowsWindow)
	wglCallback = syscall.NewCallback(wglProcedure)
)

type pixelFormat struct {
	Size, Version                                                                                                   uint16
	Flags                                                                                                           uint32
	PixelType, ColorBits, RedBits, RedShift, GreenBits, GreenShift, BlueBits, BlueShift, AlphaBits, AlphaShift      uint8
	AccumBits, AccumRed, AccumGreen, AccumBlue, AccumAlpha, DepthBits, StencilBits, AuxBuffers, LayerType, Reserved uint8
	LayerMask, VisibleMask, DamageMask                                                                              uint32
}

var wglClass struct {
	sync.Once
	err error
}

type wglContext struct {
	window             *windowsWindow
	child, dc, context uintptr
	width, height      int32
}

func (w *windowsWindow) SetGPUEnabled(enabled bool) bool {
	changed := w.graphics.set(enabled, w.newGPUContext)
	return changed
}
func (w *windowsWindow) GPUActive() bool                       { return w.graphics.renderer != nil }
func (w *windowsWindow) SetFragmentShader(source string) error { return w.graphics.shader(source) }

func (w *windowsWindow) newGPUContext() (glContext, error) {
	if err := wglCreate.Find(); err != nil {
		return nil, err
	}
	wglClass.Do(func() {
		name := winUTF16("FegoOpenGLSurface")
		wc := winClass{Style: 0x20, Procedure: wglCallback, Instance: w.backend.instance, Name: &name[0]}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if result, _, err := winRegister.Call(uintptr(unsafe.Pointer(&wc))); result == 0 && err != syscall.Errno(1410) {
			wglClass.err = fmt.Errorf("register GPU window: %w", err)
		}
	})
	if wglClass.err != nil {
		return nil, wglClass.err
	}
	name := winUTF16("FegoOpenGLSurface")
	// A separate drawable lets GDI keep working after any GPU failure.
	child, _, err := winCreate.Call(0, uintptr(unsafe.Pointer(&name[0])), 0, 0x50000000, 0, 0, 1, 1, w.hwnd, 0, w.backend.instance, 0)
	if child == 0 {
		return nil, fmt.Errorf("create GPU drawable: %w", err)
	}
	dc, _, _ := winGetDC.Call(child)
	c := &wglContext{window: w, child: child, dc: dc}
	format := pixelFormat{Size: uint16(unsafe.Sizeof(pixelFormat{})), Version: 1, Flags: 4 | 0x20 | 1, ColorBits: 32, AlphaBits: 8}
	index, _, _ := wglChoose.Call(dc, uintptr(unsafe.Pointer(&format)))
	if index == 0 {
		c.Close()
		return nil, errors.New("no OpenGL pixel format")
	}
	if ok, _, _ := wglSetPixel.Call(dc, index, uintptr(unsafe.Pointer(&format))); ok == 0 {
		c.Close()
		return nil, errors.New("set OpenGL pixel format")
	}
	c.context, _, _ = wglCreate.Call(dc)
	if c.context == 0 || !c.MakeCurrent() {
		c.Close()
		return nil, errors.New("create WGL context")
	}
	wglWindows[child] = w
	if address := c.Proc("wglSwapIntervalEXT"); address != 0 {
		var interval func(int32) int32
		purego.RegisterFunc(&interval, address)
		interval(0)
	}
	return c, nil
}

func (c *wglContext) MakeCurrent() bool {
	if c.context == 0 || c.window.closed {
		return false
	}
	var rect winRect
	if ok, _, _ := winGetClient.Call(c.window.hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return false
	}
	w, h := rect.Right-rect.Left, rect.Bottom-rect.Top
	if w != c.width || h != c.height {
		winSetPos.Call(c.child, 0, 0, 0, uintptr(w), uintptr(h), 0x14)
		c.width, c.height = w, h
	}
	ok, _, _ := wglCurrent.Call(c.dc, c.context)
	return ok != 0
}
func (c *wglContext) Swap() bool { ok, _, _ := wglSwap.Call(c.dc); return ok != 0 }
func (c *wglContext) Proc(name string) uintptr {
	text, _ := syscall.BytePtrFromString(name)
	address, _, _ := wglAddress.Call(uintptr(unsafe.Pointer(text)))
	runtime.KeepAlive(text)
	if address > 3 && address != ^uintptr(0) {
		return address
	}
	proc := wglDLL.NewProc(name)
	if proc.Find() != nil {
		return 0
	}
	return proc.Addr()
}
func (c *wglContext) Close() {
	delete(wglWindows, c.child)
	if c.context != 0 {
		wglCurrent.Call(0, 0)
		wglDelete.Call(c.context)
		c.context = 0
	}
	if c.dc != 0 {
		winReleaseDC.Call(c.child, c.dc)
		c.dc = 0
	}
	if c.child != 0 {
		winDestroy.Call(c.child)
		c.child = 0
	}
}

func wglProcedure(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
	switch message {
	case 0x84:
		return ^uintptr(0) // Keep input on the parent view.
	case 0x14:
		return 1
	case 0x0f:
		var paint winPaint
		winBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
		if w := wglWindows[hwnd]; w != nil && !w.closed {
			w.events(Event{Kind: Paint})
		}
		winEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
		return 0
	}
	result, _, _ := winDefault.Call(hwnd, uintptr(message), wp, lp)
	return result
}
