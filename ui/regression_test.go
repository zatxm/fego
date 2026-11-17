package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/zatxm/fego"
)

func TestFillDimensionsAndScrollClipping(t *testing.T) {
	sidebar := Column(Text("nav")).Width(190).FillHeight()
	var last *Element
	content := Column()
	for i := 0; i < 30; i++ {
		last = Button(fmt.Sprint(i), nil)
		content.Append(last)
	}
	scroll := ScrollView(content).Fill()
	view := NewTester(Row(sidebar, scroll).Gap(0), 700, 300)
	defer view.Close()
	if sidebar.rect.W != 190 || sidebar.rect.H != 300 || scroll.rect.W != 510 {
		t.Fatalf("incorrect fill geometry: %v %v", sidebar.rect, scroll.rect)
	}
	if hit(view.Document.root, last.rect.X+10, last.rect.Y+10) != nil {
		t.Fatal("offscreen row receives input")
	}
	view.Document.live.focus(last)
	view.Step()
	if last.rect.Y+last.rect.H > 300.1 {
		t.Fatal("focused item was not revealed")
	}
	scroll.scroll = 0
	scroll.touch(true)
	view.Step()
	view.Send(fego.Event{Kind: fego.Scroll, X: 400, Y: 280, DY: 100000})
	if scroll.scroll != scroll.maxScroll() || last.rect.Y+last.rect.H > 300.1 {
		t.Fatal("scroll did not reveal last item", last.rect, scroll.scroll)
	}
}

func TestLongPopupMouseSelectsPaintedItem(t *testing.T) {
	options := make([]string, 40)
	for i := range options {
		options[i] = fmt.Sprint(i)
	}
	control := Select(options, 30)
	view := NewTester(Column(control), 400, 500)
	defer view.Close()
	clickElement(view, control)
	p := view.Document.live.popup
	if p.first == 0 {
		t.Fatal("selected option not revealed")
	}
	expected := p.first + 1
	view.Click(p.rect.X+20, p.rect.Y+4+34+17)
	if control.Selected() != expected {
		t.Fatal("clicked a different option", control.Selected(), expected)
	}
}

func TestInputScrollAndEmptyWordNavigation(t *testing.T) {
	input := TextInput("").Width(130)
	view := NewTester(Column(input), 250, 100)
	defer view.Close()
	clickElement(view, input)
	view.Key("ArrowLeft", false, true)
	view.Type(strings.Repeat("abc", 50))
	inner := view.Document.live.inputInner(input)
	if input.scrollX == 0 || float32(view.Caret.Min.X) > inner.X+inner.W {
		t.Fatal("caret left viewport", view.Caret)
	}
	view.Key("Home", false, true)
	if input.scrollX != 0 {
		t.Fatal("Home did not reveal start")
	}
}

func TestKeyboardActivationKeepsSelectedRow(t *testing.T) {
	list := List(100, func(i int) string { return fmt.Sprint(i) })
	selected := -1
	list.OnSelect(func(i int) { selected = i })
	view := NewTester(Column(list), 300, 280)
	defer view.Close()
	view.Key("Tab", false, false)
	view.Key("End", false, false)
	view.Key("Enter", false, false)
	if selected != 99 || list.Selected() != 99 {
		t.Fatal("keyboard activated another row", selected, list.Selected())
	}
}

func TestScrollbarDragDoesNotSelectRows(t *testing.T) {
	list := List(1000, func(i int) string { return fmt.Sprint(i) })
	view := NewTester(Column(list.Fill()), 300, 300)
	defer view.Close()
	view.Send(fego.Event{Kind: fego.PointerDown, X: 297, Y: 10, Button: 1})
	view.Send(fego.Event{Kind: fego.PointerMove, X: 297, Y: 295})
	view.Send(fego.Event{Kind: fego.PointerUp, X: 297, Y: 295, Button: 1})
	if list.scroll < list.maxScroll()*.95 || list.Selected() != -1 {
		t.Fatal("scrollbar drag selected data or did not scroll", list.scroll, list.Selected())
	}
}

func TestDocumentCannotStealMountedRoot(t *testing.T) {
	root := Column(Text("owned"))
	first, second := View(root), View(root)
	view := NewDocumentTester(first, 200, 100)
	defer view.Close()
	defer func() {
		if recover() == nil {
			t.Fatal("mounted shared root accepted")
		}
		if root.view != first.live {
			t.Fatal("live root was stolen")
		}
	}()
	NewDocumentTester(second, 200, 100)
}

func TestTreeRejectsExpandedCycle(t *testing.T) {
	node := &TreeNode{Label: "cycle"}
	node.Children = []*TreeNode{node}
	tree := Tree(node)
	tree.expanded[node] = true
	defer func() {
		if recover() == nil {
			t.Fatal("expanded cycle accepted")
		}
	}()
	tree.rebuildTree()
}

func TestSliderExtremeFiniteRange(t *testing.T) {
	slider := Slider(0, -math.MaxFloat64, math.MaxFloat64)
	view := NewTester(Column(slider), 300, 100)
	defer view.Close()
	view.Click(slider.rect.X+slider.rect.W/2, slider.rect.Y+slider.rect.H/2)
	if math.IsNaN(slider.Value()) || math.IsInf(slider.Value(), 0) {
		t.Fatal("invalid midpoint")
	}
	view.Key("End", false, false)
	view.Key("ArrowRight", false, false)
	if slider.Value() != math.MaxFloat64 {
		t.Fatal("range end", slider.Value())
	}
}

