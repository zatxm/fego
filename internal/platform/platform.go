// Package platform owns the native windows. UI layout, widgets and painting live in Go.
package platform

import "image"

type EventKind uint8

const (
	Paint EventKind = iota
	Resize
	PointerMove
	PointerDown
	PointerUp
	Scroll
	KeyDown
	KeyUp
	Text
	Focus
	Close
	AccessAction
)

// A Paint event may carry the lost area in X, Y, Width and Height (device
// pixels); a zero size means the whole window.
type Event struct {
	Kind                       EventKind
	X, Y, DX, DY               float32
	Width, Height              int
	Scale                      float32
	Button                     int
	Key                        string
	Text                       string
	Shift, Control, Alt, Super bool
	Focused                    bool
	AccessID                   uint64
	Action                     string
}

type Options struct {
	Title         string
	Width, Height int
}

// All methods except Wake run on the main thread. Wake may run on any goroutine.
type Window interface {
	Present(*image.RGBA, []image.Rectangle)
	SetTitle(string)
	SetSize(int, int)
	SetCursor(string)
	SetClipboard(string)
	Clipboard() string
	TextInput(bool, image.Rectangle)
	Close()
}

// Backend calls dispatch when Wake is received. Run blocks without polling while idle.
type Backend interface {
	Open(Options, func(Event)) (Window, error)
	Run() error
	Wake()
	Quit()
}

// AccessNode describes one retained control. Bounds are device pixels relative
// to the client area; Parent 0 attaches a node to the native window.
type AccessNode struct {
	ID, Parent                           uint64
	Role, Label, Value                   string
	Bounds                               image.Rectangle
	Disabled, Focused, Checked, Selected bool
	Min, Max, Number                     float64
	Caret, SelectionStart, SelectionEnd  int // Unicode rune offsets
}

// Accessible exports controls to the platform's real assistive-technology API.
// Actions use focus, press, setValue, select, setCaret and setSelection; text
// carries the new value, a decimal rune offset, or a start:end rune range.
type Accessible interface {
	// The slice is borrowed for this call; implementations retain their own snapshot.
	SetAccessibility([]AccessNode)
}

// PixelScroller copies retained window pixels before damaged strips are presented.
// Returning false leaves the window untouched and requests normal presentation.
type PixelScroller interface {
	ScrollPixels(image.Rectangle, image.Point) bool
}

// RegionPresenter windows retain presented pixels and emit Paint after losing
// any of them. PresentRegion uploads img at its bounds, in window device pixels,
// so callers can paint damaged regions without a full-window client surface.
// img is borrowed for the call and must not be retained.
type RegionPresenter interface {
	PresentRegion(*image.RGBA)
}
