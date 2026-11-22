package ui

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zatxm/fego/v26"
)

type accessTarget struct {
	element  *Element
	row      int
	disabled bool
	popup    bool
}

func (v *view) updateAccessibility() {
	host, ok := v.host.(interface{ SetAccessibility([]fego.AccessNode) })
	if !ok {
		return
	}
	clear(v.access)
	if v.access == nil {
		v.access = make(map[uint64]accessTarget)
	}
	clear(v.accessNodes)
	nodes := v.accessNodes[:0]
	var walk func(*Element, uint64, bool)
	walk = func(e *Element, parent uint64, disabled bool) {
		if e.hidden || e.clip.W <= 0 || e.clip.H <= 0 {
			return
		}
		disabled = disabled || e.disabled
		role := "group"
		switch e.kind {
		case kText, kRichText:
			role = "text"
		case kButton, kSelect:
			role = "button"
		case kInput, kEditor:
			role = "input"
		case kCheck:
			role = "checkbox"
		case kSwitch:
			role = "switch"
		case kRadio:
			role = "radio"
		case kSlider:
			role = "slider"
		case kProgress:
			role = "progress"
		case kList, kVirtualList:
			role = "list"
		case kVirtualRow:
			role = "row"
		case kTable:
			role = "table"
		case kTree:
			role = "tree"
		case kImage, kCanvas:
			role = "image"
		case kSeparator:
			return
		}
		label := e.label
		value := ""
		if label == "" && e.kind != kInput && e.kind != kEditor {
			label = e.text
		}
		switch e.kind {
		case kInput, kEditor:
			value = e.text
			if e.password {
				value = strings.Repeat("•", utf8.RuneCountInString(value))
			}
			if label == "" {
				label = e.placeholder
			}
		case kSelect:
			if e.selected >= 0 && e.selected < len(e.options) {
				value = e.options[e.selected]
			}
		}
		node := fego.AccessNode{ID: e.id, Parent: parent, Role: role, Label: label, Value: value, Bounds: e.clip.pixels(v.scale), Disabled: disabled, Focused: v.focused == e, Checked: e.checked, Min: e.minimum, Max: e.maximum, Number: e.value, Caret: e.cursor, SelectionStart: min(e.cursor, e.anchor), SelectionEnd: max(e.cursor, e.anchor)}
		if e.kind == kVirtualRow {
			l := e.parent.virtual
			_, node.Selected = l.chosen[l.key(e.selected)]
			node.Focused = node.Focused || v.focused == l.Element && l.lead == e.selected
		}
		nodes = append(nodes, node)
		v.access[e.id] = accessTarget{element: e, row: -1, disabled: disabled}
		if e.kind == kSlider && e.interval != nil {
			nodes[len(nodes)-1].Label = strings.TrimSpace(node.Label + " lower value")
			nodes[len(nodes)-1].Max = e.interval.high
			node.ID++
			node.Label = strings.TrimSpace(node.Label + " upper value")
			node.Min, node.Number = e.value, e.interval.high
			node.Focused = v.focused == e && e.interval.activeHigh
			if node.Focused {
				nodes[len(nodes)-1].Focused = false
			}
			nodes = append(nodes, node)
			v.access[node.ID] = accessTarget{element: e, row: 1, disabled: disabled}
		}
		for _, child := range e.children {
			walk(child, e.id, disabled)
		}
		if e.rowCount() > 0 {
			first := max(0, int(e.scroll/e.rowHeight()))
			last := min(e.rowCount(), int((e.scroll+e.rect.H-e.headerHeight())/e.rowHeight())+1)
			for row := first; row < last; row++ {
				text := ""
				role := "row"
				switch e.kind {
				case kList:
					text = e.listText(row)
				case kTree:
					text = e.visibleTree[row].node.Label
					role = "treeitem"
				case kTable:
					index := row
					if len(e.order) > 0 {
						index = e.order[row]
					}
					text = e.listText(index)
				}
				r := Rect{e.rect.X, e.rect.Y + e.headerHeight() + float32(row)*e.rowHeight() - e.scroll, e.rect.W, e.rowHeight()}.intersect(e.clip)
				id := e.id + uint64(row) + 1
				nodes = append(nodes, fego.AccessNode{ID: id, Parent: e.id, Role: role, Label: text, Bounds: r.pixels(v.scale), Disabled: disabled, Selected: e.selected == row, Focused: v.focused == e && e.selected == row})
				v.access[id] = accessTarget{element: e, row: row, disabled: disabled}
			}
		}
	}
	root := v.root
	if v.dialog != nil {
		root = v.dialog
	}
	walk(root, 0, false)
	if p := v.popup; p != nil {
		for i := p.first; i < len(p.options) && float32(i-p.first)*34 < p.rect.H-8; i++ {
			id := p.owner.id + (1 << 31) + uint64(i)
			r := Rect{p.rect.X, p.rect.Y + 4 + float32(i-p.first)*34, p.rect.W, 34}.intersect(p.rect)
			nodes = append(nodes, fego.AccessNode{ID: id, Parent: p.owner.id, Role: "button", Label: p.options[i], Bounds: r.pixels(v.scale), Focused: p.selected == i, Selected: p.selected == i})
			v.access[id] = accessTarget{element: p.owner, row: i, popup: true}
		}
	}
	host.SetAccessibility(nodes)
	v.accessNodes = nodes
}

