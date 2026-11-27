//go:build darwin && !ios && (amd64 || arm64)

package platform

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"strings"
	"structs"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type NSPoint struct {
	_    structs.HostLayout
	X, Y float64
}
type NSSize struct {
	_             structs.HostLayout
	Width, Height float64
}
type NSRect struct {
	_      structs.HostLayout
	Origin NSPoint
	Size   NSSize
}
type NSRange struct {
	_                structs.HostLayout
	Location, Length uint64
}
type cfSourceContext struct {
	_                                                                          structs.HostLayout
	Version                                                                    int64
	Info, Retain, Release, Description, Equal, Hash, Schedule, Cancel, Perform uintptr
}

var mac struct {
	once                                  sync.Once
	err                                   error
	msg                                   uintptr
	poolPush                              func() uintptr
	poolPop                               func(uintptr)
	rect                                  func(objc.ID, objc.SEL) NSRect
	point                                 func(objc.ID, objc.SEL) NSPoint
	scalar                                func(objc.ID, objc.SEL) float64
	initRect                              func(objc.ID, objc.SEL, NSRect) objc.ID
	initWindow                            func(objc.ID, objc.SEL, NSRect, uint64, uint64, bool) objc.ID
	setSize                               func(objc.ID, objc.SEL, NSSize)
	setRect                               func(objc.ID, objc.SEL, NSRect)
	convertPoint                          func(objc.ID, objc.SEL, NSPoint, objc.ID) NSPoint
	convertRect                           func(objc.ID, objc.SEL, NSRect, objc.ID) NSRect
	screenRect                            func(objc.ID, objc.SEL, NSRect) NSRect
	initTracking                          func(objc.ID, objc.SEL, NSRect, uint64, objc.ID, objc.ID) objc.ID
	event                                 func(objc.ID, objc.SEL, uint64, NSPoint, uint64, float64, int64, objc.ID, int16, int64, int64) objc.ID
	cfRelease                             func(uintptr)
	cfData                                func(uintptr, unsafe.Pointer, int64) uintptr
	loopMain                              func() uintptr
	sourceNew                             func(uintptr, int64, *cfSourceContext) uintptr
	sourceAdd                             func(uintptr, uintptr, uintptr)
	sourceRemove                          func(uintptr, uintptr, uintptr)
	sourceSignal                          func(uintptr)
	loopWake                              func(uintptr)
	providerNew                           func(uintptr) uintptr
	providerRelease                       func(uintptr)
	colorNew                              func() uintptr
	imageNew                              func(uint64, uint64, uint64, uint64, uint64, uintptr, uint32, uintptr, uintptr, bool, uint32) uintptr
	imageRelease                          func(uintptr)
	accessNotify                          func(objc.ID, objc.ID)
	number                                func(objc.ID, objc.SEL, float64) objc.ID
	save                                  func(uintptr)
	restore                               func(uintptr)
	translate                             func(uintptr, float64, float64)
	scale                                 func(uintptr, float64, float64)
	draw                                  func(uintptr, NSRect, uintptr)
	colorSpace, commonMode, perform       uintptr
	viewClass, delegateClass, accessClass objc.Class
	selectors                             sync.Map
}
var macActive *cocoaBackend

