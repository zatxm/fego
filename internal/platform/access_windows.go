//go:build windows && (amd64 || arm64)

package platform

import (
	"image"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// MSAA exposes real IAccessible objects; Windows' accessibility bridge can
// present these to UI Automation clients as well as traditional screen readers.
var (
	winOle           = syscall.NewLazyDLL("ole32.dll")
	winOleAut        = syscall.NewLazyDLL("oleaut32.dll")
	winOleAcc        = syscall.NewLazyDLL("oleacc.dll")
	winCoInit        = winOle.NewProc("CoInitializeEx")
	winCoUninit      = winOle.NewProc("CoUninitialize")
	winCoAlloc       = winOle.NewProc("CoTaskMemAlloc")
	winCoFree        = winOle.NewProc("CoTaskMemFree")
	winBSTR          = winOleAut.NewProc("SysAllocStringLen")
	winBSTRLen       = winOleAut.NewProc("SysStringLen")
	winAccResult     = winOleAcc.NewProc("LresultFromObject")
	winAccFromWindow = winOleAcc.NewProc("AccessibleObjectFromWindow")
	winAccNotify     = winUser.NewProc("NotifyWinEvent")
	winAccOnce       sync.Once
	winAccVTable     [28]uintptr
	winAccMu         sync.RWMutex
	winAccObjects    = map[uintptr]*winAccessObject{}
	iidUnknown       = [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0xc0, 0, 0, 0, 0, 0, 0, 0x46}
	iidDispatch      = [16]byte{0, 4, 2, 0, 0, 0, 0, 0, 0xc0, 0, 0, 0, 0, 0, 0, 0x46}
	iidAccessible    = [16]byte{0xe0, 0x36, 0x87, 0x61, 0x3d, 0x3c, 0xcf, 0x11, 0x81, 0x0c, 0, 0xaa, 0, 0x38, 0x9b, 0x71}
)

const (
	accInvalid      = uintptr(0x80070057)
	accPointer      = uintptr(0x80004003)
	accDisconnected = uintptr(0x800401fd)
	accNotImpl      = uintptr(0x80004001)
)

type accVariant struct {
	Type, R1, R2, R3 uint16
	Value, Extra     uintptr
}
type accParams struct {
	Args, Named       uintptr
	Count, NamedCount uint32
}

var _ [24]byte = [unsafe.Sizeof(accVariant{})]byte{}
var _ [24]byte = [unsafe.Sizeof(accParams{})]byte{}

type winAccessTree struct {
	mu       sync.RWMutex
	window   *windowsWindow
	nodes    map[uint64]AccessNode
	children map[uint64][]uint64
	order    []uint64
	objects  map[uint64]*winAccessObject
	closed   bool
}
type winAccessObject struct {
	ptr  uintptr
	id   uint64
	tree *winAccessTree
	refs atomic.Int32
}

func (w *windowsWindow) SetAccessibility(nodes []AccessNode) {
	if w.closed {
		return
	}
	if w.access == nil {
		w.access = &winAccessTree{window: w, nodes: map[uint64]AccessNode{}, children: map[uint64][]uint64{}, objects: map[uint64]*winAccessObject{}}
	}
	t := w.access
	t.mu.Lock()
	var client winRect
	winGetClient.Call(w.hwnd, uintptr(unsafe.Pointer(&client)))
	root := AccessNode{Role: "window", Label: w.title, Bounds: image.Rect(0, 0, int(client.Right), int(client.Bottom))}
	oldRoot, hasRoot := t.nodes[0]
	changed := len(t.order) != len(nodes) || !hasRoot || oldRoot != root
	focused := false
	for i, n := range nodes {
		old, ok := t.nodes[n.ID]
		if !ok || old != n || i >= len(t.order) || t.order[i] != n.ID {
			changed = true
		}
		if n.Focused && (!ok || !old.Focused) {
			focused = true
		}
	}
	if !changed {
		t.mu.Unlock()
		return
	}
	next := make(map[uint64]AccessNode, len(nodes)+1)
	next[0] = root
	children := make(map[uint64][]uint64)
	t.order = t.order[:0]
	for _, n := range nodes {
		next[n.ID] = n
		children[n.Parent] = append(children[n.Parent], n.ID)
		t.order = append(t.order, n.ID)
	}
	t.nodes, t.children = next, children
	var removed []*winAccessObject
	for id, o := range t.objects {
		if _, ok := next[id]; !ok {
			delete(t.objects, id)
			removed = append(removed, o)
		}
	}
	t.mu.Unlock()
	for _, o := range removed {
		accRelease(o.ptr)
	}
	winAccNotify.Call(0x8004, w.hwnd, uintptr(uint32(0xfffffffc)), 0)
	if focused {
		winAccNotify.Call(0x8005, w.hwnd, uintptr(uint32(0xfffffffc)), 0)
	}
}
func (t *winAccessTree) close() {
	t.mu.Lock()
	t.closed = true
	objects := t.objects
	t.objects = nil
	t.nodes = nil
	t.children = nil
	t.order = nil
	t.window = nil
	t.mu.Unlock()
	for _, o := range objects {
		accRelease(o.ptr)
	}
}
func (t *winAccessTree) object(id uint64) *winAccessObject {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	if _, ok := t.nodes[id]; !ok {
		return nil
	}
	if o := t.objects[id]; o != nil {
		o.refs.Add(1)
		return o
	}
	winAccOnce.Do(initWinAccess)
	ptr, _, _ := winCoAlloc.Call(unsafe.Sizeof(uintptr(0)))
	if ptr == 0 {
		return nil
	}
	*(*uintptr)(nativeAddress(ptr)) = uintptr(unsafe.Pointer(&winAccVTable[0]))
	o := &winAccessObject{ptr: ptr, id: id, tree: t}
	o.refs.Store(2) // tree owner and caller
	t.objects[id] = o
	winAccMu.Lock()
	winAccObjects[ptr] = o
	winAccMu.Unlock()
	return o
}
func accObject(ptr uintptr) *winAccessObject {
	winAccMu.RLock()
	o := winAccObjects[ptr]
	winAccMu.RUnlock()
	return o
}
func accAddRef(ptr uintptr) uintptr {
	if o := accObject(ptr); o != nil {
		return uintptr(o.refs.Add(1))
	}
	return 0
}
func accRelease(ptr uintptr) uintptr {
	if o := accObject(ptr); o != nil {
		n := o.refs.Add(-1)
		if n == 0 {
			winAccMu.Lock()
			delete(winAccObjects, ptr)
			winAccMu.Unlock()
			winCoFree.Call(ptr)
		}
		return uintptr(n)
	}
	return 0
}
func accQuery(this, iid, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*uintptr)(nativeAddress(out)) = 0
	if iid == 0 {
		return accInvalid
	}
	value := *(*[16]byte)(nativeAddress(iid))
	if value != iidUnknown && value != iidDispatch && value != iidAccessible {
		return 0x80004002
	}
	if accAddRef(this) == 0 {
		return accDisconnected
	}
	*(*uintptr)(nativeAddress(out)) = this
	return 0
}
func accTarget(this, child uintptr) (*winAccessObject, AccessNode, bool) {
	o := accObject(this)
	if o == nil {
		return nil, AccessNode{}, false
	}
	id := o.id
	o.tree.mu.RLock()
	defer o.tree.mu.RUnlock()
	if o.tree.closed {
		return nil, AccessNode{}, false
	}
	if child != 0 {
		v := (*accVariant)(nativeAddress(child))
		if v.Type != 3 {
			return nil, AccessNode{}, false
		}
		index := int32(v.Value)
		if index < 0 {
			return nil, AccessNode{}, false
		}
		if index > 0 {
			ids := o.tree.children[id]
			if int(index) > len(ids) {
				return nil, AccessNode{}, false
			}
			id = ids[index-1]
		}
	}
	n, ok := o.tree.nodes[id]
	return o, n, ok
}
func accString(out uintptr, s string) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*uintptr)(nativeAddress(out)) = 0
	if s == "" {
		return 1
	}
	chars := utf16.Encode([]rune(s))
	b, _, _ := winBSTR.Call(uintptr(unsafe.Pointer(unsafe.SliceData(chars))), uintptr(len(chars)))
	if b == 0 {
		return 0x8007000e
	}
	*(*uintptr)(nativeAddress(out)) = b
	return 0
}
func accGetName(this, child, out uintptr) uintptr {
	_, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	return accString(out, n.Label)
}
func accGetValue(this, child, out uintptr) uintptr {
	_, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	value := n.Value
	if n.Role == "slider" || n.Role == "progress" {
		value = strconv.FormatFloat(n.Number, 'g', -1, 64)
	}
	return accString(out, value)
}
func accEmptyString(_this, _child, out uintptr) uintptr { return accString(out, "") }
func accGetRole(this, child, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	_, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	role := uintptr(20)
	switch n.Role {
	case "window":
		role = 10
	case "button":
		role = 43
	case "input", "editor":
		role = 42
	case "text":
		role = 41
	case "checkbox", "switch":
		role = 44
	case "radio":
		role = 45
	case "select", "combobox":
		role = 46
	case "slider":
		role = 51
	case "progress":
		role = 48
	case "list":
		role = 33
	case "row", "listitem":
		role = 34
	case "table":
		role = 24
	case "cell":
		role = 29
	case "tree":
		role = 35
	case "treeitem":
		role = 36
	case "image":
		role = 40
	}
	*(*accVariant)(nativeAddress(out)) = accVariant{Type: 3, Value: role}
	return 0
}
func accGetState(this, child, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	_, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	state := uintptr(0)
	if n.Disabled {
		state |= 1
	}
	if n.Selected {
		state |= 2
	}
	if n.Focused {
		state |= 4
	}
	if n.Checked {
		state |= 16
	}
	switch n.Role {
	case "text", "progress", "image":
		state |= 0x40
	case "group", "window":
	default:
		state |= 0x100000
	}
	if n.Role == "row" || n.Role == "treeitem" || n.Role == "cell" {
		state |= 0x200000
	}
	*(*accVariant)(nativeAddress(out)) = accVariant{Type: 3, Value: state}
	return 0
}
func accChildCount(this, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	o := accObject(this)
	if o == nil {
		return accDisconnected
	}
	o.tree.mu.RLock()
	defer o.tree.mu.RUnlock()
	if o.tree.closed {
		return accDisconnected
	}
	*(*int32)(nativeAddress(out)) = int32(len(o.tree.children[o.id]))
	return 0
}
func accChild(this, child, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*uintptr)(nativeAddress(out)) = 0
	o, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	target := o.tree.object(n.ID)
	if target == nil {
		return accDisconnected
	}
	*(*uintptr)(nativeAddress(out)) = target.ptr
	return 0
}
func accParent(this, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*uintptr)(nativeAddress(out)) = 0
	o, n, ok := accTarget(this, 0)
	if !ok {
		return accDisconnected
	}
	if n.ID == 0 {
		o.tree.mu.RLock()
		w := o.tree.window
		o.tree.mu.RUnlock()
		if w == nil {
			return accDisconnected
		}
		r, _, _ := winAccFromWindow.Call(w.hwnd, 0, uintptr(unsafe.Pointer(&iidDispatch[0])), out)
		return r
	}
	p := o.tree.object(n.Parent)
	if p == nil {
		return accDisconnected
	}
	*(*uintptr)(nativeAddress(out)) = p.ptr
	return 0
}
func accVariantObject(t *winAccessTree, id uint64, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*accVariant)(nativeAddress(out)) = accVariant{}
	o := t.object(id)
	if o == nil {
		return 1
	}
	*(*accVariant)(nativeAddress(out)) = accVariant{Type: 9, Value: o.ptr}
	return 0
}
func accFind(this, out uintptr, selected bool) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*accVariant)(nativeAddress(out)) = accVariant{}
	o := accObject(this)
	if o == nil {
		return accDisconnected
	}
	o.tree.mu.RLock()
	found := uint64(0)
	ok := false
	for i := len(o.tree.order) - 1; i >= 0; i-- {
		id := o.tree.order[i]
		n := o.tree.nodes[id]
		if selected && !n.Selected || !selected && !n.Focused {
			continue
		}
		for p := id; p != 0; p = o.tree.nodes[p].Parent {
			if p == o.id || o.id == 0 {
				found, ok = id, true
				break
			}
		}
		if ok {
			break
		}
	}
	o.tree.mu.RUnlock()
	if !ok {
		return 1
	}
	return accVariantObject(o.tree, found, out)
}
func accFocus(this, out uintptr) uintptr     { return accFind(this, out, false) }
func accSelection(this, out uintptr) uintptr { return accFind(this, out, true) }
func accDefault(this, child, out uintptr) uintptr {
	_, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	action := ""
	switch n.Role {
	case "button", "checkbox", "switch", "radio", "select", "combobox":
		action = "Press"
	case "row", "treeitem", "cell":
		action = "Select"
	}
	return accString(out, action)
}
func accAction(this, child uintptr, action, text string) uintptr {
	o, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	if n.Disabled {
		return 1
	}
	o.tree.mu.RLock()
	w := o.tree.window
	o.tree.mu.RUnlock()
	if w == nil {
		return accDisconnected
	}
	w.backend.postAction(func() {
		o.tree.mu.RLock()
		current, exists := o.tree.nodes[n.ID]
		valid := exists && !o.tree.closed && !current.Disabled && current.Role == n.Role
		o.tree.mu.RUnlock()
		if !w.closed && valid {
			w.events(Event{Kind: AccessAction, AccessID: n.ID, Action: action, Text: text})
		}
	})
	return 0
}
func accDoDefault(this, child uintptr) uintptr {
	_, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	action := "press"
	switch n.Role {
	case "row", "treeitem", "cell":
		action = "select"
	case "button", "checkbox", "switch", "radio", "select", "combobox":
	default:
		return 1
	}
	return accAction(this, child, action, "")
}
func accSelect(this, flags, child uintptr) uintptr {
	if flags&1 != 0 {
		if r := accAction(this, child, "focus", ""); r != 0 {
			return r
		}
	}
	if flags&0x1e != 0 {
		return accAction(this, child, "select", "")
	}
	return 0
}
func accPutValue(this, child, value uintptr) uintptr {
	_, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	if n.Role != "input" && n.Role != "editor" && n.Role != "slider" {
		return 1
	}
	var s string
	if value != 0 {
		length, _, _ := winBSTRLen.Call(value)
		if length > nativeTextMaxBytes/2 {
			return accInvalid
		}
		var ok bool
		s, ok = nativeUTF16Text(unsafe.Slice((*uint16)(nativeAddress(value)), length))
		if !ok {
			return accInvalid
		}
	}
	return accAction(this, child, "setValue", s)
}
func accLocation(this, x, y, width, height, child uintptr) uintptr {
	if x == 0 || y == 0 || width == 0 || height == 0 {
		return accPointer
	}
	o, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	o.tree.mu.RLock()
	w := o.tree.window
	o.tree.mu.RUnlock()
	if w == nil {
		return accDisconnected
	}
	p := winPoint{}
	winUser.NewProc("ClientToScreen").Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	*(*int32)(nativeAddress(x)) = p.X + int32(n.Bounds.Min.X)
	*(*int32)(nativeAddress(y)) = p.Y + int32(n.Bounds.Min.Y)
	*(*int32)(nativeAddress(width)) = int32(n.Bounds.Dx())
	*(*int32)(nativeAddress(height)) = int32(n.Bounds.Dy())
	return 0
}
func accHitTest(this, x, y, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*accVariant)(nativeAddress(out)) = accVariant{}
	o := accObject(this)
	if o == nil {
		return accDisconnected
	}
	o.tree.mu.RLock()
	w := o.tree.window
	if w == nil {
		o.tree.mu.RUnlock()
		return accDisconnected
	}
	p := winPoint{int32(x), int32(y)}
	winUser.NewProc("ScreenToClient").Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	at := image.Pt(int(p.X), int(p.Y))
	id := o.id
	found := at.In(o.tree.nodes[id].Bounds)
	if found {
		for i := len(o.tree.order) - 1; i >= 0; i-- {
			candidate := o.tree.order[i]
			n := o.tree.nodes[candidate]
			if !at.In(n.Bounds) {
				continue
			}
			for parent := candidate; parent != 0; parent = o.tree.nodes[parent].Parent {
				if parent == o.id || o.id == 0 {
					id = candidate
					break
				}
			}
			if id != o.id {
				break
			}
		}
	}
	o.tree.mu.RUnlock()
	if !found {
		return 1
	}
	return accVariantObject(o.tree, id, out)
}
func accNavigate(this, direction, child, out uintptr) uintptr {
	if out == 0 {
		return accPointer
	}
	*(*accVariant)(nativeAddress(out)) = accVariant{}
	o, n, ok := accTarget(this, child)
	if !ok {
		return accInvalid
	}
	o.tree.mu.RLock()
	id := uint64(0)
	found := false
	switch direction {
	case 7, 8:
		ids := o.tree.children[n.ID]
		if len(ids) > 0 {
			index := 0
			if direction == 8 {
				index = len(ids) - 1
			}
			id, found = ids[index], true
		}
	case 5, 6:
		ids := o.tree.children[n.Parent]
		i := slices.Index(ids, n.ID)
		if direction == 5 {
			i++
		} else {
			i--
		}
		if i >= 0 && i < len(ids) {
			id, found = ids[i], true
		}
	}
	o.tree.mu.RUnlock()
	if !found {
		return 1
	}
	return accVariantObject(o.tree, id, out)
}

