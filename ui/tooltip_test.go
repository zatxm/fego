package ui

import (
	"testing"
	"time"

	"github.com/zatxm/fego/v26"
)

func TestTooltipAnchorsToElementAndDismisses(t *testing.T) {
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
	first := v.tip.rect
	bounds := button.Bounds()
	for _, x := range []float32{bounds.X + 2, bounds.X + bounds.W - 2} {
		move(icon)
		view.Send(fego.Event{Kind: fego.PointerMove, X: x, Y: bounds.Y + 2})
		if !v.tip.shown || v.tip.rect != first || v.tip.anchorX != bounds.X+bounds.W/2 {
			t.Fatalf("tooltip moved with the pointer: got %v, want %v", v.tip.rect, first)
		}
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

func TestTooltipFitsAtWindowEdges(t *testing.T) {
	button := Button("", nil).Size(28, 24).Tooltip("Delete node")
	view := NewTester(Column(Box().Height(170), Row(Box().Width(210), button)), 240, 200)
	defer view.Close()
	bounds := button.Bounds()
	view.Send(fego.Event{Kind: fego.PointerMove, X: bounds.X + 2, Y: bounds.Y + 2})
	tip := view.Document.live.tip
	if r := tip.rect; !tip.shown || !tip.above || r.Y+r.H >= bounds.Y || r.X+r.W >= 240 {
		t.Fatalf("tooltip did not fit above its anchor: button %v, tooltip %v", bounds, r)
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

func TestNotificationActionExpiryAndLocalPaint(t *testing.T) {
	tester := &Tester{Width: 800, Height: 500, Scale: 1}
	host := &delayedHost{Tester: tester}
	tester.Document = View(Text("Content"))
	tester.Document.Mount(host)
	tester.Step()
	defer tester.Close()
	doc, v := tester.Document, tester.Document.live
	doc.ShowToast("Saved")
	opened := false
	doc.ShowNotification("Project created", "Workspace", ToastAction{Label: "Open", Run: func() { opened = true }})
	tester.Step()
	if v.toastCount != 2 || v.toastButton() == nil || host.pending == nil {
		t.Fatal("notification did not preserve the older toast and attach its action")
	}
	for _, region := range tester.Damage() {
		if region.Min.X <= 0 || region.Min.Y <= 0 {
			t.Fatalf("toast repainted the whole viewport: %v", region)
		}
	}
	tester.Key("Tab", false, false)
	tester.Key("Enter", false, false)
	if !opened || v.toastCount != 1 || v.toastButton() != nil {
		t.Fatal("toast keyboard action failed or cleared unrelated notifications")
	}
	v.toasts[0].expires = time.Now().Add(-time.Millisecond)
	host.pending()
	tester.Step()
	if v.toastCount != 0 || v.toastCancel != nil {
		t.Fatal("expired toast retained a notification or timer")
	}
}
