package ui

import (
	"image"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zatxm/fego"
)

type popup struct {
	owner    *Element
	options  []string
	selected int
	first    int
	choose   func(int)
	rect     Rect
}

func (v *view) forget(subtree *Element) {
	inside := func(e *Element) bool {
		for ; e != nil; e = e.parent {
			if e == subtree {
				return true
			}
		}
		return false
	}
	if inside(v.focused) {
		v.focus(nil)
	}
	if inside(v.pressed) {
		v.pressed = nil
	}
	if inside(v.scrollDrag) {
		v.scrollDrag = nil
	}
	if inside(v.hover) {
		v.hover = nil
		v.host.SetCursor("default")
	}
	if v.popup != nil && inside(v.popup.owner) {
		v.popup = nil
		v.invalidate(v.root, false)
	}
}

func (e *Element) interactive() bool {
	switch e.kind {
	case kButton, kInput, kEditor, kCheck, kRadio, kSwitch, kSlider, kSelect, kList, kTable, kTree:
		return true
	}
	return e.onClick != nil || e.onLink != nil
}
func hit(e *Element, x, y float32) *Element {
	if e.hidden || e.disabled || !e.clip.contains(x, y) {
		return nil
	}
	for i := len(e.children) - 1; i >= 0; i-- {
		if c := hit(e.children[i], x, y); c != nil {
			return c
		}
	}
	if e.interactive() && e.rect.contains(x, y) {
		return e
	}
	return nil
}
func scrollHit(e *Element, x, y float32) *Element {
	if e.hidden || e.disabled || !e.clip.contains(x, y) {
		return nil
	}
	for i := len(e.children) - 1; i >= 0; i-- {
		if child := scrollHit(e.children[i], x, y); child != nil {
			return child
		}
	}
	if e.scrollable || e.kind == kList || e.kind == kTable || e.kind == kTree || e.multiline {
		return e
	}
	return nil
}
func (v *view) input(ev fego.Event) {
	scale := v.scale
	if scale <= 0 {
		scale = 1
	}
	x, y := ev.X/scale, ev.Y/scale
	root := v.root
	if v.dialog != nil {
		root = v.dialog
	}
	if ev.Kind == fego.PointerMove || ev.Kind == fego.PointerDown || ev.Kind == fego.PointerUp {
		v.pointerX, v.pointerY = x, y
	}
	if v.popup != nil && v.popupInput(ev, x, y) {
		return
	}
	switch ev.Kind {
	case fego.PointerMove:
		if v.scrollDrag != nil {
			v.dragScroll(y)
			return
		}
		e := hit(root, x, y)
		if e != v.hover {
			old := v.hover
			v.hover = e
			if old != nil {
				v.invalidate(old, false)
			}
			if e != nil {
				v.invalidate(e, false)
			}
			cursor := "default"
			if e != nil {
				if e.kind == kInput || e.kind == kEditor {
					cursor = "text"
				} else {
					cursor = "pointer"
				}
			}
			v.host.SetCursor(cursor)
		}
		if e := v.pressed; e != nil {
			if e.kind == kSlider {
				v.slide(e, x)
			} else if e.kind == kInput || e.kind == kEditor {
				e.own().cursor = v.textPosition(e, x, y)
				v.invalidate(e, false)
			}
		}
	case fego.PointerDown:
		if ev.Button != 1 {
			return
		}
		if target := scrollHit(root, x, y); target != nil {
			track, thumb := scrollbar(target)
			if target.maxScroll() > 0 && track.contains(x, y) {
				v.scrollDrag = target
				v.scrollGrab = thumb.H / 2
				if thumb.contains(x, y) {
					v.scrollGrab = y - thumb.Y
				}
				v.dragScroll(y)
				return
			}
		}
		e := hit(root, x, y)
		v.pressed = e
		v.focus(e)
		if e != nil {
			if e.kind == kInput || e.kind == kEditor {
				e.own().cursor = v.textPosition(e, x, y)
				if !ev.Shift {
					e.own().anchor = e.cursor
				}
			}
			if e.kind == kSlider {
				v.slide(e, x)
			}
			v.invalidate(e, false)
		}
	case fego.PointerUp:
		if ev.Button != 1 {
			return
		}
		if v.scrollDrag != nil {
			v.scrollDrag = nil
			return
		}
		e := v.pressed
		v.pressed = nil
		if e != nil {
			v.invalidate(e, false)
			if e == hit(root, x, y) {
				v.activate(e, x, y)
			}
		}
	case fego.Scroll:
		e := scrollHit(root, x, y)
		for e != nil {
			if e.scrollable || e.kind == kList || e.kind == kTable || e.kind == kTree || e.multiline {
				v.scrollRows(e, max(0, min(e.maxScroll(), e.scroll+ev.DY/scale)))
				break
			}
			e = e.parent
		}
	case fego.KeyDown:
		if ev.Key == "Tab" {
			v.focusNext(ev.Shift)
			return
		}
		if ev.Key == "Escape" {
			if v.dialog != nil {
				v.doc.CloseDialog()
			} else if v.toast != "" {
				v.toast = ""
				v.invalidate(v.root, false)
			}
			return
		}
		e := v.focused
		if e == nil || !v.available(e) {
			return
		}
		if e.kind == kInput || e.kind == kEditor {
			v.editKey(e, ev)
			return
		}
		switch ev.Key {
		case "Enter", "Space":
			y := e.rect.Y + e.rect.H/2
			if e.rowCount() > 0 {
				if e.selected < 0 || e.selected >= e.rowCount() {
					return
				}
				y = e.rect.Y + e.headerHeight() + (float32(e.selected)+.5)*e.rowHeight() - e.scroll
			}
			v.activate(e, e.rect.X+e.rect.W/2, y)
		case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End":
			v.navigate(e, ev.Key)
		}
	case fego.Text:
		e := v.focused
		if e != nil && v.available(e) && (e.kind == kInput || e.kind == kEditor) && len(ev.Text) <= 4<<20 {
			s := ev.Text
			if !e.multiline {
				s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
			}
			v.replaceSelection(e, s)
		}
	}
}

