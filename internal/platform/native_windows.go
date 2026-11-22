//go:build windows && (amd64 || arm64)

package platform

import (
	"fmt"
	"image"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	winUser           = syscall.NewLazyDLL("user32.dll")
	winGDI            = syscall.NewLazyDLL("gdi32.dll")
	winKernel         = syscall.NewLazyDLL("kernel32.dll")
	winIMM            = syscall.NewLazyDLL("imm32.dll")
	winRegister       = winUser.NewProc("RegisterClassExW")
	winCreate         = winUser.NewProc("CreateWindowExW")
	winDefault        = winUser.NewProc("DefWindowProcW")
	winPost           = winUser.NewProc("PostMessageW")
	winDestroy        = winUser.NewProc("DestroyWindow")
	winGetMessage     = winUser.NewProc("GetMessageW")
	winTranslate      = winUser.NewProc("TranslateMessage")
	winDispatch       = winUser.NewProc("DispatchMessageW")
	winPostQuit       = winUser.NewProc("PostQuitMessage")
	winGetClient      = winUser.NewProc("GetClientRect")
	winGetDC          = winUser.NewProc("GetDC")
	winReleaseDC      = winUser.NewProc("ReleaseDC")
	winBeginPaint     = winUser.NewProc("BeginPaint")
	winEndPaint       = winUser.NewProc("EndPaint")
	winGetKeyState    = winUser.NewProc("GetKeyState")
	winDPI            = winUser.NewProc("GetDpiForWindow")
	winSetPos         = winUser.NewProc("SetWindowPos")
	winSetTitle       = winUser.NewProc("SetWindowTextW")
	winLoadCursor     = winUser.NewProc("LoadCursorW")
	winSetCursor      = winUser.NewProc("SetCursor")
	winOpenClipboard  = winUser.NewProc("OpenClipboard")
	winCloseClipboard = winUser.NewProc("CloseClipboard")
	winGetClipboard   = winUser.NewProc("GetClipboardData")
	winSetClipboard   = winUser.NewProc("SetClipboardData")
	winEmptyClipboard = winUser.NewProc("EmptyClipboard")
	winGlobalAlloc    = winKernel.NewProc("GlobalAlloc")
	winGlobalFree     = winKernel.NewProc("GlobalFree")
	winGlobalLock     = winKernel.NewProc("GlobalLock")
	winGlobalUnlock   = winKernel.NewProc("GlobalUnlock")
	winGlobalSize     = winKernel.NewProc("GlobalSize")
	winBlit           = winGDI.NewProc("StretchDIBits")
	winSaveDC         = winGDI.NewProc("SaveDC")
	winRestoreDC      = winGDI.NewProc("RestoreDC")
	winClip           = winGDI.NewProc("IntersectClipRect")
	winIMMGet         = winIMM.NewProc("ImmGetContext")
	winIMMRelease     = winIMM.NewProc("ImmReleaseContext")
	winIMMString      = winIMM.NewProc("ImmGetCompositionStringW")
	winIMMPosition    = winIMM.NewProc("ImmSetCompositionWindow")
	winIMMCandidate   = winIMM.NewProc("ImmSetCandidateWindow")
	winIMMAssociate   = winIMM.NewProc("ImmAssociateContextEx")
	winCallback       = syscall.NewCallback(windowProcedure)
	winActive         *windowsBackend
)

const winWakeMessage = 0x8001