//go:uintptrescapes
func macSend(obj objc.ID, name string, args ...uintptr) objc.ID {
	var values [12]uintptr
	values[0], values[1] = uintptr(obj), uintptr(macSel(name))
	n := copy(values[2:], args)
	r, _, _ := purego.SyscallN(mac.msg, values[:n+2]...)
	return objc.ID(r)
}
func macSel(name string) objc.SEL {
	if value, ok := mac.selectors.Load(name); ok {
		return value.(objc.SEL)
	}
	sel := objc.RegisterName(name)
	mac.selectors.Store(name, sel)
	return sel
}
func macClass(name string) objc.ID { return objc.ID(objc.GetClass(name)) }
func macString(s string) objc.ID {
	s = strings.ToValidUTF8(s, "�")
	b := []byte(s)
	obj := macSend(macClass("NSString"), "alloc")
	obj = macSend(obj, "initWithBytes:length:encoding:", uintptr(unsafe.Pointer(unsafe.SliceData(b))), uintptr(len(b)), 4)
	return macSend(obj, "autorelease")
}
func macText(s objc.ID) string {
	text, _ := macBoundedText(s)
	return text
}
func macBoundedText(s objc.ID) (string, bool) {
	if s == 0 {
		return "", true
	}
	if macSend(s, "respondsToSelector:", uintptr(macSel("string"))) != 0 {
		s = macSend(s, "string")
	}
	n := macSend(s, "lengthOfBytesUsingEncoding:", 4)
	if n > nativeTextMaxBytes {
		return "", false
	}
	if n == 0 {
		return "", true
	}
	p := macSend(s, "UTF8String")
	if p == 0 {
		return "", false
	}
	return nativeUTF8Text(unsafe.Slice((*byte)(nativeAddress(uintptr(p))), int(n)))
}

