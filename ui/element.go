// Package ui implements retained desktop controls, layout and rendering in Go.
// Build a tree once; setters and event callbacks update only the affected regions.
package ui

import (
	"image"
	"image/color"
	"math"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

type Color = color.RGBA

func RGB(r, g, b uint8) Color     { return Color{R: r, G: g, B: b, A: 255} }
func RGBA(r, g, b, a uint8) Color { return Color{R: r, G: g, B: b, A: a} }

// Theme maps the shadcn Neutral tokens to native sRGB colors. Accent and
// AccentText are the existing fego names for primary and primary-foreground.
type Theme struct {
	Background, Surface, Raised, Text, Muted, Border, Accent, AccentText, Danger Color
	InputBorder, Ring, Sidebar                                                   Color
	Chart                                                                        [5]Color
	FontSize                                                                     float32
}

var Light = Theme{
	Background: RGB(255, 255, 255), Surface: RGB(255, 255, 255), Raised: RGB(245, 245, 245),
	Text: RGB(10, 10, 10), Muted: RGB(115, 115, 115), Border: RGB(229, 229, 229),
	Accent: RGB(23, 23, 23), AccentText: RGB(250, 250, 250), Danger: RGB(231, 0, 11),
	InputBorder: RGB(229, 229, 229), Ring: RGB(161, 161, 161), Sidebar: RGB(250, 250, 250),
	Chart:    [5]Color{RGB(245, 74, 0), RGB(0, 150, 137), RGB(16, 78, 100), RGB(255, 185, 0), RGB(254, 154, 0)},
	FontSize: 14,
}

var Dark = Theme{
	Background: RGB(10, 10, 10), Surface: RGB(23, 23, 23), Raised: RGB(38, 38, 38),
	Text: RGB(250, 250, 250), Muted: RGB(161, 161, 161), Border: RGBA(255, 255, 255, 26),
	Accent: RGB(229, 229, 229), AccentText: RGB(23, 23, 23), Danger: RGB(255, 100, 103),
	InputBorder: RGBA(255, 255, 255, 38), Ring: RGB(115, 115, 115), Sidebar: RGB(23, 23, 23),
	Chart:    [5]Color{RGB(20, 71, 230), RGB(0, 188, 125), RGB(254, 154, 0), RGB(173, 70, 255), RGB(255, 32, 86)},
	FontSize: 14,
}

type tone uint8

const (
	standardTone tone = iota
	mutedText
	dangerText
	sidebarSurface
	mutedSurface
	primarySurface
	primaryText
)

type Align uint8

const (
	Start Align = iota
	Center
	End
	Stretch
	SpaceBetween
)

type Rect struct{ X, Y, W, H float32 }

func (r Rect) contains(x, y float32) bool { return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H }
func (r Rect) pixels(scale float32) image.Rectangle {
	return image.Rect(int(math.Floor(float64(r.X*scale))), int(math.Floor(float64(r.Y*scale))), int(math.Ceil(float64((r.X+r.W)*scale))), int(math.Ceil(float64((r.Y+r.H)*scale))))
}
func (r Rect) intersect(o Rect) Rect {
	x, y := max(r.X, o.X), max(r.Y, o.Y)
	return Rect{x, y, max(0, min(r.X+r.W, o.X+o.W)-x), max(0, min(r.Y+r.H, o.Y+o.H)-y)}
}

type kind uint8

const (
	kBox kind = iota
	kText
	kButton
	kInput
	kCheck
	kSwitch
	kRadio
	kSlider
	kProgress
	kSelect
	kSeparator
	kList
	kTable
	kTree
	kImage
	kCanvas
	kEditor
	kRichText
	kVirtualList
	kVirtualRow
)

type direction uint8

const (
	vertical direction = iota
	horizontal
	grid
)

type style struct {
	w, h, minW, minH, maxW, maxH, grow                      float32
	padding                                                 [4]float32
	gap, radius, font                                       float32
	align, justify                                          Align
	bg, fg, border                                          Color
	bgSet, fgSet, borderSet, bold, fillW, fillH, card, clip bool
	dir                                                     direction
	tone                                                    tone
	columns                                                 int
}

// Element is owned by one view. Mutate it on the UI thread; use Window.Update
// when data arrives from a goroutine. Children cannot be shared between parents.
type Element struct {
	id               uint64
	kind             kind
	layoutY          float32
	style            style
	parent           *Element
	children         []*Element
	view             *view
	rect, clip       Rect
	text, label      string
	tooltip          string
	disabled, hidden bool
	onClick          func()
	scroll, contentH float32
	scrollable       bool
	*widget          // control state; layout-only elements share noWidget
}

// widget holds state used by controls but not by plain boxes and labels.
type widget struct {
	placeholder                                string
	primary, checked, multiline, password, tab bool
	value, minimum, maximum                    float64
	selected                                   int
	options                                    []string
	count                                      int
	rowText                                    func(int) string
	columns                                    []ColumnDef
	rows                                       [][]string
	order                                      []int
	tree                                       []*TreeNode
	visibleTree                                []treeRow
	expanded                                   map[*TreeNode]bool
	spans                                      []Span
	bitmap                                     image.Image
	draw                                       func(*Painter)
	onChange                                   func(string)
	onToggle                                   func(bool)
	onValue                                    func(float64)
	onSelect                                   func(int)
	onLink                                     func(string)
	scrollX                                    float32
	cursor, anchor                             int
	undo                                       []string
	undoBytes                                  int
	virtual                                    *VirtualListView
	interval                                   *RangeSliderView
	submit                                     func()
	otpLength                                  int
}

// noWidget is read-only; own gives an element private state before any write.
var noWidget = widget{selected: -1}

func (e *Element) own() *widget {
	if e.widget == &noWidget {
		e.widget = &widget{selected: -1}
	}
	return e.widget
}

var elementIDs atomic.Uint64

func element(k kind) *Element {
	e := &Element{id: elementIDs.Add(1) << 32, kind: k, style: style{radius: 8, align: Stretch}, widget: &noWidget}
	if k != kBox && k != kText && k != kSeparator {
		e.widget = &widget{selected: -1}
	}
	return e
}
func Box(children ...*Element) *Element    { return element(kBox).Children(children...) }
func Column(children ...*Element) *Element { return Box(children...).Gap(12) }
func Row(children ...*Element) *Element {
	e := Box(children...).Gap(10)
	e.style.dir = horizontal
	e.style.align = Center
	return e
}
func Grid(columns int, children ...*Element) *Element {
	if columns < 1 {
		panic("ui: grid needs at least one column")
	}
	e := Box(children...).Gap(12)
	e.style.dir = grid
	e.style.columns = columns
	return e
}
func Card(children ...*Element) *Element {
	e := Column(children...).Padding(20).Radius(12)
	e.style.card = true
	return e
}

// ScrollView clips one retained content tree and scrolls it vertically.
func ScrollView(content *Element) *Element {
	e := Box(content).Clip()
	e.scrollable = true
	return e
}
func Text(s string) *Element { e := element(kText); e.text = s; return e }
func Button(s string, click func()) *Element {
	e := element(kButton)
	e.text = s
	e.onClick = click
	return e
}
func PrimaryButton(s string, click func()) *Element {
	e := Button(s, click)
	e.own().primary = true
	return e
}
func TextInput(s string) *Element {
	e := element(kInput)
	e.text = s
	e.own().cursor = utf8.RuneCountInString(s)
	e.own().anchor = e.cursor
	return e
}
func TextArea(s string) *Element   { e := TextInput(s); e.own().multiline = true; return e.Height(120) }
func CodeEditor(s string) *Element { e := TextArea(s); e.kind = kEditor; return e.Height(240) }
func Checkbox(s string, checked bool) *Element {
	e := element(kCheck)
	e.text = s
	e.own().checked = checked
	return e
}
func Switch(s string, checked bool) *Element { e := Checkbox(s, checked); e.kind = kSwitch; return e }
func Radio(s string, checked bool) *Element  { e := Checkbox(s, checked); e.kind = kRadio; return e }
func Slider(value, low, high float64) *Element {
	if math.IsNaN(low) || math.IsNaN(high) || math.IsInf(low, 0) || math.IsInf(high, 0) || low >= high {
		panic("ui: invalid slider range")
	}
	e := element(kSlider)
	e.own().minimum, e.own().maximum = low, high
	return e.SetValue(value)
}
func Progress(value float64) *Element {
	e := element(kProgress)
	e.own().maximum = 1
	return e.SetValue(value)
}
func Select(options []string, selected int) *Element {
	e := element(kSelect)
	e.own().options = append([]string(nil), options...)
	e.SetSelected(selected)
	return e
}
func Separator() *Element { return element(kSeparator).Height(1) }

// List obtains visible labels on demand. Call Refresh after changing provider data.
func List(count int, text func(int) string) *Element {
	if count < 0 || text == nil {
		panic("ui: invalid list source")
	}
	e := element(kList)
	e.own().count = count
	e.own().rowText = text
	return e.Height(240)
}

type ColumnDef struct {
	Title string
	Width float32
}

func Table(columns []ColumnDef, rows [][]string) *Element {
	e := element(kTable)
	e.own().columns = append([]ColumnDef(nil), columns...)
	e.own().rows = rows
	return e.Height(260)
}

type TreeNode struct {
	Label    string
	Children []*TreeNode
}
type treeRow struct {
	node  *TreeNode
	depth int
}

func Tree(nodes ...*TreeNode) *Element {
	e := element(kTree)
	e.own().tree = nodes
	e.own().expanded = map[*TreeNode]bool{}
	e.rebuildTree()
	return e.Height(240)
}
func Image(img image.Image) *Element { e := element(kImage); e.own().bitmap = img; return e }
func Canvas(width, height float32, draw func(*Painter)) *Element {
	e := element(kCanvas)
	e.own().draw = draw
	return e.Size(width, height)
}

type Span struct {
	Text  string
	Color Color
	Bold  bool
	Link  string
}

func RichText(spans ...Span) *Element {
	e := element(kRichText)
	e.own().spans = append([]Span(nil), spans...)
	// Accessibility reuses the immutable plain text instead of joining spans every frame.
	var text strings.Builder
	for _, span := range spans {
		text.WriteString(span.Text)
	}
	e.text = text.String()
	return e
}

func (e *Element) Children(children ...*Element) *Element {
	retained := make(map[*Element]bool, len(children))
	for _, c := range children {
		if c == nil || c == e || retained[c] || (c.parent != nil && c.parent != e) || (c.view != nil && c.parent != e) {
			panic("ui: child must have one parent")
		}
		for p := e; p != nil; p = p.parent {
			if p == c {
				panic("ui: cyclic widget tree")
			}
		}
		retained[c] = true
	}
	for _, c := range e.children {
		if retained[c] {
			continue
		}
		if e.view != nil {
			e.view.forget(c)
		}
		c.parent = nil
		c.attach(nil)
	}
	e.children = append([]*Element(nil), children...)
	for _, c := range children {
		if c.parent == e {
			continue
		}
		c.parent = e
		c.attach(e.view)
	}
	e.touch(true)
	return e
}
func (e *Element) Append(children ...*Element) *Element {
	all := append(append([]*Element(nil), e.children...), children...)
	return e.Children(all...)
}
func (e *Element) attach(v *view) {
	e.view = v
	for _, c := range e.children {
		c.attach(v)
	}
}

// Reuse bounded provider data while scrolling; Refresh invalidates these labels.
func (e *Element) listText(row int) string {
	v := e.view
	id := [2]uint64{e.id, uint64(row)}
	if label, ok := v.rowLabels[id]; ok {
		if e.kind != kTable || matchesRowLabel(label, e.rows[row]) {
			return label
		}
	}
	var label string
	if e.kind == kTable {
		label = strings.Join(e.rows[row], ", ")
	} else {
		label = e.rowText(row)
	}
	if len(label) <= 1024 {
		if v.rowLabels == nil {
			v.rowLabels = make(map[[2]uint64]string)
		}
		if len(v.rowLabels) >= 64 {
			clear(v.rowLabels)
		}
		if e.kind == kList {
			label = strings.Clone(label)
		}
		v.rowLabels[id] = label
	}
	return label
}

// Reuse the accessible label even when a table's caller edits a supplied slice.
func matchesRowLabel(label string, cells []string) bool {
	for i, cell := range cells {
		if i > 0 {
			var ok bool
			label, ok = strings.CutPrefix(label, ", ")
			if !ok {
				return false
			}
		}
		var ok bool
		label, ok = strings.CutPrefix(label, cell)
		if !ok {
			return false
		}
	}
	return label == ""
}

func (e *Element) touch(layout bool) {
	if e.view != nil {
		e.view.invalidate(e, layout)
	}
}
func (e *Element) SetText(s string) *Element {
	if e.kind == kInput && e.otpLength > 0 {
		s = otpDigits(s, e.otpLength)
	}
	if e.text != s {
		variable := e.kind == kText || e.kind == kButton || e.kind == kCheck || e.kind == kSwitch || e.kind == kRadio
		layout := variable && e.style.w == 0
		if variable && e.style.h == 0 && e.view != nil {
			_, before := e.view.text.Measure(e.text, e.view.font(e), e.style.bold)
			_, after := e.view.text.Measure(s, e.view.font(e), e.style.bold)
			layout = layout || before != after
		}
		e.text = s
		if e.kind == kInput || e.kind == kEditor {
			n := utf8.RuneCountInString(s)
			e.own().cursor = min(e.cursor, n)
			e.own().anchor = min(e.anchor, n)
			e.own().undo = nil
			e.own().undoBytes = 0
			e.scroll = min(e.scroll, e.maxScroll())
			e.own().scrollX = 0
		}
		e.touch(layout)
	}
	return e
}
func (e *Element) Text() string { return e.text }
func (e *Element) SetChecked(v bool) *Element {
	if e.checked != v {
		e.own().checked = v
		e.touch(false)
	}
	return e
}
func (e *Element) Checked() bool { return e.checked }
func (e *Element) SetValue(v float64) *Element {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		panic("ui: value must be finite")
	}
	v = max(e.minimum, min(e.maximum, v))
	if e.kind == kSlider && e.interval != nil {
		v = min(e.interval.high, v)
	}
	if e.value != v {
		e.own().value = v
		e.touch(false)
	}
	return e
}
func (e *Element) Value() float64 { return e.value }
func (e *Element) SetSelected(i int) *Element {
	if e.kind == kSelect && (i < -1 || i >= len(e.options)) {
		panic("ui: selection outside options")
	}
	if e.selected != i {
		e.own().selected = i
		e.touch(false)
	}
	return e
}
func (e *Element) Selected() int { return e.selected }
func (e *Element) SetRows(rows [][]string) *Element {
	e.own().rows = rows
	if e.view != nil {
		clear(e.view.rowLabels)
	}
	e.own().order = nil
	e.own().selected = -1
	e.scroll = 0
	e.touch(false)
	return e
}
func (e *Element) SetCount(n int) *Element {
	if n < 0 {
		panic("ui: negative list length")
	}
	e.own().count = n
	if e.view != nil {
		clear(e.view.rowLabels)
	}
	e.own().selected = min(e.selected, n-1)
	e.scroll = min(e.scroll, e.maxScroll())
	e.touch(false)
	return e
}
func (e *Element) Width(w float32) *Element     { e.style.w = w; e.touch(true); return e }
func (e *Element) Height(h float32) *Element    { e.style.h = h; e.touch(true); return e }
func (e *Element) Size(w, h float32) *Element   { e.style.w, e.style.h = w, h; e.touch(true); return e }
func (e *Element) MinWidth(w float32) *Element  { e.style.minW = w; e.touch(true); return e }
func (e *Element) MinHeight(h float32) *Element { e.style.minH = h; e.touch(true); return e }
func (e *Element) MaxWidth(w float32) *Element  { e.style.maxW = w; e.touch(true); return e }
func (e *Element) MaxHeight(h float32) *Element { e.style.maxH = h; e.touch(true); return e }
func (e *Element) Grow(weight float32) *Element { e.style.grow = weight; e.touch(true); return e }
func (e *Element) Fill() *Element {
	e.style.fillW, e.style.fillH = true, true
	e.touch(true)
	return e
}
func (e *Element) FillWidth() *Element { e.style.fillW = true; e.touch(true); return e }
func (e *Element) FillHeight() *Element {
	e.style.fillH = true
	e.touch(true)
	return e
}
func (e *Element) Gap(gap float32) *Element { e.style.gap = gap; e.touch(true); return e }
func (e *Element) Padding(values ...float32) *Element {
	switch len(values) {
	case 1:
		e.style.padding = [4]float32{values[0], values[0], values[0], values[0]}
	case 2:
		e.style.padding = [4]float32{values[0], values[1], values[0], values[1]}
	case 4:
		copy(e.style.padding[:], values)
	default:
		panic("ui: Padding takes 1, 2 or 4 values")
	}
	e.touch(true)
	return e
}
func (e *Element) Radius(radius float32) *Element { e.style.radius = radius; e.touch(false); return e }
func (e *Element) Background(c Color) *Element {
	if !e.style.bgSet || e.style.bg != c {
		e.style.bg, e.style.bgSet = c, true
		e.touch(false)
	}
	return e
}

