//go:build linux && (amd64 || arm64)

package platform

import (
	"fmt"
	"image"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const atspiPrefix = "org.a11y.atspi."
const atspiBase dbusPath = "/org/a11y/atspi/accessible"
const atspiRoot dbusPath = "/org/a11y/atspi/accessible/root"
const atspiWindow = ^uint64(0)
const atspiRegistryPath dbusPath = "/org/a11y/atspi/registry"

type atspiRef struct {
	Bus  string
	Path dbusPath
}
type atspiRect struct{ X, Y, Width, Height int32 }
type atspiRelation struct {
	Kind    uint32
	Targets []atspiRef
}
type atspiActionInfo struct{ Name, Description, Key string }
type atspiNodeMeta struct {
	id, parent uint64
	role       string
	min, max   float64
}
type atspiNodeText struct{ label, value string }
type atspiNodeState struct {
	disabled, focused, checked, selected bool
	number                               float64
	caret, selectionStart, selectionEnd  int
}

func atspiStateOf(n AccessNode) atspiNodeState {
	return atspiNodeState{n.Disabled, n.Focused, n.Checked, n.Selected, n.Number, n.Caret, n.SelectionStart, n.SelectionEnd}
}

type atspiTree struct {
	nodes     []atspiNodeMeta
	text      []atspiNodeText
	bounds    []image.Rectangle
	states    []atspiNodeState
	positions map[uint64]int
	children  map[uint64][]uint64
	order     []uint64
	index     map[uint64]int32
	origin    image.Point
}

type atspiSubscriptions struct{ events map[string]bool }

type atspiBridge struct {
	conn      *dbusConn
	stop      chan struct{}
	name      string
	tree      atomic.Pointer[atspiTree]
	desktop   atomic.Pointer[atspiRef]
	appID     atomic.Int32
	listeners atomic.Pointer[atspiSubscriptions]
	mu        sync.Mutex
	queue     []Event
	closed    bool
	wake      func()
	// Without event listeners, frames only replace pending nodes; queries
	// publish them on demand instead of rebuilding a snapshot every frame.
	publishMu sync.Mutex
	pending   atspiPending
	dirty     atomic.Bool
}

type atspiPending struct {
	title  string
	bounds image.Rectangle
	origin image.Point
	nodes  []AccessNode
}

// Discovery never starts a session or accessibility bus for an ordinary window.
func atspiAddress() string {
	if address := os.Getenv("AT_SPI_BUS_ADDRESS"); address != "" {
		return address
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return ""
	}
	c, err := dbusConnect(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		return ""
	}
	defer c.Close()
	reply, err := c.call(250*time.Millisecond, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "NameHasOwner", 0, "org.a11y.Bus")
	if err != nil || len(reply) != 1 || reply[0] != true {
		return ""
	}
	reply, err = c.call(250*time.Millisecond, "org.a11y.Bus", "/org/a11y/bus", "org.a11y.Bus", "GetAddress", dbusNoAutoStart)
	if err != nil || len(reply) != 1 {
		return ""
	}
	address, _ := reply[0].(string)
	return address
}

func newATSPI(address, title string, bounds image.Rectangle, wake func()) (*atspiBridge, error) {
	c, err := dbusConnect(address)
	if err != nil {
		return nil, err
	}
	a := &atspiBridge{conn: c, name: c.name, wake: wake, stop: make(chan struct{})}
	a.update(title, bounds, image.Point{}, nil)
	objects := a.objects()
	objects["org.freedesktop.DBus.Introspectable"] = atspiIntrospection{a}
	c.Export(atspiBase, objects)
	// The registry may start on an already-running accessibility bus.
	reply, err := c.call(400*time.Millisecond, atspiPrefix+"Registry", atspiRoot, atspiPrefix+"Socket", "Embed", 0, a.ref(0))
	if err == nil && len(reply) == 1 {
		if desktop, ok := atspiRefOf(reply[0]); ok {
			a.desktop.Store(&desktop)
		}
	}
	if err := a.watchSubscriptions(); err != nil {
		a.close()
		return nil, err
	}
	return a, nil
}

func atspiRefOf(v any) (atspiRef, bool) {
	fields, ok := v.([]any)
	if !ok || len(fields) != 2 {
		return atspiRef{}, false
	}
	bus, ok1 := fields[0].(string)
	path, ok2 := fields[1].(dbusPath)
	return atspiRef{bus, path}, ok1 && ok2
}

// Registry subscriptions, unlike D-Bus match rules, identify actual assistive
// technology interest. Keep the query tree live even when no events are needed.
func (a *atspiBridge) watchSubscriptions() error {
	changed := make(chan struct{}, 1)
	onSignal := func(m *dbusMessage) {
		if m.iface == atspiPrefix+"Registry" && (m.member == "EventListenerRegistered" || m.member == "EventListenerDeregistered") || m.iface == "org.freedesktop.DBus" && m.member == "NameOwnerChanged" {
			// The reader goroutine must not wait for a reply; refresh elsewhere.
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}
	a.conn.onSignal.Store(&onSignal)
	if err := a.conn.AddMatch("type='signal',sender='" + atspiPrefix + "Registry',path='" + string(atspiRegistryPath) + "',interface='" + atspiPrefix + "Registry'"); err != nil {
		return err
	}
	if err := a.conn.AddMatch("type='signal',sender='org.freedesktop.DBus',interface='org.freedesktop.DBus',member='NameOwnerChanged',arg0='" + atspiPrefix + "Registry'"); err != nil {
		return err
	}
	// Subscribe before the initial query so a registration cannot be lost between them.
	a.refreshSubscriptions()
	go func() {
		for {
			select {
			case <-changed:
				// Re-querying preserves application-specific registration filters and handles
				// listener disconnection, duplicate registration and deregistration prefixes.
				a.refreshSubscriptions()
			case <-a.stop:
				return
			}
		}
	}()
	return nil
}
func (a *atspiBridge) refreshSubscriptions() {
	reply, err := a.conn.call(400*time.Millisecond, atspiPrefix+"Registry", atspiRegistryPath, atspiPrefix+"Registry", "GetRegisteredEvents", dbusNoAutoStart)
	subscriptions := &atspiSubscriptions{events: make(map[string]bool)}
	if err == nil && len(reply) == 1 {
		registrations, _ := reply[0].([]any)
		for _, registration := range registrations {
			if fields, ok := registration.([]any); ok && len(fields) == 2 {
				if event, ok := fields[1].(string); ok {
					subscriptions.events[atspiEventName(event)] = true
				}
			}
		}
	}
	a.listeners.Store(subscriptions)
}
func atspiEventName(event string) string {
	event = strings.ToLower(strings.ReplaceAll(event, "-", ""))
	fields := strings.Split(event, ":")
	for i, field := range fields {
		if field == "" {
			fields = fields[:i]
			break
		}
	}
	return strings.Join(fields, ":")
}
func (a *atspiBridge) wants(name, detail string) bool {
	subscriptions := a.listeners.Load()
	if subscriptions == nil || len(subscriptions.events) == 0 {
		return false
	}
	event := atspiEventName("object:" + name + ":" + detail)
	for {
		if subscriptions.events[event] {
			return true
		}
		at := strings.LastIndexByte(event, ':')
		if at < 0 {
			return subscriptions.events[""]
		}
		event = event[:at]
	}
}

func (a *atspiBridge) objects() map[string]any {
	return map[string]any{
		atspiPrefix + "Accessible":        atspiAccessible{a},
		atspiPrefix + "Component":         atspiComponent{a},
		atspiPrefix + "Action":            atspiAction{a},
		atspiPrefix + "Application":       atspiApplication{a},
		atspiPrefix + "Text":              atspiText{a},
		atspiPrefix + "EditableText":      atspiEditable{a},
		"org.freedesktop.DBus.Properties": atspiProperties{a},
	}
}
func (a *atspiBridge) ref(id uint64) atspiRef {
	path := dbusPath(string(atspiBase) + "/n" + strconv.FormatUint(id, 10))
	if id == 0 {
		path = atspiRoot
	}
	return atspiRef{a.name, path}
}
func atspiNull() atspiRef { return atspiRef{"", "/org/a11y/atspi/null"} }
func atspiError(s string) *dbusError {
	return &dbusError{"org.freedesktop.DBus.Error.InvalidArgs", s}
}
func (a *atspiBridge) lookup(msg *dbusMessage) (*atspiTree, AccessNode, *dbusError) {
	path := msg.path
	var id uint64
	var err error
	if path != atspiRoot {
		s, ok := strings.CutPrefix(string(path), string(atspiBase)+"/n")
		if !ok {
			return nil, AccessNode{}, atspiError("unknown accessible")
		}
		id, err = strconv.ParseUint(s, 10, 64)
	}
	t := a.current()
	n, ok := t.node(id)
	if err != nil || !ok {
		return nil, AccessNode{}, atspiError("defunct accessible")
	}
	return t, n, nil
}
func (a *atspiBridge) parent(n AccessNode) atspiRef {
	if n.ID == 0 {
		if p := a.desktop.Load(); p != nil {
			return *p
		}
		return atspiNull()
	}
	return a.ref(n.Parent)
}
func (a *atspiBridge) action(n AccessNode, action, value string) bool {
	if n.ID == 0 || n.ID == atspiWindow || n.Disabled {
		return false
	}
	a.mu.Lock()
	if a.closed || len(a.queue) >= 64 {
		a.mu.Unlock()
		return false
	}
	a.queue = append(a.queue, Event{Kind: AccessAction, AccessID: n.ID, Action: action, Text: value})
	a.mu.Unlock()
	a.wake()
	return true
}
func (a *atspiBridge) drain(emit func(Event)) {
	a.mu.Lock()
	queue := a.queue
	a.queue = nil
	a.mu.Unlock()
	for _, e := range queue {
		if n, ok := a.current().node(e.AccessID); ok && !n.Disabled {
			emit(e)
		}
	}
}
func (a *atspiBridge) close() {
	a.mu.Lock()
	if !a.closed {
		close(a.stop)
	}
	a.closed = true
	a.queue = nil
	a.mu.Unlock()
	a.conn.Close()
}
func (a *atspiBridge) signal(id uint64, name, detail string, arg1, arg2 int32, value any) {
	if !a.wants(name, detail) {
		return
	}
	_ = a.conn.Emit(a.ref(id).Path, atspiPrefix+"Event.Object", name, detail, arg1, arg2, dbusMakeVariant(value), map[string]dbusVariant{})
}

// set accepts borrowed nodes from the UI thread.
func (a *atspiBridge) set(title string, bounds image.Rectangle, origin image.Point, nodes []AccessNode) {
	a.publishMu.Lock()
	defer a.publishMu.Unlock()
	if s := a.listeners.Load(); s != nil && len(s.events) > 0 {
		// Event diffs must follow every change in order.
		a.dirty.Store(false)
		a.update(title, bounds, origin, nodes)
		return
	}
	p := &a.pending
	p.title, p.bounds, p.origin = title, bounds, origin
	clear(p.nodes)
	p.nodes = append(p.nodes[:0], nodes...)
	a.dirty.Store(true)
}

// current returns the latest snapshot, publishing pending nodes first.
func (a *atspiBridge) current() *atspiTree {
	if a.dirty.Load() {
		a.publishMu.Lock()
		if a.dirty.Load() {
			p := &a.pending
			a.update(p.title, p.bounds, p.origin, p.nodes)
			a.dirty.Store(false)
		}
		a.publishMu.Unlock()
	}
	return a.tree.Load()
}

func (a *atspiBridge) update(title string, bounds image.Rectangle, origin image.Point, nodes []AccessNode) {
	old := a.tree.Load()
	stable := old != nil && len(old.nodes) == len(nodes)+2
	textChanged, boundsChanged, stateChanged, metadataChanged := false, false, false, false
	if stable {
		textChanged, boundsChanged = old.text[0].label != title, old.bounds[0] != bounds
		for i := range nodes {
			n := &nodes[i]
			parent := n.Parent
			if parent == 0 {
				parent = atspiWindow
			}
			prior := &old.nodes[i+2]
			if n.ID != prior.id || parent != prior.parent {
				stable = false
				break
			}
			textChanged = textChanged || n.Label != old.text[i+2].label || n.Value != old.text[i+2].value
			boundsChanged = boundsChanged || n.Bounds != old.bounds[i+2]
			stateChanged = stateChanged || atspiStateOf(*n) != old.states[i+2]
			metadataChanged = metadataChanged || n.Role != prior.role || n.Min != prior.min || n.Max != prior.max
		}
	}
	if stable && old.origin == origin && !textChanged && !boundsChanged && !stateChanged && !metadataChanged {
		return
	}
	t := &atspiTree{origin: origin}
	if stable {
		// Published topology is immutable, so readers can share it across snapshots.
		t.positions, t.children, t.index, t.order = old.positions, old.children, old.index, old.order
	} else {
		t.positions = map[uint64]int{0: 0, atspiWindow: 1}
		t.children = map[uint64][]uint64{0: {atspiWindow}}
		t.index = map[uint64]int32{0: -1, atspiWindow: 0}
		t.order = make([]uint64, 2, len(nodes)+2)
		t.order[1] = atspiWindow
	}
	if stable {
		// Copy only changed columns; every published backing array stays immutable.
		t.nodes, t.text, t.bounds, t.states = old.nodes, old.text, old.bounds, old.states
		if metadataChanged {
			t.nodes = make([]atspiNodeMeta, len(nodes)+2)
			t.nodes[0], t.nodes[1] = atspiNodeMeta{role: "application"}, atspiNodeMeta{id: atspiWindow, role: "window"}
		}
		if textChanged {
			t.text = make([]atspiNodeText, len(nodes)+2)
			t.text[0], t.text[1] = atspiNodeText{label: title}, atspiNodeText{label: title}
		}
		if boundsChanged {
			t.bounds = make([]image.Rectangle, len(nodes)+2)
			t.bounds[0], t.bounds[1] = bounds, bounds
		}
		if stateChanged {
			t.states = make([]atspiNodeState, len(nodes)+2)
		}
		for i := range nodes {
			n := &nodes[i]
			if metadataChanged {
				parent := n.Parent
				if parent == 0 {
					parent = atspiWindow
				}
				t.nodes[i+2] = atspiNodeMeta{n.ID, parent, n.Role, n.Min, n.Max}
			}
			if textChanged {
				t.text[i+2] = atspiNodeText{n.Label, n.Value}
			}
			if boundsChanged {
				t.bounds[i+2] = n.Bounds
			}
			if stateChanged {
				t.states[i+2] = atspiStateOf(*n)
			}
		}
	} else {
		t.nodes = make([]atspiNodeMeta, 2, len(nodes)+2)
		t.text = make([]atspiNodeText, 2, len(nodes)+2)
		t.bounds = make([]image.Rectangle, 2, len(nodes)+2)
		t.states = make([]atspiNodeState, 2, len(nodes)+2)
		t.nodes[0], t.nodes[1] = atspiNodeMeta{role: "application"}, atspiNodeMeta{id: atspiWindow, role: "window"}
		t.text[0], t.text[1] = atspiNodeText{label: title}, atspiNodeText{label: title}
		t.bounds[0], t.bounds[1] = bounds, bounds
		for _, n := range nodes {
			if n.ID == 0 || n.ID == atspiWindow {
				continue
			}
			if n.Parent == 0 {
				n.Parent = atspiWindow
			}
			if !stable {
				if _, duplicate := t.positions[n.ID]; duplicate {
					continue
				}
				t.positions[n.ID] = len(t.nodes)
				t.order = append(t.order, n.ID)
				t.index[n.ID] = int32(len(t.children[n.Parent]))
				t.children[n.Parent] = append(t.children[n.Parent], n.ID)
			}
			t.nodes = append(t.nodes, atspiNodeMeta{n.ID, n.Parent, n.Role, n.Min, n.Max})
			t.text = append(t.text, atspiNodeText{n.Label, n.Value})
			t.bounds = append(t.bounds, n.Bounds)
			t.states = append(t.states, atspiStateOf(n))
		}
	}
	a.tree.Store(t)
	if subscriptions := a.listeners.Load(); old == nil || subscriptions == nil || len(subscriptions.events) == 0 {
		return
	}
	for _, id := range old.order {
		n, _ := old.node(id)
		if current, exists := t.node(id); !exists || current.Parent != n.Parent || atspiIndex(t, current) != atspiIndex(old, n) {
			a.signal(n.Parent, "ChildrenChanged", "remove", atspiIndex(old, n), 0, a.ref(id))
		}
	}
	for _, id := range t.order {
		n, _ := t.node(id)
		prior, exists := old.node(id)
		if exists && n == prior && t.origin == old.origin && atspiIndex(t, n) == atspiIndex(old, prior) {
			continue
		}
		if !exists || prior.Parent != n.Parent || atspiIndex(t, n) != atspiIndex(old, prior) {
			a.signal(n.Parent, "ChildrenChanged", "add", atspiIndex(t, n), 0, a.ref(id))
		}
		if !exists {
			continue
		}
		if n.Label != prior.Label {
			a.signal(id, "PropertyChange", "accessible-name", 0, 0, n.Label)
		}
		if atspiIsText(n) && atspiTextValue(n) != atspiTextValue(prior) {
			before, after := atspiTextValue(prior), atspiTextValue(n)
			if before != "" {
				a.signal(id, "TextChanged", "delete", 0, int32(utf8.RuneCountInString(before)), before)
			}
			if after != "" {
				a.signal(id, "TextChanged", "insert", 0, int32(utf8.RuneCountInString(after)), after)
			}
		} else if n.Value != prior.Value {
			a.signal(id, "PropertyChange", "accessible-value", 0, 0, n.Value)
		}
		if n.Bounds != prior.Bounds || t.origin != old.origin {
			r := n.Bounds.Add(t.origin)
			a.signal(id, "BoundsChanged", "", 0, 0, atspiRect{int32(r.Min.X), int32(r.Min.Y), int32(r.Dx()), int32(r.Dy())})
		}
		if n.Caret != prior.Caret {
			a.signal(id, "TextCaretMoved", "", int32(n.Caret), 0, int32(0))
		}
		if n.SelectionStart != prior.SelectionStart || n.SelectionEnd != prior.SelectionEnd {
			a.signal(id, "TextSelectionChanged", "", 0, 0, int32(0))
		}
		if n.Number != prior.Number {
			a.signal(id, "PropertyChange", "accessible-value", 0, 0, n.Number)
		}
		for _, s := range []struct {
			name          string
			before, after bool
		}{{"focused", prior.Focused, n.Focused}, {"checked", prior.Checked, n.Checked}, {"selected", prior.Selected, n.Selected}, {"enabled", !prior.Disabled, !n.Disabled}, {"sensitive", !prior.Disabled, !n.Disabled}} {
			if s.before != s.after {
				var enabled int32
				if s.after {
					enabled = 1
				}
				a.signal(id, "StateChanged", s.name, enabled, 0, int32(0))
			}
		}
	}
}
func (t *atspiTree) node(id uint64) (AccessNode, bool) {
	index, ok := t.positions[id]
	if !ok {
		return AccessNode{}, false
	}
	return t.nodeAt(index), true
}

func (t *atspiTree) nodeAt(index int) AccessNode {
	m, text, state := t.nodes[index], t.text[index], t.states[index]
	return AccessNode{ID: m.id, Parent: m.parent, Role: m.role, Label: text.label, Value: text.value, Bounds: t.bounds[index], Disabled: state.disabled, Focused: state.focused, Checked: state.checked, Selected: state.selected, Min: m.min, Max: m.max, Number: state.number, Caret: state.caret, SelectionStart: state.selectionStart, SelectionEnd: state.selectionEnd}
}
func atspiIndex(t *atspiTree, n AccessNode) int32 {
	if index, ok := t.index[n.ID]; ok {
		return index
	}
	return -1
}
func atspiIsText(n AccessNode) bool {
	return n.Role == "input" || n.Role == "textbox" || n.Role == "text" || n.Role == "entry" || n.Role == "editor" || n.Role == "label"
}
func atspiEditableNode(n AccessNode) bool {
	return n.Role == "input" || n.Role == "textbox" || n.Role == "entry" || n.Role == "editor"
}
func atspiActionable(n AccessNode) bool {
	switch n.Role {
	case "button", "checkbox", "switch", "radio", "tab", "menuitem", "link", "listitem", "treeitem", "row", "cell":
		return true
	}
	return false
}
func atspiHasValue(n AccessNode) bool {
	return n.Role == "slider" || n.Role == "progress" || n.Role == "spinbutton"
}
func atspiInterfaces(n AccessNode) []string {
	out := []string{atspiPrefix + "Accessible", atspiPrefix + "Component"}
	if n.ID == 0 {
		out = append(out, atspiPrefix+"Application")
	}
	if atspiActionable(n) {
		out = append(out, atspiPrefix+"Action")
	}
	if atspiIsText(n) {
		out = append(out, atspiPrefix+"Text")
	}
	if atspiEditableNode(n) {
		out = append(out, atspiPrefix+"EditableText")
	}
	if atspiHasValue(n) {
		out = append(out, atspiPrefix+"Value")
	}
	return out
}
func atspiRole(role string) uint32 {
	switch role {
	case "application":
		return 75
	case "window":
		return 23
	case "button":
		return 43
	case "checkbox":
		return 7
	case "switch":
		return 62
	case "radio":
		return 44
	case "input", "textbox", "entry":
		return 79
	case "editor", "text":
		return 61
	case "label":
		return 29
	case "slider":
		return 51
	case "spinbutton":
		return 52
	case "progress":
		return 42
	case "list":
		return 31
	case "listitem":
		return 32
	case "table":
		return 55
	case "row":
		return 90
	case "cell":
		return 56
	case "tree":
		return 65
	case "treeitem":
		return 91
	case "tab":
		return 37
	case "tablist":
		return 38
	case "combobox":
		return 11
	case "menu":
		return 33
	case "menuitem":
		return 35
	case "separator":
		return 50
	case "image":
		return 27
	case "link":
		return 88
	}
	return 39
}
func atspiStates(n AccessNode) []uint32 {
	states := []uint32{1<<25 | 1<<30, 0}
	set := func(i uint) { states[i/32] |= 1 << (i % 32) }
	if !n.Disabled {
		set(8)
		set(24)
	}
	if atspiActionable(n) || atspiEditableNode(n) || n.Role == "slider" || n.Role == "combobox" || n.Role == "list" || n.Role == "tree" || n.Role == "table" {
		set(11)
	}
	if n.Focused {
		set(12)
	}
	if n.Checked {
		set(4)
	}
	if n.Selected {
		set(23)
	}
	if n.Role == "checkbox" || n.Role == "switch" || n.Role == "radio" {
		set(41)
	}
	if n.Role == "listitem" || n.Role == "treeitem" || n.Role == "tab" || n.Role == "row" {
		set(22)
	}
	if atspiEditableNode(n) {
		set(7)
		set(38)
		if strings.Contains(n.Value, "\n") || n.Role == "editor" {
			set(17)
		} else {
			set(26)
		}
	}
	return states
}

type atspiAccessible struct{ a *atspiBridge }

func (o atspiAccessible) GetChildren(m *dbusMessage) ([]atspiRef, *dbusError) {
	t, n, e := o.a.lookup(m)
	if e != nil {
		return nil, e
	}
	r := make([]atspiRef, 0, len(t.children[n.ID]))
	for _, id := range t.children[n.ID] {
		r = append(r, o.a.ref(id))
	}
	return r, nil
}
func (o atspiAccessible) GetChildAtIndex(m *dbusMessage, index int32) (atspiRef, *dbusError) {
	r, e := o.GetChildren(m)
	if e != nil {
		return atspiNull(), e
	}
	if index < 0 || int(index) >= len(r) {
		return atspiNull(), atspiError("child index out of range")
	}
	return r[index], nil
}
func (o atspiAccessible) GetIndexInParent(m *dbusMessage) (int32, *dbusError) {
	t, n, e := o.a.lookup(m)
	if e != nil {
		return -1, e
	}
	return atspiIndex(t, n), nil
}
func (o atspiAccessible) GetRelationSet(m *dbusMessage) ([]atspiRelation, *dbusError) {
	_, _, e := o.a.lookup(m)
	return []atspiRelation{}, e
}
func (o atspiAccessible) GetRole(m *dbusMessage) (uint32, *dbusError) {
	_, n, e := o.a.lookup(m)
	return atspiRole(n.Role), e
}
func (o atspiAccessible) GetRoleName(m *dbusMessage) (string, *dbusError) {
	_, n, e := o.a.lookup(m)
	return n.Role, e
}
func (o atspiAccessible) GetLocalizedRoleName(m *dbusMessage) (string, *dbusError) {
	return o.GetRoleName(m)
}
func (o atspiAccessible) GetState(m *dbusMessage) ([]uint32, *dbusError) {
	_, n, e := o.a.lookup(m)
	return atspiStates(n), e
}
func (o atspiAccessible) GetAttributes(m *dbusMessage) (map[string]string, *dbusError) {
	_, _, e := o.a.lookup(m)
	return map[string]string{"toolkit": "fego"}, e
}
func (o atspiAccessible) GetApplication(m *dbusMessage) (atspiRef, *dbusError) {
	_, _, e := o.a.lookup(m)
	return o.a.ref(0), e
}
func (o atspiAccessible) GetInterfaces(m *dbusMessage) ([]string, *dbusError) {
	_, n, e := o.a.lookup(m)
	return atspiInterfaces(n), e
}

type atspiComponent struct{ a *atspiBridge }

func atspiBounds(t *atspiTree, n AccessNode, coord uint32) (image.Rectangle, *dbusError) {
	r := n.Bounds
	switch coord {
	case 0:
		r = r.Add(t.origin)
	case 1:
	case 2:
		parent, _ := t.node(n.Parent)
		r = r.Sub(parent.Bounds.Min)
	default:
		return r, atspiError("invalid coordinate type")
	}
	return r, nil
}
func (o atspiComponent) GetExtents(m *dbusMessage, coord uint32) (atspiRect, *dbusError) {
	t, n, e := o.a.lookup(m)
	if e != nil {
		return atspiRect{}, e
	}
	r, e := atspiBounds(t, n, coord)
	return atspiRect{int32(r.Min.X), int32(r.Min.Y), int32(r.Dx()), int32(r.Dy())}, e
}
func (o atspiComponent) GetPosition(m *dbusMessage, coord uint32) (int32, int32, *dbusError) {
	r, e := o.GetExtents(m, coord)
	return r.X, r.Y, e
}
func (o atspiComponent) GetSize(m *dbusMessage) (int32, int32, *dbusError) {
	r, e := o.GetExtents(m, 1)
	return r.Width, r.Height, e
}
func (o atspiComponent) Contains(m *dbusMessage, x, y int32, coord uint32) (bool, *dbusError) {
	r, e := o.GetExtents(m, coord)
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height, e
}
func (o atspiComponent) GetAccessibleAtPoint(m *dbusMessage, x, y int32, coord uint32) (atspiRef, *dbusError) {
	t, n, e := o.a.lookup(m)
	if e != nil {
		return atspiNull(), e
	}
	r, e := atspiBounds(t, n, coord)
	if e != nil {
		return atspiNull(), e
	}
	p := image.Pt(int(x), int(y)).Sub(r.Min).Add(n.Bounds.Min)
	if !p.In(n.Bounds) {
		return atspiNull(), nil
	}
	result := n.ID
	for _, id := range t.children[n.ID] {
		child, _ := t.node(id)
		if p.In(child.Bounds) {
			result = id
		}
	}
	return o.a.ref(result), nil
}
func (o atspiComponent) GrabFocus(m *dbusMessage) (bool, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return false, e
	}
	return o.a.action(n, "focus", ""), nil
}
func (o atspiComponent) GetLayer(m *dbusMessage) (uint32, *dbusError) {
	_, n, e := o.a.lookup(m)
	if n.ID == atspiWindow {
		return 7, e
	}
	return 3, e
}
func (o atspiComponent) GetMDIZOrder(m *dbusMessage) (int16, *dbusError) {
	_, _, e := o.a.lookup(m)
	return -1, e
}
func (o atspiComponent) GetAlpha(m *dbusMessage) (float64, *dbusError) {
	_, _, e := o.a.lookup(m)
	return 1, e
}

type atspiAction struct{ a *atspiBridge }

func (o atspiAction) GetName(m *dbusMessage, i int32) (string, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return "", e
	}
	if i != 0 || !atspiActionable(n) {
		return "", atspiError("action index out of range")
	}
	return "press", nil
}
func (o atspiAction) GetLocalizedName(m *dbusMessage, i int32) (string, *dbusError) {
	return o.GetName(m, i)
}
func (o atspiAction) GetDescription(m *dbusMessage, i int32) (string, *dbusError) {
	return o.GetName(m, i)
}
func (o atspiAction) GetKeyBinding(m *dbusMessage, i int32) (string, *dbusError) {
	_, e := o.GetName(m, i)
	return "", e
}
func (o atspiAction) GetActions(m *dbusMessage) ([]atspiActionInfo, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return nil, e
	}
	if !atspiActionable(n) {
		return []atspiActionInfo{}, nil
	}
	return []atspiActionInfo{{"press", "press", ""}}, nil
}
func (o atspiAction) DoAction(m *dbusMessage, i int32) (bool, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return false, e
	}
	if i != 0 || !atspiActionable(n) {
		return false, nil
	}
	return o.a.action(n, "press", ""), nil
}

