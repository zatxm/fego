//go:build !android

package platform

import (
	"context"
	_ "embed"
	"image"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	sniPath     dbus.ObjectPath = "/StatusNotifierItem"
	menuPath    dbus.ObjectPath = "/Menu"
	sniIface                    = "org.kde.StatusNotifierItem"
	menuIface                   = "com.canonical.dbusmenu"
	propsIface                  = "org.freedesktop.DBus.Properties"
	watcher                     = "org.kde.StatusNotifierWatcher"
	watcherPath dbus.ObjectPath = "/StatusNotifierWatcher"
)

// The tray interfaces are fixed. Serving their descriptions directly avoids
// deriving and marshaling the same XML for every tray.
//
//go:embed tray_item.xml
var itemSchema string

//go:embed tray_menu.xml
var menuSchema string

type traySchema string

func (s traySchema) Introspect() (string, *dbus.Error) { return string(s), nil }

type linuxPixmap struct {
	Width, Height int32
	ARGB          []byte
}

type linuxToolTip struct {
	Icon        string
	Pixmaps     []linuxPixmap
	Title, Body string
}

type linuxTray struct {
	conn      *dbus.Conn
	cancel    context.CancelFunc
	click     func(int32)
	available atomic.Bool
	mu        sync.RWMutex
	state     TrayState
	pixmap    []linuxPixmap
	revision  uint32
}

func sniPixmap(icon *image.NRGBA) []linuxPixmap {
	argb := make([]byte, len(icon.Pix))
	for i := 0; i < len(argb); i += 4 {
		copy(argb[i+1:i+4], icon.Pix[i:i+3])
		argb[i] = icon.Pix[i+3]
	}
	b := icon.Bounds()
	return []linuxPixmap{{int32(b.Dx()), int32(b.Dy()), argb}}
}

func (b *xBackend) NewTray(s TrayState, click func(int32)) (Tray, error) {
	return newLinuxTray(s, click)
}

func newLinuxTray(s TrayState, click func(int32)) (*linuxTray, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &linuxTray{conn: conn, cancel: cancel, click: click, state: s, pixmap: sniPixmap(s.Icon), revision: 1}
	ok := false
	defer func() {
		if !ok {
			t.Close()
		}
	}()
	item := &linuxItem{t}
	menu := &linuxMenu{t}
	for _, entry := range []struct {
		path dbus.ObjectPath
		name string
		obj  any
	}{
		{sniPath, sniIface, item}, {menuPath, menuIface, menu},
		{sniPath, propsIface, &linuxProps{t, sniIface}}, {menuPath, propsIface, &linuxProps{t, menuIface}},
		{sniPath, "org.freedesktop.DBus.Introspectable", traySchema(itemSchema)},
		{menuPath, "org.freedesktop.DBus.Introspectable", traySchema(menuSchema)},
	} {
		if err := conn.Export(entry.obj, entry.path, entry.name); err != nil {
			return nil, err
		}
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	if err := conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, watcher)); err != nil {
		return nil, err
	}
	if err := conn.AddMatchSignal(dbus.WithMatchInterface(watcher), dbus.WithMatchObjectPath(watcherPath)); err != nil {
		return nil, err
	}
	t.register(ctx)
	ok = true
	go t.watch(ctx, signals)
	return t, nil
}

func (t *linuxTray) register(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	obj := t.conn.Object(watcher, watcherPath)
	t.available.Store(false)
	// A leading slash registers this path under the caller's unique bus name.
	if err := obj.CallWithContext(ctx, watcher+".RegisterStatusNotifierItem", 0, string(sniPath)).Err; err == nil {
		t.available.Store(t.hasHost(ctx))
	}
}

func (t *linuxTray) hasHost(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var host dbus.Variant
	err := t.conn.Object(watcher, watcherPath).CallWithContext(ctx, propsIface+".Get", 0,
		watcher, "IsStatusNotifierHostRegistered").Store(&host)
	return err == nil && host.Value() == true
}

