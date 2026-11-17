//go:build windows && (amd64 || arm64)

package platform

import (
	"testing"
	"unsafe"
)

func TestWindowsAccessibilityCOMTree(t *testing.T) {
	var events []Event
	b := &windowsBackend{}
	w := &windowsWindow{backend: b, events: func(e Event) { events = append(events, e) }}
	tree := &winAccessTree{window: w, nodes: map[uint64]AccessNode{
		0: {Role: "window"}, 1: {ID: 1, Role: "list", Focused: true},
		2: {ID: 2, Parent: 1, Role: "row", Label: "first", Focused: true},
	}, children: map[uint64][]uint64{0: {1}, 1: {2}}, order: []uint64{1, 2}, objects: map[uint64]*winAccessObject{}}
	w.access = tree
	defer tree.close()
	root := tree.object(0)
	if root == nil {
		t.Fatal("COM allocation failed")
	}
	defer accRelease(root.ptr)
	if root.refs.Load() != 2 {
		t.Fatal("tree and caller must each own a reference")
	}
	var queried uintptr
	if accQuery(root.ptr, uintptr(unsafe.Pointer(&iidAccessible[0])), uintptr(unsafe.Pointer(&queried))) != 0 || queried != root.ptr {
		t.Fatal("QueryInterface IAccessible")
	}
	if accRelease(queried) != 2 {
		t.Fatal("QueryInterface reference not balanced")
	}
	var count int32
	if accChildCount(root.ptr, uintptr(unsafe.Pointer(&count))) != 0 || count != 1 {
		t.Fatal("root children")
	}
	var focus accVariant
	if accFocus(root.ptr, uintptr(unsafe.Pointer(&focus))) != 0 || focus.Type != 9 {
		t.Fatal("focused IDispatch")
	}
	row := accObject(focus.Value)
	if row == nil || row.id != 2 {
		t.Fatal("focus must reach the selected row")
	}
	defer accRelease(focus.Value)
	if accDoDefault(row.ptr, 0) != 0 || len(events) != 0 {
		t.Fatal("COM action must be queued")
	}
	b.deliverActions()
	if len(events) != 1 || events[0].AccessID != 2 || events[0].Action != "select" {
		t.Fatalf("action: %+v", events)
	}
	accDoDefault(row.ptr, 0)
	n := tree.nodes[2]
	n.Disabled = true
	tree.nodes[2] = n
	b.deliverActions()
	if len(events) != 1 || accDoDefault(row.ptr, 0) == 0 {
		t.Fatal("disabled or stale action was delivered")
	}
	if accQuery(root.ptr, 0, uintptr(unsafe.Pointer(&queried))) != accInvalid || queried != 0 || accChildCount(root.ptr, 0) != accPointer {
		t.Fatal("invalid output contract")
	}
	tree.close()
	if accDoDefault(row.ptr, 0) == 0 {
		t.Fatal("closed tree remains actionable")
	}
	// Closing releases the tree's owner references, but client references survive.
	if root.refs.Load() != 1 || row.refs.Load() != 1 {
		t.Fatal("tree reference leak")
	}
}
