package ui

import "strconv"

type pageControl uint8

const (
	pageBar pageControl = iota + 1
	pageNumber
	pagePrevious
	pageNext
	pageFirst
	pageLast
	pageDots
	pageJump
	pageEntry
)

const pageButtonSize = 36

// Pagination shows page navigation and an editable current page. Page numbers
// start at 1. Use PaginationWithTotal to also display the total record count.
func Pagination(page, pages int, onChange func(int)) *Element {
	return pagination(page, pages, -1, onChange)
}

// PaginationWithTotal also shows the total record count after the page count.
func PaginationWithTotal(page, pages, total int, onChange func(int)) *Element {
	return pagination(page, pages, max(0, total), onChange)
}

func pagination(page, pages, total int, onChange func(int)) *Element {
	pages = max(1, pages)
	page = min(max(1, page), pages)

	bar := Row().Gap(4).FillWidth().Label("Pagination")
	bar.own().pagination = pageBar
	makeButton := func(kind pageControl, name string, click func()) *Element {
		b := Button("", click).Label(name).Height(pageButtonSize).Radius(10).MinWidth(pageButtonSize)
		b.own().pagination = kind
		if kind != pageNumber && kind != pageJump {
			b.Width(pageButtonSize)
		}
		return b
	}
	newEllipsis := func() *Element {
		dots := Canvas(pageButtonSize, pageButtonSize, func(p *Painter) {
			r := p.Bounds()
			for _, offset := range [...]float32{-4, 0, 4} {
				p.Circle(r.W/2+offset, r.H/2, 1, p.Theme().Text)
			}
		})
		dots.own().pagination = pageDots
		return dots.Label("More pages")
	}
	leading, trailing := newEllipsis(), newEllipsis()

	var update func()
	selectPage := func(next int) {
		if next == page || next < 1 || next > pages {
			return
		}
		page = next
		update()
		if onChange != nil {
			onChange(page)
		}
	}
	first := makeButton(pageFirst, "First page", func() { selectPage(1) })
	previous := makeButton(pagePrevious, "Previous page", func() { selectPage(page - 1) })
	next := makeButton(pageNext, "Next page", func() {
		if page < pages {
			selectPage(page + 1)
		}
	})
	last := makeButton(pageLast, "Last page", func() { selectPage(pages) })

	pageCount := strconv.Itoa(pages)
	countText, countLabel := "/"+pageCount, pageCount+" pages"
	if total >= 0 {
		records := strconv.Itoa(total)
		countText += "  " + records
		countLabel += ", " + records + " records"
	}
	counts := Text(countText).Label(countLabel)
	jump := makeButton(pageJump, "", nil)
	jump.own().count = len(pageCount)
	status := Row(jump, counts).Gap(0)
	var entry *Element
	jump.OnClick(func() {
		v := jump.view
		if v == nil {
			return
		}
		if entry == nil {
			entry = TextInput("").Height(pageButtonSize).Label("Page number")
			entry.own().pagination, entry.own().count = pageEntry, len(pageCount)
			entry.OnSubmit(func() {
				number, err := strconv.Atoi(entry.text)
				if err == nil && number >= 1 && number <= pages {
					if v := entry.view; v != nil {
						v.finishPageEdit(entry)
						selectPage(number)
					}
				}
			})
			status.Children(jump, entry, counts)
		}
		entry.SetText(jump.text)
		entry.own().cursor, entry.own().anchor = len(entry.text), len(entry.text)
		jump.Hidden(true)
		entry.Hidden(false)
		// The input takes the number's existing slot so focus and IME can update immediately.
		v.layout(entry, jump.rect, jump.clip)
		v.focus(entry)
	})

	var children [10]*Element
	items := append(children[:0], first, previous, leading)
	for range min(3, pages) {
		button := makeButton(pageNumber, "", nil)
		button.OnClick(func() { selectPage(button.selected) })
		items = append(items, button)
	}
	items = append(items, trailing, next, last, status)
	bar.Children(items...)
	numbers := bar.children[3 : 3+min(3, pages)]

	update = func() {
		atFirst, atLast := page == 1, page == pages
		first.Disabled(atFirst)
		previous.Disabled(atFirst)
		next.Disabled(atLast)
		last.Disabled(atLast)
		start := min(max(1, page-1), max(1, pages-2))
		leading.Hidden(start == 1)
		trailing.Hidden(start+(len(numbers)-1) == pages)

		// Keep overlapping pages attached and recycle the remaining buttons.
		var order, unused [3]*Element
		var digits [3]int
		copy(unused[:], numbers)
		for i, button := range numbers {
			digits[i] = len(button.text)
			for j, button := range unused {
				if button != nil && button.selected == start+i {
					order[i], unused[j] = button, nil
					break
				}
			}
		}
		j := 0
		widthChanged := false
		for i := range numbers {
			number := start + i
			button := order[i]
			current := number == page
			if button == nil {
				for unused[j] == nil {
					j++
				}
				button = unused[j]
				j++
				button.own().selected = number
				button.text = strconv.Itoa(number)
			}
			widthChanged = widthChanged || digits[i] != len(button.text)
			numbers[i] = button
			if order[i] == nil || current != button.currentPage {
				button.own().currentPage = current
				if current {
					button.label = "Page " + button.text + ", current page"
					jump.text = button.text
					jump.label = "Current page " + button.text + ", jump to page"
				} else {
					button.label = "Page " + button.text
				}
			}
		}
		if v := bar.view; v != nil {
			if widthChanged || v.needsLayout || v.popover.layout {
				bar.touch(true)
			} else {
				// The pager keeps its footprint. Move its controls and repaint only their slots.
				v.layoutPagination(bar, bar.innerRect(), bar.clip)
				for _, button := range numbers {
					button.touch(false)
				}
				jump.touch(false)
			}
		}
	}
	update()
	return bar
}

