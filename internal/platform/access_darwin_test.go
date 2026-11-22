//go:build darwin && !ios && (amd64 || arm64)

package platform

import (
	"runtime"
	"testing"

	"github.com/ebitengine/purego/objc"
)

func TestCocoaTextRanges(t *testing.T) {
	text := "a🧪汉z"
	for _, tc := range []struct {
		rangeValue NSRange
		start, end int
		valid      bool
	}{
		{NSRange{Location: 1, Length: 2}, 1, 2, true},
		{NSRange{Location: 3, Length: 1}, 2, 3, true},
		{NSRange{Location: 5}, 4, 4, true},
		{NSRange{Location: 2}, 0, 0, false},
		{NSRange{Location: 1, Length: 1}, 0, 0, false},
		{NSRange{Location: 6}, 0, 0, false},
		{NSRange{Location: 1, Length: ^uint64(0)}, 0, 0, false},
	} {
		start, end, ok := macRuneRange(text, tc.rangeValue)
		if ok != tc.valid || ok && (start != tc.start || end != tc.end) {
			t.Fatalf("range %+v = %d:%d, %v", tc.rangeValue, start, end, ok)
		}
	}
	if got := macTextRange(AccessNode{Value: text, SelectionStart: 1, SelectionEnd: 3}); got.Location != 1 || got.Length != 3 {
		t.Fatalf("rune to UTF16: %+v", got)
	}
	for _, role := range []string{"text", "progress", "image", "group"} {
		if macAccessAllowed(AccessNode{Role: role}, "accessibilityPerformPress") || macAccessAllowed(AccessNode{Role: role}, "setAccessibilityValue:") {
			t.Fatalf("read-only role %q exposes mutation", role)
		}
	}
	if macAccessAllowed(AccessNode{Role: "input", Disabled: true}, "setAccessibilitySelectedTextRange:") {
		t.Fatal("disabled input exposes selection mutation")
	}
}

func TestCocoaAccessibilityTreeAndActions(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMac(); err != nil {
		t.Fatal(err)
	}
	pool := mac.poolPush()
	defer mac.poolPop(pool)
	notify, signal, wake := mac.accessNotify, mac.sourceSignal, mac.loopWake
	defer func() { mac.accessNotify, mac.sourceSignal, mac.loopWake = notify, signal, wake }()
	mac.accessNotify = func(objc.ID, objc.ID) {}
	mac.sourceSignal, mac.loopWake = func(uintptr) {}, func(uintptr) {}
	var events []Event
	b := &cocoaBackend{}
	w := &cocoaWindow{backend: b, events: func(e Event) { events = append(events, e) }}
	defer w.closeAccessibility()
	nodes := []AccessNode{{ID: 1, Role: "list", Focused: true}, {ID: 2, Parent: 1, Role: "row", Label: "first", Focused: true}}
	w.SetAccessibility(nodes)
	parent, row := w.access[1], w.access[2]
	if parent == 0 || row == 0 || macAccessFocused(w) != row {
		t.Fatal("missing native elements or deepest focus")
	}
	children := macAccessChildren(w, 1, false)
	if macSend(children, "count") != 1 || macSend(children, "objectAtIndex:", 0) != row {
		t.Fatal("native children are not connected")
	}
	if macText(macSend(row, "accessibilityLabel")) != "first" || macSend(row, "accessibilityParent") != parent {
		t.Fatal("native getters are not connected")
	}
	if macSend(row, "accessibilityPerformPress") == 0 || len(events) != 0 {
		t.Fatal("action must be queued")
	}
	b.deliverActions()
	if len(events) != 1 || events[0].AccessID != 2 || events[0].Action != "select" {
		t.Fatalf("action: %+v", events)
	}
	macAccessAction(row, "select", "")
	nodes[1].Disabled = true
	w.SetAccessibility(nodes)
	b.deliverActions()
	if len(events) != 1 || macAccessAction(row, "select", "") {
		t.Fatal("disabled or stale action was delivered")
	}
	// A client may retain a removed native element; it must become inert.
	macSend(row, "retain")
	defer macSend(row, "release")
	w.SetAccessibility(nodes[:1])
	if _, ok := macAccessNode(row); ok || macSend(row, "isAccessibilityElement") != 0 || macSend(row, "accessibilityPerformPress") != 0 {
		t.Fatal("removed object remains active")
	}
	if w.access[1] != parent {
		t.Fatal("unchanged element identity was replaced")
	}
}
