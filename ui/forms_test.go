package ui

import (
	"reflect"
	"testing"

	"github.com/zatxm/fego/v26"
)

func TestFormControlsEditing(t *testing.T) {
	var code, money string
	otp := InputOTP(4, func(s string) { code = s })
	decimal := DecimalInput("", 2, func(s string) { money = s })
	var tags []string
	token := TokenField(nil, func(s []string) { tags = s })
	test := NewTester(Column(otp, decimal, token), 320, 280)
	defer test.Close()
	test.Click(10, 10)
	test.Type("12x 345")
	if code != "1234" || otp.Text() != "1234" {
		t.Fatalf("OTP %q text %q", code, otp.Text())
	}
	test.Click(10, 60)
	test.Type("9007199254740993.25")
	if money != "9007199254740993.25" {
		t.Fatalf("rounded monetary value %q", money)
	}
	input := token.children[1].children[0]
	test.Send(fego.Event{Kind: fego.PointerDown, X: 10, Y: 115, Button: 1})
	test.Send(fego.Event{Kind: fego.PointerUp, X: 10, Y: 115, Button: 1})
	test.Type("network")
	test.Key("Enter", false, false)
	if !reflect.DeepEqual(tags, []string{"network"}) || input.Text() != "" {
		t.Fatalf("tags %v input %q", tags, input.Text())
	}
}

func TestVirtualListRetainsVisibleRowsAndSelectsByKey(t *testing.T) {
	calls := 0
	var selected []int
	var activation int
	list := VirtualList(VirtualListOptions{Count: 100000, Key: func(i int) int { return i + 100 }, Headers: []int{0, 10, 30}, MultiSelect: true}, func(i int) *Element {
		calls++
		if i%2 == 0 {
			return Text("Tall").Height(48)
		}
		return Button("Short", func() { activation = i }).Height(24)
	}).OnSelection(func(keys []int) { selected = keys })
	test := NewTester(list.Element, 320, 240)
	defer test.Close()
	if calls < 4 || calls > 20 {
		t.Fatalf("built %d initial rows", calls)
	}
	test.Click(18, 60)
	if activation != 1 {
		t.Fatalf("virtual child did not receive click: %d", activation)
	}
	initial := list.rows[4]
	test.Send(fego.Event{Kind: fego.Scroll, X: 300, Y: 160, DY: 30})
	if list.rows[4] != initial {
		t.Fatal("visible row was replaced across scrolling")
	}
	list.ScrollTo(14)
	test.Step()
	if a, b := list.Visible(); a != 14 || b < 16 {
		t.Fatalf("noticeable scroll jump: visible %d..%d", a, b)
	}
	header := list.rows[10]
	if header == nil || header.rect.Y != 0 {
		t.Fatal("section header not pinned")
	}
	test.Document.live.focus(list.Element)
	test.Key("Home", false, false)
	test.Key("ArrowDown", true, false)
	if !reflect.DeepEqual(selected, []int{101, 102}) {
		t.Fatalf("range selection %v", selected)
	}
	list.ScrollTo(90000)
	test.Step()
	if len(list.rows) > 25 || calls > 55 {
		t.Fatalf("built %d and retained %d rows", calls, len(list.rows))
	}
}

func TestVirtualListFollowsAppendAndReorders(t *testing.T) {
	var dragged []int
	var before int
	list := VirtualList(VirtualListOptions{
		Count: 100000,
		Reorder: func(rows []int, destination int) {
			dragged, before = rows, destination
		},
	}, func(i int) *Element {
		return Text("Row").Height(float32(30 + i%3*12))
	})
	test := NewTester(list.Element, 260, 190)
	defer test.Close()
	test.Send(fego.Event{Kind: fego.PointerDown, X: 40, Y: 10, Button: 1})
	test.Send(fego.Event{Kind: fego.PointerUp, X: 40, Y: 65, Button: 1})
	if !reflect.DeepEqual(dragged, []int{0}) || before != 2 {
		t.Fatalf("drag moved %v before %d", dragged, before)
	}
	list.ScrollToEnd()
	test.Step()
	_, last := list.Visible()
	if last != 99999 || !list.AtEnd() {
		t.Fatalf("last visual row %d, at end %t", last, list.AtEnd())
	}
	lastRow := list.rows[last]
	list.SetCount(100001, true)
	test.Step()
	_, last = list.Visible()
	if last != 100000 || list.rows[99999] != lastRow {
		t.Fatalf("append lost the tail at row %d", last)
	}
}

func TestCommandPaletteRunsKeyboardMatch(t *testing.T) {
	root := Column()
	doc := View(root)
	ran := false
	root.Append(CommandPalette(doc, "Actions", Command{
		Label: "Open preferences", Keywords: "Settings", Action: func() { ran = true },
	}))
	test := NewDocumentTester(doc, 480, 320)
	defer test.Close()
	test.Click(10, 10)
	test.Type("settings")
	test.Key("Enter", false, false)
	if !ran || doc.live.dialog != nil {
		t.Fatal("keyboard did not invoke and dismiss the palette")
	}
}