type atspiApplication struct{ a *atspiBridge }

func (o atspiApplication) GetLocale(_ uint32) (string, *dbusError)        { return atspiLocale(), nil }
func (o atspiApplication) GetApplicationBusAddress() (string, *dbusError) { return "", nil }
func atspiLocale() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if s := os.Getenv(k); s != "" {
			return s
		}
	}
	return "C"
}

type atspiProperties struct{ a *atspiBridge }

func (o atspiProperties) values(m *dbusMessage, iface string) (map[string]dbusVariant, *dbusError) {
	t, n, e := o.a.lookup(m)
	if e != nil {
		return nil, e
	}
	values := map[string]any{"version": uint32(1)}
	switch iface {
	case atspiPrefix + "Accessible":
		values["Name"] = n.Label
		values["Description"] = ""
		values["HelpText"] = ""
		values["Parent"] = o.a.parent(n)
		values["ChildCount"] = int32(len(t.children[n.ID]))
		values["Locale"] = atspiLocale()
		values["AccessibleId"] = strconv.FormatUint(n.ID, 10)
	case atspiPrefix + "Application":
		if n.ID != 0 {
			return nil, atspiError("not an application")
		}
		values["ToolkitName"] = "fego"
		values["Version"] = "1"
		values["ToolkitVersion"] = "1"
		values["AtspiVersion"] = "2.1"
		values["InterfaceVersion"] = uint32(1)
		values["Id"] = o.a.appID.Load()
	case atspiPrefix + "Action":
		var count int32
		if atspiActionable(n) {
			count = 1
		}
		values["NActions"] = count
	case atspiPrefix + "Text":
		if !atspiIsText(n) {
			return nil, atspiError("not text")
		}
		values["CharacterCount"] = int32(utf8.RuneCountInString(atspiTextValue(n)))
		values["CaretOffset"] = int32(n.Caret)
	case atspiPrefix + "Value":
		if !atspiHasValue(n) {
			return nil, atspiError("not a value")
		}
		values["MinimumValue"] = n.Min
		values["MaximumValue"] = n.Max
		values["CurrentValue"] = n.Number
		values["MinimumIncrement"] = float64(0)
		values["Text"] = n.Value
	case atspiPrefix + "Component", atspiPrefix + "EditableText":
	default:
		return nil, atspiError("unknown interface")
	}
	r := make(map[string]dbusVariant, len(values))
	for k, v := range values {
		r[k] = dbusMakeVariant(v)
	}
	return r, nil
}
func (o atspiProperties) Get(m *dbusMessage, iface, name string) (dbusVariant, *dbusError) {
	v, e := o.values(m, iface)
	if e != nil {
		return dbusMakeVariant(""), e
	}
	r, ok := v[name]
	if !ok {
		return dbusMakeVariant(""), atspiError("unknown property")
	}
	return r, nil
}
func (o atspiProperties) GetAll(m *dbusMessage, iface string) (map[string]dbusVariant, *dbusError) {
	return o.values(m, iface)
}
func (o atspiProperties) Set(m *dbusMessage, iface, name string, value dbusVariant) *dbusError {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return e
	}
	if n.ID == 0 && iface == atspiPrefix+"Application" && name == "Id" {
		v, ok := value.value.(int32)
		if !ok {
			return atspiError("id must be int32")
		}
		o.a.appID.Store(v)
		return nil
	}
	if iface == atspiPrefix+"Value" && name == "CurrentValue" && atspiHasValue(n) && n.Role != "progress" {
		v, ok := value.value.(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < n.Min || v > n.Max {
			return atspiError("value outside range")
		}
		if o.a.action(n, "setValue", strconv.FormatFloat(v, 'g', -1, 64)) {
			return nil
		}
		return atspiError("value unavailable")
	}
	return &dbusError{"org.freedesktop.DBus.Error.PropertyReadOnly", "property is read only"}
}

