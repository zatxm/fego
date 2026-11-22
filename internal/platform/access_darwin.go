//go:build darwin && !ios && (amd64 || arm64)

package platform

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/ebitengine/purego/objc"
)

type macAccessEntry struct {
	window *cocoaWindow
	node   AccessNode
}

var macAccess = struct {
	sync.RWMutex
	objects map[objc.ID]macAccessEntry
}{objects: map[objc.ID]macAccessEntry{}}

func macAccessNode(obj objc.ID) (macAccessEntry, bool) {
	macAccess.RLock()
	entry, ok := macAccess.objects[obj]
	macAccess.RUnlock()
	return entry, ok
}

func (w *cocoaWindow) SetAccessibility(nodes []AccessNode) {
	if w.closed {
		return
	}
	if w.access == nil {
		w.access = make(map[uint64]objc.ID)
	}
	changed := len(nodes) != len(w.accessOrder)
	next := make(map[uint64]bool, len(nodes))
	var focus objc.ID
	var valueChanges, selectionChanges []objc.ID
	for i, n := range nodes {
		if n.ID == 0 || next[n.ID] {
			continue
		}
		next[n.ID] = true
		obj := w.access[n.ID]
		old, exists := macAccessNode(obj)
		if exists && old.node == n && i < len(w.accessOrder) && w.accessOrder[i] == n.ID {
			continue
		}
		changed = true
		if obj == 0 {
			obj = macSend(macSend(objc.ID(mac.accessClass), "alloc"), "init")
			if obj == 0 {
				continue
			}
			w.access[n.ID] = obj
		}
		macAccess.Lock()
		macAccess.objects[obj] = macAccessEntry{w, n}
		macAccess.Unlock()
		if n.Focused && (!exists || !old.node.Focused) {
			focus = obj
		}
		if exists && (old.node.Value != n.Value || old.node.Number != n.Number || old.node.Checked != n.Checked) {
			valueChanges = append(valueChanges, obj)
		}
		if exists && (old.node.SelectionStart != n.SelectionStart || old.node.SelectionEnd != n.SelectionEnd) {
			selectionChanges = append(selectionChanges, obj)
		}
	}
	if !changed {
		return
	}
	for id, obj := range w.access {
		if !next[id] {
			delete(w.access, id)
			macAccess.Lock()
			delete(macAccess.objects, obj)
			macAccess.Unlock()
			mac.accessNotify(obj, macString("AXUIElementDestroyed"))
			macSend(obj, "release")
		}
	}
	w.accessOrder = w.accessOrder[:0]
	for _, n := range nodes {
		if w.access[n.ID] != 0 {
			w.accessOrder = append(w.accessOrder, n.ID)
		}
	}
	mac.accessNotify(w.view, macString("AXLayoutChanged"))
	for _, obj := range valueChanges {
		mac.accessNotify(obj, macString("AXValueChanged"))
	}
	for _, obj := range selectionChanges {
		mac.accessNotify(obj, macString("AXSelectedTextChanged"))
	}
	if focus != 0 {
		mac.accessNotify(focus, macString("AXFocusedUIElementChanged"))
	}
}

func (w *cocoaWindow) closeAccessibility() {
	for _, obj := range w.access {
		macAccess.Lock()
		delete(macAccess.objects, obj)
		macAccess.Unlock()
		mac.accessNotify(obj, macString("AXUIElementDestroyed"))
		macSend(obj, "release")
	}
	w.access, w.accessOrder = nil, nil
}

func macAccessChildren(w *cocoaWindow, parent uint64, selected bool) objc.ID {
	array := macSend(macClass("NSMutableArray"), "array")
	for _, id := range w.accessOrder {
		obj := w.access[id]
		if e, ok := macAccessNode(obj); ok && e.node.Parent == parent && (!selected || e.node.Selected) {
			macSend(array, "addObject:", uintptr(obj))
		}
	}
	return array
}

func macAccessFocused(w *cocoaWindow) objc.ID {
	for i := len(w.accessOrder) - 1; i >= 0; i-- {
		id := w.accessOrder[i]
		if e, ok := macAccessNode(w.access[id]); ok && e.node.Focused {
			return w.access[id]
		}
	}
	return w.view
}

func macAccessFrame(e macAccessEntry) NSRect {
	w, bounds := e.window, e.node.Bounds
	scale := w.scale()
	r := NSRect{Origin: NSPoint{X: float64(bounds.Min.X) / scale, Y: float64(bounds.Min.Y) / scale}, Size: NSSize{Width: float64(bounds.Dx()) / scale, Height: float64(bounds.Dy()) / scale}}
	r = mac.convertRect(w.view, macSel("convertRect:toView:"), r, 0)
	return mac.screenRect(w.window, macSel("convertRectToScreen:"), r)
}