func scrollbar(e *Element) (track, thumb Rect) {
	if !e.scrollable && e.rowCount() == 0 {
		return
	}
	header := e.headerHeight()
	track = Rect{e.rect.X + e.rect.W - 9, e.rect.Y + header, 9, max(0, e.rect.H-header)}
	content := track.H + e.maxScroll()
	height := min(track.H, max(24, track.H*track.H/max(1, content)))
	y := track.Y + (track.H-height)*e.scroll/max(1, e.maxScroll())
	thumb = Rect{track.X, y, track.W, height}
	return
}
func (v *view) dragScroll(y float32) {
	e := v.scrollDrag
	track, thumb := scrollbar(e)
	fraction := max(0, min(1, (y-track.Y-v.scrollGrab)/max(1, track.H-thumb.H)))
	v.scrollRows(e, fraction*e.maxScroll())
}
func (v *view) focus(e *Element) {
	if e != nil && !v.available(e) {
		e = nil
	}
	if v.focused == e {
		return
	}
	old := v.focused
	v.focused = e
	if old != nil {
		v.invalidate(old, false)
	}
	if e != nil {
		v.invalidate(e, false)
		for parent := e.parent; parent != nil; parent = parent.parent {
			if !parent.scrollable {
				continue
			}
			top := parent.rect.Y + parent.style.padding[0]
			bottom := parent.rect.Y + parent.rect.H - parent.style.padding[2]
			previous := parent.scroll
			if e.rect.Y < top {
				parent.scroll = max(0, parent.scroll+e.rect.Y-top)
			} else if e.rect.Y+e.rect.H > bottom {
				parent.scroll = min(parent.maxScroll(), parent.scroll+e.rect.Y+e.rect.H-bottom)
			}
			if parent.scroll != previous {
				v.invalidate(parent, true)
			}
		}
	}
	v.updateCaret()
}

