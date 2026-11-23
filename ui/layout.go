package ui

import "strings"

func (v *view) measure(e *Element) (w, h float32) {
	if e.hidden {
		return
	}
	s := &e.style
	font := v.font(e)
	switch e.kind {
	case kBox, kVirtualRow:
		visible := 0
		var rowH float32
		for _, c := range e.children {
			if c.hidden {
				continue
			}
			cw, ch := v.measure(c)
			switch s.dir {
			case horizontal:
				w += cw
				h = max(h, ch)
			case grid:
				w = max(w, cw)
				rowH = max(rowH, ch)
			default:
				w = max(w, cw)
				h += ch
			}
			visible++
		}
		if visible > 0 {
			switch s.dir {
			case horizontal:
				w += float32(visible-1) * s.gap
			case grid:
				cols := min(s.columns, visible)
				rows := (visible + cols - 1) / cols
				w = w*float32(cols) + s.gap*float32(cols-1)
				h = rowH*float32(rows) + s.gap*float32(rows-1)
			default:
				h += float32(visible-1) * s.gap
			}
		}
	case kText:
		w, h = v.text.Measure(e.text, font, s.bold)
	case kRichText:
		for _, sp := range e.spans {
			sw, sh := v.text.Measure(sp.Text, font, sp.Bold || s.bold)
			w += sw
			h = max(h, sh)
		}
	case kButton:
		if e.pagination != 0 {
			w, h = pageButtonSize, pageButtonSize
			switch e.pagination {
			case pageJump:
				w = pageEntryWidth(e.count, font)
			case pageNumber:
				w = max(w, float32(len(e.text))*font*.66+12)
			}
			break
		}
		w, h = v.text.Measure(e.text, font, s.bold)
		w += e.style.button.inset() * 2
		if e.style.icon != noIcon && e.text != "" {
			w += 20
		}
		h = max(h+12, 36)
	case kCheck, kRadio:
		w, h = v.text.Measure(e.text, font, s.bold)
		w += 24
		h = max(28, h)
	case kSwitch:
		w, h = v.text.Measure(e.text, font, s.bold)
		w += switchTrackWidth + 10
		h = max(30, h)
	case kInput, kEditor:
		w = 220
		h = 36
		if e.multiline {
			h = 120
		} else if e.pagination == pageEntry {
			w = pageEntryWidth(e.count, font)
		}
	case kSelect:
		w = 180
		h = 36
	case kSlider:
		w = 200
		h = 32
	case kProgress:
		w = 180
		h = 8
	case kSeparator:
		w = 1
		h = 1
	case kList, kTable, kTree, kVirtualList:
		w = 280
		h = 240
	case kImage:
		if e.bitmap != nil {
			b := e.bitmap.Bounds()
			w, h = float32(b.Dx()), float32(b.Dy())
		}
	case kCanvas:
		w, h = 100, 100
	}
	w += s.padding[1] + s.padding[3]
	h += s.padding[0] + s.padding[2]
	if e.scrollable {
		h = 240
	}
	if s.w > 0 {
		w = s.w
	}
	if s.h > 0 {
		h = s.h
	}
	w = max(w, s.minW)
	h = max(h, s.minH)
	if s.maxW > 0 {
		w = min(w, s.maxW)
	}
	if s.maxH > 0 {
		h = min(h, s.maxH)
	}
	return
}

func (e *Element) innerRect() Rect {
	r, padding := e.rect, e.style.padding
	return Rect{r.X + padding[3], r.Y + padding[0], max(0, r.W-padding[1]-padding[3]), max(0, r.H-padding[0]-padding[2])}
}