func (v *view) finishPageEdit(entry *Element) {
	button := entry.parent.children[0]
	button.Hidden(false)
	v.focus(button)
}

func (e *Element) closePageEntry() {
	e.parent.children[0].Hidden(false)
	e.Hidden(true)
	e.SetText("")
	e.own().undo = nil
	e.own().undoBytes = 0
}

func pageEntryWidth(digits int, font float32) float32 {
	return max(pageButtonSize, float32(digits)*font*.66+18)
}

// A narrow pager keeps space for the readout and compresses its navigation.
func (v *view) layoutPagination(bar *Element, r, clip Rect) {
	if len(bar.children) == 0 {
		return
	}
	gap := bar.style.gap
	status := bar.children[len(bar.children)-1]
	navigation := bar.children[:len(bar.children)-1]
	statusW, statusH := v.measure(status)
	var navW, dotsW float32
	for _, child := range navigation {
		if !child.hidden {
			w, _ := v.measure(child)
			navW += w + gap
			if child.pagination == pageDots {
				dotsW += w + gap
			}
		}
	}
	sectionGap := min(12, r.W/24)
	hideDots := statusW+navW+sectionGap > r.W
	if hideDots {
		navW -= dotsW
		statusW = min(statusW, r.W*.55)
	}
	fit := min(1, max(0, r.W-statusW-sectionGap)/max(1, navW))
	x := r.X + max(0, (r.W-navW*fit-sectionGap-statusW)/2)
	clip = clip.intersect(r)
	for _, child := range navigation {
		if child.hidden || hideDots && child.pagination == pageDots {
			v.layout(child, Rect{}, Rect{})
			continue
		}
		w, h := v.measure(child)
		w, h = w*fit, min(h, r.H)
		v.layout(child, Rect{x, r.Y + (r.H-h)/2, w, h}, clip)
		x += w + gap*fit
	}
	h := min(statusH, r.H)
	v.layout(status, Rect{x + sectionGap, r.Y + (r.H-h)/2, statusW, h}, clip)
}

func (v *view) paintPageButton(p *Painter, button *Element, r Rect, ink, border Color, disabled bool) {
	fill := Color{}
	if button.currentPage {
		p.RoundRect(r, button.style.radius, border)
		r = Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}
		fill = v.theme.Background
	}
	if !disabled && (v.hover == button || v.pressed == button) {
		fill = v.theme.Raised
	}
	p.RoundRect(r, max(0, button.style.radius-1), fill)

	if button.pagination == pageNumber || button.pagination == pageJump {
		font := v.font(button)
		width, height := p.Measure(button.text, font, button.style.bold)
		x := r.X + (r.W-width)/2
		if button.pagination == pageJump {
			x = r.X + r.W - width - 1
		}
		p.Label(button.text, x, r.Y+(r.H-height)/2, font, ink, button.style.bold)
		return
	}

	x, y := r.X+r.W/2, r.Y+r.H/2
	direction := float32(1)
	if button.pagination == pageFirst || button.pagination == pagePrevious {
		direction = -1
	}
	if button.pagination == pageFirst || button.pagination == pageLast {
		p.Line(x+5*direction, y-5, x+5*direction, y+5, 1.6, ink)
		x -= 2 * direction
	}
	p.Line(x-2.5*direction, y-5, x+2.5*direction, y, 1.6, ink)
	p.Line(x+2.5*direction, y, x-2.5*direction, y+5, 1.6, ink)
}