func (v *view) available(e *Element) bool {
	if v.doc.live != v || e == nil {
		return false
	}
	var root *Element
	for p := e; p != nil; p = p.parent {
		if p.hidden || p.disabled || p.view != v {
			return false
		}
		root = p
	}
	if v.dialog != nil {
		return root == v.dialog
	}
	return root == v.root
}
func (v *view) focusNext(back bool) {
	var list []*Element
	var walk func(*Element)
	walk = func(e *Element) {
		if e.hidden || e.disabled {
			return
		}
		if e.interactive() {
			list = append(list, e)
		}
		for _, c := range e.children {
			walk(c)
		}
	}
	r := v.root
	if v.dialog != nil {
		r = v.dialog
	}
	walk(r)
	if len(list) == 0 {
		return
	}
	i := -1
	for j, e := range list {
		if e == v.focused {
			i = j
			break
		}
	}
	if back {
		if i < 0 {
			i = 0
		}
		i = (i - 1 + len(list)) % len(list)
	} else {
		i = (i + 1) % len(list)
	}
	v.focus(list[i])
}
func (v *view) activate(e *Element, x, y float32) {
	switch e.kind {
	case kCheck, kSwitch, kRadio:
		if e.kind == kRadio {
			e.SetChecked(true)
		} else {
			e.SetChecked(!e.checked)
		}
		if e.onToggle != nil {
			e.onToggle(e.checked)
		}
	case kSelect:
		v.openPopup(e, e.options, e.selected, func(i int) {
			e.SetSelected(i)
			if e.onSelect != nil {
				e.onSelect(i)
			}
		})
	case kList, kTree, kTable:
		if e.kind == kTable && y < e.rect.Y+e.headerHeight() {
			v.sortTable(e, x)
			break
		}
		i := int((y - e.rect.Y - e.headerHeight() + e.scroll) / e.rowHeight())
		if i < 0 || i >= e.rowCount() {
			break
		}
		e.SetSelected(i)
		if e.kind == kTree {
			n := e.visibleTree[i].node
			if len(n.Children) > 0 {
				e.own().expanded[n] = !e.expanded[n]
				e.rebuildTree()
				e.scroll = min(e.scroll, e.maxScroll())
			}
		}
		if e.onSelect != nil {
			index := i
			if e.kind == kTable && len(e.order) > 0 {
				index = e.order[i]
			}
			e.onSelect(index)
		}
	case kRichText:
		left := e.rect.X
		for _, sp := range e.spans {
			w, _ := v.text.Measure(sp.Text, v.font(e), sp.Bold || e.style.bold)
			if x >= left && x < left+w && sp.Link != "" && e.onLink != nil {
				e.onLink(sp.Link)
				break
			}
			left += w
		}
	}
	if e.onClick != nil {
		e.onClick()
	}
}
func (v *view) navigate(e *Element, key string) {
	if e.kind == kSlider {
		step := e.maximum/100 - e.minimum/100
		value := e.value
		switch key {
		case "ArrowLeft", "ArrowDown":
			value -= step
		case "ArrowRight", "ArrowUp":
			value += step
		case "Home":
			value = e.minimum
		case "End":
			value = e.maximum
		}
		e.SetValue(max(e.minimum, min(e.maximum, value)))
		if e.onValue != nil {
			e.onValue(e.value)
		}
		return
	}
	if e.kind == kSelect {
		i := e.selected
		if key == "ArrowDown" || key == "ArrowRight" {
			i++
		} else if key == "ArrowUp" || key == "ArrowLeft" {
			i--
		} else if key == "Home" {
			i = 0
		} else {
			i = len(e.options) - 1
		}
		if len(e.options) > 0 {
			e.SetSelected(max(0, min(len(e.options)-1, i)))
			if e.onSelect != nil {
				e.onSelect(e.selected)
			}
		}
		return
	}
	if n := e.rowCount(); n > 0 {
		i := e.selected
		switch key {
		case "ArrowDown":
			i++
		case "ArrowUp":
			i--
		case "Home":
			i = 0
		case "End":
			i = n - 1
		case "ArrowRight", "ArrowLeft":
			if e.kind == kTree && i >= 0 && i < n {
				e.own().expanded[e.visibleTree[i].node] = key == "ArrowRight"
				e.rebuildTree()
				e.touch(false)
			}
			return
		}
		i = max(0, min(n-1, i))
		e.SetSelected(i)
		top := float32(i) * e.rowHeight()
		bottom := top + e.rowHeight()
		height := e.rect.H - e.headerHeight()
		if top < e.scroll {
			e.scroll = top
		} else if bottom > e.scroll+height {
			e.scroll = bottom - height
		}
		e.scroll = max(0, min(e.scroll, e.maxScroll()))
		if e.onSelect != nil {
			index := i
			if e.kind == kTable && len(e.order) > 0 {
				index = e.order[i]
			}
			e.onSelect(index)
		}
	}
}
func (v *view) slide(e *Element, x float32) {
	f := max(0, min(1, (x-e.rect.X-8)/max(1, e.rect.W-16)))
	old := e.value
	e.SetValue(e.minimum*(1-float64(f)) + e.maximum*float64(f))
	if old != e.value && e.onValue != nil {
		e.onValue(e.value)
	}
}
func (v *view) sortTable(e *Element, x float32) {
	col := 0
	left := e.rect.X
	for i := range e.columns {
		if x < left+v.columnWidth(e, i) {
			col = i
			break
		}
		left += v.columnWidth(e, i)
	}
	if len(e.order) != len(e.rows) {
		e.own().order = make([]int, len(e.rows))
		for i := range e.order {
			e.own().order[i] = i
		}
	}
	value := func(i int) string {
		if col < len(e.rows[i]) {
			return e.rows[i][col]
		}
		return ""
	}
	ascending := true
	if len(e.order) > 1 {
		ascending = value(e.order[0]) > value(e.order[len(e.order)-1])
	}
	sort.SliceStable(e.order, func(i, j int) bool {
		a, b := value(e.order[i]), value(e.order[j])
		if ascending {
			return a < b
		}
		return a > b
	})
	e.own().selected = -1
	e.touch(false)
}
func (v *view) inputInner(e *Element) Rect {
	r := e.rect
	inner := Rect{r.X + 12, r.Y + 10, r.W - 24, r.H - 20}
	if e.kind == kEditor {
		inner.X += 34
		inner.W -= 34
	}
	return inner
}
func (v *view) textPosition(e *Element, x, y float32) int {
	inner := v.inputInner(e)
	font := v.font(e)
	row := max(0, int((y-inner.Y+e.scroll)/v.text.LineHeight(font, false)))
	start, offset := 0, 0
	for row > 0 {
		at := strings.IndexByte(e.text[start:], '\n')
		if at < 0 {
			return utf8.RuneCountInString(e.text)
		}
		offset += utf8.RuneCountInString(e.text[start:start+at]) + 1
		start += at + 1
		row--
	}
	end := strings.IndexByte(e.text[start:], '\n')
	if end < 0 {
		end = len(e.text) - start
	}
	line := e.text[start : start+end]
	if e.password {
		line = strings.Repeat("•", utf8.RuneCountInString(line))
	}
	return offset + v.text.Hit(line, font, false, x-inner.X+e.scrollX)
}

