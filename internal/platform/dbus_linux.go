//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// A minimal D-Bus peer for the AT-SPI bridge: SASL EXTERNAL over a Unix
// socket, method calls with replies, signals, match rules and exported methods
// dispatched by reflection. It avoids a general client library and its
// networking, XML and process dependencies in every Linux binary.

type dbusPath string
type dbusSignature string

// dbusVariant holds a value with its D-Bus type signature.
type dbusVariant struct {
	sig   string
	value any
}

func dbusMakeVariant(v any) dbusVariant {
	return dbusVariant{dbusSignatureOf(reflect.TypeOf(v)), v}
}

type dbusError struct {
	name, message string
}

func (e *dbusError) Error() string { return e.name + ": " + e.message }

const (
	dbusMethodCall   = 1
	dbusMethodReturn = 2
	dbusErrorReply   = 3
	dbusSignal       = 4

	dbusNoReplyExpected = 1
	dbusNoAutoStart     = 2
)

type dbusMessage struct {
	kind, flags         byte
	serial, replySerial uint32
	path                dbusPath
	iface, member       string
	errName, dest       string
	sender, sig         string
	body                []any
}

var (
	dbusPathType      = reflect.TypeFor[dbusPath]()
	dbusSignatureType = reflect.TypeFor[dbusSignature]()
	dbusVariantType   = reflect.TypeFor[dbusVariant]()
	dbusMessageType   = reflect.TypeFor[*dbusMessage]()
	dbusErrorType     = reflect.TypeFor[*dbusError]()
)

func dbusSignatureOf(t reflect.Type) string {
	switch t {
	case dbusPathType:
		return "o"
	case dbusSignatureType:
		return "g"
	case dbusVariantType:
		return "v"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "b"
	case reflect.Uint8:
		return "y"
	case reflect.Int16:
		return "n"
	case reflect.Uint16:
		return "q"
	case reflect.Int32:
		return "i"
	case reflect.Uint32:
		return "u"
	case reflect.Int64:
		return "x"
	case reflect.Uint64:
		return "t"
	case reflect.Float64:
		return "d"
	case reflect.String:
		return "s"
	case reflect.Slice:
		return "a" + dbusSignatureOf(t.Elem())
	case reflect.Map:
		return "a{" + dbusSignatureOf(t.Key()) + dbusSignatureOf(t.Elem()) + "}"
	case reflect.Struct:
		var b strings.Builder
		b.WriteByte('(')
		for i := range t.NumField() {
			b.WriteString(dbusSignatureOf(t.Field(i).Type))
		}
		b.WriteByte(')')
		return b.String()
	}
	panic("dbus: unsupported type " + t.String())
}

func dbusAlignment(sig byte) int {
	switch sig {
	case 'n', 'q':
		return 2
	case 'b', 'i', 'u', 's', 'o', 'a', 'h':
		return 4
	case 'x', 't', 'd', '(', '{':
		return 8
	}
	return 1
}

type dbusEncoder struct{ buf []byte }

func (e *dbusEncoder) align(n int) {
	for len(e.buf)%n != 0 {
		e.buf = append(e.buf, 0)
	}
}
func (e *dbusEncoder) u32(v uint32) {
	e.align(4)
	e.buf = binary.LittleEndian.AppendUint32(e.buf, v)
}
func (e *dbusEncoder) str(s string) {
	e.u32(uint32(len(s)))
	e.buf = append(append(e.buf, s...), 0)
}
func (e *dbusEncoder) sig(s string) {
	e.buf = append(append(append(e.buf, byte(len(s))), s...), 0)
}