func TestTreeEditsPreserveFocusAndClampScroll(t *testing.T) {
	input := TextInput("a")
	root := Column(input)
	view := NewTester(root, 300, 200)
	defer view.Close()
	clickElement(view, input)
	root.Append(Text("new"))
	view.Step()
	view.Type("b")
	if input.Text() != "ab" {
		t.Fatal("append lost existing focus", input.Text())
	}
	label := Text("line").Width(100)
	root.Children(label)
	view.Step()
	height := label.rect.H
	label.SetText("line\nline")
	view.Step()
	if label.rect.H <= height {
		t.Fatal("fixed width prevented height update")
	}
	area := TextArea(strings.Repeat("line\n", 100))
	root.Children(area)
	view.Step()
	view.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 20, DY: 10000})
	area.SetText("short")
	view.Step()
	if area.scroll != 0 {
		t.Fatal("text scroll remained beyond content")
	}
	list := List(100, func(i int) string { return fmt.Sprint(i) })
	root.Children(list)
	view.Step()
	clickElement(view, list)
	view.Key("End", false, false)
	list.SetCount(2)
	view.Step()
	if list.scroll != 0 {
		t.Fatal("list scroll remained beyond content")
	}
}

func TestModalDoesNotRestoreHiddenOrDisabledFocus(t *testing.T) {
	for _, disableParent := range []bool{false, true} {
		called := false
		button := Button("background", func() { called = true })
		root := Column(button)
		view := NewTester(root, 300, 200)
		view.Key("Tab", false, false)
		view.Document.ShowDialog(Dialog("Modal", Text("body")))
		if disableParent {
			root.Disabled(true)
		} else {
			button.Hidden(true)
		}
		view.Document.CloseDialog()
		view.Step()
		view.Key("Enter", false, false)
		if called {
			t.Fatal("invalid focus restored")
		}
		view.Close()
	}
}

type reentrantClipboardHost struct {
	*Tester
	read func() string
}

func (h *reentrantClipboardHost) Clipboard() string { return h.read() }

func TestClipboardReadDoesNotMutateStaleEditor(t *testing.T) {
	for _, action := range []string{"empty", "hide", "modal", "close"} {
		t.Run(action, func(t *testing.T) {
			changes := 0
			input := TextInput("keep").OnChange(func(string) { changes++ })
			doc := View(Column(input))
			host := &reentrantClipboardHost{Tester: &Tester{Document: doc, Width: 300, Height: 200, Scale: 1}}
			doc.Mount(host)
			doc.live.frame(false)
			doc.live.focus(input)
			input.anchor = 0
			input.cursor = 4
			host.read = func() string {
				switch action {
				case "empty":
					return ""
				case "hide":
					input.Hidden(true)
				case "modal":
					doc.ShowDialog(Dialog("Modal", Text("body")))
				case "close":
					doc.live.Close()
				}
				return "replace"
			}
			live := doc.live
			live.Event(fego.Event{Kind: fego.KeyDown, Key: "v", Control: true})
			if input.Text() != "keep" || changes != 0 {
				t.Fatal("stale clipboard changed input", input.Text(), changes)
			}
			host.Close()
		})
	}
}

func TestOversizedInputRejectedBeforeRuneAllocation(t *testing.T) {
	input := TextInput("keep")
	view := NewTester(Column(input), 300, 100)
	defer view.Close()
	clickElement(view, input)
	text := strings.Repeat("a", 8<<20)
	if n := testing.AllocsPerRun(10, func() { view.Document.live.replaceSelection(input, text) }); n != 0 {
		t.Fatal("oversized text allocated", n)
	}
	if input.Text() != "keep" {
		t.Fatal("oversized input accepted")
	}
}

type accessibleHost struct {
	*Tester
	nodes []fego.AccessNode
}

func (h *accessibleHost) SetAccessibility(nodes []fego.AccessNode) { h.nodes = nodes }

func TestAccessibilityActionsPrivacyAndModalBoundary(t *testing.T) {
	clicks := 0
	button := Button("Run", func() { clicks++ })
	secret := TextInput("private-token").Password(true).Label("Password")
	check := Checkbox("Enabled", false)
	doc := View(Column(button, secret, check))
	host := &accessibleHost{Tester: &Tester{Document: doc, Width: 400, Height: 300, Scale: 1}}
	doc.Mount(host)
	doc.live.frame(false)
	defer host.Close()
	for _, n := range host.nodes {
		if strings.Contains(n.Value, "private") || strings.Contains(n.Label, "private") {
			t.Fatal("password leaked into accessibility")
		}
	}
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: button.id, Action: "press"})
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: check.id, Action: "press"})
	if clicks != 1 || !check.Checked() {
		t.Fatal("accessible action not routed")
	}
	button.Disabled(true)
	host.Step()
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: button.id, Action: "press"})
	if clicks != 1 {
		t.Fatal("disabled accessibility action executed")
	}
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: secret.id, Action: "setValue", Text: "new中😀"})
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: secret.id, Action: "setSelection", Text: "1:3"})
	if secret.Text() != "new中😀" || secret.anchor != 1 || secret.cursor != 3 {
		t.Fatal("text action failed")
	}
	doc.ShowDialog(Dialog("Confirm", Text("Modal"), Button("OK", nil)))
	host.Step()
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: check.id, Action: "press"})
	if !check.Checked() {
		t.Fatal("background control activated through modal")
	}
	doc.CloseDialog()
	host.Step()
	check.Hidden(true)
	host.Step()
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: check.id, Action: "press"})
	if !check.Checked() {
		t.Fatal("hidden control activated")
	}
}
