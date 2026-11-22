package ui

import "strconv"

type pageControl uint8

const (
	pageBar pageControl = iota + 1
	pageNumber
	pagePrevious
	pageNext
)

const pageButtonSize = 36

// Pagination displays a window of page numbers and calls onChange with the
// selected page, numbered from 1.
func Pagination(page, pages int, onChange func(int)) *Element {
	pages = max(1, pages)
	page = min(max(1, page), pages)

	bar := Row().Gap(4).Justify(Center).FillWidth().Label("Pagination")
	bar.own().pagination = pageBar
	makeButton := func(kind pageControl, text string, click func()) *Element {
		b := Button(text, click).Height(pageButtonSize).Radius(10).MinWidth(pageButtonSize)
		b.own().pagination = kind
		if kind != pageNumber {
			b.Padding(0, 4)
		}
		return b
	}
	newEllipsis := func() *Element {
		return Canvas(pageButtonSize, pageButtonSize, func(p *Painter) {
			r := p.Bounds()
			for _, offset := range [...]float32{-4, 0, 4} {
				p.Circle(r.W/2+offset, r.H/2, 1, p.Theme().Text)
			}
		}).Label("More pages")
	}
	leading, trailing := newEllipsis(), newEllipsis()
	var visible [3]*Element
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
	previous := makeButton(pagePrevious, "Previous", func() { selectPage(page - 1) }).Label("Previous page")
	next := makeButton(pageNext, "Next", func() {
		if page < pages {
			selectPage(page + 1)
		}
	}).Label("Next page")

	update = func() {
		previous.Disabled(page == 1)
		next.Disabled(page == pages)

		first := min(max(1, page-1), max(1, pages-2))
		count := min(len(visible), pages-first+1)
		old := visible
		var children [7]*Element
		items := append(children[:0], previous)
		if first > 1 {
			items = append(items, leading)
		}
		for i := range count {
			number := first + i
			var button *Element
			for _, candidate := range old {
				if candidate != nil && candidate.selected == number {
					button = candidate
					break
				}
			}
			if button == nil {
				button = makeButton(pageNumber, strconv.Itoa(number), func() { selectPage(number) })
				button.own().selected = number
			}
			if current := number == page; button.currentPage != current || button.label == "" {
				button.own().currentPage = current
				name := "Page " + button.text
				if current {
					name += ", current page"
				}
				button.Label(name)
			}
			visible[i] = button
			items = append(items, button)
		}
		if first+(count-1) < pages {
			items = append(items, trailing)
		}
		items = append(items, next)
		bar.Children(items...)
	}
	update()
	return bar
}

// Narrow bars keep the chevrons and reserve the available width for page numbers.
func (v *view) layoutPagination(bar *Element, r, clip Rect) {
	width := bar.style.gap * float32(max(0, len(bar.children)-1))
	for _, child := range bar.children {
		w, _ := v.measure(child)
		width += w
	}
	compact := width > r.W
	if compact {
		for _, child := range bar.children {
			if child.pagination == pagePrevious || child.pagination == pageNext {
				w, _ := v.measure(child)
				width += pageButtonSize - w
			}
		}
	}
	x := r.X + max(0, (r.W-width)/2)
	space := min(1, r.W/max(1, width))
	for _, child := range bar.children {
		w, h := v.measure(child)
		if compact && (child.pagination == pagePrevious || child.pagination == pageNext) {
			w = pageButtonSize
		}
		w *= space
		h = min(h, r.H)
		v.layout(child, Rect{x, r.Y + (r.H-h)/2, w, h}, clip.intersect(r))
		x += w + bar.style.gap*space
	}
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

	font := v.font(button)
	width, height := p.Measure(button.text, font, button.style.bold)
	x := r.X + (r.W-width)/2
	y := r.Y + (r.H-height)/2
	if button.pagination == pageNumber {
		p.Label(button.text, x, y, font, ink, button.style.bold)
		return
	}
	chevronX := r.X + r.W/2
	if width+28 <= r.W {
		x -= 10
		chevronX = x + width + 12
		if button.pagination == pagePrevious {
			chevronX = x + 8
			x += 20
		}
		p.Label(button.text, x, y, font, ink, button.style.bold)
	}
	chevronY := r.Y + r.H/2
	direction := float32(1)
	if button.pagination == pagePrevious {
		direction = -1
	}
	p.Line(chevronX-2.5*direction, chevronY-5, chevronX+2.5*direction, chevronY, 1.6, ink)
	p.Line(chevronX+2.5*direction, chevronY, chevronX-2.5*direction, chevronY+5, 1.6, ink)
}