type atspiText struct{ a *atspiBridge }

func atspiTextValue(n AccessNode) string {
	if n.Value != "" || atspiEditableNode(n) {
		return n.Value
	}
	return n.Label
}
func (o atspiText) runes(m *dbusMessage) ([]rune, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return nil, e
	}
	if !atspiIsText(n) {
		return nil, atspiError("not text")
	}
	return []rune(atspiTextValue(n)), nil
}
func (o atspiText) GetText(m *dbusMessage, start, end int32) (string, *dbusError) {
	r, e := o.runes(m)
	if e != nil {
		return "", e
	}
	if end == -1 {
		end = int32(len(r))
	}
	if start < 0 || end < start || int(end) > len(r) {
		return "", atspiError("text range out of bounds")
	}
	return string(r[start:end]), nil
}
func (o atspiText) GetCharacterAtOffset(m *dbusMessage, offset int32) (int32, *dbusError) {
	r, e := o.runes(m)
	if e != nil {
		return 0, e
	}
	if offset < 0 || int(offset) >= len(r) {
		return 0, atspiError("text offset out of bounds")
	}
	return r[offset], nil
}
func (o atspiText) GetStringAtOffset(m *dbusMessage, offset int32, granularity uint32) (string, int32, int32, *dbusError) {
	r, e := o.runes(m)
	if e != nil {
		return "", 0, 0, e
	}
	if offset < 0 || int(offset) > len(r) || granularity > 4 {
		return "", 0, 0, atspiError("invalid text offset or granularity")
	}
	start, end := int(offset), int(offset)
	if end < len(r) {
		end++
	}
	boundary := func(c rune) bool {
		if granularity == 1 {
			return unicode.IsSpace(c)
		}
		if granularity == 2 {
			return c == '.' || c == '!' || c == '?' || c == '。' || c == '！' || c == '？' || c == '\n'
		}
		return c == '\n'
	}
	if granularity > 0 {
		for start > 0 && !boundary(r[start-1]) {
			start--
		}
		for end < len(r) && !boundary(r[end-1]) {
			end++
		}
	}
	return string(r[start:end]), int32(start), int32(end), nil
}
func (o atspiText) GetNSelections(m *dbusMessage) (int32, *dbusError) {
	_, n, e := o.a.lookup(m)
	if n.SelectionStart != n.SelectionEnd {
		return 1, e
	}
	return 0, e
}
func (o atspiText) GetSelection(m *dbusMessage, index int32) (int32, int32, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return 0, 0, e
	}
	if index != 0 || n.SelectionStart == n.SelectionEnd {
		return 0, 0, atspiError("selection index out of range")
	}
	return int32(n.SelectionStart), int32(n.SelectionEnd), nil
}
func (o atspiText) SetCaretOffset(m *dbusMessage, offset int32) (bool, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return false, e
	}
	if !atspiEditableNode(n) || offset < 0 || int(offset) > utf8.RuneCountInString(n.Value) {
		return false, atspiError("caret offset out of range")
	}
	return o.a.action(n, "setCaret", strconv.FormatInt(int64(offset), 10)), nil
}
func (o atspiText) SetSelection(m *dbusMessage, index, start, end int32) (bool, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return false, e
	}
	if !atspiEditableNode(n) || index != 0 || start < 0 || end < start || int(end) > utf8.RuneCountInString(n.Value) {
		return false, atspiError("selection out of range")
	}
	return o.a.action(n, "setSelection", fmt.Sprintf("%d:%d", start, end)), nil
}
func (o atspiText) AddSelection(m *dbusMessage, start, end int32) (bool, *dbusError) {
	return o.SetSelection(m, 0, start, end)
}
func (o atspiText) RemoveSelection(m *dbusMessage, index int32) (bool, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return false, e
	}
	return o.SetSelection(m, index, int32(n.Caret), int32(n.Caret))
}
func (o atspiText) GetDefaultAttributes(m *dbusMessage) (map[string]string, *dbusError) {
	_, e := o.runes(m)
	return map[string]string{}, e
}
func (o atspiText) GetDefaultAttributeSet(m *dbusMessage) (map[string]string, *dbusError) {
	return o.GetDefaultAttributes(m)
}
func (o atspiText) GetAttributes(m *dbusMessage, offset int32) (map[string]string, int32, int32, *dbusError) {
	r, e := o.runes(m)
	if e == nil && (offset < 0 || int(offset) > len(r)) {
		e = atspiError("text offset out of bounds")
	}
	return map[string]string{}, 0, int32(len(r)), e
}
func (o atspiText) GetAttributeRun(m *dbusMessage, offset int32, _ bool) (map[string]string, int32, int32, *dbusError) {
	return o.GetAttributes(m, offset)
}
func (o atspiText) GetAttributeValue(m *dbusMessage, offset int32, _ string) (string, *dbusError) {
	_, _, _, e := o.GetAttributes(m, offset)
	return "", e
}