// Muted uses the active theme's muted foreground color.
func (e *Element) Muted() *Element {
	e.style.tone = mutedText
	e.style.fgSet = false
	e.touch(false)
	return e
}

func (e *Element) TextColor(c Color) *Element {
	e.style.fg, e.style.fgSet = c, true
	e.touch(false)
	return e
}
func (e *Element) Border(c Color) *Element {
	e.style.border, e.style.borderSet = c, true
	e.touch(false)
	return e
}
func (e *Element) FontSize(size float32) *Element  { e.style.font = size; e.touch(true); return e }
func (e *Element) Bold() *Element                  { e.style.bold = true; e.touch(true); return e }
func (e *Element) AlignItems(align Align) *Element { e.style.align = align; e.touch(true); return e }
func (e *Element) Justify(align Align) *Element    { e.style.justify = align; e.touch(true); return e }
func (e *Element) Center() *Element {
	e.style.justify, e.style.align = Center, Center
	e.touch(true)
	return e
}
func (e *Element) Clip() *Element { e.style.clip = true; e.touch(false); return e }
func (e *Element) Disabled(disabled bool) *Element {
	if e.disabled != disabled {
		if disabled && e.view != nil {
			e.view.forget(e)
		}
		e.disabled = disabled
		e.touch(false)
	}
	return e
}
func (e *Element) Hidden(hidden bool) *Element {
	if e.hidden != hidden {
		if hidden && e.view != nil {
			e.view.forget(e)
		}
		e.hidden = hidden
		e.touch(true)
	}
	return e
}
func (e *Element) Placeholder(s string) *Element { e.own().placeholder = s; e.touch(false); return e }
func (e *Element) Password(secret bool) *Element { e.own().password = secret; e.touch(false); return e }
func (e *Element) Label(s string) *Element       { e.label = s; e.touch(false); return e }

