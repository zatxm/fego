package ui

import (
	"math"
	"slices"
	"testing"
)

func TestPaginationNavigation(t *testing.T) {
	var changes []int
	pager := Pagination(2, 10, func(page int) { changes = append(changes, page) })
	host := NewTester(Column(pager).Fill(), 450, 100)
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
	third := pager.children[3]
	click(third)
	if check([]int{2, 3, 4}, 3, 2) != third || host.Document.live.focused != third {
		t.Fatal("number lost focus while its window shifted")
	}
	click(third)
	click(pager.children[0])
	check([]int{1, 2, 3}, 2, 1)
	host.Document.live.focus(pager.children[len(pager.children)-1])
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
	prev, next := pager.children[0], pager.children[len(pager.children)-1]
	if prev.rect.W != pageButtonSize || next.rect.W != pageButtonSize || next.rect.X+next.rect.W > 280 {
		t.Fatalf("compact arrows overflow: %v / %v", prev.rect, next.rect)
	}
	for _, theme := range []Theme{Light, Dark} {
		doc.Theme(theme)
		img := host.Snapshot()
		active := pager.children[3]
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
		if p.children[0].disabled != test.prev || p.children[len(p.children)-1].disabled != test.next {
			t.Fatalf("boundary buttons for %d/%d", test.page, test.pages)
		}
		if !slices.ContainsFunc(p.children, func(e *Element) bool {
			return e.currentPage && e.Selected() == test.current
		}) {
			t.Fatalf("wrong current page for %d/%d", test.page, test.pages)
		}
	}
}