type atspiEditable struct{ a *atspiBridge }

func (o atspiEditable) SetTextContents(m *dbusMessage, text string) (bool, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return false, e
	}
	if !atspiEditableNode(n) || len(text) > 16<<20 || !utf8.ValidString(text) {
		return false, atspiError("invalid editable text")
	}
	return o.a.action(n, "setValue", text), nil
}

type atspiIntrospection struct{ a *atspiBridge }

func (o atspiIntrospection) Introspect(m *dbusMessage) (string, *dbusError) {
	_, n, e := o.a.lookup(m)
	if e != nil {
		return "", e
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE node PUBLIC \"-//freedesktop//DTD D-BUS Object Introspection 1.0//EN\"\n \"http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd\">\n")
	fmt.Fprintf(&b, "<node name=%q>\n", string(o.a.ref(n.ID).Path))
	objects := o.a.objects()
	objects["org.freedesktop.DBus.Introspectable"] = o
	for _, name := range append(atspiInterfaces(n), "org.freedesktop.DBus.Properties", "org.freedesktop.DBus.Introspectable") {
		fmt.Fprintf(&b, "  <interface name=%q>\n", name)
		if obj, ok := objects[name]; ok {
			dbusIntrospectMethods(&b, obj)
		}
		if values, err := (atspiProperties{o.a}).values(m, name); err == nil {
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				access := "read"
				if (name == atspiPrefix+"Application" && key == "Id") || (name == atspiPrefix+"Value" && key == "CurrentValue") {
					access = "readwrite"
				}
				fmt.Fprintf(&b, "    <property name=%q type=%q access=%q/>\n", key, values[key].sig, access)
			}
		}
		b.WriteString("  </interface>\n")
	}
	b.WriteString("</node>\n")
	return b.String(), nil
}

