package ui

import (
	"math"
	"slices"
	"testing"

	"github.com/zatxm/fego/v26"
)

func TestPaginationNavigation(t *testing.T) {
	var changes []int
	pager := Pagination(2, 10, func(page int) { changes = append(changes, page) })
	host := NewTester(Column(pager).Fill(), 640, 100)
	defer host.Close()

	click := func(button *Element) {
		r := button.Bounds()
		host.Click(r.X+r.W/2, r.Y+r.H/2)
	}
	check := func(want []int, current int, ellipses int) *Element {
		t.Helper()
		var numbers []int
		var active *Element
		dots := 0
		for _, child := range pager.children {
			if child.hidden {
				continue
			}
			if child.pagination == pageNumber {
				numbers = append(numbers, child.Selected())
				if child.currentPage {
					if active != nil {
						t.Fatal("multiple current pages")
					}
					active = child
				}
			} else if child.kind == kCanvas {
				dots++
				if child.interactive() {
					t.Fatal("ellipsis takes keyboard focus")
				}
			}
		}
		if !slices.Equal(numbers, want) || active == nil || active.Selected() != current || dots != ellipses {
			t.Fatalf("visible pages %v, active %v, ellipses %d", numbers, active, dots)
		}
		return active
	}
	check([]int{1, 2, 3}, 2, 1)
	third := pager.children[5]
	click(third)
	if check([]int{2, 3, 4}, 3, 2) != third || host.Document.live.focused != third {
		t.Fatal("number lost focus while its window shifted")
	}
	click(third)
	click(pager.children[1])
	check([]int{1, 2, 3}, 2, 1)
	host.Document.live.focus(pager.children[len(pager.children)-3])
	host.Key("Enter", false, false)
	check([]int{2, 3, 4}, 3, 2)
	if !slices.Equal(changes, []int{3, 2, 3}) {
		t.Fatalf("page changes %v", changes)
	}
}

func TestPaginationCompactAndBounds(t *testing.T) {
	pager := Pagination(50, 100, nil)
	doc := View(Column(pager).Fill())
	host := NewDocumentTester(doc, 280, 100)
	defer host.Close()
	prev, next := pager.children[1], pager.children[len(pager.children)-3]
	for _, child := range []*Element{pager.children[0], prev, next, pager.children[len(pager.children)-2]} {
		if child.rect.W <= 0 || child.rect.X+child.rect.W > 280 || child.text != "" {
			t.Fatalf("compact icon is missing or overflows: %v", child.rect)
		}
	}
	for _, theme := range []Theme{Light, Dark} {
		doc.Theme(theme)
		img := host.Snapshot()
		active := pager.children[4]
		if !active.currentPage {
			t.Fatal("missing current page")
		}
		r := active.Bounds()
		if img.RGBAAt(int(r.X+r.W/2), int(r.Y)) == theme.Background {
			t.Fatal("current page outline missing")
		}
		r = prev.Bounds()
		if img.RGBAAt(int(r.X+4), int(r.Y+r.H/2)) != theme.Background {
			t.Fatal("idle previous button has a fill")
		}
	}
	for _, test := range []struct {
		page, pages, current int
		prev, next           bool
	}{
		{0, 3, 1, true, false},
		{10, 3, 3, false, true},
		{1, 0, 1, true, true},
		{math.MaxInt, math.MaxInt, math.MaxInt, false, true},
	} {
		p := Pagination(test.page, test.pages, nil)
		if p.children[0].disabled != test.prev || p.children[1].disabled != test.prev ||
			p.children[len(p.children)-2].disabled != test.next || p.children[len(p.children)-3].disabled != test.next {
			t.Fatalf("boundary buttons for %d/%d", test.page, test.pages)
		}
		if !slices.ContainsFunc(p.children, func(e *Element) bool {
			return e.currentPage && e.Selected() == test.current
		}) {
			t.Fatalf("wrong current page for %d/%d", test.page, test.pages)
		}
	}
}

