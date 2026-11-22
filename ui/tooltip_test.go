package ui

import (
	"testing"
	"time"

	"github.com/zatxm/fego/v26"
)

func TestTooltipFollowsPointerAndDismisses(t *testing.T) {
	icon := Text("i")
	button := Button("", nil).Tooltip("Delete node")
	row := Row(icon, button).Tooltip("Row help")
	root := Column(row, Box().Height(400))
	view := NewTester(root, 400, 300)
	defer view.Close()
	move := func(e *Element) {
		r := e.Bounds()
		view.Send(fego.Event{Kind: fego.PointerMove, X: r.X + r.W/2, Y: r.Y + r.H/2})
	}
	v := view.Document.live
	before := view.Snapshot()
	move(button)
	if !v.tip.shown || v.tip.target != button {
		t.Fatal("tooltip not shown for hovered button")
	}
	if r := v.tip.rect; r.X < 0 || r.Y < 0 || r.X+r.W > 400 || r.Y+r.H > 300 {
		t.Fatal("tooltip outside the window", r)
	}
	after := view.Snapshot()
	if string(before.Pix) == string(after.Pix) {
		t.Fatal("tooltip was not painted")
	}
	move(icon)
	if v.tip.target != row {
		t.Fatal("element without a tooltip did not use its ancestor's")
	}
	view.Click(icon.Bounds().X+1, icon.Bounds().Y+1)
	if v.tip.shown {
		t.Fatal("pressing did not dismiss the tooltip")
	}
	button.Tooltip("Renamed")
	move(button)
	if !v.tip.shown || button.tooltip != "Renamed" {
		t.Fatal("changed tooltip not shown")
	}
	root.Children(Text("replaced"))
	view.Step()
	if v.tip.target != nil || v.tip.shown {
		t.Fatal("tooltip kept a detached element")
	}
}

type delayedHost struct {
	*Tester
	pending func()
}

func (h *delayedHost) After(_ time.Duration, fn func()) func() {
	h.pending = fn
	return func() { h.pending = nil }
}

func TestTooltipWaitsForDelay(t *testing.T) {
	button := Button("", nil).Tooltip("Refresh")
	other := Button("other", nil)
	tester := &Tester{Width: 300, Height: 200, Scale: 1}
	host := &delayedHost{Tester: tester}
	tester.Document = View(Column(button, other))
	tester.Document.Mount(host)
	tester.Document.live.frame(false)
	defer tester.Close()
	v := tester.Document.live
	r := button.Bounds()
	tester.Send(fego.Event{Kind: fego.PointerMove, X: r.X + 2, Y: r.Y + 2})
	if v.tip.shown || host.pending == nil {
		t.Fatal("tooltip shown before the delay")
	}
	o := other.Bounds()
	tester.Send(fego.Event{Kind: fego.PointerMove, X: o.X + 2, Y: o.Y + 2})
	if host.pending != nil {
		t.Fatal("leaving the element did not cancel the pending tooltip")
	}
	tester.Send(fego.Event{Kind: fego.PointerMove, X: r.X + 2, Y: r.Y + 2})
	host.pending()
	if !v.tip.shown {
		t.Fatal("tooltip not shown after the delay")
	}
}

func TestTransparentBorderLeavesBackgroundVisible(t *testing.T) {
	row := Row(Text("")).Size(80, 30).Background(Color{}).Border(Color{}).OnClick(func() {})
	view := NewTester(Column(row), 200, 100)
	defer view.Close()
	img := view.Snapshot()
	r := row.Bounds()
	got := img.RGBAAt(int(r.X+r.W/2), int(r.Y+r.H/2))
	bg := Light.Background
	if got.R != bg.R || got.G != bg.G || got.B != bg.B {
		t.Fatal("transparent border painted a fill", got)
	}
}