func (v *view) layout(e *Element, r, clip Rect) {
	e.rect = r
	e.clip = r.intersect(clip)
	if e.hidden {
		e.rect = Rect{}
		return
	}
	s := &e.style
	inner := e.innerRect()
	if e.kind == kVirtualList {
		e.virtual.layout(v, inner, clip, true)
		return
	}
	if e.kind != kBox && e.kind != kVirtualRow {
		return
	}
	if e.pagination == pageBar {
		v.layoutPagination(e, inner, clip)
		return
	}
	if s.clip {
		clip = clip.intersect(r)
	}
	count := 0
	for _, c := range e.children {
		if !c.hidden {
			count++
		}
	}
	if count == 0 {
		if e.scrollable {
			e.contentH = 0
			e.scroll = 0
		}
		return
	}
	if e.scrollable {
		child := e.children[0]
		_, height := v.measure(child)
		e.contentH = max(inner.H, height)
		e.scroll = max(0, min(e.scroll, e.contentH-inner.H))
		child.layoutY = -e.scroll
		v.layout(child, Rect{inner.X, inner.Y + child.layoutY, inner.W, e.contentH}, clip.intersect(inner))
		return
	}
	if s.dir == grid {
		cols := min(s.columns, count)
		rows := (count + cols - 1) / cols
		cw := max(0, (inner.W-float32(cols-1)*s.gap)/float32(cols))
		var ch float32
		if s.fillH {
			ch = max(0, (inner.H-float32(rows-1)*s.gap)/float32(rows))
		} else {
			for _, c := range e.children {
				if !c.hidden {
					_, height := v.measure(c)
					ch = max(ch, height)
				}
			}
		}
		i := 0
		for _, c := range e.children {
			if !c.hidden {
				c.layoutY = float32(i/cols) * (ch + s.gap)
				v.layout(c, Rect{inner.X + float32(i%cols)*(cw+s.gap), inner.Y + c.layoutY, cw, ch}, clip)
				i++
			}
		}
		return
	}
	horizontal := s.dir == horizontal
	main, cross := inner.H, inner.W
	if horizontal {
		main, cross = inner.W, inner.H
	}
	used, grow := float32(count-1)*s.gap, float32(0)
	for _, c := range e.children {
		if c.hidden {
			continue
		}
		if weight := c.growth(horizontal); weight > 0 {
			grow += weight
		} else {
			cw, ch := v.measure(c)
			if horizontal {
				used += cw
			} else {
				used += ch
			}
		}
	}
	free := max(0, main-used)
	offset, gap := float32(0), s.gap
	if grow == 0 {
		switch s.justify {
		case Center:
			offset = free / 2
		case End:
			offset = free
		case SpaceBetween:
			if count > 1 {
				gap += free / float32(count-1)
			}
		}
	}
	for _, c := range e.children {
		if c.hidden {
			c.rect = Rect{}
			continue
		}
		weight := c.growth(horizontal)
		fillCross := s.align == Stretch || (!horizontal && c.style.fillW) || (horizontal && c.style.fillH)
		var cm, cc float32
		if weight == 0 || !fillCross {
			cw, ch := v.measure(c)
			cm, cc = ch, cw
			if horizontal {
				cm, cc = cw, ch
			}
		}
		if weight > 0 {
			cm = free * weight / grow
		}
		if fillCross {
			cc = cross
		}
		cc = min(cc, cross)
		co := float32(0)
		if s.align == Center {
			co = (cross - cc) / 2
		} else if s.align == End {
			co = cross - cc
		}
		c.layoutY = offset
		cr := Rect{inner.X + co, inner.Y + c.layoutY, max(0, cc), max(0, cm)}
		if horizontal {
			c.layoutY = co
			cr = Rect{inner.X + offset, inner.Y + c.layoutY, max(0, cm), max(0, cc)}
		}
		v.layout(c, cr, clip)
		offset += cm + gap
	}
}

// Clean scrolling changes positions and clips, not measured sizes or alignment.
func (v *view) moveLayout(e *Element, y float32, clip Rect) {
	if e.hidden {
		e.rect = Rect{}
		return
	}
	e.rect.Y = y
	e.clip = e.rect.intersect(clip)
	if e.kind == kVirtualList {
		v.layout(e, e.rect, clip)
		return
	}
	if e.kind != kBox {
		return
	}
	s := &e.style
	inner := Rect{e.rect.X + s.padding[3], y + s.padding[0], max(0, e.rect.W-s.padding[1]-s.padding[3]), max(0, e.rect.H-s.padding[0]-s.padding[2])}
	if s.clip {
		clip = clip.intersect(e.rect)
	}
	if e.scrollable {
		if len(e.children) != 0 {
			child := e.children[0]
			v.moveLayout(child, inner.Y+child.layoutY, clip.intersect(inner))
		}
		return
	}
	for _, c := range e.children {
		v.moveLayout(c, inner.Y+c.layoutY, clip)
	}
}

func (e *Element) growth(horizontal bool) float32 {
	if e.style.grow > 0 {
		return e.style.grow
	}
	if (horizontal && e.style.fillW) || (!horizontal && e.style.fillH) {
		return 1
	}
	return 0
}

func (v *view) font(e *Element) float32 {
	if e.style.font > 0 {
		return e.style.font
	}
	return v.theme.FontSize
}
func (e *Element) rowCount() int {
	switch e.kind {
	case kList:
		return e.count
	case kTable:
		return len(e.rows)
	case kTree:
		return len(e.visibleTree)
	}
	return 0
}
func (e *Element) rowHeight() float32 {
	if e.kind == kTable {
		return 42
	}
	if e.style.tone == commandSurface || e.style.tone == suggestionSurface {
		return 32
	}
	return 36
}
func (e *Element) headerHeight() float32 {
	if e.kind == kTable {
		return 40
	}
	return 0
}
func (e *Element) maxScroll() float32 {
	if e.kind == kVirtualList {
		return max(0, float32(e.virtual.top(e.virtual.options.Count))-e.rect.H+e.style.padding[0]+e.style.padding[2])
	}
	if e.scrollable {
		return max(0, e.contentH-e.rect.H+e.style.padding[0]+e.style.padding[2])
	}
	if e.kind == kInput || e.kind == kEditor {
		font := float32(14)
		if e.view != nil {
			font = e.view.font(e)
		}
		lineH := font * 1.5
		if e.view != nil {
			lineH = e.view.text.LineHeight(font, false)
		}
		lines := 1
		if e.kind == kEditor {
			lines = e.sourceIndex().visibleCount()
		} else {
			lines = strings.Count(e.text, "\n") + 1
		}
		return max(0, float32(lines)*lineH-e.rect.H+20)
	}
	return max(0, float32(e.rowCount())*e.rowHeight()-e.rect.H+e.headerHeight())
}
