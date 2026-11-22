//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"bufio"
	"context"
	"image"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/godbus/dbus/v5"
)

func atspiTestBus(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("D-Bus integration requires dbus-daemon")
	}
	cmd := exec.Command(binary, "--session", "--nofork", "--print-address=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("D-Bus daemon did not report its address")
	}
	return scanner.Text()
}

func atspiTestCall(t *testing.T, object dbus.BusObject, method string, args ...any) *dbus.Call {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	call := object.CallWithContext(ctx, method, 0, args...)
	if call.Err != nil {
		t.Fatalf("%s: %v", method, call.Err)
	}
	return call
}

func TestATSPIRealBus(t *testing.T) {
	address := atspiTestBus(t)
	wakes := make(chan struct{}, 64)
	a, err := newATSPI(address, "Accessible demo", image.Rect(0, 0, 400, 300), func() { wakes <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	nodes := []AccessNode{
		{ID: 1, Role: "group", Label: "Settings", Bounds: image.Rect(5, 6, 300, 250)},
		{ID: 2, Parent: 1, Role: "button", Label: "Save", Bounds: image.Rect(10, 20, 110, 50)},
		{ID: 3, Parent: 1, Role: "input", Label: "Name", Value: "你好 Go", Bounds: image.Rect(10, 60, 200, 90), Caret: 2, SelectionStart: 0, SelectionEnd: 2},
		{ID: 4, Parent: 1, Role: "slider", Label: "Volume", Min: 0, Max: 100, Number: 25, Bounds: image.Rect(10, 100, 200, 130)},
		{ID: 5, Parent: 1, Role: "checkbox", Label: "Disabled", Disabled: true},
	}
	a.update("Accessible demo", image.Rect(0, 0, 400, 300), image.Pt(100, 200), nodes)
	client, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	object := func(id uint64) dbus.BusObject { return client.Object(a.name, dbus.ObjectPath(a.ref(id).Path)) }
	var children []atspiRef
	if err := atspiTestCall(t, object(1), atspiPrefix+"Accessible.GetChildren").Store(&children); err != nil {
		t.Fatal(err)
	}
	if len(children) != 4 || children[0] != a.ref(2) {
		t.Fatalf("children=%v", children)
	}
	value, err := object(2).GetProperty(atspiPrefix + "Accessible.Name")
	if err != nil || value.Value() != "Save" {
		t.Fatalf("Name=%v err=%v", value, err)
	}
	var parent atspiRef
	value, err = object(2).GetProperty(atspiPrefix + "Accessible.Parent")
	if err != nil || dbus.Store([]any{value.Value()}, &parent) != nil || parent != a.ref(1) {
		t.Fatalf("Parent=%v err=%v", value, err)
	}
	var role uint32
	if err := atspiTestCall(t, object(2), atspiPrefix+"Accessible.GetRole").Store(&role); err != nil || role != 43 {
		t.Fatalf("Role=%d err=%v", role, err)
	}
	var bounds atspiRect
	if err := atspiTestCall(t, object(2), atspiPrefix+"Component.GetExtents", uint32(0)).Store(&bounds); err != nil || bounds != (atspiRect{110, 220, 100, 30}) {
		t.Fatalf("bounds=%v err=%v", bounds, err)
	}
	if err := atspiTestCall(t, object(2), atspiPrefix+"Component.GetExtents", uint32(2)).Store(&bounds); err != nil || bounds.X != 5 || bounds.Y != 14 {
		t.Fatalf("parent bounds=%v err=%v", bounds, err)
	}
	var text string
	if err := atspiTestCall(t, object(3), atspiPrefix+"Text.GetText", int32(0), int32(2)).Store(&text); err != nil || text != "你好" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	value, err = object(3).GetProperty(atspiPrefix + "Text.CaretOffset")
	if err != nil || value.Value() != int32(2) {
		t.Fatalf("caret=%v err=%v", value, err)
	}
	var selectionStart, selectionEnd int32
	if err := atspiTestCall(t, object(3), atspiPrefix+"Text.GetSelection", int32(0)).Store(&selectionStart, &selectionEnd); err != nil || selectionStart != 0 || selectionEnd != 2 {
		t.Fatalf("selection=%d:%d err=%v", selectionStart, selectionEnd, err)
	}
	var accepted bool
	if err := atspiTestCall(t, object(2), atspiPrefix+"Action.DoAction", int32(0)).Store(&accepted); err != nil || !accepted {
		t.Fatalf("press=%v err=%v", accepted, err)
	}
	atspiTestCall(t, object(3), atspiPrefix+"Component.GrabFocus")
	atspiTestCall(t, object(3), atspiPrefix+"EditableText.SetTextContents", "更新")
	atspiTestCall(t, object(3), atspiPrefix+"Text.SetCaretOffset", int32(1))
	atspiTestCall(t, object(3), atspiPrefix+"Text.SetSelection", int32(0), int32(1), int32(3))
	atspiTestCall(t, object(4), "org.freedesktop.DBus.Properties.Set", atspiPrefix+"Value", "CurrentValue", dbus.MakeVariant(float64(80)))
	if err := atspiTestCall(t, object(5), atspiPrefix+"Action.DoAction", int32(0)).Store(&accepted); err != nil || accepted {
		t.Fatalf("disabled action=%v err=%v", accepted, err)
	}
	var received []Event
	a.drain(func(e Event) { received = append(received, e) })
	if len(received) != 6 {
		t.Fatalf("queued actions=%v", received)
	}
	for i, want := range []Event{{AccessID: 2, Action: "press"}, {AccessID: 3, Action: "focus"}, {AccessID: 3, Action: "setValue", Text: "更新"}, {AccessID: 3, Action: "setCaret", Text: "1"}, {AccessID: 3, Action: "setSelection", Text: "1:3"}, {AccessID: 4, Action: "setValue", Text: "80"}} {
		got := received[i]
		if got.Kind != AccessAction || got.AccessID != want.AccessID || got.Action != want.Action || got.Text != want.Text {
			t.Fatalf("action %d=%+v want=%+v", i, got, want)
		}
	}
	if len(wakes) != 6 {
		t.Fatalf("wake count=%d", len(wakes))
	}
	if err := object(4).Call("org.freedesktop.DBus.Properties.Set", 0, atspiPrefix+"Value", "CurrentValue", dbus.MakeVariant(float64(101))).Err; err == nil {
		t.Fatal("out-of-range numeric value accepted")
	}
	if err := object(3).Call(atspiPrefix+"Text.GetText", 0, int32(-1), int32(2)).Err; err == nil {
		t.Fatal("negative text range accepted")
	}
	var xml string
	if err := atspiTestCall(t, object(3), "org.freedesktop.DBus.Introspectable.Introspect").Store(&xml); err != nil || !strings.Contains(xml, "CaretOffset") || !strings.Contains(xml, "SetTextContents") {
		t.Fatalf("text introspection=%q err=%v", xml, err)
	}
	signals := make(chan *dbus.Signal, 20)
	client.Signal(signals)
	defer client.RemoveSignal(signals)
	if err := client.AddMatchSignal(dbus.WithMatchInterface(atspiPrefix + "Event.Object")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		nodes[1].Focused = i%2 == 0
		nodes[1].Label = "Updated " + strconv.Itoa(i)
		nodes[2].Value = "新值"
		a.update("Accessible demo", image.Rect(0, 0, 400, 300), image.Pt(100, 200), nodes)
	}
	value, err = object(2).GetProperty(atspiPrefix + "Accessible.Name")
	if err != nil || value.Value() != "Updated 199" {
		t.Fatalf("unsubscribed query tree stale: %v %v", value, err)
	}
	select {
	case signal := <-signals:
		t.Fatalf("emitted without a registered listener: %s", signal.Name)
	case <-time.After(40 * time.Millisecond):
	}
	a.update("Accessible demo", image.Rect(0, 0, 400, 300), image.Point{}, nodes[:1])
	if err := object(2).Call(atspiPrefix+"Action.DoAction", 0, int32(0)).Err; err == nil {
		t.Fatal("removed control remained actionable")
	}
}

func TestATSPISnapshotTopologyAndValuesAreImmutable(t *testing.T) {
	a := &atspiBridge{}
	first := []AccessNode{{ID: 1, Role: "group"}, {ID: 2, Parent: 1, Role: "button", Label: "before"}}
	a.update("title", image.Rect(0, 0, 100, 100), image.Point{}, first)
	old := a.tree.Load()
	first[1].Label = "after"
	a.update("title", image.Rect(0, 0, 100, 100), image.Point{}, first)
	updated := a.tree.Load()
	oldNode, _ := old.node(2)
	newNode, _ := updated.node(2)
	if oldNode.Label != "before" || newNode.Label != "after" {
		t.Fatal("reader snapshot mutated")
	}
	a.update("title", image.Rect(0, 0, 100, 100), image.Point{}, first)
	if a.tree.Load() != updated {
		t.Fatal("unchanged snapshot replaced")
	}
	first[1].Parent = 0
	a.update("title", image.Rect(0, 0, 100, 100), image.Point{}, first)
	if len(old.children[1]) != 1 || len(updated.children[1]) != 1 || len(a.tree.Load().children[1]) != 0 {
		t.Fatal("published topology mutated")
	}
}

func TestX11AccessibilityQueriesOriginOnlyOnWindowChanges(t *testing.T) {
	previous := xl.translateCoordinates
	defer func() { xl.translateCoordinates = previous }()
	calls := 0
	xl.translateCoordinates = func(_, _, _ uintptr, _, _ int32, x, y *int32, _ *uintptr) int32 {
		calls++
		*x = int32(calls * 10)
		*y = int32(calls * 20)
		return 1
	}
	w := &xWindow{b: &xBackend{}, access: &atspiBridge{}, accessTried: true, width: 100, height: 100}
	nodes := []AccessNode{{ID: 1, Role: "button", Label: "before"}}
	w.SetAccessibility(nodes)
	nodes[0].Label = "after"
	w.SetAccessibility(nodes)
	w.SetAccessibility(nodes)
	if calls != 1 || w.access.current().origin != image.Pt(10, 20) {
		t.Fatal("origin queried per frame", calls)
	}
	w.refreshAccessibility()
	if calls != 2 || w.access.current().origin != image.Pt(20, 40) {
		t.Fatal("window change kept stale origin", calls)
	}
}

func TestATSPIRegistry(t *testing.T) {
	if os.Getenv("FEGO_ATSPI_TEST") != "1" {
		t.Skip("set FEGO_ATSPI_TEST=1 to test the installed AT-SPI registry")
	}
	address := atspiTestBus(t)
	registry := exec.Command("/usr/libexec/at-spi2-registryd")
	registry.Env = append(os.Environ(), "AT_SPI_BUS_ADDRESS="+address, "DBUS_SESSION_BUS_ADDRESS="+address)
	registry.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var log strings.Builder
	registry.Stderr = &log
	if err := registry.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-registry.Process.Pid, syscall.SIGTERM); _ = registry.Wait() }()
	client, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var ready bool
		err := client.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, atspiPrefix+"Registry").Store(&ready)
		if err == nil && ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry did not claim bus: %s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	a, err := newATSPI(address, "Registry test", image.Rect(0, 0, 100, 100), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	a.update("Registry test", image.Rect(0, 0, 100, 100), image.Point{}, []AccessNode{{ID: 1, Role: "button", Label: "Start"}})
	if a.desktop.Load() == nil {
		t.Fatalf("application did not embed in registry: %s", log.String())
	}
	var children []atspiRef
	if err := atspiTestCall(t, client.Object(atspiPrefix+"Registry", dbus.ObjectPath(atspiRoot)), atspiPrefix+"Accessible.GetChildren").Store(&children); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, child := range children {
		if child == a.ref(0) {
			found = true
		}
	}
	if !found {
		t.Fatalf("registered application missing from desktop: %v", children)
	}
	var appChildren []atspiRef
	if err := atspiTestCall(t, client.Object(a.name, dbus.ObjectPath(atspiRoot)), atspiPrefix+"Accessible.GetChildren").Store(&appChildren); err != nil || len(appChildren) != 1 {
		t.Fatalf("app window query=%v err=%v", appChildren, err)
	}
	// Exercise the installed client library used by Linux assistive technology.
	probe := exec.Command("/usr/bin/python3", "-c", `import gi
import os
gi.require_version("Atspi", "2.0")
from gi.repository import Atspi
desktop = Atspi.get_desktop(0)
apps = [desktop.get_child_at_index(i) for i in range(desktop.get_child_count())]
app = next(a for a in apps if a.get_name() == "Registry test")
window = app.get_child_at_index(0)
button = window.get_child_at_index(0)
assert button.get_name() == "Start"
assert button.get_role() == Atspi.Role.PUSH_BUTTON
assert button.get_action_iface().do_action(0)
print("libatspi queried and activated Start")`)
	probe.Env = append(os.Environ(), "AT_SPI_BUS_ADDRESS="+address, "DBUS_SESSION_BUS_ADDRESS="+address)
	if output, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("libatspi probe: %v\n%s", err, output)
	}
	var action Event
	a.drain(func(e Event) { action = e })
	if action.AccessID != 1 || action.Action != "press" {
		t.Fatalf("libatspi action=%+v", action)
	}
}

