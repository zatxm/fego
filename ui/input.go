package ui

import (
	"image"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zatxm/fego/v26"
)

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
		v.hoveredRow = -1
		v.host.SetCursor("default")
	}
	if inside(v.tip.target) {
		v.dismissTip()
		v.tip.target = nil
	}
	if v.popup != nil && inside(v.popup.owner) {
		v.closePopup()
	}
	if v.popover.owner != nil && inside(v.popover.owner) {
		v.closePopover()
	}
}

func (e *Element) interactive() bool {
	switch e.kind {
	case kButton, kInput, kEditor, kCheck, kRadio, kSwitch, kSlider, kSelect, kList, kTable, kTree, kVirtualList:
		return true
	case kVirtualRow:
		return e.parent != nil && !e.parent.virtual.header(int(e.rowIndex))
	}
	return e.onClick != nil || e.onLink != nil || e.onPoint != nil
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
	if e.scrollable || e.kind == kList || e.kind == kTable || e.kind == kTree || e.kind == kVirtualList || e.multiline {
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
	switch ev.Kind {
	case fego.PointerDown, fego.Scroll, fego.KeyDown:
		// Acting on the element replaces the hint until the pointer moves on.
		v.dismissTip()
	}
	if ev.Kind == fego.PointerDown && ev.Button == 1 {
		v.setFocusVisible(false)
	} else if ev.Kind == fego.KeyDown {
		v.setFocusVisible(true)
	}
	if v.popup != nil && v.popupInput(ev, x, y) {
		return
	}
	root = v.popoverRoot(root, ev, x, y)
	if v.dialog == nil {
		if action := v.toastButton(); action != nil && action.clip.contains(x, y) {
			root = action
		}
	}
	if v.dialogPlacement == bottomPanel && v.dialog != nil && v.dragDrawer(ev, x, y) {
		return
	}
	switch ev.Kind {
	case fego.PointerMove:
		if v.scrollDrag != nil {
			v.dragScroll(y)
			return
		}
		if v.popup == nil {
			v.trackTip(root, x, y)
		}
		e := hit(root, x, y)
		if e != v.hover {
			old := v.hover
			v.hover = e
			v.hoveredRow = -1
			if old != nil {
				if old.onHover != nil {
					old.onHover(-1, -1)
				}
				v.repaint(old, false)
			}
			if e != nil {
				v.repaint(e, false)
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
		if e != nil {
			if e.onHover != nil {
				e.onHover(x-e.rect.X, y-e.rect.Y)
			}
			if e.kind == kTable || e.style.tone == commandSurface || e.style.tone == suggestionSurface {
				next := -1
				bodyY := e.rect.Y + e.headerHeight()
				if y >= bodyY {
					i := int((y - bodyY + e.scroll) / e.rowHeight())
					if i < e.rowCount() {
						next = i
					}
				}
				v.updateHoveredRow(e, next)
			}
		}
		if e := v.pressed; e != nil {
			if e.kind == kSlider {
				v.slide(e, x)
			} else if e.kind == kInput || e.kind == kEditor {
				if cursor := v.textPosition(e, x, y); cursor != e.cursor {
					e.own().cursor = cursor
					v.invalidate(e, false)
				}
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
		if e != nil && e.kind == kVirtualRow {
			e.parent.virtual.dragY = y
			v.focus(e.parent)
		} else {
			v.focus(e)
		}
		if e != nil {
			if e.kind == kEditor {
				state := e.sourceIndex()
				if ev.Alt {
					at := v.textPosition(e, x, y)
					state.extra = append(state.extra, Selection{at, at})
					v.pressed = nil
					v.invalidate(e, false)
					return
				}
				state.extra = state.extra[:0]
				if x >= e.rect.X+32 && x < v.inputInner(e).X {
					position := v.textPosition(e, v.inputInner(e).X+e.scrollX, y)
					line, _ := state.locate(e.text, position)
					if e.toggleGutter(line) {
						v.pressed = nil
						return
					}
				}
			}
			if e.kind == kInput || e.kind == kEditor {
				e.own().cursor = v.textPosition(e, x, y)
				if !ev.Shift {
					e.own().anchor = e.cursor
				}
			}
			if e.kind == kSlider {
				if r := e.interval; r != nil {
					p := (x - e.rect.X - 8) / max(1, e.rect.W-16)
					r.activeHigh = p > (sliderFraction(e, r.value)+sliderFraction(e, r.high))/2
				}
				v.slide(e, x)
			}
			v.invalidate(e, false)
		}
	case fego.PointerCancel:
		v.scrollDrag = nil
		if e := v.pressed; e != nil {
			v.pressed = nil
			v.repaint(e, false)
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
			v.repaint(e, false)
			if e.kind == kVirtualRow && e.parent != nil {
				l := e.parent.virtual
				if l.options.Reorder != nil && l.clip.contains(x, y) &&
					(y-l.dragY > 5 || l.dragY-y > 5) {
					pos := l.scroll + y - l.rect.Y
					before := l.index(pos)
					if before < l.options.Count && float64(pos) > (l.top(before)+l.top(before+1))/2 {
						before++
					}
					l.drop(int(e.rowIndex), before)
				} else if e == hit(root, x, y) {
					l.click(int(e.rowIndex), ev)
				}
				break
			}
			if e == hit(root, x, y) {
				v.activate(e, x, y)
			}
		}
	case fego.Scroll:
		e := scrollHit(root, x, y)
		for e != nil {
			if e.scrollable || e.kind == kList || e.kind == kTable || e.kind == kTree || e.kind == kVirtualList || e.multiline {
				if next := max(0, min(e.maxScroll(), e.scroll+ev.DY/scale)); next != e.scroll {
					v.scrollRows(e, next)
					break
				}
			}
			e = e.parent
		}
	case fego.KeyDown:
		if ev.Key == "Tab" {
			v.focusNext(ev.Shift)
			return
		}
		if ev.Key == "Escape" {
			if v.popover.content != nil {
				trigger := v.popover.trigger
				v.closePopover()
				v.focus(trigger)
			} else if v.dialog != nil {
				v.doc.CloseDialog()
			} else {
				v.clearToasts()
			}
			return
		}
		e := v.focused
		if e == nil || !v.available(e) {
			return
		}
		if e.kind == kInput || e.kind == kEditor {
			if e.onNavigate != nil && (ev.Key == "ArrowUp" || ev.Key == "ArrowDown") {
				e.onNavigate(ev.Key)
			} else {
				v.editKey(e, ev)
			}
			return
		}
		if e.kind == kSlider && e.interval != nil && e.interval.key(ev) {
			return
		}
		if e.kind == kVirtualList || e.kind == kVirtualRow {
			if e.kind == kVirtualRow {
				e = e.parent
			}
			e.virtual.keyDown(ev)
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
	case fego.Text, fego.TextEdit, fego.TextSelection:
		e := v.focused
		if e != nil && v.available(e) && (e.kind == kInput || e.kind == kEditor) && len(ev.Text) <= 4<<20 {
			if ev.Kind != fego.Text {
				n := utf8.RuneCountInString(e.text)
				e.own().anchor = min(n, max(0, ev.Start))
				e.own().cursor = min(n, max(0, ev.End))
				if ev.Kind == fego.TextSelection {
					v.invalidate(e, false)
					return
				}
			}
			s := ev.Text
			if !e.multiline {
				s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
			}
			v.replaceSelection(e, s)
		}
	}
}

func (v *view) dragDrawer(ev fego.Event, x, y float32) bool {
	panel := v.dialog
	base := float32(v.size.Dy())/v.scale - panel.rect.H
	move := func(top float32) {
		if panel.rect.Y == top {
			return
		}
		v.invalidate(panel, false)
		v.moveLayout(panel, top, Rect{W: float32(v.size.Dx()) / v.scale, H: float32(v.size.Dy()) / v.scale})
		v.invalidate(panel, false)
	}
	if !v.drawerDragging {
		if ev.Kind == fego.PointerDown && ev.Button == 1 &&
			(Rect{panel.rect.X + (panel.rect.W-112)/2, panel.rect.Y, 112, 32}).contains(x, y) {
			v.drawerDragging = true
			v.drawerOrigin = y
			return true
		}
		return false
	}
	switch ev.Kind {
	case fego.PointerMove:
		move(base + max(0, y-v.drawerOrigin))
	case fego.PointerUp:
		v.drawerDragging = false
		if y-v.drawerOrigin >= 48 {
			v.doc.CloseDialog()
		} else {
			move(base)
		}
	case fego.PointerCancel:
		v.drawerDragging = false
		move(base)
	default:
		return false
	}
	return true
}

func (v *view) updateHoveredRow(e *Element, next int) {
	if next == v.hoveredRow {
		return
	}
	for _, i := range [...]int{v.hoveredRow, next} {
		if i >= 0 {
			y := e.rect.Y + e.headerHeight() + float32(i)*e.rowHeight() - e.scroll
			v.damage(Rect{e.rect.X, y, e.rect.W, e.rowHeight()}.intersect(e.clip).pixels(v.scale))
		}
	}
	v.hoveredRow = next
	v.scrolling = nil
	v.host.Invalidate()
}

func scrollbar(e *Element) (track, thumb Rect) {
	if !e.scrollable && e.kind != kVirtualList && e.rowCount() == 0 {
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
	if p := v.popover; p.content != nil && e != nil && !within(e, p.content) && !within(e, p.owner) {
		v.closePopover()
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

func (v *view) setFocusVisible(visible bool) {
	if v.focusVisible != visible {
		v.focusVisible = visible
		if v.focused != nil {
			v.repaint(v.focused, false)
		}
	}
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
	if root == v.popover.content {
		return v.available(v.popover.owner)
	}
	if root == v.toastButton() {
		return v.dialog == nil
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
		if e.hidden || e.disabled || (e.kind == kVirtualRow && (e.clip.W <= 0 || e.clip.H <= 0)) {
			return
		}
		if e.interactive() {
			list = append(list, e)
		}
		for _, c := range e.children {
			walk(c)
		}
		if e == v.popover.owner {
			walk(v.popover.content)
		}
	}
	r := v.root
	if v.dialog != nil {
		r = v.dialog
	}
	walk(r)
	if action := v.toastButton(); action != nil && v.dialog == nil {
		walk(action)
	}
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
	if e.onPoint != nil {
		e.onPoint(x-e.rect.X, y-e.rect.Y)
	}
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
		v.openPopup(e, nil)
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
	if e.onNavigate != nil {
		e.onNavigate(key)
		return
	}
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
		e.revealRow(i)
		if e.onSelect != nil {
			index := i
			if e.kind == kTable && len(e.order) > 0 {
				index = e.order[i]
			}
			e.onSelect(index)
		}
	}
}
func (e *Element) revealRow(i int) {
	top := float32(i) * e.rowHeight()
	bottom := top + e.rowHeight()
	height := e.rect.H - e.headerHeight()
	if top < e.scroll {
		e.scroll = top
	} else if bottom > e.scroll+height {
		e.scroll = bottom - height
	}
	e.scroll = max(0, min(e.scroll, e.maxScroll()))
}

func (v *view) slide(e *Element, x float32) {
	f := max(0, min(1, (x-e.rect.X-8)/max(1, e.rect.W-16)))
	old := e.value
	value := e.minimum*(1-float64(f)) + e.maximum*float64(f)
	if r := e.interval; r != nil {
		r.move(value, r.activeHigh)
		return
	}
	e.SetValue(value)
	if old != e.value && e.onValue != nil {
		e.onValue(e.value)
	}
}
func (v *view) sortTable(e *Element, x float32) {
	col := 0
	left := e.rect.X
	flex := flexibleColumnWidth(e)
	for i, column := range e.columns {
		width := column.Width
		if width <= 0 {
			width = flex
		}
		if x < left+width {
			col = i
			break
		}
		left += width
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
	slices.SortStableFunc(e.order, func(i, j int) int {
		a, b := value(i), value(j)
		if !ascending {
			a, b = b, a
		}
		return strings.Compare(a, b)
	})
	e.own().selected = -1
	e.touch(false)
}
func (v *view) inputInner(e *Element) Rect {
	r := e.rect
	inner := Rect{r.X + 12, r.Y + 10, r.W - 24, r.H - 20}
	if !e.multiline {
		line := v.text.LineHeight(v.font(e), false)
		inner.Y = r.Y + (r.H-line)/2
		inner.H = line
	}
	if e.kind == kEditor {
		inner.X += 40
		inner.W -= 40
	}
	return inner
}
func (v *view) textPosition(e *Element, x, y float32) int {
	if e.otpLength > 0 {
		return min(len(e.text), max(0, int((x-e.rect.X)/max(1, e.otpSlot(0).W))))
	}
	inner := v.inputInner(e)
	font := v.font(e)
	row := max(0, int((y-inner.Y+e.scroll)/v.text.LineHeight(font, false)))
	if e.kind == kEditor {
		s := e.sourceIndex()
		if row >= s.visibleCount() {
			return s.runes
		}
		line := s.source(row)
		head := s.head(e.text, line)
		return head.rune + v.text.Hit(e.text[head.pos:lineEnd(e.text, head.pos)], font, false, x-inner.X+e.scrollX)
	}
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
	if e.otpLength > 0 {
		slot := e.otpSlot(min(e.cursor, e.otpLength-1))
		return slot.X + slot.W/2, inner.Y
	}
	if e.kind == kEditor {
		s := e.sourceIndex()
		i, line := s.locate(e.text, e.cursor)
		part := e.text[line.pos : line.pos+runeByteOffset(e.text[line.pos:], e.cursor-line.rune)]
		w, _ := v.text.Measure(part, v.font(e), false)
		return inner.X + w - e.scrollX, inner.Y + float32(s.visual(i))*v.text.LineHeight(v.font(e), false) - e.scroll
	}
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
	inner := v.inputInner(e)
	x, y := v.caretPosition(e, inner)
	previous := e.scrollX
	if x > inner.X+inner.W-2 {
		e.own().scrollX += x - (inner.X + inner.W - 2)
	} else if x < inner.X {
		e.own().scrollX = max(0, e.scrollX-(inner.X-x))
	}
	if e.scrollX != previous {
		x, y = v.caretPosition(e, inner)
	}
	r := Rect{x, y, 2, v.text.LineHeight(v.font(e), false)}
	v.host.TextInput(true, r.pixels(v.scale))
	if h, ok := v.host.(interface {
		SetTextInputState(string, int, int, bool, bool)
	}); ok {
		h.SetTextInputState(e.text, e.anchor, e.cursor, e.password, e.otpLength > 0)
	}
}

type textUndo struct {
	removed         string
	start, inserted int
}

func (e *Element) remember(start, end, inserted int) {
	const maxUndoBytes = 4 << 20
	state := e.own()
	if end-start > maxUndoBytes {
		state.undo, state.undoBytes = nil, 0
		return
	}
	removed := e.text[start:end]
	if start != 0 || end != len(e.text) {
		// A substring must not keep the old document's backing string alive.
		removed = strings.Clone(removed)
	}
	state.undoBytes += len(removed)
	state.undo = append(state.undo, textUndo{removed, start, inserted})
	for len(state.undo) > 64 || state.undoBytes > maxUndoBytes {
		state.undoBytes -= len(state.undo[0].removed)
		state.undo[0] = textUndo{}
		state.undo = state.undo[1:]
	}
}

func (v *view) replaceSelection(e *Element, text string) {
	if len(text) > 4<<20 || v.editCarets(e, text, "") || (text == "" && e.cursor == e.anchor) {
		return
	}
	a, b := min(e.cursor, e.anchor), max(e.cursor, e.anchor)
	// Text input is bounded at its external clipboard/IME boundary.
	length := 0
	var state *editorState
	if e.kind == kEditor {
		state = e.sourceIndex()
		length = state.runes
	} else {
		length = utf8.RuneCountInString(e.text)
	}
	newRunes := utf8.RuneCountInString(text)
	if length-(b-a)+newRunes > 1<<20 {
		return
	}
	if e.otpLength > 0 {
		original := text
		text = otpDigits(text, e.otpLength-(len(e.text)-(b-a)))
		if text == "" && original != "" {
			return
		}
	}
	var start, end int
	if state != nil {
		start, end = state.byteAt(e.text, a), state.byteAt(e.text, b)
	} else {
		start, end = runeByteOffset(e.text, a), runeByteOffset(e.text, b)
	}
	e.remember(start, end, len(text))
	if state != nil {
		state.splice(e.text, a, b, start, end, text, newRunes)
	}
	e.text = e.text[:start] + text + e.text[end:]
	e.own().cursor = a + utf8.RuneCountInString(text)
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
	e.touchInputText()
	v.updateCaret()
	if e.onChange != nil {
		e.onChange(e.text)
	}
}
func (v *view) editKey(e *Element, ev fego.Event) {
	if e.kind == kEditor && v.sourceKey(e, ev) {
		return
	}
	var rs []rune
	command := ev.Control || ev.Super
	var index *editorState
	length := 0
	if e.kind == kEditor {
		index = e.sourceIndex()
		length = index.runes
		if command && (ev.Key == "ArrowLeft" || ev.Key == "ArrowRight") {
			rs = []rune(e.text)
		}
	} else {
		rs = []rune(e.text)
		length = len(rs)
	}
	if command {
		switch strings.ToLower(ev.Key) {
		case "a":
			e.own().anchor = 0
			e.own().cursor = length
			e.touch(false)
			return
		case "c", "x":
			if e.password {
				return
			}
			a, b := min(e.cursor, e.anchor), max(e.cursor, e.anchor)
			if index != nil {
				v.host.SetClipboard(e.text[index.byteAt(e.text, a):index.byteAt(e.text, b)])
			} else {
				v.host.SetClipboard(string(rs[a:b]))
			}
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
				last := e.undo[len(e.undo)-1]
				e.text = e.text[:last.start] + last.removed + e.text[last.start+last.inserted:]
				e.markEditorEdit()
				if e.editor != nil {
					e.editor.extra, e.editor.folds = nil, nil
				}
				e.own().undoBytes -= len(last.removed)
				e.undo[len(e.undo)-1] = textUndo{}
				e.own().undo = e.undo[:len(e.undo)-1]
				e.own().cursor = utf8.RuneCountInString(e.text)
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
		if e.cursor == e.anchor && e.cursor < length {
			e.own().anchor = e.cursor + 1
		}
		v.replaceSelection(e, "")
		return
	case "Enter":
		if e.multiline {
			v.replaceSelection(e, "\n")
		} else if e.submit != nil {
			e.submit()
		} else if e.onClick != nil {
			e.onClick()
		}
		return
	case "ArrowLeft":
		e.own().cursor = max(0, e.cursor-1)
		if command {
			for e.cursor > 0 && e.cursor < length && unicode.IsSpace(rs[e.cursor]) {
				e.own().cursor--
			}
			for e.cursor > 0 && !unicode.IsSpace(rs[e.cursor-1]) {
				e.own().cursor--
			}
		}
	case "ArrowRight":
		e.own().cursor = min(length, e.cursor+1)
		if command {
			for e.cursor < length && !unicode.IsSpace(rs[e.cursor]) {
				e.own().cursor++
			}
		}
	case "Home":
		if command {
			e.own().cursor = 0
		} else if index != nil {
			_, line := index.locate(e.text, e.cursor)
			e.own().cursor = line.rune
		} else {
			for e.cursor > 0 && rs[e.cursor-1] != '\n' {
				e.own().cursor--
			}
		}
	case "End":
		if command {
			e.own().cursor = length
		} else if index != nil {
			_, line := index.locate(e.text, e.cursor)
			e.own().cursor = line.rune + utf8.RuneCountInString(e.text[line.pos:lineEnd(e.text, line.pos)])
		} else {
			for e.cursor < length && rs[e.cursor] != '\n' {
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
		if index != nil {
			v.revealEditorCaret(e)
		} else {
			e.touch(false)
		}
		v.updateCaret()
	}
}