func (e *dbusEncoder) value(v reflect.Value) {
	switch v.Type() {
	case dbusPathType:
		e.str(v.String())
		return
	case dbusSignatureType:
		e.sig(v.String())
		return
	case dbusVariantType:
		variant := v.Interface().(dbusVariant)
		e.sig(variant.sig)
		e.value(reflect.ValueOf(variant.value))
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		b := uint32(0)
		if v.Bool() {
			b = 1
		}
		e.u32(b)
	case reflect.Uint8:
		e.buf = append(e.buf, byte(v.Uint()))
	case reflect.Int16:
		e.align(2)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(v.Int()))
	case reflect.Uint16:
		e.align(2)
		e.buf = binary.LittleEndian.AppendUint16(e.buf, uint16(v.Uint()))
	case reflect.Int32:
		e.u32(uint32(v.Int()))
	case reflect.Uint32:
		e.u32(uint32(v.Uint()))
	case reflect.Int64:
		e.align(8)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, uint64(v.Int()))
	case reflect.Uint64:
		e.align(8)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, v.Uint())
	case reflect.Float64:
		e.align(8)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(v.Float()))
	case reflect.String:
		e.str(v.String())
	case reflect.Slice, reflect.Map:
		e.u32(0)
		at := len(e.buf) - 4
		elem := dbusSignatureOf(v.Type())[1]
		e.align(dbusAlignment(elem))
		start := len(e.buf)
		if v.Kind() == reflect.Slice {
			for i := range v.Len() {
				e.value(v.Index(i))
			}
		} else {
			keys := v.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, k := range keys {
				e.align(8)
				e.value(k)
				e.value(v.MapIndex(k))
			}
		}
		binary.LittleEndian.PutUint32(e.buf[at:], uint32(len(e.buf)-start))
	case reflect.Struct:
		e.align(8)
		for i := range v.NumField() {
			e.value(v.Field(i))
		}
	case reflect.Interface:
		e.value(v.Elem())
	default:
		panic("dbus: unsupported value " + v.Type().String())
	}
}

type dbusDecoder struct {
	buf   []byte
	pos   int
	order binary.ByteOrder
}

var errDBusMessage = errors.New("dbus: malformed message")

func (d *dbusDecoder) align(n int) error {
	for d.pos%n != 0 {
		if d.pos >= len(d.buf) {
			return errDBusMessage
		}
		d.pos++
	}
	return nil
}
func (d *dbusDecoder) take(n int) ([]byte, error) {
	if n < 0 || d.pos+n > len(d.buf) {
		return nil, errDBusMessage
	}
	b := d.buf[d.pos : d.pos+n]
	d.pos += n
	return b, nil
}
func (d *dbusDecoder) u32() (uint32, error) {
	if err := d.align(4); err != nil {
		return 0, err
	}
	b, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return d.order.Uint32(b), nil
}

// dbusNextType splits the first complete type from a signature.
func dbusNextType(sig string) (string, string, error) {
	if sig == "" {
		return "", "", errDBusMessage
	}
	switch sig[0] {
	case 'a':
		elem, rest, err := dbusNextType(sig[1:])
		return "a" + elem, rest, err
	case '(', '{':
		closing := byte(')')
		if sig[0] == '{' {
			closing = '}'
		}
		inner := sig[1:]
		for inner != "" && inner[0] != closing {
			var err error
			if _, inner, err = dbusNextType(inner); err != nil {
				return "", "", err
			}
		}
		if inner == "" {
			return "", "", errDBusMessage
		}
		n := len(sig) - len(inner) + 1
		return sig[:n], sig[n:], nil
	}
	return sig[:1], sig[1:], nil
}