// OnSubmit invokes fn when Enter is pressed in a single-line text field.
func (e *Element) OnSubmit(fn func()) *Element {
	if e.kind != kInput || e.multiline {
		panic("ui: OnSubmit requires a single-line text input")
	}
	e.own().submit = fn
	return e
}

func (e *Element) OnClick(fn func()) *Element        { e.onClick = fn; return e }
func (e *Element) OnChange(fn func(string)) *Element { e.own().onChange = fn; return e }
func (e *Element) OnToggle(fn func(bool)) *Element   { e.own().onToggle = fn; return e }
func (e *Element) OnValue(fn func(float64)) *Element { e.own().onValue = fn; return e }
func (e *Element) OnSelect(fn func(int)) *Element    { e.own().onSelect = fn; return e }
func (e *Element) OnLink(fn func(string)) *Element   { e.own().onLink = fn; return e }
func (e *Element) Bounds() Rect                      { return e.rect }
func (e *Element) Refresh() {
	if e.view != nil {
		clear(e.view.rowLabels)
	}
	e.touch(false)
}

func (e *Element) paintPadding(scale float32) int {
	padding := float32(3)
	if e.style.card {
		padding = 6
	}
	return max(4, int(math.Ceil(float64(padding*scale))))
}

func (e *Element) rebuildTree() {
	e.own().visibleTree = e.visibleTree[:0]
	seen := make(map[*TreeNode]bool)
	var walk func([]*TreeNode, int)
	walk = func(nodes []*TreeNode, depth int) {
		for _, n := range nodes {
			if n == nil || seen[n] {
				panic("ui: tree nodes must be non-nil and have one parent")
			}
			seen[n] = true
			e.own().visibleTree = append(e.visibleTree, treeRow{n, depth})
			if e.expanded[n] {
				walk(n.Children, depth+1)
			}
		}
	}
	walk(e.tree, 0)
	if cap(e.visibleTree) > max(256, len(e.visibleTree)*4) {
		e.own().visibleTree = append([]treeRow(nil), e.visibleTree...)
	}
}