func initMac() error {
	mac.once.Do(func() {
		load := func(path string) uintptr {
			if mac.err != nil {
				return 0
			}
			h, e := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
			mac.err = e
			return h
		}
		obj := load("/usr/lib/libobjc.A.dylib")
		load("/System/Library/Frameworks/Foundation.framework/Foundation")
		appKit := load("/System/Library/Frameworks/AppKit.framework/AppKit")
		cf := load("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		cg := load("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		if mac.err != nil {
			return
		}
		mac.msg, mac.err = purego.Dlsym(obj, "objc_msgSend")
		if mac.err != nil {
			return
		}
		rectResult := mac.msg
		if runtime.GOARCH == "amd64" {
			rectResult, mac.err = purego.Dlsym(obj, "objc_msgSend_stret")
			if mac.err != nil {
				return
			}
		}
		purego.RegisterFunc(&mac.rect, rectResult)
		purego.RegisterFunc(&mac.convertRect, rectResult)
		purego.RegisterFunc(&mac.screenRect, rectResult)
		purego.RegisterFunc(&mac.point, mac.msg)
		purego.RegisterFunc(&mac.scalar, mac.msg)
		purego.RegisterFunc(&mac.initRect, mac.msg)
		purego.RegisterFunc(&mac.initWindow, mac.msg)
		purego.RegisterFunc(&mac.setSize, mac.msg)
		purego.RegisterFunc(&mac.setRect, mac.msg)
		purego.RegisterFunc(&mac.convertPoint, mac.msg)
		purego.RegisterFunc(&mac.initTracking, mac.msg)
		purego.RegisterFunc(&mac.event, mac.msg)
		purego.RegisterFunc(&mac.number, mac.msg)
		purego.RegisterLibFunc(&mac.accessNotify, appKit, "NSAccessibilityPostNotification")
		purego.RegisterLibFunc(&mac.poolPush, obj, "objc_autoreleasePoolPush")
		purego.RegisterLibFunc(&mac.poolPop, obj, "objc_autoreleasePoolPop")
		purego.RegisterLibFunc(&mac.cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&mac.cfData, cf, "CFDataCreate")
		purego.RegisterLibFunc(&mac.loopMain, cf, "CFRunLoopGetMain")
		purego.RegisterLibFunc(&mac.sourceNew, cf, "CFRunLoopSourceCreate")
		purego.RegisterLibFunc(&mac.sourceAdd, cf, "CFRunLoopAddSource")
		purego.RegisterLibFunc(&mac.sourceRemove, cf, "CFRunLoopRemoveSource")
		purego.RegisterLibFunc(&mac.sourceSignal, cf, "CFRunLoopSourceSignal")
		purego.RegisterLibFunc(&mac.loopWake, cf, "CFRunLoopWakeUp")
		purego.RegisterLibFunc(&mac.providerNew, cg, "CGDataProviderCreateWithCFData")
		purego.RegisterLibFunc(&mac.providerRelease, cg, "CGDataProviderRelease")
		purego.RegisterLibFunc(&mac.colorNew, cg, "CGColorSpaceCreateDeviceRGB")
		purego.RegisterLibFunc(&mac.imageNew, cg, "CGImageCreate")
		purego.RegisterLibFunc(&mac.imageRelease, cg, "CGImageRelease")
		purego.RegisterLibFunc(&mac.save, cg, "CGContextSaveGState")
		purego.RegisterLibFunc(&mac.restore, cg, "CGContextRestoreGState")
		purego.RegisterLibFunc(&mac.translate, cg, "CGContextTranslateCTM")
		purego.RegisterLibFunc(&mac.scale, cg, "CGContextScaleCTM")
		purego.RegisterLibFunc(&mac.draw, cg, "CGContextDrawImage")
		mode, e := purego.Dlsym(cf, "kCFRunLoopCommonModes")
		if e != nil {
			mac.err = e
			return
		}
		mac.commonMode = *(*uintptr)(nativeAddress(mode))
		mac.colorSpace = mac.colorNew()
		mac.perform = purego.NewCallback(func(_ uintptr) {
			if b := macActive; b != nil {
				pool := mac.poolPush()
				b.deliverActions()
				b.dispatch()
				mac.poolPop(pool)
			}
		})
		mac.viewClass, mac.err = objc.RegisterClass("FegoCanvasView", objc.GetClass("NSView"), []*objc.Protocol{objc.GetProtocol("NSTextInputClient")}, nil, macViewMethods())
		if mac.err != nil {
			return
		}
		mac.delegateClass, mac.err = objc.RegisterClass("FegoWindowDelegate", objc.GetClass("NSObject"), nil, nil, macDelegateMethods())
		if mac.err == nil {
			mac.accessClass, mac.err = objc.RegisterClass("FegoAccessibilityElement", objc.GetClass("NSAccessibilityElement"), nil, nil, macAccessMethods())
		}
	})
	return mac.err
}

type cocoaBackend struct {
	dispatch           func()
	app, appDelegate   objc.ID
	windows            map[objc.ID]*cocoaWindow
	views              map[objc.ID]*cocoaWindow
	delegates          map[objc.ID]*cocoaWindow
	trays              map[objc.ID]*cocoaTray
	loop, source, pool uintptr
	mu                 sync.Mutex
	actions            []func()
	closed             bool
	running, quitting  bool
}
type cocoaWindow struct {
	graphics                gpuState
	backend                 *cocoaBackend
	window, view, delegate  objc.ID
	image                   uintptr
	events                  func(Event)
	caret                   image.Rectangle
	marked                  string
	selected                NSRange
	input, painting, closed bool
	access                  map[uint64]objc.ID
	accessOrder             []uint64
}

func New(dispatch func()) (Backend, error) {
	if err := initMac(); err != nil {
		return nil, fmt.Errorf("load AppKit: %w", err)
	}
	if macActive != nil {
		return nil, fmt.Errorf("fego: only one native event loop may run")
	}
	b := &cocoaBackend{dispatch: dispatch, windows: map[objc.ID]*cocoaWindow{}, views: map[objc.ID]*cocoaWindow{}, delegates: map[objc.ID]*cocoaWindow{}}
	b.pool = mac.poolPush()
	b.app = macSend(macClass("NSApplication"), "sharedApplication")
	macSend(b.app, "setActivationPolicy:", 0)
	b.loop = mac.loopMain()
	ctx := cfSourceContext{Perform: mac.perform}
	b.source = mac.sourceNew(0, 0, &ctx)
	if b.source == 0 {
		mac.poolPop(b.pool)
		return nil, fmt.Errorf("create native wake source")
	}
	mac.sourceAdd(b.loop, b.source, mac.commonMode)
	macActive = b
	b.appDelegate = macSend(macSend(objc.ID(mac.delegateClass), "alloc"), "init")
	mainMenu := macSend(macSend(macClass("NSMenu"), "alloc"), "init")
	item := macSend(macSend(macClass("NSMenuItem"), "alloc"), "initWithTitle:action:keyEquivalent:", uintptr(macString("fego")), 0, uintptr(macString("")))
	submenu := macSend(macSend(macClass("NSMenu"), "alloc"), "initWithTitle:", uintptr(macString("fego")))
	quit := macSend(macSend(macClass("NSMenuItem"), "alloc"), "initWithTitle:action:keyEquivalent:", uintptr(macString("Quit fego")), uintptr(macSel("quit:")), uintptr(macString("q")))
	macSend(quit, "setTarget:", uintptr(b.appDelegate))
	macSend(submenu, "addItem:", uintptr(quit))
	macSend(item, "setSubmenu:", uintptr(submenu))
	macSend(mainMenu, "addItem:", uintptr(item))
	macSend(b.app, "setMainMenu:", uintptr(mainMenu))
	for _, obj := range []objc.ID{quit, submenu, item, mainMenu} {
		macSend(obj, "release")
	}
	macSend(b.app, "finishLaunching")
	return b, nil
}
func (b *cocoaBackend) Open(o Options, events func(Event)) (Window, error) {
	rect := NSRect{Size: NSSize{Width: float64(o.Width), Height: float64(o.Height)}}
	win := mac.initWindow(macSend(macClass("NSWindow"), "alloc"), macSel("initWithContentRect:styleMask:backing:defer:"), rect, 15, 2, false)
	if win == 0 {
		return nil, fmt.Errorf("create Cocoa window")
	}
	view := mac.initRect(macSend(objc.ID(mac.viewClass), "alloc"), macSel("initWithFrame:"), rect)
	delegate := macSend(macSend(objc.ID(mac.delegateClass), "alloc"), "init")
	if view == 0 || delegate == 0 {
		macSend(delegate, "release")
		macSend(view, "release")
		macSend(win, "release")
		return nil, fmt.Errorf("create Cocoa content view")
	}
	w := &cocoaWindow{backend: b, window: win, view: view, delegate: delegate, events: events}
	b.windows[win], b.views[view], b.delegates[delegate] = w, w, w
	macSend(win, "setReleasedWhenClosed:", 0)
	macSend(win, "setTitle:", uintptr(macString(o.Title)))
	macSend(win, "setDelegate:", uintptr(delegate))
	macSend(win, "setContentView:", uintptr(view))
	if o.Minimum != (image.Point{}) {
		mac.setSize(win, macSel("setContentMinSize:"), NSSize{Width: float64(o.Minimum.X), Height: float64(o.Minimum.Y)})
	}
	macSend(win, "setAcceptsMouseMovedEvents:", 1)
	macSend(view, "setAutoresizingMask:", 18)
	tracking := mac.initTracking(macSend(macClass("NSTrackingArea"), "alloc"), macSel("initWithRect:options:owner:userInfo:"), NSRect{}, 2|0x20|0x200, view, 0)
	macSend(view, "addTrackingArea:", uintptr(tracking))
	macSend(tracking, "release")
	w.setIcons(o)
	macSend(win, "center")
	macSend(win, "makeFirstResponder:", uintptr(view))
	macSend(win, "makeKeyAndOrderFront:", 0)
	macSend(b.app, "activateIgnoringOtherApps:", 1)
	w.resized()
	return w, nil
}
func (b *cocoaBackend) Run() error {
	if !b.quitting {
		b.running = true
		macSend(b.app, "run")
		b.running = false
	}
	for _, tray := range b.trays {
		tray.Close()
	}
	b.mu.Lock()
	b.closed = true
	b.actions = nil
	mac.sourceRemove(b.loop, b.source, mac.commonMode)
	mac.cfRelease(b.source)
	b.source = 0
	b.mu.Unlock()
	for _, w := range b.windows {
		w.Close()
	}
	macSend(b.app, "setMainMenu:", 0)
	macSend(b.appDelegate, "release")
	macActive = nil
	mac.poolPop(b.pool)
	return nil
}
func (b *cocoaBackend) Wake() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		mac.sourceSignal(b.source)
		mac.loopWake(b.loop)
	}
}
func (b *cocoaBackend) Quit() {
	if b.quitting {
		return
	}
	b.quitting = true
	if !b.running {
		return
	}
	macSend(b.app, "stop:", 0)
	// stop: finishes after an event; posting one also wakes an otherwise idle app.
	ev := mac.event(macClass("NSEvent"), macSel("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:"), 15, NSPoint{}, 0, 0, 0, 0, 0, 0, 0)
	macSend(b.app, "postEvent:atStart:", uintptr(ev), 1)
}
func (w *cocoaWindow) scale() float64 { return mac.scalar(w.window, macSel("backingScaleFactor")) }
func (w *cocoaWindow) resized() {
	r := mac.rect(w.view, macSel("bounds"))
	scale := w.scale()
	w.events(Event{Kind: Resize, Width: int(math.Round(r.Size.Width * scale)), Height: int(math.Round(r.Size.Height * scale)), Scale: float32(scale)})
}
func (w *cocoaWindow) Present(img *image.RGBA, damage []image.Rectangle) {
	if w.closed || img.Bounds().Empty() {
		return
	}
	if w.GPUActive() {
		if w.graphics.present(img, damage) {
			return
		}
		damage = nil
	}
	data := mac.cfData(0, unsafe.Pointer(unsafe.SliceData(img.Pix)), int64(len(img.Pix)))
	if data == 0 {
		return
	}
	provider := mac.providerNew(data)
	if provider == 0 {
		mac.cfRelease(data)
		return
	}
	cgImage := mac.imageNew(uint64(img.Rect.Dx()), uint64(img.Rect.Dy()), 8, 32, uint64(img.Stride), mac.colorSpace, 1|4<<12, provider, 0, false, 0)
	mac.providerRelease(provider)
	mac.cfRelease(data)
	if cgImage == 0 {
		return
	}
	if w.image != 0 {
		mac.imageRelease(w.image)
	}
	w.image = cgImage
	if !w.painting {
		if len(damage) == 0 {
			macSend(w.view, "setNeedsDisplay:", 1)
		} else {
			scale := w.scale()
			for _, d := range damage {
				d = d.Intersect(img.Bounds())
				if !d.Empty() {
					mac.setRect(w.view, macSel("setNeedsDisplayInRect:"), NSRect{Origin: NSPoint{X: float64(d.Min.X) / scale, Y: float64(d.Min.Y) / scale}, Size: NSSize{Width: float64(d.Dx()) / scale, Height: float64(d.Dy()) / scale}})
				}
			}
		}
	}
}
func (w *cocoaWindow) SetTitle(s string) { macSend(w.window, "setTitle:", uintptr(macString(s))) }
func (w *cocoaWindow) SetSize(width, height int) {
	mac.setSize(w.window, macSel("setContentSize:"), NSSize{Width: float64(width), Height: float64(height)})
}
func (w *cocoaWindow) SetCursor(name string) {
	selector := "arrowCursor"
	switch name {
	case "text":
		selector = "IBeamCursor"
	case "pointer":
		selector = "pointingHandCursor"
	case "crosshair":
		selector = "crosshairCursor"
	case "resize-ew":
		selector = "resizeLeftRightCursor"
	case "resize-ns":
		selector = "resizeUpDownCursor"
	}
	macSend(macSend(macClass("NSCursor"), selector), "set")
}
func (w *cocoaWindow) SetClipboard(s string) {
	pb := macSend(macClass("NSPasteboard"), "generalPasteboard")
	macSend(pb, "clearContents")
	macSend(pb, "setString:forType:", uintptr(macString(s)), uintptr(macString("public.utf8-plain-text")))
}
func (w *cocoaWindow) Clipboard() string {
	pb := macSend(macClass("NSPasteboard"), "generalPasteboard")
	return macText(macSend(pb, "stringForType:", uintptr(macString("public.utf8-plain-text"))))
}
func (w *cocoaWindow) TextInput(enabled bool, caret image.Rectangle) {
	w.input, w.caret = enabled, caret
	if !enabled {
		w.marked = ""
		macSend(macSend(w.view, "inputContext"), "discardMarkedText")
	}
	macSend(macSend(w.view, "inputContext"), "invalidateCharacterCoordinates")
}
func (w *cocoaWindow) Close() {
	if w.closed {
		return
	}
	w.graphics.close()
	w.closed = true
	w.closeAccessibility()
	delete(w.backend.windows, w.window)
	delete(w.backend.views, w.view)
	delete(w.backend.delegates, w.delegate)
	macSend(w.window, "setDelegate:", 0)
	macSend(w.window, "close")
	if w.image != 0 {
		mac.imageRelease(w.image)
		w.image = 0
	}
	// An Objective-C callback can still have these on its stack.
	macSend(w.view, "autorelease")
	macSend(w.delegate, "autorelease")
	macSend(w.window, "autorelease")
}

