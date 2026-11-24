package ui

import (
	"image"
	"math"
)

type pixelScroll struct {
	element            *Element
	rect, full         image.Rectangle
	dy                 int
	eraseRow, selected int
	start              float32
	copyThumb          bool
}

func (v *view) scrollRows(e *Element, next float32) {
	if e.kind == kVirtualList {
		if e.scroll != next {
			e.scroll = next
			e.virtual.layout(v, e.innerRect(), e.clip, v.needsLayout)
			v.invalidate(e, false)
		}
		return
	}
	old := e.scroll
	if old == next {
		return
	}
	previous := v.scrolling
	coalescing := previous != nil && previous.element == e
	clean := !v.needsLayout && (len(v.dirty) == 0 || coalescing) && !v.size.Empty()
	eraseRow := -1
	start, selected := old, e.selected
	if coalescing {
		eraseRow = previous.eraseRow
		start, selected = previous.start, previous.selected
	}
	if e == v.hover && v.hoveredRow >= 0 {
		if v.hoveredRow != e.selected {
			eraseRow = v.hoveredRow
		}
		v.hoveredRow = -1
	}
	e.scroll = next
	localLayout := clean && e.scrollable && len(e.children) == 1 && !e.children[0].hidden
	exactShift := true
	if localLayout {
		s := &e.style
		inner := Rect{e.rect.X + s.padding[3], e.rect.Y + s.padding[0], max(0, e.rect.W-s.padding[1]-s.padding[3]), max(0, e.rect.H-s.padding[0]-s.padding[2])}
		exactShift = (inner.Y-next)-e.children[0].rect.Y == old-next
		e.children[0].layoutY = -next
		v.moveLayout(e.children[0], inner.Y-next, e.clip.intersect(inner))
	}
	v.invalidate(e, e.scrollable && !localLayout)
	editor := e.kind == kEditor && v.focused != e && v.theme.Raised.A == 255
	opaque := (e.rowCount() > 0 || editor) && v.theme.Surface.A == 255
	if localLayout {
		if v.scrollCheck != e {
			v.scrollCheck = e
			v.scrollCopyOK = scrollCopySafe(e)
		}
		opaque = exactShift && (e.style.bgSet && e.style.bg.A == 255 || e == v.root && v.theme.Background.A == 255) && v.scrollCopyOK
	}
	if !clean || !opaque || v.dialog != nil || v.popup != nil || v.popover.content != nil || v.toastCount != 0 || v.tip.shown {
		return
	}
	delta := (old - next) * v.scale
	if math.IsNaN(float64(delta)) || math.IsInf(float64(delta), 0) || delta != float32(math.Round(float64(delta))) {
		return
	}
	// Keep rounded edges, fixed headers and the scrollbar out of the copied area.
	margin := int(math.Ceil(float64(max(0, e.style.radius)*v.scale))) + 2
	r := e.rect.pixels(v.scale)
	r.Min.X = int(math.Ceil(float64(e.rect.X * v.scale)))
	r.Max.X = min(r.Max.X, int(math.Floor(float64((e.rect.X+e.rect.W-9)*v.scale))))
	copyThumb := false
	if e.rowCount() > 0 {
		// Keep rounded corners in narrow side bands instead of repainting
		// full-width margins above and below the copied rows.
		flat := e.style.radius == 0 || (e == v.root && v.theme.Background == v.theme.Surface &&
			e.style.tone == standardTone && !e.style.card && !e.style.bgSet && !e.style.borderSet &&
			!(v.focusVisible && v.focused == e))
		if !flat {
			r.Min.X += margin
			r.Max.X = min(r.Max.X, int(math.Floor(float64((e.rect.X+e.rect.W)*v.scale)))-margin)
		} else if e.rect.H-e.headerHeight() >= 24 {
			// The thumb is the only moving overlay. Repaint just its old and
			// new positions after copying the otherwise opaque row surface.
			r.Max.X = int(math.Floor(float64((e.rect.X + e.rect.W) * v.scale)))
			copyThumb = true
		}
		r.Min.Y = int(math.Ceil(float64((e.rect.Y + e.headerHeight()) * v.scale)))
		r.Max.Y = int(math.Floor(float64((e.rect.Y + e.rect.H) * v.scale)))
	} else {
		r.Min.Y += margin
		r.Max.Y -= margin
	}
	if editor {
		inner := v.inputInner(e)
		scrolling := Rect{e.rect.X + 4, inner.Y, inner.W + 48, inner.H}
		r = r.Intersect(scrolling.pixels(v.scale))
	}
	r = r.Intersect(e.clip.pixels(v.scale)).Intersect(v.size)
	if coalescing {
		if r != previous.rect {
			return
		}
		delta += float32(previous.dy)
	}
	if r.Empty() || math.Abs(float64(delta)) >= float64(r.Dy()) || delta == 0 {
		return
	}
	for child := e; child.parent != nil; child = child.parent {
		for _, sibling := range child.parent.children {
			if sibling != child && !sibling.hidden && sibling.clip.pixels(v.scale).Inset(-sibling.paintPadding(v.scale)).Overlaps(r) {
				return
			}
		}
	}
	full := v.dirty[0]
	if header := e.headerHeight(); header > 0 {
		// The fixed header did not scroll. Excluding it keeps paint/upload bands
		// sized for the exposed body strips instead of the entire header.
		full.Min.Y = max(full.Min.Y, int(math.Floor(float64((e.rect.Y+header)*v.scale))))
	}
	v.scrollState = pixelScroll{element: e, rect: r, full: full, dy: int(delta), eraseRow: eraseRow, selected: selected, start: start, copyThumb: copyThumb}
	v.scrolling = &v.scrollState
}

func scrollCopySafe(e *Element) bool {
	// Custom canvas callbacks may depend on absolute layout coordinates.
	if e.kind == kCanvas || e.kind == kVirtualList {
		return false
	}
	for _, child := range e.children {
		if !child.hidden && !scrollCopySafe(child) {
			return false
		}
	}
	return true
}

// copy handles overlapping rows; copy bottom first when moving pixels downward.
func movePixels(img *image.RGBA, r image.Rectangle, dy int) image.Rectangle {
	dst := r.Intersect(r.Add(image.Pt(0, dy)))
	start, end, step := dst.Min.Y, dst.Max.Y, 1
	if dy > 0 {
		start, end, step = dst.Max.Y-1, dst.Min.Y-1, -1
	}
	for y := start; y != end; y += step {
		d := img.PixOffset(dst.Min.X, y)
		s := img.PixOffset(dst.Min.X, y-dy)
		copy(img.Pix[d:d+dst.Dx()*4], img.Pix[s:s+dst.Dx()*4])
	}
	return dst
}