func runeByteOffset(s string, n int) int {
	for at := range s {
		if n <= 0 {
			return at
		}
		n--
	}
	return len(s)
}

func (v *view) caretPosition(e *Element, inner Rect) (float32, float32) {
	prefix := e.text[:runeByteOffset(e.text, e.cursor)]
	row := strings.Count(prefix, "\n")
	prefix = prefix[strings.LastIndexByte(prefix, '\n')+1:]
	if e.password {
		prefix = strings.Repeat("•", utf8.RuneCountInString(prefix))
	}
	w, _ := v.text.Measure(prefix, v.font(e), false)
	return inner.X + w - e.scrollX, inner.Y + float32(row)*v.text.LineHeight(v.font(e), false) - e.scroll
}

func (v *view) updateCaret() {
	e := v.focused
	if e == nil || (e.kind != kInput && e.kind != kEditor) {
		v.host.TextInput(false, image.Rectangle{})
		return
	}
	x, y := v.caretPosition(e, v.inputInner(e))
	inner := v.inputInner(e)
	if x > inner.X+inner.W-2 {
		e.own().scrollX += x - (inner.X + inner.W - 2)
	} else if x < inner.X {
		e.own().scrollX = max(0, e.scrollX-(inner.X-x))
	}
	x, y = v.caretPosition(e, inner)
	r := Rect{x, y, 2, v.text.LineHeight(v.font(e), false)}
	v.host.TextInput(true, r.pixels(v.scale))
}
func (v *view) replaceSelection(e *Element, text string) {
	if text == "" && e.cursor == e.anchor {
		return
	}
	if len(text) > 4<<20 {
		return
	}
	a, b := min(e.cursor, e.anchor), max(e.cursor, e.anchor)
	// Text input is bounded at its external clipboard/IME boundary.
	if utf8.RuneCountInString(e.text)-(b-a)+utf8.RuneCountInString(text) > 1<<20 {
		return
	}
	rs := []rune(e.text)
	insert := []rune(text)
	e.own().undo = append(e.undo, e.text)
	e.own().undoBytes += len(e.text)
	for len(e.undo) > 64 || e.undoBytes > 4<<20 {
		e.own().undoBytes -= len(e.undo[0])
		e.own().undo[0] = ""
		e.own().undo = e.undo[1:]
	}
	next := make([]rune, 0, len(rs)-(b-a)+len(insert))
	next = append(next, rs[:a]...)
	next = append(next, insert...)
	next = append(next, rs[b:]...)
	e.text = string(next)
	e.own().cursor = a + len(insert)
	e.own().anchor = e.cursor
	v.edited(e)
}
func (v *view) edited(e *Element) {
	e.scroll = min(e.scroll, e.maxScroll())
	if e.multiline {
		_, y := v.caretPosition(e, v.inputInner(e))
		inner := v.inputInner(e)
		if y < inner.Y {
			e.scroll = max(0, e.scroll-(inner.Y-y))
		} else if y+v.text.LineHeight(v.font(e), false) > inner.Y+inner.H {
			e.scroll = min(e.maxScroll(), e.scroll+y+v.text.LineHeight(v.font(e), false)-inner.Y-inner.H)
		}
	}
	e.touch(false)
	v.updateCaret()
	if e.onChange != nil {
		e.onChange(e.text)
	}
}
func (v *view) editKey(e *Element, ev fego.Event) {
	rs := []rune(e.text)
	command := ev.Control || ev.Super
	if command {
		switch strings.ToLower(ev.Key) {
		case "a":
			e.own().anchor = 0
			e.own().cursor = len(rs)
			e.touch(false)
			return
		case "c", "x":
			if e.password {
				return
			}
			a, b := min(e.cursor, e.anchor), max(e.cursor, e.anchor)
			v.host.SetClipboard(string(rs[a:b]))
			if strings.ToLower(ev.Key) == "x" {
				v.replaceSelection(e, "")
			}
			return
		case "v":
			s := v.host.Clipboard()
			if s == "" || len(s) > 4<<20 || v.focused != e || !v.available(e) {
				return
			}
			if !e.multiline {
				s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
			}
			v.replaceSelection(e, s)
			return
		case "z":
			if len(e.undo) > 0 {
				e.text = e.undo[len(e.undo)-1]
				e.own().undoBytes -= len(e.text)
				e.own().undo[len(e.undo)-1] = ""
				e.own().undo = e.undo[:len(e.undo)-1]
				e.own().cursor = len([]rune(e.text))
				e.own().anchor = e.cursor
				v.edited(e)
			}
			return
		}
	}
	old := e.cursor
	switch ev.Key {
	case "Backspace":
		if e.cursor == e.anchor && e.cursor > 0 {
			e.own().anchor = e.cursor - 1
		}
		v.replaceSelection(e, "")
		return
	case "Delete":
		if e.cursor == e.anchor && e.cursor < len(rs) {
			e.own().anchor = e.cursor + 1
		}
		v.replaceSelection(e, "")
		return
	case "Enter":
		if e.multiline {
			v.replaceSelection(e, "\n")
		} else if e.onClick != nil {
			e.onClick()
		}
		return
	case "ArrowLeft":
		e.own().cursor = max(0, e.cursor-1)
		if command {
			for e.cursor > 0 && e.cursor < len(rs) && unicode.IsSpace(rs[e.cursor]) {
				e.own().cursor--
			}
			for e.cursor > 0 && !unicode.IsSpace(rs[e.cursor-1]) {
				e.own().cursor--
			}
		}
	case "ArrowRight":
		e.own().cursor = min(len(rs), e.cursor+1)
		if command {
			for e.cursor < len(rs) && !unicode.IsSpace(rs[e.cursor]) {
				e.own().cursor++
			}
		}
	case "Home":
		if command {
			e.own().cursor = 0
		} else {
			for e.cursor > 0 && rs[e.cursor-1] != '\n' {
				e.own().cursor--
			}
		}
	case "End":
		if command {
			e.own().cursor = len(rs)
		} else {
			for e.cursor < len(rs) && rs[e.cursor] != '\n' {
				e.own().cursor++
			}
		}
	case "ArrowUp", "ArrowDown":
		x, y := v.caretPosition(e, v.inputInner(e))
		dy := v.text.LineHeight(v.font(e), false)
		if ev.Key == "ArrowUp" {
			dy = -dy
		}
		e.own().cursor = v.textPosition(e, x, y+dy+v.font(e)*.3)
	default:
		return
	}
	if !ev.Shift {
		e.own().anchor = e.cursor
	}
	if old != e.cursor {
		e.touch(false)
		v.updateCaret()
	}
}