func macAccessHit(w *cocoaWindow, point NSPoint) objc.ID {
	for i := len(w.accessOrder) - 1; i >= 0; i-- {
		obj := w.access[w.accessOrder[i]]
		if e, ok := macAccessNode(obj); ok {
			r := macAccessFrame(e)
			if point.X >= r.Origin.X && point.Y >= r.Origin.Y && point.X < r.Origin.X+r.Size.Width && point.Y < r.Origin.Y+r.Size.Height {
				return obj
			}
		}
	}
	return w.view
}

func (b *cocoaBackend) postAction(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.actions = append(b.actions, fn)
		mac.sourceSignal(b.source)
		mac.loopWake(b.loop)
	}
}

func (b *cocoaBackend) deliverActions() {
	b.mu.Lock()
	actions := b.actions
	b.actions = nil
	b.mu.Unlock()
	for _, fn := range actions {
		fn()
	}
}

func macAccessAction(obj objc.ID, action, text string) bool {
	e, ok := macAccessNode(obj)
	if !ok || e.node.Disabled {
		return false
	}
	e.window.backend.postAction(func() {
		current, exists := macAccessNode(obj)
		if exists && current.window == e.window && current.node.ID == e.node.ID && current.node.Role == e.node.Role && !current.node.Disabled && !e.window.closed {
			e.window.events(Event{Kind: AccessAction, AccessID: e.node.ID, Action: action, Text: text})
		}
	})
	return true
}

func macAccessRole(role string) string {
	switch role {
	case "button":
		return "AXButton"
	case "input":
		return "AXTextField"
	case "editor":
		return "AXTextArea"
	case "text":
		return "AXStaticText"
	case "checkbox", "switch":
		return "AXCheckBox"
	case "radio":
		return "AXRadioButton"
	case "select", "combobox":
		return "AXPopUpButton"
	case "slider":
		return "AXSlider"
	case "progress":
		return "AXProgressIndicator"
	case "list":
		return "AXList"
	case "row", "listitem", "treeitem":
		return "AXRow"
	case "table":
		return "AXTable"
	case "cell":
		return "AXCell"
	case "tree":
		return "AXOutline"
	case "image":
		return "AXImage"
	default:
		return "AXGroup"
	}
}

func macAccessPress(role string) string {
	switch role {
	case "button", "checkbox", "switch", "radio", "select", "combobox":
		return "press"
	case "row", "listitem", "treeitem", "cell":
		return "select"
	}
	return ""
}

func macAccessEditable(n AccessNode) bool {
	return !n.Disabled && (n.Role == "input" || n.Role == "editor")
}

func macAccessAllowed(n AccessNode, selector string) bool {
	switch selector {
	case "accessibilityPerformPress":
		return !n.Disabled && macAccessPress(n.Role) != ""
	case "accessibilityPerformIncrement", "accessibilityPerformDecrement":
		return !n.Disabled && n.Role == "slider"
	case "setAccessibilityValue:":
		return macAccessEditable(n) || !n.Disabled && n.Role == "slider"
	case "setAccessibilitySelectedTextRange:":
		return macAccessEditable(n)
	case "setAccessibilitySelectedText:":
		return false
	case "setAccessibilitySelected:":
		return !n.Disabled && macAccessPress(n.Role) == "select"
	case "setAccessibilityFocused:":
		return !n.Disabled && n.Role != "text" && n.Role != "image" && n.Role != "progress" && n.Role != "group"
	}
	return true
}

// Cocoa ranges count UTF-16 code units; the Go UI counts Unicode runes.
func macRuneOffset(text string, units uint64) (int, bool) {
	index := 0
	for _, r := range text {
		if units == 0 {
			return index, true
		}
		width := uint64(1)
		if r > 0xffff {
			width = 2
		}
		if units < width {
			return 0, false
		}
		units -= width
		index++
	}
	return index, units == 0
}

func macRuneRange(text string, selection NSRange) (int, int, bool) {
	start, ok := macRuneOffset(text, selection.Location)
	if !ok || selection.Length > ^uint64(0)-selection.Location {
		return 0, 0, false
	}
	end, ok := macRuneOffset(text, selection.Location+selection.Length)
	return start, end, ok
}

func macTextRange(n AccessNode) NSRange {
	runes := []rune(n.Value)
	start := max(0, min(n.SelectionStart, len(runes)))
	end := max(start, min(n.SelectionEnd, len(runes)))
	return NSRange{Location: uint64(len(utf16.Encode(runes[:start]))), Length: uint64(len(utf16.Encode(runes[start:end])))}
}