func (w *xWindow) SetAccessibility(nodes []AccessNode) {
	if w.closed {
		return
	}
	if !w.accessTried {
		w.accessTried = true
		if address := atspiAddress(); address != "" {
			var err error
			w.access, err = newATSPI(address, w.title, image.Rect(0, 0, w.width, w.height), w.b.Wake)
			if err != nil {
				fmt.Fprintln(os.Stderr, "fego accessibility:", err)
			}
		}
	}
	if w.access == nil {
		return
	}
	if !w.originKnown {
		w.updateAccessOrigin()
	}
	w.access.set(w.title, image.Rect(0, 0, w.width, w.height), w.accessOrigin, nodes)
}

func (w *xWindow) updateAccessOrigin() {
	var x, y int32
	var child uintptr
	if xl.translateCoordinates(w.b.display, w.id, w.b.root, 0, 0, &x, &y, &child) != 0 {
		w.accessOrigin = image.Pt(int(x), int(y))
		w.originKnown = true
	}
}

func (w *xWindow) refreshAccessibility() {
	if w.access == nil || w.closed {
		return
	}
	w.updateAccessOrigin()
	t := w.access.current()
	nodes := make([]AccessNode, 0, len(t.order)-2)
	for _, id := range t.order[2:] {
		n, _ := t.node(id)
		nodes = append(nodes, n)
	}
	w.SetAccessibility(nodes)
}
