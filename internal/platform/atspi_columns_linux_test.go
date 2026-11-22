//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"image"
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

func TestATSPISnapshotColumnsStayImmutable(t *testing.T) {
	if bytes := unsafe.Sizeof(atspiNodeMeta{}) + unsafe.Sizeof(atspiNodeText{}) + unsafe.Sizeof(image.Rectangle{}) + unsafe.Sizeof(atspiNodeState{}); bytes > unsafe.Sizeof(AccessNode{}) {
		t.Fatal("snapshot columns increased per-node retained storage", bytes)
	}
	a := &atspiBridge{}
	nodes := []AccessNode{{ID: 1, Role: "group"}, {ID: 2, Parent: 1, Role: "input", Label: "Name", Value: "before", Bounds: image.Rect(1, 2, 30, 40)}}
	title, bounds, origin := "title", image.Rect(0, 0, 100, 100), image.Point{}
	type snapshot struct {
		tree   *atspiTree
		nodes  []AccessNode
		title  string
		bounds image.Rectangle
		origin image.Point
	}
	var snapshots []snapshot
	changes := []func(){
		func() {},
		func() { nodes[1].Label, nodes[1].Value = "Changed", "after" },
		func() { nodes[1].Bounds = image.Rect(4, 5, 34, 45) },
		func() {
			nodes[1].Disabled, nodes[1].Focused, nodes[1].Checked, nodes[1].Selected = true, true, true, true
			nodes[1].Number, nodes[1].Caret, nodes[1].SelectionStart, nodes[1].SelectionEnd = 4.5, 4, 1, 4
		},
		func() { nodes[1].Role, nodes[1].Min, nodes[1].Max = "slider", 1, 10 },
		func() { title, bounds, origin = "new title", image.Rect(0, 0, 140, 110), image.Pt(-20, 10) },
		func() { nodes[1].Parent = 0 },
	}
	for step, change := range changes {
		prior := a.tree.Load()
		change()
		a.update(title, bounds, origin, nodes)
		current := a.tree.Load()
		if step > 0 && step < 4 && &current.nodes[0] != &prior.nodes[0] {
			t.Fatal("value-only update copied unchanged base nodes")
		}
		if step == 2 && &current.text[0] != &prior.text[0] {
			t.Fatal("geometry update copied unchanged text")
		}
		if step == 3 && (&current.text[0] != &prior.text[0] || &current.bounds[0] != &prior.bounds[0]) {
			t.Fatal("state update copied unchanged columns")
		}
		if step == 4 && &current.nodes[0] == &prior.nodes[0] {
			t.Fatal("metadata update kept stale base nodes")
		}
		snapshots = append(snapshots, snapshot{current, append([]AccessNode(nil), nodes...), title, bounds, origin})
		a.update(title, bounds, origin, nodes)
		if current != a.tree.Load() {
			t.Fatal("unchanged snapshot was replaced")
		}
	}
	clear(nodes)
	for _, snapshot := range snapshots {
		for _, expected := range snapshot.nodes {
			if expected.Parent == 0 {
				expected.Parent = atspiWindow
			}
			if actual, ok := snapshot.tree.node(expected.ID); !ok || actual != expected {
				t.Fatalf("published snapshot changed: actual=%+v expected=%+v", actual, expected)
			}
		}
		root, _ := snapshot.tree.node(0)
		window, _ := snapshot.tree.node(atspiWindow)
		if root.Label != snapshot.title || window.Label != snapshot.title || root.Bounds != snapshot.bounds || window.Bounds != snapshot.bounds || snapshot.tree.origin != snapshot.origin {
			t.Fatal("application/window snapshot changed")
		}
	}
}

func TestATSPISnapshotColumnsConcurrentReaders(t *testing.T) {
	a := &atspiBridge{}
	nodes := []AccessNode{{ID: 1, Role: "input", Label: "a", Value: "a", Bounds: image.Rect(0, 0, 10, 10)}}
	a.update("title", image.Rect(0, 0, 100, 100), image.Point{}, nodes)
	done := make(chan struct{})
	var readers sync.WaitGroup
	for range 2 {
		readers.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				snapshot := a.tree.Load()
				before, _ := snapshot.node(1)
				runtime.Gosched()
				after, _ := snapshot.node(1)
				if before != after || before.Label != before.Value || before.Bounds.Min.X != int(before.Number) {
					t.Error("reader observed a mixed or mutated snapshot")
					return
				}
			}
		})
	}
	for frame := 1; frame <= 100; frame++ {
		nodes[0].Label = []string{"a", "b"}[frame%2]
		nodes[0].Value = nodes[0].Label
		nodes[0].Number = float64(frame)
		nodes[0].Bounds = image.Rect(frame, 0, frame+10, 10)
		a.update("title", image.Rect(0, 0, 100, 100), image.Point{}, nodes)
	}
	close(done)
	readers.Wait()
}
