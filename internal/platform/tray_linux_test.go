package platform

import (
	"image"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

type trayWatcher struct {
	requests chan dbus.Sender
}

func (w *trayWatcher) RegisterStatusNotifierItem(sender dbus.Sender, path string) *dbus.Error {
	if path != string(sniPath) {
		return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
	}
	w.requests <- sender
	return nil
}

func TestTrayDBus(t *testing.T) {
	address := atspiTestBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	host, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	w := &trayWatcher{requests: make(chan dbus.Sender, 4)}
	if err := host.Export(w, watcherPath, watcher); err != nil {
		t.Fatal(err)
	}
	if _, err := prop.Export(host, watcherPath, prop.Map{watcher: {
		"IsStatusNotifierHostRegistered": {Value: true},
	}}); err != nil {
		t.Fatal(err)
	}
	claim := func() {
		t.Helper()
		if reply, err := host.RequestName(watcher, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("claim watcher: %v %v", reply, err)
		}
	}
	click := make(chan int32, 1)
	state := TrayState{Title: "Fego", ToolTip: "Test", Icon: image.NewNRGBA(image.Rect(0, 0, 8, 8)),
		Menu: []MenuItem{{ID: 1, Label: "Open"}, {ID: 2, Label: "Group", Submenu: []MenuItem{{ID: 3, Label: "Checked", Checkable: true, Checked: true}}}},
	}
	tray, err := newLinuxTray(state, func(id int32) { click <- id })
	if err != nil {
		t.Fatal(err)
	}
	defer tray.Close()
	if tray.Available() {
		t.Fatal("tray cannot be available before a watcher exists")
	}
	waitRegistration := func() string {
		t.Helper()
		select {
		case destination := <-w.requests:
			return string(destination)
		case <-time.After(3 * time.Second):
			t.Fatal("tray did not register")
			return ""
		}
	}
	waitAvailable := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for tray.Available() != want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if tray.Available() != want {
			t.Fatalf("available = %v, want %v", tray.Available(), want)
		}
	}
	claim()
	destination := waitRegistration()
	waitAvailable(true)
	menu := host.Object(destination, menuPath)
	var rev uint32
	var layout menuLayout
	if err := atspiTestCall(t, menu, menuIface+".GetLayout", int32(0), int32(-1), []string{}).Store(&rev, &layout); err != nil || rev != 1 || len(layout.Children) != 2 {
		t.Fatalf("unexpected menu layout: rev=%d layout=%+v err=%v", rev, layout, err)
	}
	atspiTestCall(t, menu, menuIface+".Event", int32(3), "clicked", dbus.MakeVariant(""), uint32(0))
	select {
	case id := <-click:
		if id != 3 {
			t.Fatalf("clicked ID = %d", id)
		}
	case <-time.After(time.Second):
		t.Fatal("menu click was not delivered")
	}
	state.Menu = []MenuItem{{ID: 4, Label: "Öffnen"}}
	if err := tray.Update(state); err != nil {
		t.Fatal(err)
	}
	var label dbus.Variant
	if err := atspiTestCall(t, menu, menuIface+".GetProperty", int32(4), "label").Store(&label); err != nil || label.Value() != "Öffnen" {
		t.Fatalf("updated label=%v err=%v", label, err)
	}
	if _, err := host.ReleaseName(watcher); err != nil {
		t.Fatal(err)
	}
	waitAvailable(false)
	claim()
	if got := waitRegistration(); got != destination {
		t.Fatalf("registered a different tray: %q", got)
	}
	waitAvailable(true)
}