func (d *dbusDecoder) values(sig string) ([]any, error) {
	var out []any
	for sig != "" {
		one, rest, err := dbusNextType(sig)
		if err != nil {
			return nil, err
		}
		v, err := d.value(one, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		sig = rest
	}
	return out, nil
}

// value decodes one complete type: arrays become []any or map[any]any,
// structs []any, and variants dbusVariant.
func (d *dbusDecoder) value(sig string, depth int) (any, error) {
	if depth > 64 {
		return nil, errDBusMessage
	}
	if err := d.align(dbusAlignment(sig[0])); err != nil {
		return nil, err
	}
	switch sig[0] {
	case 'y':
		b, err := d.take(1)
		if err != nil {
			return nil, err
		}
		return b[0], nil
	case 'b':
		v, err := d.u32()
		return v != 0, err
	case 'n', 'q':
		b, err := d.take(2)
		if err != nil {
			return nil, err
		}
		if sig[0] == 'n' {
			return int16(d.order.Uint16(b)), nil
		}
		return d.order.Uint16(b), nil
	case 'i':
		v, err := d.u32()
		return int32(v), err
	case 'u', 'h':
		return d.u32()
	case 'x', 't', 'd':
		b, err := d.take(8)
		if err != nil {
			return nil, err
		}
		v := d.order.Uint64(b)
		switch sig[0] {
		case 'x':
			return int64(v), nil
		case 'd':
			return math.Float64frombits(v), nil
		}
		return v, nil
	case 's', 'o':
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		b, err := d.take(int(n) + 1)
		if err != nil || b[n] != 0 {
			return nil, errDBusMessage
		}
		if sig[0] == 'o' {
			return dbusPath(b[:n]), nil
		}
		return string(b[:n]), nil
	case 'g':
		n, err := d.take(1)
		if err != nil {
			return nil, err
		}
		b, err := d.take(int(n[0]) + 1)
		if err != nil {
			return nil, errDBusMessage
		}
		return dbusSignature(b[:n[0]]), nil
	case 'v':
		s, err := d.value("g", depth+1)
		if err != nil {
			return nil, err
		}
		inner := string(s.(dbusSignature))
		if one, rest, err := dbusNextType(inner); err != nil || rest != "" || one == "" {
			return nil, errDBusMessage
		}
		v, err := d.value(inner, depth+1)
		return dbusVariant{inner, v}, err
	case 'a':
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.align(dbusAlignment(sig[1])); err != nil {
			return nil, err
		}
		end := d.pos + int(n)
		if n > 64<<20 || end > len(d.buf) {
			return nil, errDBusMessage
		}
		if sig[1] == '{' {
			m := map[any]any{}
			key, rest, _ := dbusNextType(sig[2:])
			if len(key) != 1 || !strings.Contains("ybnqiuxtdsog", key) {
				return nil, errDBusMessage // keys must be basic types
			}
			for d.pos < end {
				if err := d.align(8); err != nil {
					return nil, err
				}
				k, err := d.value(key, depth+1)
				if err != nil {
					return nil, err
				}
				v, err := d.value(rest[:len(rest)-1], depth+1)
				if err != nil {
					return nil, err
				}
				m[k] = v
			}
			return m, nil
		}
		var items []any
		for d.pos < end {
			v, err := d.value(sig[1:], depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return items, nil
	case '(':
		var fields []any
		inner := sig[1 : len(sig)-1]
		for inner != "" {
			one, rest, err := dbusNextType(inner)
			if err != nil {
				return nil, err
			}
			v, err := d.value(one, depth+1)
			if err != nil {
				return nil, err
			}
			fields = append(fields, v)
			inner = rest
		}
		return fields, nil
	}
	return nil, errDBusMessage
}

func (m *dbusMessage) encode(serial uint32) []byte {
	var body dbusEncoder
	sig := ""
	for _, v := range m.body {
		rv := reflect.ValueOf(v)
		sig += dbusSignatureOf(rv.Type())
		body.value(rv)
	}
	type field struct {
		code byte
		v    any
	}
	fields := []field{}
	if m.path != "" {
		fields = append(fields, field{1, m.path})
	}
	if m.iface != "" {
		fields = append(fields, field{2, m.iface})
	}
	if m.member != "" {
		fields = append(fields, field{3, m.member})
	}
	if m.errName != "" {
		fields = append(fields, field{4, m.errName})
	}
	if m.replySerial != 0 {
		fields = append(fields, field{5, m.replySerial})
	}
	if m.dest != "" {
		fields = append(fields, field{6, m.dest})
	}
	if sig != "" {
		fields = append(fields, field{8, dbusSignature(sig)})
	}
	h := dbusEncoder{buf: []byte{'l', m.kind, m.flags, 1, 0, 0, 0, 0, 0, 0, 0, 0}}
	binary.LittleEndian.PutUint32(h.buf[4:], uint32(len(body.buf)))
	binary.LittleEndian.PutUint32(h.buf[8:], serial)
	h.u32(0)
	start := len(h.buf)
	for _, f := range fields {
		h.align(8)
		h.buf = append(h.buf, f.code)
		h.value(reflect.ValueOf(dbusMakeVariant(f.v)))
	}
	binary.LittleEndian.PutUint32(h.buf[12:], uint32(len(h.buf)-start))
	h.align(8)
	return append(h.buf, body.buf...)
}

func dbusReadMessage(r io.Reader) (*dbusMessage, error) {
	var fixed [16]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return nil, err
	}
	var order binary.ByteOrder = binary.LittleEndian
	if fixed[0] == 'B' {
		order = binary.BigEndian
	} else if fixed[0] != 'l' {
		return nil, errDBusMessage
	}
	bodyLen, fieldsLen := order.Uint32(fixed[4:]), order.Uint32(fixed[12:])
	if bodyLen > 128<<20 || fieldsLen > 1<<20 {
		return nil, errDBusMessage
	}
	headerLen := (16 + int(fieldsLen) + 7) &^ 7
	buf := make([]byte, headerLen+int(bodyLen))
	copy(buf, fixed[:])
	if _, err := io.ReadFull(r, buf[16:]); err != nil {
		return nil, err
	}
	m := &dbusMessage{kind: fixed[1], flags: fixed[2], serial: order.Uint32(fixed[8:])}
	d := dbusDecoder{buf: buf[:16+fieldsLen], pos: 16, order: order}
	for d.pos < len(d.buf) {
		if err := d.align(8); err != nil {
			return nil, err
		}
		code, err := d.take(1)
		if err != nil {
			return nil, err
		}
		v, err := d.value("v", 0)
		if err != nil {
			return nil, err
		}
		switch value := v.(dbusVariant).value; code[0] {
		case 1:
			m.path, _ = value.(dbusPath)
		case 2:
			m.iface, _ = value.(string)
		case 3:
			m.member, _ = value.(string)
		case 4:
			m.errName, _ = value.(string)
		case 5:
			m.replySerial, _ = value.(uint32)
		case 6:
			m.dest, _ = value.(string)
		case 7:
			m.sender, _ = value.(string)
		case 8:
			s, _ := value.(dbusSignature)
			m.sig = string(s)
		}
	}
	body := dbusDecoder{buf: buf[headerLen:], order: order}
	var err error
	m.body, err = body.values(m.sig)
	return m, err
}

type dbusConn struct {
	file     *os.File
	name     string
	serial   atomic.Uint32
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    map[uint32]chan *dbusMessage
	closed   bool
	exports  map[dbusPath]map[string]any // subtree root -> interface -> object
	onSignal atomic.Pointer[func(*dbusMessage)]
}

// dbusConnect tries each address in turn, authenticates and says Hello.
func dbusConnect(addresses string) (*dbusConn, error) {
	err := errors.New("dbus: no supported address")
	for _, address := range strings.Split(addresses, ";") {
		var c *dbusConn
		if c, err = dbusDial(address); err == nil {
			return c, nil
		}
	}
	return nil, err
}

func dbusDial(address string) (*dbusConn, error) {
	kind, params, ok := strings.Cut(address, ":")
	if !ok || kind != "unix" {
		return nil, errors.New("dbus: unsupported address " + address)
	}
	var path string
	for param := range strings.SplitSeq(params, ",") {
		key, value, _ := strings.Cut(param, "=")
		value, err := dbusUnescape(value)
		if err != nil {
			return nil, err
		}
		switch key {
		case "path":
			path = value
		case "abstract":
			path = "@" + value
		}
	}
	if path == "" {
		return nil, errors.New("dbus: unsupported address " + address)
	}
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := syscall.Connect(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	c := &dbusConn{file: os.NewFile(uintptr(fd), "dbus"), calls: map[uint32]chan *dbusMessage{}, exports: map[dbusPath]map[string]any{}}
	r := bufio.NewReader(c.file)
	if err := c.auth(r); err != nil {
		c.file.Close()
		return nil, err
	}
	go c.read(r)
	reply, err := c.call(2*time.Second, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "Hello", 0)
	if err != nil {
		c.Close()
		return nil, err
	}
	if len(reply) != 1 {
		c.Close()
		return nil, errDBusMessage
	}
	c.name, _ = reply[0].(string)
	return c, nil
}

func dbusUnescape(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", errors.New("dbus: bad address escape")
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return "", errors.New("dbus: bad address escape")
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String(), nil
}

func (c *dbusConn) auth(r *bufio.Reader) error {
	c.file.SetDeadline(time.Now().Add(5 * time.Second))
	defer c.file.SetDeadline(time.Time{})
	uid := strconv.Itoa(os.Getuid())
	if _, err := fmt.Fprintf(c.file, "\x00AUTH EXTERNAL %x\r\n", uid); err != nil {
		return err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "OK ") {
		return errors.New("dbus: authentication rejected")
	}
	_, err = io.WriteString(c.file, "BEGIN\r\n")
	return err
}

func (c *dbusConn) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	calls := c.calls
	c.calls = nil
	c.mu.Unlock()
	c.file.Close()
	for _, ch := range calls {
		close(ch)
	}
}

func (c *dbusConn) send(m *dbusMessage) (uint32, error) {
	serial := c.serial.Add(1)
	data := m.encode(serial)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.file.Write(data)
	return serial, err
}

func (c *dbusConn) call(timeout time.Duration, dest string, path dbusPath, iface, member string, flags byte, args ...any) ([]any, error) {
	ch := make(chan *dbusMessage, 1)
	m := &dbusMessage{kind: dbusMethodCall, flags: flags, dest: dest, path: path, iface: iface, member: member, body: args}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("dbus: connection closed")
	}
	// Register before sending so a fast reply cannot arrive unclaimed.
	serial := c.serial.Add(1)
	c.calls[serial] = ch
	c.mu.Unlock()
	data := m.encode(serial)
	c.writeMu.Lock()
	_, err := c.file.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		c.forget(serial)
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case reply, ok := <-ch:
		if !ok {
			return nil, errors.New("dbus: connection closed")
		}
		if reply.kind == dbusErrorReply {
			message := ""
			if len(reply.body) > 0 {
				message, _ = reply.body[0].(string)
			}
			return nil, &dbusError{reply.errName, message}
		}
		return reply.body, nil
	case <-timer.C:
		c.forget(serial)
		return nil, errors.New("dbus: call timed out")
	}
}