var accDispatchNames = map[string]int32{"accparent": -5000, "accchildcount": -5001, "accchild": -5002, "accname": -5003, "accvalue": -5004, "accdescription": -5005, "accrole": -5006, "accstate": -5007, "acchelp": -5008, "acchelptopic": -5009, "acckeyboardshortcut": -5010, "accfocus": -5011, "accselection": -5012, "accdefaultaction": -5013, "accselect": -5014, "acclocation": -5015, "accnavigate": -5016, "acchittest": -5017, "accdodefaultaction": -5018}

func accIDs(_this, _iid, names, count, _locale, out uintptr) uintptr {
	if names == 0 || out == 0 {
		return accPointer
	}
	ptrs := unsafe.Slice((*uintptr)(nativeAddress(names)), count)
	ids := unsafe.Slice((*int32)(nativeAddress(out)), count)
	for i, p := range ptrs {
		if p == 0 {
			return accPointer
		}
		var chars [64]uint16
		n := 0
		for ; n < len(chars); n++ {
			chars[n] = *(*uint16)(unsafe.Add(nativeAddress(p), 2*n))
			if chars[n] == 0 {
				break
			}
		}
		s := string(utf16.Decode(chars[:n]))
		id, ok := accDispatchNames[strings.ToLower(s)]
		if !ok {
			ids[i] = -1
			return 0x80020006
		}
		ids[i] = id
	}
	return 0
}
func accInvoke(this, member, _iid, _locale, flags, params, result, _exception, argError uintptr) uintptr {
	if result != 0 {
		*(*accVariant)(nativeAddress(result)) = accVariant{}
	}
	if argError != 0 {
		*(*uint32)(nativeAddress(argError)) = 0
	}
	var args []accVariant
	if params != 0 {
		p := (*accParams)(nativeAddress(params))
		if p.Count > 5 {
			return 0x8002000e
		}
		if p.Count > 0 {
			if p.Args == 0 {
				return accPointer
			}
			args = unsafe.Slice((*accVariant)(nativeAddress(p.Args)), p.Count)
		}
	}
	self := accVariant{Type: 3}
	child := uintptr(unsafe.Pointer(&self))
	if len(args) > 0 {
		child = uintptr(unsafe.Pointer(&args[0]))
	}
	id := int32(member)
	if flags&4 != 0 && id == -5004 {
		if len(args) != 2 || args[0].Type != 8 {
			return 0x80020005
		}
		return accPutValue(this, uintptr(unsafe.Pointer(&args[1])), args[0].Value)
	}
	if result == 0 && id != -5014 && id != -5015 && id != -5018 {
		return accPointer
	}
	v := (*accVariant)(nativeAddress(result))
	switch id {
	case -5000:
		var p uintptr
		r := accParent(this, uintptr(unsafe.Pointer(&p)))
		if r == 0 {
			*v = accVariant{Type: 9, Value: p}
		}
		return r
	case -5001:
		var n int32
		r := accChildCount(this, uintptr(unsafe.Pointer(&n)))
		if r == 0 {
			*v = accVariant{Type: 3, Value: uintptr(n)}
		}
		return r
	case -5002:
		var p uintptr
		r := accChild(this, child, uintptr(unsafe.Pointer(&p)))
		if r == 0 {
			*v = accVariant{Type: 9, Value: p}
		}
		return r
	case -5003, -5004, -5005, -5008, -5010, -5013:
		var text uintptr
		fn := accEmptyString
		switch id {
		case -5003:
			fn = accGetName
		case -5004:
			fn = accGetValue
		case -5013:
			fn = accDefault
		}
		r := fn(this, child, uintptr(unsafe.Pointer(&text)))
		if result != 0 {
			*v = accVariant{Type: 8, Value: text}
		}
		return r
	case -5006:
		return accGetRole(this, child, result)
	case -5007:
		return accGetState(this, child, result)
	case -5011:
		return accFocus(this, result)
	case -5012:
		return accSelection(this, result)
	case -5018:
		return accDoDefault(this, child)
	case -5014:
		if len(args) != 2 || args[1].Type != 3 {
			return 0x80020005
		}
		return accSelect(this, args[1].Value, child)
	case -5016:
		if len(args) != 2 || args[1].Type != 3 {
			return 0x80020005
		}
		return accNavigate(this, args[1].Value, child, result)
	case -5017:
		if len(args) != 2 || args[0].Type != 3 || args[1].Type != 3 {
			return 0x80020005
		}
		return accHitTest(this, args[1].Value, args[0].Value, result)
	case -5015:
		if len(args) != 5 {
			return 0x8002000e
		}
		for _, a := range args[1:] {
			if a.Type != 0x4003 {
				return 0x80020005
			}
		}
		return accLocation(this, args[4].Value, args[3].Value, args[2].Value, args[1].Value, child)
	}
	return 0x80020003
}
func initWinAccess() {
	callbacks := []any{
		accQuery, accAddRef, accRelease,
		func(_this, out uintptr) uintptr {
			if out == 0 {
				return accPointer
			}
			*(*uint32)(nativeAddress(out)) = 0
			return 0
		},
		func(_this, _index, _locale, out uintptr) uintptr {
			if out != 0 {
				*(*uintptr)(nativeAddress(out)) = 0
			}
			return accNotImpl
		},
		accIDs, accInvoke, accParent, accChildCount, accChild, accGetName, accGetValue, accEmptyString, accGetRole, accGetState, accEmptyString,
		func(_this, file, _child, topic uintptr) uintptr {
			if file != 0 {
				*(*uintptr)(nativeAddress(file)) = 0
			}
			if topic != 0 {
				*(*int32)(nativeAddress(topic)) = 0
			}
			return 1
		},
		accEmptyString, accFocus, accSelection, accDefault, accSelect, accLocation, accNavigate, accHitTest, accDoDefault,
		func(_this, _child, _name uintptr) uintptr { return accNotImpl }, accPutValue,
	}
	for i, fn := range callbacks {
		winAccVTable[i] = syscall.NewCallback(fn)
	}
}