func (v *view) openPopup(owner *Element, options []string, selected int, choose func(int)) {
	if len(options) == 0 {
		return
	}
	r := owner.rect
	h := min(float32(len(options))*34+8, float32(360))
	w := max(r.W, 160)
	screenW, screenH, _ := v.host.Size()
	w = min(w, max(1, float32(screenW)/v.scale-16))
	h = min(h, max(1, float32(screenH)/v.scale-16))
	r = Rect{min(r.X, float32(screenW)/v.scale-w-8), r.Y + r.H + 6, w, h}
	if r.Y+r.H > float32(screenH)/v.scale {
		r.Y = owner.rect.Y - h - 6
	}
	r.X = max(8, r.X)
	r.Y = max(8, r.Y)
	v.popup = &popup{owner: owner, options: options, selected: max(0, selected), choose: choose, rect: r}
	v.popup.reveal()
	v.invalidate(v.root, false)
}
func (v *view) popupInput(ev fego.Event, x, y float32) bool {
	p := v.popup
	close := func() { v.popup = nil; v.invalidate(v.root, false) }
	choose := func() { i := p.selected; close(); p.choose(i) }
	switch ev.Kind {
	case fego.KeyDown:
		switch ev.Key {
		case "Escape", "Tab":
			close()
		case "ArrowDown":
			p.selected = (p.selected + 1) % len(p.options)
			p.reveal()
			v.invalidate(v.root, false)
		case "ArrowUp":
			p.selected = (p.selected - 1 + len(p.options)) % len(p.options)
			p.reveal()
			v.invalidate(v.root, false)
		case "Enter", "Space":
			choose()
		}
		return true
	case fego.PointerDown:
		if !p.rect.contains(x, y) {
			close()
		}
		return true
	case fego.PointerMove:
		if p.rect.contains(x, y) {
			p.selected = max(0, min(len(p.options)-1, p.first+int((y-p.rect.Y-4)/34)))
			v.invalidate(v.root, false)
		}
		return true
	case fego.PointerUp:
		if p.rect.contains(x, y) {
			p.selected = max(0, min(len(p.options)-1, p.first+int((y-p.rect.Y-4)/34)))
			choose()
		}
		return true
	case fego.Scroll:
		rows := max(1, int((p.rect.H-8)/34))
		step := int(ev.DY / 34)
		if step == 0 && ev.DY != 0 {
			if ev.DY > 0 {
				step = 1
			} else {
				step = -1
			}
		}
		p.first = max(0, min(max(0, len(p.options)-rows), p.first+step))
		v.invalidate(v.root, false)
		return true
	case fego.Text:
		return true
	}
	return false
}
func (v *view) paintPopup(p *Painter) {
	menu := v.popup
	t := v.theme
	r := menu.rect
	p.Shadow(r, 10)
	p.RoundRect(r, 10, t.Border)
	p.RoundRect(Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}, 9, t.Surface)
	clip := p.clip
	p.clip = p.clip.Intersect(r.pixels(v.scale))
	defer func() { p.clip = clip }()
	first := menu.first
	for i := first; i < len(menu.options); i++ {
		y := r.Y + 4 + float32(i-first)*34
		if y > r.Y+r.H-4 {
			break
		}
		if i == menu.selected {
			p.RoundRect(Rect{r.X + 5, y, r.W - 10, 32}, 6, mix(t.Surface, t.Accent, .12))
		}
		p.Label(menu.options[i], r.X+14, y+8, v.theme.FontSize, t.Text, false)
	}
}

func (p *popup) reveal() {
	rows := max(1, int((p.rect.H-8)/34))
	if p.selected < p.first {
		p.first = p.selected
	} else if p.selected >= p.first+rows {
		p.first = p.selected - rows + 1
	}
}