func TestATSPIMainThread(t *testing.T) {
	if os.Getenv("FEGO_X11_TEST") != "1" {
		t.Skip("set FEGO_X11_TEST=1 on a dedicated X display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	address := atspiTestBus(t)
	t.Setenv("AT_SPI_BUS_ADDRESS", address)
	backend, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.(*xBackend)
	defer b.release()
	thread := syscall.Gettid()
	received := make(chan Event, 1)
	native, err := b.Open(Options{Title: "AT-SPI dispatch", Width: 100, Height: 100}, func(e Event) {
		if e.Kind == AccessAction {
			if syscall.Gettid() != thread {
				t.Errorf("action ran on thread %d, want %d", syscall.Gettid(), thread)
			}
			received <- e
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	w := native.(*xWindow)
	w.SetAccessibility([]AccessNode{{ID: 42, Role: "button", Label: "Run", Bounds: image.Rect(0, 0, 50, 20)}})
	if w.access == nil {
		t.Fatal("X11 window failed to export accessibility")
	}
	client, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	object := client.Object(w.access.name, dbus.ObjectPath(string(atspiBase)+"/n"+strconv.Itoa(42)))
	lib, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(lib)
	var move func(uintptr, uintptr, int32, int32) int32
	var syncX func(uintptr, int32) int32
	var reparent func(uintptr, uintptr, uintptr, int32, int32) int32
	purego.RegisterLibFunc(&move, lib, "XMoveWindow")
	purego.RegisterLibFunc(&syncX, lib, "XSync")
	purego.RegisterLibFunc(&reparent, lib, "XReparentWindow")
	flushEvents := func() {
		syncX(b.display, 0)
		for xl.pending(b.display) > 0 {
			var raw xEvent
			xl.nextEvent(b.display, &raw)
			b.event(&raw)
		}
	}
	checkOrigin := func(want image.Point) {
		t.Helper()
		flushEvents()
		var bounds atspiRect
		atspiTestCall(t, object, atspiPrefix+"Component.GetExtents", uint32(0)).Store(&bounds)
		if bounds.X != int32(want.X) || bounds.Y != int32(want.Y) {
			t.Fatalf("moved accessible bounds=%v want origin %v", bounds, want)
		}
	}
	move(b.display, w.id, 123, 77)
	checkOrigin(image.Pt(123, 77))
	frame := xl.createWindow(b.display, b.root, 40, 20, 180, 180, 0, 0, 0xffffff)
	if frame == 0 {
		t.Fatal("create test frame")
	}
	defer func() { w.Close(); xl.destroyWindow(b.display, frame) }()
	xl.mapWindow(b.display, frame)
	reparent(b.display, w.id, frame, 10, 8)
	checkOrigin(image.Pt(50, 28))
	atspiTestCall(t, object, atspiPrefix+"Action.DoAction", int32(0))
	select {
	case <-received:
		t.Fatal("action bypassed main-thread dispatch")
	default:
	}
	if err := b.wait(time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-received:
		if e.AccessID != 42 || e.Action != "press" {
			t.Fatalf("action=%+v", e)
		}
	default:
		t.Fatal("wake did not dispatch action")
	}
	snapshot := w.access.current()
	w.Close()
	if w.access != nil {
		t.Fatal("closed window retained its accessibility bridge")
	}
	if node, ok := snapshot.node(42); !ok || node.Label != "Run" {
		t.Fatal("closing a window invalidated an already published snapshot")
	}
}

func TestATSPIRegistrySubscriptions(t *testing.T) {
	if os.Getenv("FEGO_ATSPI_TEST") != "1" {
		t.Skip("set FEGO_ATSPI_TEST=1 to test actual registry subscriptions")
	}
	address := atspiTestBus(t)
	registry := exec.Command("/usr/libexec/at-spi2-registryd")
	registry.Env = append(os.Environ(), "AT_SPI_BUS_ADDRESS="+address, "DBUS_SESSION_BUS_ADDRESS="+address)
	registry.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := registry.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-registry.Process.Pid, syscall.SIGTERM); _ = registry.Wait() }()
	listener, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wait := func(predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !predicate() {
			if time.Now().After(deadline) {
				t.Fatal("subscription update did not arrive")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(func() bool {
		var owned bool
		_ = listener.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, atspiPrefix+"Registry").Store(&owned)
		return owned
	})
	registryObject := listener.Object(atspiPrefix+"Registry", dbus.ObjectPath(atspiRegistryPath))
	// The constructor must load listeners that registered before the application.
	atspiTestCall(t, registryObject, atspiPrefix+"Registry.RegisterEvent", "object:state-changed:focused", []string{}, "")
	a, err := newATSPI(address, "Subscriptions", image.Rect(0, 0, 400, 200), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if !a.wants("StateChanged", "focused") || a.wants("StateChanged", "checked") || a.wants("PropertyChange", "accessible-name") {
		t.Fatal("initial registry event filtering is incorrect")
	}
	nodes := []AccessNode{{ID: 1, Role: "button", Label: "Run"}, {ID: 2, Role: "input", Label: "Name", Value: "one"}}
	a.update("Subscriptions", image.Rect(0, 0, 400, 200), image.Point{}, nodes)
	signals := make(chan *dbus.Signal, 64)
	listener.Signal(signals)
	defer listener.RemoveSignal(signals)
	if err := listener.AddMatchSignal(dbus.WithMatchSender(a.name), dbus.WithMatchInterface(atspiPrefix+"Event.Object")); err != nil {
		t.Fatal(err)
	}
	nodes[0].Focused = true
	nodes[0].Checked = true
	nodes[0].Label = "Running"
	nodes[1].Value = "two"
	a.update("Subscriptions", image.Rect(0, 0, 400, 200), image.Point{}, nodes)
	select {
	case signal := <-signals:
		if signal.Name != atspiPrefix+"Event.Object.StateChanged" || signal.Body[0] != "focused" {
			t.Fatalf("unexpected filtered event: %+v", signal)
		}
	case <-time.After(time.Second):
		t.Fatal("registered focus listener received nothing")
	}
	select {
	case signal := <-signals:
		t.Fatalf("unregistered event leaked: %s %v", signal.Name, signal.Body)
	case <-time.After(30 * time.Millisecond):
	}
	// Registration signals update filtering without a poll or repaint trigger.
	atspiTestCall(t, registryObject, atspiPrefix+"Registry.RegisterEvent", "object:text-changed", []string{}, "")
	atspiTestCall(t, registryObject, atspiPrefix+"Registry.RegisterEvent", "object:property-change:accessible-name", []string{}, a.name)
	wait(func() bool { return a.wants("TextChanged", "insert") && a.wants("PropertyChange", "accessible-name") })
	nodes[0].Label = "Ready"
	nodes[1].Value = "three"
	a.update("Subscriptions", image.Rect(0, 0, 400, 200), image.Point{}, nodes)
	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case signal := <-signals:
			seen[signal.Name+":"+signal.Body[0].(string)] = true
		case <-time.After(time.Second):
			t.Fatalf("registered text/name events missing: %v", seen)
		}
	}
	for _, event := range []string{"PropertyChange:accessible-name", "TextChanged:delete", "TextChanged:insert"} {
		if !seen[atspiPrefix+"Event.Object."+event] {
			t.Fatalf("missing %s: %v", event, seen)
		}
	}
	atspiTestCall(t, registryObject, atspiPrefix+"Registry.DeregisterEvent", "object:property-change", a.name)
	wait(func() bool { return !a.wants("PropertyChange", "accessible-name") })
	// A listener for another application's bounds must not enable our notifications.
	atspiTestCall(t, registryObject, atspiPrefix+"Registry.RegisterEvent", "object:bounds-changed", []string{}, ":1.999999")
	// Order a matching registration after it, so completion of refresh is observable.
	atspiTestCall(t, registryObject, atspiPrefix+"Registry.RegisterEvent", "object:text-caret-moved", []string{}, "")
	wait(func() bool { return a.wants("TextCaretMoved", "") })
	if a.wants("BoundsChanged", "") {
		t.Fatal("another application's event registration leaked")
	}
	atspiTestCall(t, registryObject, atspiPrefix+"Registry.DeregisterEvent", "", "")
	wait(func() bool { return len(a.listeners.Load().events) == 0 })
	nodes[0].Label = "Unsubscribed"
	nodes[0].Focused = false
	nodes[1].Value = "four"
	a.update("Subscriptions", image.Rect(0, 0, 400, 200), image.Point{}, nodes)
	value, err := listener.Object(a.name, dbus.ObjectPath(a.ref(1).Path)).GetProperty(atspiPrefix + "Accessible.Name")
	if err != nil || value.Value() != "Unsubscribed" {
		t.Fatalf("query tree stopped after unsubscribe: %v %v", value, err)
	}
	select {
	case signal := <-signals:
		t.Fatalf("event after unsubscribe: %s", signal.Name)
	case <-time.After(30 * time.Millisecond):
	}
	// Registry deregisters all interests when an assistive client disconnects.
	transient, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	atspiTestCall(t, transient.Object(atspiPrefix+"Registry", dbus.ObjectPath(atspiRegistryPath)), atspiPrefix+"Registry.RegisterEvent", "object", []string{}, "")
	wait(func() bool { return a.wants("BoundsChanged", "") })
	transient.Close()
	wait(func() bool { return len(a.listeners.Load().events) == 0 })
}

func TestATSPIEventFilterNames(t *testing.T) {
	a := &atspiBridge{}
	for _, tc := range []struct {
		subscription, name, detail string
		want                       bool
	}{
		{"Object::", "PropertyChange", "accessible-name", true},
		{"object:state-changed:focused", "StateChanged", "focused", true},
		{"Object:StateChanged:Focused", "StateChanged", "checked", false},
		{"object:text-changed", "TextChanged", "insert", true},
		{"object:text-changed", "TextCaretMoved", "", false},
		{"window", "StateChanged", "focused", false},
	} {
		a.listeners.Store(&atspiSubscriptions{events: map[string]bool{atspiEventName(tc.subscription): true}})
		if got := a.wants(tc.name, tc.detail); got != tc.want {
			t.Errorf("%q vs %s:%s = %v", tc.subscription, tc.name, tc.detail, got)
		}
	}
}