func macAccessViewMethods() []objc.MethodDef {
	return []objc.MethodDef{
		{Cmd: macSel("isAccessibilityElement"), Fn: func(objc.ID, objc.SEL) bool { return true }},
		{Cmd: macSel("accessibilityRole"), Fn: func(objc.ID, objc.SEL) objc.ID { return macString("AXGroup") }},
		{Cmd: macSel("accessibilityChildren"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			if w := macWindow(self); w != nil {
				return macAccessChildren(w, 0, false)
			}
			return 0
		}},
		{Cmd: macSel("accessibilityFocusedUIElement"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			if w := macWindow(self); w != nil {
				return macAccessFocused(w)
			}
			return self
		}},
		{Cmd: macSel("accessibilityHitTest:"), Fn: func(self objc.ID, _ objc.SEL, point NSPoint) objc.ID {
			if w := macWindow(self); w != nil {
				return macAccessHit(w, point)
			}
			return self
		}},
	}
}

func macAccessMethods() []objc.MethodDef {
	methods := []objc.MethodDef{
		{Cmd: macSel("isAccessibilityElement"), Fn: func(self objc.ID, _ objc.SEL) bool { _, ok := macAccessNode(self); return ok }},
		{Cmd: macSel("isAccessibilityEnabled"), Fn: func(self objc.ID, _ objc.SEL) bool { e, ok := macAccessNode(self); return ok && !e.node.Disabled }},
		{Cmd: macSel("isAccessibilityFocused"), Fn: func(self objc.ID, _ objc.SEL) bool { e, ok := macAccessNode(self); return ok && e.node.Focused }},
		{Cmd: macSel("isAccessibilitySelected"), Fn: func(self objc.ID, _ objc.SEL) bool { e, ok := macAccessNode(self); return ok && e.node.Selected }},
		{Cmd: macSel("accessibilityRole"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, _ := macAccessNode(self)
			return macString(macAccessRole(e.node.Role))
		}},
		{Cmd: macSel("accessibilityLabel"), Fn: func(self objc.ID, _ objc.SEL) objc.ID { e, _ := macAccessNode(self); return macString(e.node.Label) }},
		{Cmd: macSel("accessibilityIdentifier"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, _ := macAccessNode(self)
			return macString(strconv.FormatUint(e.node.ID, 10))
		}},
		{Cmd: macSel("accessibilityParent"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, ok := macAccessNode(self)
			if !ok {
				return 0
			}
			if parent := e.window.access[e.node.Parent]; parent != 0 {
				return parent
			}
			return e.window.view
		}},
		{Cmd: macSel("accessibilityChildren"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, ok := macAccessNode(self)
			if !ok {
				return 0
			}
			return macAccessChildren(e.window, e.node.ID, false)
		}},
		{Cmd: macSel("accessibilitySelectedChildren"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, ok := macAccessNode(self)
			if !ok {
				return 0
			}
			return macAccessChildren(e.window, e.node.ID, true)
		}},
		{Cmd: macSel("accessibilityWindow"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, ok := macAccessNode(self)
			if !ok {
				return 0
			}
			return e.window.window
		}},
		{Cmd: macSel("accessibilityTopLevelUIElement"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, ok := macAccessNode(self)
			if !ok {
				return 0
			}
			return e.window.window
		}},
		{Cmd: macSel("accessibilityFrame"), Fn: func(self objc.ID, _ objc.SEL) NSRect {
			e, ok := macAccessNode(self)
			if !ok {
				return NSRect{}
			}
			return macAccessFrame(e)
		}},
		{Cmd: macSel("accessibilityValue"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, ok := macAccessNode(self)
			if !ok {
				return 0
			}
			n := e.node
			switch n.Role {
			case "checkbox", "switch", "radio":
				value := uintptr(0)
				if n.Checked {
					value = 1
				}
				return macSend(macClass("NSNumber"), "numberWithBool:", value)
			case "slider", "progress":
				return mac.number(macClass("NSNumber"), macSel("numberWithDouble:"), n.Number)
			}
			if n.Role == "text" && n.Value == "" {
				return macString(n.Label)
			}
			return macString(n.Value)
		}},
		{Cmd: macSel("accessibilityMinValue"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, _ := macAccessNode(self)
			return mac.number(macClass("NSNumber"), macSel("numberWithDouble:"), e.node.Min)
		}},
		{Cmd: macSel("accessibilityMaxValue"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, _ := macAccessNode(self)
			return mac.number(macClass("NSNumber"), macSel("numberWithDouble:"), e.node.Max)
		}},
		{Cmd: macSel("isAccessibilitySelectorAllowed:"), Fn: func(self objc.ID, _ objc.SEL, selector objc.SEL) bool {
			e, ok := macAccessNode(self)
			if !ok {
				return false
			}
			for _, name := range []string{"accessibilityPerformPress", "accessibilityPerformIncrement", "accessibilityPerformDecrement", "setAccessibilityValue:", "setAccessibilitySelectedTextRange:", "setAccessibilitySelectedText:", "setAccessibilitySelected:", "setAccessibilityFocused:"} {
				if selector == macSel(name) {
					return macAccessAllowed(e.node, name)
				}
			}
			return true
		}},
		{Cmd: macSel("setAccessibilityFocused:"), Fn: func(self objc.ID, _ objc.SEL, focused bool) {
			if e, ok := macAccessNode(self); focused && ok && macAccessAllowed(e.node, "setAccessibilityFocused:") {
				macAccessAction(self, "focus", "")
			}
		}},
		{Cmd: macSel("setAccessibilitySelected:"), Fn: func(self objc.ID, _ objc.SEL, selected bool) {
			if e, ok := macAccessNode(self); selected && ok && macAccessAllowed(e.node, "setAccessibilitySelected:") {
				macAccessAction(self, "select", "")
			}
		}},
		{Cmd: macSel("accessibilityPerformPress"), Fn: func(self objc.ID, _ objc.SEL) bool {
			e, ok := macAccessNode(self)
			if !ok || macAccessPress(e.node.Role) == "" {
				return false
			}
			return macAccessAction(self, macAccessPress(e.node.Role), "")
		}},
		{Cmd: macSel("setAccessibilityValue:"), Fn: func(self objc.ID, _ objc.SEL, value objc.ID) {
			e, ok := macAccessNode(self)
			if !ok || !macAccessAllowed(e.node, "setAccessibilityValue:") || value == 0 {
				return
			}
			if e.node.Role == "slider" {
				if macSend(value, "respondsToSelector:", uintptr(macSel("doubleValue"))) == 0 {
					return
				}
				number := mac.scalar(value, macSel("doubleValue"))
				if math.IsNaN(number) || math.IsInf(number, 0) {
					return
				}
				macAccessAction(self, "setValue", strconv.FormatFloat(number, 'g', -1, 64))
				return
			}
			if macSend(value, "isKindOfClass:", uintptr(macClass("NSString"))) != 0 {
				if text, valid := macBoundedText(value); valid {
					macAccessAction(self, "setValue", text)
				}
			}
		}},
		{Cmd: macSel("accessibilityNumberOfCharacters"), Fn: func(self objc.ID, _ objc.SEL) int64 {
			e, _ := macAccessNode(self)
			return int64(len(utf16.Encode([]rune(e.node.Value))))
		}},
		{Cmd: macSel("accessibilitySelectedTextRange"), Fn: func(self objc.ID, _ objc.SEL) NSRange { e, _ := macAccessNode(self); return macTextRange(e.node) }},
		{Cmd: macSel("accessibilitySelectedText"), Fn: func(self objc.ID, _ objc.SEL) objc.ID {
			e, _ := macAccessNode(self)
			start, end, _ := macRuneRange(e.node.Value, macTextRange(e.node))
			return macString(string([]rune(e.node.Value)[start:end]))
		}},
		{Cmd: macSel("accessibilityStringForRange:"), Fn: func(self objc.ID, _ objc.SEL, selection NSRange) objc.ID {
			e, ok := macAccessNode(self)
			if !ok {
				return 0
			}
			start, end, ok := macRuneRange(e.node.Value, selection)
			if !ok {
				return 0
			}
			return macString(string([]rune(e.node.Value)[start:end]))
		}},
		{Cmd: macSel("setAccessibilitySelectedTextRange:"), Fn: func(self objc.ID, _ objc.SEL, selection NSRange) {
			e, ok := macAccessNode(self)
			if !ok || !macAccessEditable(e.node) {
				return
			}
			start, end, ok := macRuneRange(e.node.Value, selection)
			if !ok {
				return
			}
			macAccessAction(self, "setSelection", strconv.Itoa(start)+":"+strconv.Itoa(end))
		}},
		{Cmd: macSel("accessibilityInsertionPointLineNumber"), Fn: func(self objc.ID, _ objc.SEL) int64 {
			e, _ := macAccessNode(self)
			text := []rune(e.node.Value)
			caret := max(0, min(e.node.Caret, len(text)))
			return int64(strings.Count(string(text[:caret]), "\n"))
		}},
	}
	for _, entry := range []struct {
		selector string
		delta    float64
	}{{"accessibilityPerformIncrement", 1}, {"accessibilityPerformDecrement", -1}} {
		delta := entry.delta
		methods = append(methods, objc.MethodDef{Cmd: macSel(entry.selector), Fn: func(self objc.ID, _ objc.SEL) bool {
			e, ok := macAccessNode(self)
			if !ok || e.node.Role != "slider" {
				return false
			}
			value := max(e.node.Min, min(e.node.Max, e.node.Number+delta))
			return macAccessAction(self, "setValue", strconv.FormatFloat(value, 'g', -1, 64))
		}})
	}
	return methods
}