func TestPaginationJumpAndEndpoints(t *testing.T) {
	var changes []int
	pager := PaginationWithTotal(5, 240, 23941, func(page int) { changes = append(changes, page) })
	host := newScrollScreen(View(Column(pager).Fill()), 1, true)
	defer host.Close()
	status := pager.children[len(pager.children)-1]
	readout, totals := status.children[0], status.children[1]
	if readout.text != "5" || totals.text != "/240  23941" {
		t.Fatalf("readout %q and totals %q", readout.text, totals.text)
	}
	positions := totals.rect
	clickElement(host.Tester, readout)
	entry := status.children[1]
	if !readout.hidden || entry.kind != kInput || !host.Editing || entry.text != "5" || totals.rect != positions {
		t.Fatal("inline editor did not preserve the readout layout or activate text input")
	}
	role := func(id uint64) string {
		for _, node := range host.nodes {
			if node.ID == id {
				return node.Role
			}
		}
		return ""
	}
	if role(entry.id) != "input" || role(readout.id) != "" {
		t.Fatal("accessibility should expose only the active input")
	}

	host.Key("a", false, true)
	host.Type("a123")
	host.Type("1234")
	if entry.text != "5" {
		t.Fatal("page entry must reject non-digits and oversized values")
	}
	host.Type("241")
	host.Key("Enter", false, false)
	if len(changes) != 0 || !host.Editing {
		t.Fatal("out-of-range jump changed page or closed the editor")
	}
	host.Key("a", false, true)
	host.Type("120")
	host.Key("Enter", false, false)
	if readout.text != "120" || host.Editing || host.Document.live.focused != readout ||
		role(readout.id) != "button" || role(entry.id) != "" {
		t.Fatal("valid jump did not return focus to the new page number")
	}

	host.Key("Enter", false, false)
	if entry.text != "120" {
		t.Fatalf("reopened editor did not start with the current page %q", entry.text)
	}
	host.Key("Backspace", false, false)
	host.Key("Escape", false, false)
	if host.Editing || readout.text != "120" || entry.text != "" || len(entry.undo) != 0 {
		t.Fatal("cancelling a jump retained the draft or changed the page")
	}

	first, previous := pager.children[0], pager.children[1]
	next, last := pager.children[len(pager.children)-3], pager.children[len(pager.children)-2]
	host.Send(fego.Event{Kind: fego.AccessAction, AccessID: last.id, Action: "press"})
	if !next.disabled || !last.disabled || first.disabled || previous.disabled || readout.text != "240" {
		t.Fatal("last-page navigation and disabled states")
	}
	clickElement(host.Tester, readout)
	clickElement(host.Tester, first)
	if !first.disabled || !previous.disabled || next.disabled || last.disabled || host.Editing || readout.text != "1" {
		t.Fatal("first-page navigation should cancel an unsubmitted edit")
	}
	if !slices.Equal(changes, []int{120, 240, 1}) {
		t.Fatalf("unexpected page changes: %v", changes)
	}
}

func TestPaginationReusesNumberButtons(t *testing.T) {
	pager := PaginationWithTotal(5, math.MaxInt, math.MaxInt, nil)
	original := slices.Clone(pager.children[3:6])
	last := pager.children[len(pager.children)-2]
	last.onClick()
	for _, button := range pager.children[3:6] {
		if !slices.Contains(original, button) {
			t.Fatal("jumping across pages allocated another page button")
		}
	}
}

func TestPaginationPartialPaintMatchesFullLayout(t *testing.T) {
	for _, theme := range []Theme{Light, Dark} {
		for _, scale := range []float32{1, 1.25, 2} {
			pager := PaginationWithTotal(97, 240, 23941, nil)
			host := newScrollScreen(View(Column(pager).Fill()).Theme(theme), scale, true)
			for range 8 {
				clickElement(host.Tester, pager.children[len(pager.children)-3])
				checkScrollPixels(t, host)
			}
			host.Close()
		}
	}
}