func (c *dbusConn) forget(serial uint32) {
	c.mu.Lock()
	delete(c.calls, serial)
	c.mu.Unlock()
}

func (c *dbusConn) Emit(path dbusPath, iface, member string, args ...any) error {
	_, err := c.send(&dbusMessage{kind: dbusSignal, path: path, iface: iface, member: member, body: args})
	return err
}

func (c *dbusConn) AddMatch(rule string) error {
	_, err := c.call(2*time.Second, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "AddMatch", 0, rule)
	return err
}

// Export serves every path under root with the given interface objects.
func (c *dbusConn) Export(root dbusPath, objects map[string]any) {
	c.mu.Lock()
	c.exports[root] = objects
	c.mu.Unlock()
}

func (c *dbusConn) read(r *bufio.Reader) {
	defer c.Close()
	for {
		m, err := dbusReadMessage(r)
		if err != nil {
			return
		}
		switch m.kind {
		case dbusMethodReturn, dbusErrorReply:
			c.mu.Lock()
			ch := c.calls[m.replySerial]
			delete(c.calls, m.replySerial)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case dbusSignal:
			if f := c.onSignal.Load(); f != nil {
				(*f)(m)
			}
		case dbusMethodCall:
			c.serve(m)
		}
	}
}

func (c *dbusConn) serve(m *dbusMessage) {
	body, err := c.dispatch(m)
	if m.flags&dbusNoReplyExpected != 0 {
		return
	}
	reply := &dbusMessage{kind: dbusMethodReturn, replySerial: m.serial, dest: m.sender, body: body}
	if err != nil {
		reply = &dbusMessage{kind: dbusErrorReply, replySerial: m.serial, dest: m.sender, errName: err.name, body: []any{err.message}}
	}
	c.send(reply)
}