func (v *view) accessAction(ev fego.Event) {
	target, ok := v.access[ev.AccessID]
	if !ok || target.disabled {
		return
	}
	e := target.element
	for p := e; p != nil; p = p.parent {
		if p.hidden || p.disabled || p.view != v {
			return
		}
	}
	if v.dialog != nil {
		p := e
		for p.parent != nil {
			p = p.parent
		}
		if p != v.dialog {
			return
		}
	}
	if target.popup {
		p := v.popup
		if p == nil || p.owner != e || target.row < 0 || target.row >= len(p.options) {
			return
		}
		switch ev.Action {
		case "focus":
			p.selected = target.row
			p.reveal()
			v.invalidate(v.root, false)
		case "press", "select":
			v.popup = nil
			v.invalidate(v.root, false)
			p.choose(target.row)
		}
		return
	}
	if e.kind == kVirtualRow && (ev.Action == "press" || ev.Action == "select") {
		v.focus(e.parent)
		e.parent.virtual.choose(e.selected, false, false)
		return
	}
	switch ev.Action {
	case "focus":
		if e.kind == kSlider && e.interval != nil {
			e.interval.activeHigh = target.row == 1
		}
		if e.interactive() {
			v.focus(e)
		}
	case "press":
		if !e.interactive() {
			return
		}
		v.focus(e)
		y := e.rect.Y + e.rect.H/2
		if target.row >= 0 {
			y = e.rect.Y + e.headerHeight() + (float32(target.row)+.5)*e.rowHeight() - e.scroll
		}
		v.activate(e, e.rect.X+e.rect.W/2, y)
	case "select":
		row := target.row
		if row < 0 {
			var err error
			row, err = strconv.Atoi(ev.Text)
			if err != nil {
				return
			}
		}
		n := e.rowCount()
		if e.kind == kSelect {
			n = len(e.options)
		}
		if row < 0 || row >= n {
			return
		}
		e.SetSelected(row)
		v.focus(e)
		if e.rowCount() > 0 {
			e.scroll = max(0, min(e.maxScroll(), float32(row)*e.rowHeight()))
		}
		if e.onSelect != nil {
			if e.kind == kTable && len(e.order) > 0 {
				row = e.order[row]
			}
			e.onSelect(row)
		}
	case "setValue":
		switch e.kind {
		case kInput, kEditor:
			if len(ev.Text) > 4<<20 || utf8.RuneCountInString(ev.Text) > 1<<20 {
				return
			}
			value := ev.Text
			if !e.multiline {
				value = strings.ReplaceAll(strings.ReplaceAll(value, "\n", ""), "\r", "")
			}
			e.own().anchor = 0
			e.own().cursor = utf8.RuneCountInString(e.text)
			v.replaceSelection(e, value)
		case kSlider:
			n, err := strconv.ParseFloat(ev.Text, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return
			}
			if r := e.interval; r != nil {
				r.move(n, target.row == 1)
				return
			}
			e.SetValue(n)
			if e.onValue != nil {
				e.onValue(e.value)
			}
		}
	case "setCaret", "setSelection":
		if e.kind != kInput && e.kind != kEditor {
			return
		}
		start, end := ev.Text, ev.Text
		if ev.Action == "setSelection" {
			var found bool
			start, end, found = strings.Cut(ev.Text, ":")
			if !found {
				return
			}
		}
		a, errA := strconv.Atoi(start)
		b, errB := strconv.Atoi(end)
		if errA != nil || errB != nil || a < 0 || b < 0 || a > utf8.RuneCountInString(e.text) || b > utf8.RuneCountInString(e.text) {
			return
		}
		e.own().anchor, e.own().cursor = a, b
		v.focus(e)
		e.touch(false)
		v.updateCaret()
	}
}