type winPoint struct{ X, Y int32 }
type winRect struct{ Left, Top, Right, Bottom int32 }
type winMessage struct {
	HWND           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          winPoint
	Private        uint32
}
type winClass struct {
	Size, Style                        uint32
	Procedure                          uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type winPaint struct {
	DC                 uintptr
	Erase              int32
	Rect               winRect
	Restore, IncUpdate int32
	Reserved           [32]byte
}
type winBitmap struct {
	Size                   uint32
	Width, Height          int32
	Planes, Bits           uint16
	Compression, ImageSize uint32
	XPels, YPels           int32
	Used, Important        uint32
	Masks                  [3]uint32
}

type windowsBackend struct {
	actionsMu sync.Mutex
	actions   []func()

	dispatch       func()
	wake, instance uintptr
	windows        map[uintptr]*windowsWindow
	creating       *windowsWindow
	trays          map[uintptr]*windowsTray
	taskbarCreated uint32
	closed         atomic.Bool
}
type windowsWindow struct {
	graphics gpuState
	access   *winAccessTree
	title    string

	backend                  *windowsBackend
	hwnd, paintDC, cursor    uintptr
	icons                    [2]uintptr
	events                   func(Event)
	frame                    *image.RGBA
	highSurrogate            uint16
	caret                    image.Rectangle
	input, composing, closed bool
}

func winUTF16(s string) []uint16 { return append(utf16.Encode([]rune(s)), 0) }

func New(dispatch func()) (Backend, error) {
	if winActive != nil {
		return nil, fmt.Errorf("fego: only one native event loop may run")
	}
	co, _, _ := winCoInit.Call(0, 2)
	if int32(co) < 0 {
		return nil, fmt.Errorf("initialize UI COM apartment: HRESULT %#x", uint32(co))
	}
	awareness := winUser.NewProc("SetProcessDpiAwarenessContext")
	if awareness.Find() == nil {
		awareness.Call(^uintptr(3))
	}
	instance, _, _ := winKernel.NewProc("GetModuleHandleW").Call(0)
	className := winUTF16("FegoNativeWindow")
	cursor, _, _ := winLoadCursor.Call(0, 32512)
	wc := winClass{Style: 3, Procedure: winCallback, Instance: instance, Cursor: cursor, Name: &className[0]}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := winRegister.Call(uintptr(unsafe.Pointer(&wc))); r == 0 && err != syscall.Errno(1410) {
		winCoUninit.Call()
		return nil, fmt.Errorf("register window class: %w", err)
	}
	b := &windowsBackend{dispatch: dispatch, instance: instance, windows: make(map[uintptr]*windowsWindow)}
	winActive = b
	h, _, err := winCreate.Call(0, uintptr(unsafe.Pointer(&className[0])), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, instance, 0)
	if h == 0 {
		winActive = nil
		winCoUninit.Call()
		return nil, fmt.Errorf("create event window: %w", err)
	}
	b.wake = h
	return b, nil
}

func (b *windowsBackend) Open(o Options, events func(Event)) (Window, error) {
	w := &windowsWindow{backend: b, events: events, title: o.Title}
	class, title := winUTF16("FegoNativeWindow"), winUTF16(o.Title)
	scale := uintptr(96)
	dpi := winUser.NewProc("GetDpiForSystem")
	if dpi.Find() == nil {
		scale, _, _ = dpi.Call()
	}
	rect := winRect{Right: int32(o.Width) * int32(scale) / 96, Bottom: int32(o.Height) * int32(scale) / 96}
	const style = 0x00cf0000
	winUser.NewProc("AdjustWindowRectEx").Call(uintptr(unsafe.Pointer(&rect)), style, 0, 0)
	b.creating = w
	hwnd, _, err := winCreate.Call(0, uintptr(unsafe.Pointer(&class[0])), uintptr(unsafe.Pointer(&title[0])), style, 0x80000000, 0x80000000, uintptr(rect.Right-rect.Left), uintptr(rect.Bottom-rect.Top), 0, 0, b.instance, 0)
	b.creating = nil
	if hwnd == 0 {
		return nil, fmt.Errorf("create window: %w", err)
	}
	w.hwnd = hwnd
	b.windows[hwnd] = w
	if o.Icon != nil {
		if err := w.setIcon(o.Icon); err != nil {
			w.Close()
			return nil, err
		}
	}
	winShow.Call(hwnd, 5)
	w.resized()
	return w, nil
}

func (b *windowsBackend) Run() error {
	defer func() {
		b.closed.Store(true)
		for _, tray := range b.trays {
			tray.Close()
		}
		for _, w := range b.windows {
			w.Close()
		}
		winDestroy.Call(b.wake)
		b.actionsMu.Lock()
		b.actions = nil
		b.actionsMu.Unlock()
		winCoUninit.Call()
		winActive = nil
	}()
	var msg winMessage
	for {
		r, _, err := winGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return fmt.Errorf("native event loop: %w", err)
		}
		if r == 0 {
			break
		}
		winTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		winDispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}
func (b *windowsBackend) Wake() {
	if !b.closed.Load() {
		winPost.Call(b.wake, winWakeMessage, 0, 0)
	}
}
func (b *windowsBackend) Quit() {
	if !b.closed.Swap(true) {
		winPostQuit.Call(0)
	}
}

func (w *windowsWindow) scale() float32 {
	if winDPI.Find() == nil {
		if n, _, _ := winDPI.Call(w.hwnd); n != 0 {
			return float32(n) / 96
		}
	}
	return 1
}
func (w *windowsWindow) resized() {
	var r winRect
	winGetClient.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	w.events(Event{Kind: Resize, Width: int(r.Right), Height: int(r.Bottom), Scale: w.scale()})
}
func (w *windowsWindow) Present(img *image.RGBA, damage []image.Rectangle) {
	if w.closed {
		return
	}
	w.frame = img
	if w.GPUActive() {
		if w.graphics.present(img, damage) {
			return
		}
		damage = nil
	}
	dc := w.paintDC
	if dc == 0 {
		dc, _, _ = winGetDC.Call(w.hwnd)
		defer winReleaseDC.Call(w.hwnd, dc)
	}
	if len(damage) == 0 {
		damage = []image.Rectangle{img.Bounds()}
	}
	for _, d := range damage {
		w.blit(dc, d)
	}
}
func (w *windowsWindow) blit(dc uintptr, damage image.Rectangle) {
	img := w.frame
	if img == nil || img.Bounds().Empty() {
		return
	}
	damage = damage.Intersect(img.Bounds())
	if damage.Empty() {
		return
	}
	info := winBitmap{Size: 40, Width: int32(img.Stride / 4), Height: -int32(img.Rect.Dy()), Planes: 1, Bits: 32, Compression: 3, Masks: [3]uint32{0xff, 0xff00, 0xff0000}}
	saved, _, _ := winSaveDC.Call(dc)
	winClip.Call(dc, uintptr(damage.Min.X), uintptr(damage.Min.Y), uintptr(damage.Max.X), uintptr(damage.Max.Y))
	winBlit.Call(dc, 0, 0, uintptr(img.Rect.Dx()), uintptr(img.Rect.Dy()), 0, 0, uintptr(img.Rect.Dx()), uintptr(img.Rect.Dy()), uintptr(unsafe.Pointer(unsafe.SliceData(img.Pix))), uintptr(unsafe.Pointer(&info)), 0, 0x00cc0020)
	winRestoreDC.Call(dc, saved)
	runtime.KeepAlive(img)
}
func (w *windowsWindow) SetTitle(s string) {
	w.title = s
	t := winUTF16(s)
	winSetTitle.Call(w.hwnd, uintptr(unsafe.Pointer(&t[0])))
}
func (w *windowsWindow) SetSize(width, height int) {
	r := winRect{Right: int32(float32(width) * w.scale()), Bottom: int32(float32(height) * w.scale())}
	winUser.NewProc("AdjustWindowRectEx").Call(uintptr(unsafe.Pointer(&r)), 0x00cf0000, 0, 0)
	winSetPos.Call(w.hwnd, 0, 0, 0, uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 6)
}
func (w *windowsWindow) SetCursor(name string) {
	id := uintptr(32512)
	switch name {
	case "text":
		id = 32513
	case "pointer":
		id = 32649
	case "crosshair":
		id = 32515
	case "resize-ew":
		id = 32644
	case "resize-ns":
		id = 32645
	}
	w.cursor, _, _ = winLoadCursor.Call(0, id)
	winSetCursor.Call(w.cursor)
}
func (w *windowsWindow) SetClipboard(s string) {
	if ok, _, _ := winOpenClipboard.Call(w.hwnd); ok == 0 {
		return
	}
	defer winCloseClipboard.Call()
	data := winUTF16(s)
	h, _, _ := winGlobalAlloc.Call(2, uintptr(len(data)*2))
	if h == 0 {
		return
	}
	p, _, _ := winGlobalLock.Call(h)
	if p == 0 {
		winGlobalFree.Call(h)
		return
	}
	copy(unsafe.Slice((*uint16)(nativeAddress(p)), len(data)), data)
	winGlobalUnlock.Call(h)
	if ok, _, _ := winEmptyClipboard.Call(); ok == 0 {
		winGlobalFree.Call(h)
		return
	}
	if ok, _, _ := winSetClipboard.Call(13, h); ok == 0 {
		winGlobalFree.Call(h)
	}
}
func (w *windowsWindow) Clipboard() string {
	if ok, _, _ := winOpenClipboard.Call(w.hwnd); ok == 0 {
		return ""
	}
	defer winCloseClipboard.Call()
	h, _, _ := winGetClipboard.Call(13)
	if h == 0 {
		return ""
	}
	n, _, _ := winGlobalSize.Call(h)
	if n < 2 || n > nativeTextMaxBytes || n%2 != 0 {
		return ""
	}
	p, _, _ := winGlobalLock.Call(h)
	if p == 0 {
		return ""
	}
	defer winGlobalUnlock.Call(h)
	text := unsafe.Slice((*uint16)(nativeAddress(p)), n/2)
	if i := indexZero(text); i >= 0 {
		text = text[:i]
	}
	value, _ := nativeUTF16Text(text)
	return value
}
func indexZero(s []uint16) int {
	for i, c := range s {
		if c == 0 {
			return i
		}
	}
	return -1
}
func (w *windowsWindow) TextInput(enabled bool, caret image.Rectangle) {
	changed := enabled != w.input
	w.input, w.caret = enabled, caret
	flags := uintptr(0)
	if enabled {
		flags = 0x10
	}
	if changed {
		winIMMAssociate.Call(w.hwnd, 0, flags)
	}
	if !enabled {
		w.composing, w.highSurrogate = false, 0
		return
	}
	h, _, _ := winIMMGet.Call(w.hwnd)
	if h == 0 {
		return
	}
	defer winIMMRelease.Call(w.hwnd, h)
	composition := struct {
		Style    uint32
		Position winPoint
		Area     winRect
	}{Style: 2, Position: winPoint{int32(caret.Min.X), int32(caret.Max.Y)}}
	candidate := struct {
		Index, Style uint32
		Position     winPoint
		Area         winRect
	}{Style: 0x80, Position: composition.Position, Area: winRect{int32(caret.Min.X), int32(caret.Min.Y), int32(caret.Max.X), int32(caret.Max.Y)}}
	winIMMPosition.Call(h, uintptr(unsafe.Pointer(&composition)))
	winIMMCandidate.Call(h, uintptr(unsafe.Pointer(&candidate)))
}
func (w *windowsWindow) Close() {
	if !w.closed {
		w.graphics.close()
		w.closed = true
		if w.access != nil {
			w.access.close()
			w.access = nil
		}
		winDestroy.Call(w.hwnd)
		w.releaseIcons()
		delete(w.backend.windows, w.hwnd)
		w.frame = nil
	}
}

func winModifiers(e Event) Event {
	down := func(k uintptr) bool { r, _, _ := winGetKeyState.Call(k); return uint16(r)&0x8000 != 0 }
	e.Shift, e.Control, e.Alt, e.Super = down(0x10), down(0x11), down(0x12), down(0x5b) || down(0x5c)
	return e
}
func winKey(v uintptr) string {
	switch v {
	case 8:
		return "Backspace"
	case 9:
		return "Tab"
	case 13:
		return "Enter"
	case 27:
		return "Escape"
	case 32:
		return "Space"
	case 33:
		return "PageUp"
	case 34:
		return "PageDown"
	case 35:
		return "End"
	case 36:
		return "Home"
	case 37:
		return "ArrowLeft"
	case 38:
		return "ArrowUp"
	case 39:
		return "ArrowRight"
	case 40:
		return "ArrowDown"
	case 46:
		return "Delete"
	}
	if v >= 0x41 && v <= 0x5a {
		return strings.ToLower(string(rune(v)))
	}
	if v >= 0x30 && v <= 0x39 {
		return string(rune(v))
	}
	return ""
}
func windowProcedure(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
	b := winActive
	if b == nil {
		r, _, _ := winDefault.Call(hwnd, uintptr(message), wp, lp)
		return r
	}
	if message == winWakeMessage {
		if !b.closed.Load() {
			b.deliverActions()
			b.dispatch()
		}
		return 0
	}
	if t := b.trays[hwnd]; t != nil && t.handle(message, wp, lp) {
		return 0
	}
	w := b.windows[hwnd]
	if w == nil && message == 0x81 && b.creating != nil {
		w = b.creating
		w.hwnd = hwnd
		b.windows[hwnd] = w
	}
	if w == nil {
		r, _, _ := winDefault.Call(hwnd, uintptr(message), wp, lp)
		return r
	}
	emit := func(e Event) { w.events(winModifiers(e)) }
	switch message {
	case 0x3d:
		if int32(lp) == -4 && w.access != nil {
			if object := w.access.object(0); object != nil {
				result, _, _ := winAccResult.Call(uintptr(unsafe.Pointer(&iidAccessible[0])), wp, object.ptr)
				accRelease(object.ptr)
				return result
			}
		}
	case 0x0f:
		var p winPaint
		dc, _, _ := winBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&p)))
		w.paintDC = dc
		emit(Event{Kind: Paint})
		if !w.closed && !w.GPUActive() {
			w.blit(dc, image.Rect(int(p.Rect.Left), int(p.Rect.Top), int(p.Rect.Right), int(p.Rect.Bottom)))
		}
		w.paintDC = 0
		winEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&p)))
		return 0
	case 0x14:
		return 1
	case 0x05:
		w.resized()
		return 0
	case 0x02:
		delete(b.windows, hwnd)
		if !w.closed {
			w.graphics.close()
			w.closed = true
			w.releaseIcons()
			emit(Event{Kind: Close, Destroyed: true})
		}
		return 0
	case 0x10:
		emit(Event{Kind: Close})
		return 0
	case 0x07:
		emit(Event{Kind: Focus, Focused: true})
		return 0
	case 0x08:
		emit(Event{Kind: Focus})
		return 0
	case 0x20:
		if uint16(lp) == 1 && w.cursor != 0 {
			winSetCursor.Call(w.cursor)
			return 1
		}
	case 0x02e0:
		r := (*winRect)(nativeAddress(lp))
		winSetPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x14)
		return 0
	case 0x200, 0x201, 0x202, 0x204, 0x205, 0x207, 0x208:
		e := Event{Kind: PointerMove, X: float32(int16(lp)), Y: float32(int16(lp >> 16))}
		switch message {
		case 0x201:
			e.Kind, e.Button = PointerDown, 1
		case 0x202:
			e.Kind, e.Button = PointerUp, 1
		case 0x204:
			e.Kind, e.Button = PointerDown, 3
		case 0x205:
			e.Kind, e.Button = PointerUp, 3
		case 0x207:
			e.Kind, e.Button = PointerDown, 2
		case 0x208:
			e.Kind, e.Button = PointerUp, 2
		}
		if e.Kind == PointerDown {
			winUser.NewProc("SetCapture").Call(hwnd)
			winUser.NewProc("SetFocus").Call(hwnd)
		}
		if e.Kind == PointerUp {
			winUser.NewProc("ReleaseCapture").Call()
		}
		emit(e)
		return 0
	case 0x20a, 0x20e:
		point := winPoint{int32(int16(lp)), int32(int16(lp >> 16))}
		winUser.NewProc("ScreenToClient").Call(hwnd, uintptr(unsafe.Pointer(&point)))
		e := Event{Kind: Scroll, X: float32(point.X), Y: float32(point.Y)}
		delta := float32(int16(wp>>16)) / 120 * 40 * w.scale()
		if message == 0x20a {
			e.DY = -delta
		} else {
			e.DX = delta
		}
		emit(e)
		return 0
	case 0x100, 0x104, 0x101, 0x105:
		if w.composing {
			break // The IME owns navigation and confirmation while composing.
		}
		kind := KeyDown
		if message == 0x101 || message == 0x105 {
			kind = KeyUp
		}
		emit(Event{Kind: kind, Key: winKey(wp)})
		if message == 0x100 || message == 0x101 {
			return 0
		}
	case 0x102:
		c := uint16(wp)
		if c >= 0xd800 && c <= 0xdbff {
			w.highSurrogate = c
			return 0
		}
		var text string
		if c >= 0xdc00 && c <= 0xdfff && w.highSurrogate != 0 {
			text = string(utf16.DecodeRune(rune(w.highSurrogate), rune(c)))
		} else if c >= 32 {
			text = string(rune(c))
		}
		w.highSurrogate = 0
		if text != "" && w.input {
			emit(Event{Kind: Text, Text: text})
		}
		return 0
	case 0x10d:
		w.composing = true
	case 0x10e:
		w.composing = false
	case 0x10f:
		if lp&0x800 != 0 && w.input {
			h, _, _ := winIMMGet.Call(hwnd)
			if h != 0 {
				n, _, _ := winIMMString.Call(h, 0x800, 0, 0)
				if int32(n) > 0 && n <= nativeTextMaxBytes && n%2 == 0 {
					buf := make([]uint16, n/2)
					copied, _, _ := winIMMString.Call(h, 0x800, uintptr(unsafe.Pointer(&buf[0])), n)
					if int32(copied) > 0 && copied <= n && copied%2 == 0 {
						if text, ok := nativeUTF16Text(buf[:copied/2]); ok && text != "" {
							emit(Event{Kind: Text, Text: text})
						}
					}
				}
				winIMMRelease.Call(hwnd, h)
			}
			return 0
		}
	}
	r, _, _ := winDefault.Call(hwnd, uintptr(message), wp, lp)
	return r
}

func (b *windowsBackend) postAction(fn func()) {
	b.actionsMu.Lock()
	if !b.closed.Load() {
		b.actions = append(b.actions, fn)
	}
	b.actionsMu.Unlock()
	b.Wake()
}
func (b *windowsBackend) deliverActions() {
	b.actionsMu.Lock()
	actions := b.actions
	b.actions = nil
	b.actionsMu.Unlock()
	for _, fn := range actions {
		fn()
	}
}