func (c *dbusConn) objects(path dbusPath) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for root, objects := range c.exports {
		if path == root || strings.HasPrefix(string(path), string(root)+"/") {
			return objects
		}
	}
	return nil
}

func (c *dbusConn) dispatch(m *dbusMessage) ([]any, *dbusError) {
	if m.iface == "org.freedesktop.DBus.Peer" {
		switch m.member {
		case "Ping":
			return nil, nil
		case "GetMachineId":
			id, _ := os.ReadFile("/etc/machine-id")
			return []any{strings.TrimSpace(string(id))}, nil
		}
	}
	objects := c.objects(m.path)
	if objects == nil {
		return nil, &dbusError{"org.freedesktop.DBus.Error.UnknownObject", "no object at " + string(m.path)}
	}
	var method reflect.Value
	if obj, ok := objects[m.iface]; ok {
		method = reflect.ValueOf(obj).MethodByName(m.member)
	} else if m.iface == "" {
		names := make([]string, 0, len(objects))
		for name := range objects {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if method = reflect.ValueOf(objects[name]).MethodByName(m.member); method.IsValid() {
				break
			}
		}
	}
	if !method.IsValid() {
		return nil, &dbusError{"org.freedesktop.DBus.Error.UnknownMethod", "unknown method " + m.iface + "." + m.member}
	}
	t := method.Type()
	var in []reflect.Value
	args := m.body
	for i := range t.NumIn() {
		p := t.In(i)
		if i == 0 && p == dbusMessageType {
			in = append(in, reflect.ValueOf(m))
			continue
		}
		if len(args) == 0 {
			return nil, &dbusError{"org.freedesktop.DBus.Error.InvalidArgs", "missing arguments"}
		}
		v := reflect.ValueOf(args[0])
		if !v.IsValid() || v.Type() != p {
			return nil, &dbusError{"org.freedesktop.DBus.Error.InvalidArgs", "argument type mismatch"}
		}
		in = append(in, v)
		args = args[1:]
	}
	if len(args) != 0 {
		return nil, &dbusError{"org.freedesktop.DBus.Error.InvalidArgs", "too many arguments"}
	}
	out := method.Call(in)
	var body []any
	for _, v := range out {
		if v.Type() == dbusErrorType {
			if !v.IsNil() {
				return nil, v.Interface().(*dbusError)
			}
			continue
		}
		body = append(body, v.Interface())
	}
	return body, nil
}

// dbusIntrospectMethods writes interface methods in introspection XML.
func dbusIntrospectMethods(b *strings.Builder, obj any) {
	t := reflect.TypeOf(obj)
	for i := range t.NumMethod() {
		m := t.Method(i)
		fmt.Fprintf(b, "    <method name=%q>\n", m.Name)
		for j := 1; j < m.Type.NumIn(); j++ {
			if p := m.Type.In(j); p != dbusMessageType {
				fmt.Fprintf(b, "      <arg type=%q direction=\"in\"/>\n", dbusSignatureOf(p))
			}
		}
		for j := range m.Type.NumOut() {
			if p := m.Type.Out(j); p != dbusErrorType {
				fmt.Fprintf(b, "      <arg type=%q direction=\"out\"/>\n", dbusSignatureOf(p))
			}
		}
		b.WriteString("    </method>\n")
	}
}