func macWindow(self objc.ID) *cocoaWindow {
	if macActive == nil {
		return nil
	}
	return macActive.views[self]
}
func macDelegate(self objc.ID) *cocoaWindow {
	if macActive == nil {
		return nil
	}
	return macActive.delegates[self]
}
func macModifiers(e Event, event objc.ID) Event {
	m := uint64(macSend(event, "modifierFlags"))
	e.Shift, e.Control, e.Alt, e.Super = m&(1<<17) != 0, m&(1<<18) != 0, m&(1<<19) != 0, m&(1<<20) != 0
	return e
}
func (w *cocoaWindow) pointer(event objc.ID, kind EventKind) {
	p := mac.convertPoint(w.view, macSel("convertPoint:fromView:"), mac.point(event, macSel("locationInWindow")), 0)
	e := Event{Kind: kind, X: float32(p.X * w.scale()), Y: float32(p.Y * w.scale())}
	if kind == PointerDown || kind == PointerUp {
		switch macSend(event, "buttonNumber") {
		case 0:
			e.Button = 1
		case 1:
			e.Button = 3
		default:
			e.Button = 2
		}
	}
	if kind == Scroll {
		factor := w.scale()
		if macSend(event, "hasPreciseScrollingDeltas") == 0 {
			factor *= 12
		}
		e.DX = float32(-mac.scalar(event, macSel("scrollingDeltaX")) * factor)
		e.DY = float32(-mac.scalar(event, macSel("scrollingDeltaY")) * factor)
	}
	w.events(macModifiers(e, event))
}
func macKey(event objc.ID) string {
	switch macSend(event, "keyCode") {
	case 122:
		return "F1"
	case 120:
		return "F2"
	case 99:
		return "F3"
	case 118:
		return "F4"
	case 96:
		return "F5"
	case 97:
		return "F6"
	case 98:
		return "F7"
	case 100:
		return "F8"
	case 101:
		return "F9"
	case 109:
		return "F10"
	case 103:
		return "F11"
	case 111:
		return "F12"
	case 36, 76:
		return "Enter"
	case 48:
		return "Tab"
	case 49:
		return "Space"
	case 51:
		return "Backspace"
	case 53:
		return "Escape"
	case 117:
		return "Delete"
	case 115:
		return "Home"
	case 119:
		return "End"
	case 116:
		return "PageUp"
	case 121:
		return "PageDown"
	case 123:
		return "ArrowLeft"
	case 124:
		return "ArrowRight"
	case 125:
		return "ArrowDown"
	case 126:
		return "ArrowUp"
	}
	return strings.ToLower(macText(macSend(event, "charactersIgnoringModifiers")))
}
func macViewMethods() []objc.MethodDef {
	methods := []objc.MethodDef{
		{Cmd: macSel("isFlipped"), Fn: func(objc.ID, objc.SEL) bool { return true }},
		{Cmd: macSel("isOpaque"), Fn: func(objc.ID, objc.SEL) bool { return true }},
		{Cmd: macSel("acceptsFirstResponder"), Fn: func(objc.ID, objc.SEL) bool { return true }},
		{Cmd: macSel("acceptsFirstMouse:"), Fn: func(objc.ID, objc.SEL, objc.ID) bool { return true }},
		{Cmd: macSel("drawRect:"), Fn: func(self objc.ID, _ objc.SEL, _ NSRect) {
			if w := macWindow(self); w != nil {
				w.painting = true
				defer func() { w.painting = false }()
				w.events(Event{Kind: Paint})
				if w.image != 0 && !w.closed && !w.GPUActive() {
					ctx := macSend(macSend(macClass("NSGraphicsContext"), "currentContext"), "CGContext")
					bounds := mac.rect(self, macSel("bounds"))
					mac.save(uintptr(ctx))
					mac.translate(uintptr(ctx), 0, bounds.Size.Height)
					mac.scale(uintptr(ctx), 1, -1)
					mac.draw(uintptr(ctx), bounds, w.image)
					mac.restore(uintptr(ctx))
				}
			}
		}},
		{Cmd: macSel("keyDown:"), Fn: func(self objc.ID, _ objc.SEL, event objc.ID) {
			if w := macWindow(self); w != nil {
				e := macModifiers(Event{Kind: KeyDown, Key: macKey(event)}, event)
				if w.marked == "" || e.Control || e.Super {
					w.events(e)
				}
				if w.input && !e.Control && !e.Super {
					a := macSend(macClass("NSArray"), "arrayWithObject:", uintptr(event))
					macSend(self, "interpretKeyEvents:", uintptr(a))
				}
			}
		}},
		{Cmd: macSel("keyUp:"), Fn: func(self objc.ID, _ objc.SEL, event objc.ID) {
			if w := macWindow(self); w != nil {
				w.events(macModifiers(Event{Kind: KeyUp, Key: macKey(event)}, event))
			}
		}},
		{Cmd: macSel("insertText:replacementRange:"), Fn: func(self objc.ID, _ objc.SEL, text objc.ID, _ NSRange) {
			if w := macWindow(self); w != nil && w.input {
				w.marked = ""
				s := macText(text)
				if s != "" {
					w.events(Event{Kind: Text, Text: s})
				}
			}
		}},
		{Cmd: macSel("setMarkedText:selectedRange:replacementRange:"), Fn: func(self objc.ID, _ objc.SEL, text objc.ID, selection NSRange, _ NSRange) {
			if w := macWindow(self); w != nil && w.input {
				if marked, ok := macBoundedText(text); ok {
					w.marked, w.selected = marked, selection
				}
			}
		}},
		{Cmd: macSel("unmarkText"), Fn: func(self objc.ID, _ objc.SEL) {
			if w := macWindow(self); w != nil {
				w.marked = ""
			}
		}},
		{Cmd: macSel("hasMarkedText"), Fn: func(self objc.ID, _ objc.SEL) bool { w := macWindow(self); return w != nil && w.marked != "" }},
		{Cmd: macSel("markedRange"), Fn: func(self objc.ID, _ objc.SEL) NSRange {
			if w := macWindow(self); w != nil && w.marked != "" {
				return NSRange{Length: uint64(len(utf16.Encode([]rune(w.marked))))}
			}
			return NSRange{Location: 1<<63 - 1}
		}},
		{Cmd: macSel("selectedRange"), Fn: func(self objc.ID, _ objc.SEL) NSRange {
			if w := macWindow(self); w != nil {
				return w.selected
			}
			return NSRange{}
		}},
		{Cmd: macSel("validAttributesForMarkedText"), Fn: func(objc.ID, objc.SEL) objc.ID { return macSend(macClass("NSArray"), "array") }},
		{Cmd: macSel("attributedSubstringForProposedRange:actualRange:"), Fn: func(_ objc.ID, _ objc.SEL, _ NSRange, actual *NSRange) objc.ID {
			if actual != nil {
				*actual = NSRange{Location: 1<<63 - 1}
			}
			return 0
		}},
		{Cmd: macSel("characterIndexForPoint:"), Fn: func(objc.ID, objc.SEL, NSPoint) uint64 { return 0 }},
		{Cmd: macSel("firstRectForCharacterRange:actualRange:"), Fn: func(self objc.ID, _ objc.SEL, requested NSRange, actual *NSRange) NSRect {
			if actual != nil {
				*actual = requested
			}
			if w := macWindow(self); w != nil {
				scale := w.scale()
				r := NSRect{Origin: NSPoint{X: float64(w.caret.Min.X) / scale, Y: float64(w.caret.Min.Y) / scale}, Size: NSSize{Width: float64(w.caret.Dx()) / scale, Height: float64(w.caret.Dy()) / scale}}
				r = mac.convertRect(self, macSel("convertRect:toView:"), r, 0)
				return mac.screenRect(w.window, macSel("convertRectToScreen:"), r)
			}
			return NSRect{}
		}},
		{Cmd: macSel("doCommandBySelector:"), Fn: func(objc.ID, objc.SEL, objc.SEL) {}},
	}
	for _, entry := range []struct {
		name string
		kind EventKind
	}{{"mouseDown:", PointerDown}, {"mouseUp:", PointerUp}, {"rightMouseDown:", PointerDown}, {"rightMouseUp:", PointerUp}, {"otherMouseDown:", PointerDown}, {"otherMouseUp:", PointerUp}, {"mouseMoved:", PointerMove}, {"mouseDragged:", PointerMove}, {"rightMouseDragged:", PointerMove}, {"otherMouseDragged:", PointerMove}, {"scrollWheel:", Scroll}} {
		kind := entry.kind
		methods = append(methods, objc.MethodDef{Cmd: macSel(entry.name), Fn: func(self objc.ID, _ objc.SEL, event objc.ID) {
			if w := macWindow(self); w != nil {
				w.pointer(event, kind)
			}
		}})
	}
	return append(methods, macAccessViewMethods()...)
}
func macDelegateMethods() []objc.MethodDef {
	return []objc.MethodDef{
		{Cmd: macSel("fegoTrayAction:"), Fn: func(self objc.ID, _ objc.SEL, sender objc.ID) {
			if b := macActive; b != nil {
				if tray := b.trays[self]; tray != nil {
					tray.click(int32(macSend(sender, "tag")))
				}
			}
		}},
		{Cmd: macSel("fegoTrayActivate:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if b := macActive; b != nil {
				if tray := b.trays[self]; tray != nil {
					tray.click(0)
				}
			}
		}},
		{Cmd: macSel("windowShouldClose:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) bool {
			if w := macDelegate(self); w != nil {
				w.events(Event{Kind: Close})
			}
			return false
		}},
		{Cmd: macSel("windowDidResize:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if w := macDelegate(self); w != nil {
				w.resized()
			}
		}},
		{Cmd: macSel("windowDidChangeBackingProperties:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if w := macDelegate(self); w != nil {
				w.resized()
			}
		}},
		{Cmd: macSel("windowDidBecomeKey:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if w := macDelegate(self); w != nil {
				w.events(Event{Kind: Focus, Focused: true})
			}
		}},
		{Cmd: macSel("windowDidResignKey:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if w := macDelegate(self); w != nil {
				w.events(Event{Kind: Focus})
			}
		}},
		{Cmd: macSel("quit:"), Fn: func(objc.ID, objc.SEL, objc.ID) {
			if b := macActive; b != nil {
				b.Quit()
			}
		}},
	}
}

func (w *cocoaWindow) Maximized() bool { return macSend(w.window, "isZoomed") != 0 }
func (w *cocoaWindow) SetMaximized(on bool) {
	if on != w.Maximized() {
		macSend(w.window, "zoom:", 0)
	}
}