func (t *linuxTray) watch(ctx context.Context, signals <-chan *dbus.Signal) {
	defer t.available.Store(false)
	for {
		select {
		case <-ctx.Done():
			return
		case signal, ok := <-signals:
			if !ok {
				return
			}
			switch signal.Name {
			case "org.freedesktop.DBus.NameOwnerChanged":
				t.register(ctx)
			case watcher + ".StatusNotifierHostRegistered", watcher + ".StatusNotifierHostUnregistered":
				t.available.Store(t.hasHost(ctx))
			}
		}
	}
}

func (t *linuxTray) Available() bool { return t.available.Load() }
func (t *linuxTray) Close() {
	t.cancel()
	t.conn.Close()
	t.available.Store(false)
}

func (t *linuxTray) Update(s TrayState) error {
	t.mu.Lock()
	changedIcon := s.Icon != t.state.Icon
	changedTip := s.ToolTip != t.state.ToolTip
	changedMenu := !reflect.DeepEqual(s.Menu, t.state.Menu)
	if changedIcon {
		t.pixmap = sniPixmap(s.Icon)
	}
	if changedMenu {
		t.revision++
	}
	t.state = s
	revision := t.revision
	t.mu.Unlock()
	if changedIcon {
		t.conn.Emit(sniPath, sniIface+".NewIcon")
	}
	if changedTip {
		t.conn.Emit(sniPath, sniIface+".NewToolTip")
	}
	if changedMenu {
		t.conn.Emit(menuPath, menuIface+".LayoutUpdated", revision, int32(0))
	}
	return nil
}

type linuxProps struct {
	t     *linuxTray
	iface string
}

var itemPropertyNames = [...]string{"Category", "Id", "Title", "Status", "WindowId", "IconPixmap", "IconName", "Menu", "ItemIsMenu", "ToolTip"}
var menuPropertyNames = [...]string{"Version", "TextDirection", "Status", "IconThemePath"}

func (p *linuxProps) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != p.iface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{iface})
	}
	names := itemPropertyNames[:]
	if iface == menuIface {
		names = menuPropertyNames[:]
	}
	p.t.mu.RLock()
	defer p.t.mu.RUnlock()
	out := make(map[string]dbus.Variant, len(names))
	for _, name := range names {
		out[name] = dbus.MakeVariant(p.value(name))
	}
	return out, nil
}

// value reads one property under the caller's read lock.
func (p *linuxProps) value(name string) any {
	if p.iface == menuIface {
		switch name {
		case "Version":
			return uint32(3)
		case "TextDirection":
			return "ltr"
		case "Status":
			return "normal"
		case "IconThemePath":
			return []string{}
		}
		return nil
	}
	s := &p.t.state
	switch name {
	case "Category":
		return "ApplicationStatus"
	case "Id":
		return s.ID
	case "Title":
		return s.Title
	case "Status":
		return "Active"
	case "WindowId":
		return uint32(0)
	case "IconPixmap":
		return p.t.pixmap
	case "IconName":
		return ""
	case "Menu":
		return menuPath
	case "ItemIsMenu":
		return !s.HasActivation && len(s.Menu) > 0
	case "ToolTip":
		return linuxToolTip{"", nil, s.ToolTip, ""}
	}
	return nil
}

func variants(values map[string]any) map[string]dbus.Variant {
	out := make(map[string]dbus.Variant, len(values))
	for key, value := range values {
		out[key] = dbus.MakeVariant(value)
	}
	return out
}

func (p *linuxProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface != p.iface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{iface})
	}
	p.t.mu.RLock()
	value := p.value(name)
	p.t.mu.RUnlock()
	if value != nil {
		return dbus.MakeVariant(value), nil
	}
	return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{name})
}

func (p *linuxProps) Set(_, name string, _ dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []any{name})
}

type linuxItem struct{ t *linuxTray }

func (i *linuxItem) Activate(_, _ int32) *dbus.Error        { i.t.click(0); return nil }
func (*linuxItem) SecondaryActivate(_, _ int32) *dbus.Error { return nil }
func (*linuxItem) ContextMenu(_, _ int32) *dbus.Error       { return nil }
func (*linuxItem) Scroll(_ int32, _ string) *dbus.Error     { return nil }

type linuxMenu struct{ t *linuxTray }

type menuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}
type menuProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}
type menuEvent struct {
	ID        int32
	Event     string
	Data      dbus.Variant
	Timestamp uint32
}

func menuEntry(item MenuItem, names []string) map[string]dbus.Variant {
	values := map[string]any{"label": item.Label, "enabled": !item.Disabled, "visible": true}
	if item.Separator {
		values["type"] = "separator"
	}
	if item.ID == 0 || len(item.Submenu) > 0 {
		values["children-display"] = "submenu"
	}
	if item.Checkable {
		values["toggle-type"] = "checkmark"
		var state int32
		if item.Checked {
			state = 1
		}
		values["toggle-state"] = state
	}
	if len(names) == 0 {
		return variants(values)
	}
	out := make(map[string]dbus.Variant)
	for _, key := range names {
		if value, ok := values[key]; ok {
			out[key] = dbus.MakeVariant(value)
		}
	}
	return out
}

func findMenu(id int32, items []MenuItem) (MenuItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
		if sub, ok := findMenu(id, item.Submenu); ok {
			return sub, true
		}
	}
	return MenuItem{}, false
}

func (m *linuxMenu) root(id int32) (MenuItem, uint32, bool) {
	m.t.mu.RLock()
	defer m.t.mu.RUnlock()
	if id == 0 {
		return MenuItem{Submenu: m.t.state.Menu}, m.t.revision, true
	}
	item, ok := findMenu(id, m.t.state.Menu)
	return item, m.t.revision, ok
}

func buildMenuLayout(item MenuItem, depth int32, names []string) menuLayout {
	layout := menuLayout{ID: item.ID, Properties: menuEntry(item, names)}
	if depth != 0 {
		for _, child := range item.Submenu {
			layout.Children = append(layout.Children, dbus.MakeVariant(buildMenuLayout(child, depth-1, names)))
		}
	}
	return layout
}

func (m *linuxMenu) GetLayout(id, depth int32, names []string) (uint32, menuLayout, *dbus.Error) {
	item, rev, ok := m.root(id)
	if !ok {
		return 0, menuLayout{}, invalidMenuItem()
	}
	return rev, buildMenuLayout(item, depth, names), nil
}

func (m *linuxMenu) GetGroupProperties(ids []int32, names []string) ([]menuProperties, *dbus.Error) {
	root, _, _ := m.root(0)
	var props []menuProperties
	var add func(MenuItem)
	add = func(item MenuItem) {
		props = append(props, menuProperties{item.ID, menuEntry(item, names)})
		if len(ids) == 0 {
			for _, sub := range item.Submenu {
				add(sub)
			}
		}
	}
	if len(ids) == 0 {
		add(root)
	}
	for _, id := range ids {
		if id == 0 {
			add(root)
		} else if item, ok := findMenu(id, root.Submenu); ok {
			add(item)
		}
	}
	return props, nil
}

func (m *linuxMenu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	if item, _, ok := m.root(id); ok {
		if v, ok := menuEntry(item, []string{name})[name]; ok {
			return v, nil
		}
	}
	return dbus.Variant{}, invalidMenuItem()
}

func (m *linuxMenu) Event(id int32, event string, _ dbus.Variant, _ uint32) *dbus.Error {
	item, _, ok := m.root(id)
	if !ok {
		return invalidMenuItem()
	}
	if event == "clicked" && id != 0 && !item.Disabled && !item.Separator && len(item.Submenu) == 0 {
		m.t.click(id)
	}
	return nil
}
func (m *linuxMenu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	var failed []int32
	for _, event := range events {
		if m.Event(event.ID, event.Event, event.Data, event.Timestamp) != nil {
			failed = append(failed, event.ID)
		}
	}
	return failed, nil
}
func (m *linuxMenu) AboutToShow(id int32) (bool, *dbus.Error) {
	if _, _, ok := m.root(id); !ok {
		return false, invalidMenuItem()
	}
	return false, nil
}
func (m *linuxMenu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	var failed []int32
	for _, id := range ids {
		if _, _, ok := m.root(id); !ok {
			failed = append(failed, id)
		}
	}
	return nil, failed, nil
}
func invalidMenuItem() *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"Unknown menu item or property"})
}
