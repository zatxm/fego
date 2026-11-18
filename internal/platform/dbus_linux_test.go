//go:build linux && (amd64 || arm64)

package platform

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"reflect"
	"testing"
)

func TestDBusMessageRoundTrip(t *testing.T) {
	m := &dbusMessage{kind: dbusSignal, path: "/org/a11y/atspi/accessible/n1", iface: atspiPrefix + "Event.Object", member: "StateChanged", body: []any{
		"focused", int32(-7), uint32(9), true, int16(-3), uint16(4), byte(5), int64(-1 << 40), uint64(1 << 63), 2.5,
		atspiRef{":1.5", "/n2"}, []atspiRef{{"a", "/x"}, {"b", "/y"}}, []uint32{1 << 25, 0},
		map[string]string{"toolkit": "fego", "a": "中文"}, atspiRect{1, -2, 3, 4},
		[]atspiRelation{{1, []atspiRef{{"c", "/z"}}}}, dbusMakeVariant(atspiRef{"v", "/v"}),
		map[string]dbusVariant{"Id": dbusMakeVariant(int32(3)), "Name": dbusMakeVariant("x")}, dbusSignature("a{sv}"),
	}}
	got, err := dbusReadMessage(bytes.NewReader(m.encode(42)))
	if err != nil {
		t.Fatal(err)
	}
	if got.serial != 42 || got.path != m.path || got.iface != m.iface || got.member != m.member || got.sig != "siubnqyxtd(so)a(so)aua{ss}(iiii)a(ua(so))va{sv}g" {
		t.Fatalf("header %+v", got)
	}
	want := []any{
		"focused", int32(-7), uint32(9), true, int16(-3), uint16(4), byte(5), int64(-1 << 40), uint64(1 << 63), 2.5,
		[]any{":1.5", dbusPath("/n2")}, []any{[]any{"a", dbusPath("/x")}, []any{"b", dbusPath("/y")}}, []any{uint32(1 << 25), uint32(0)},
		map[any]any{"toolkit": "fego", "a": "中文"}, []any{int32(1), int32(-2), int32(3), int32(4)},
		[]any{[]any{uint32(1), []any{[]any{"c", dbusPath("/z")}}}}, dbusVariant{"(so)", []any{"v", dbusPath("/v")}},
		map[any]any{"Id": dbusVariant{"i", int32(3)}, "Name": dbusVariant{"s", "x"}}, dbusSignature("a{sv}"),
	}
	if !reflect.DeepEqual(got.body, want) {
		t.Fatalf("body\n got %#v\nwant %#v", got.body, want)
	}
}

func TestDBusBigEndianAndMalformedInput(t *testing.T) {
	// A big-endian method return: reply serial 7, signature "u", body 0x01020304.
	var h bytes.Buffer
	h.Write([]byte{'B', dbusMethodReturn, 0, 1})
	binary.Write(&h, binary.BigEndian, uint32(4))
	binary.Write(&h, binary.BigEndian, uint32(3))
	fields := []byte{5, 1, 'u', 0, 0, 0, 0, 7, 8, 1, 'g', 0, 1, 'u', 0}
	binary.Write(&h, binary.BigEndian, uint32(len(fields)))
	h.Write(fields)
	for h.Len()%8 != 0 {
		h.WriteByte(0)
	}
	binary.Write(&h, binary.BigEndian, uint32(0x01020304))
	m, err := dbusReadMessage(bytes.NewReader(h.Bytes()))
	if err != nil || m.replySerial != 7 || len(m.body) != 1 || m.body[0] != uint32(0x01020304) {
		t.Fatalf("big-endian reply %+v, %v", m, err)
	}
	bad := (&dbusMessage{kind: dbusMethodCall, path: "/p", member: "M", body: []any{map[string]string{"k": "v"}}}).encode(1)
	bad = bytes.Replace(bad, []byte("a{ss}"), []byte("a{vs}"), 1)
	if _, err := dbusReadMessage(bytes.NewReader(bad)); err == nil {
		t.Fatal("non-basic dictionary key accepted")
	}
	valid := (&dbusMessage{kind: dbusMethodCall, path: "/p", member: "M", body: []any{map[string]dbusVariant{"k": dbusMakeVariant([]atspiRef{{"a", "/b"}})}}}).encode(1)
	rng := rand.New(rand.NewPCG(5, 6))
	for range 20000 {
		data := bytes.Clone(valid[:rng.IntN(len(valid)+1)])
		for range rng.IntN(4) {
			if len(data) > 0 {
				data[rng.IntN(len(data))] = byte(rng.Uint32())
			}
		}
		dbusReadMessage(bytes.NewReader(data)) // must return, never panic
	}
}

func TestDBusAddressUnescape(t *testing.T) {
	if s, err := dbusUnescape("/tmp/dbus%2dtest%2Cx"); err != nil || s != "/tmp/dbus-test,x" {
		t.Fatalf("%q %v", s, err)
	}
	if _, err := dbusUnescape("bad%2"); err == nil {
		t.Fatal("truncated escape accepted")
	}
	if _, err := dbusConnect("tcp:host=localhost,port=1"); err == nil {
		t.Fatal("unsupported transport accepted")
	}
}
