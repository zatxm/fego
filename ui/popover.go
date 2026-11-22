package ui

import "github.com/zatxm/fego/v26"

type popover struct {
	owner, content, trigger *Element
	layout                  bool
	onClose                 func()
}

func (v *view) openPopover(owner, content *Element, onClose func()) {
	if v.popover.content != content {
		v.closePopover()
		v.closePopup()
		v.popover = popover{owner: owner, content: content, trigger: v.focused, layout: true, onClose: onClose}
		content.hidden = false
		content.attach(v)
	}
	v.host.Invalidate()
}

func (v *view) closePopover() {
	p := v.popover
	if p.content == nil {
		return
	}
	v.popover = popover{}
	v.forget(p.content)
	v.invalidate(p.content, false)
	p.content.hidden = true
	p.content.attach(nil)
	if p.onClose != nil {
		p.onClose()
	}
}

func (v *view) layoutPopover() {
	p := &v.popover
	if p.content == nil {
		return
	}
	if !v.available(p.owner) || p.owner.rect.intersect(p.owner.clip).H <= 0 {
		v.closePopover()
		return
	}
	p.layout = false
	v.accessDirty = true
	v.damage(p.content.rect.pixels(v.scale).Inset(-p.content.paintPadding(v.scale)))
	w, h := v.measure(p.content)
	bounds := Rect{W: float32(v.size.Dx()) / v.scale, H: float32(v.size.Dy()) / v.scale}
	if p.content.style.w == 0 {
		w = p.owner.rect.W
	}
	v.layout(p.content, popupBounds(p.owner.rect, bounds, w, h), bounds)
	v.damage(p.content.rect.pixels(v.scale).Inset(-p.content.paintPadding(v.scale)))
}

func popupBounds(owner, window Rect, w, h float32) Rect {
	w = min(w, max(0, window.W-16))
	h = min(h, max(0, window.H-16))
	y := owner.Y + owner.H + 4
	if y+h > window.H-8 && owner.Y > window.H-y {
		y = owner.Y - h - 4
	}
	return Rect{max(8, min(owner.X, window.W-w-8)), max(8, min(y, window.H-h-8)), w, h}
}

func (v *view) popoverRoot(root *Element, ev fego.Event, x, y float32) *Element {
	p := v.popover
	if p.content == nil {
		return root
	}
	switch ev.Kind {
	case fego.PointerDown, fego.Scroll:
		if !p.content.rect.contains(x, y) && (ev.Kind == fego.Scroll || !p.owner.rect.contains(x, y)) {
			v.closePopover()
			return root
		}
	case fego.PointerMove, fego.PointerUp:
	default:
		return root
	}
	if p.content.rect.contains(x, y) {
		return p.content
	}
	return root
}

func within(e, ancestor *Element) bool {
	for ; e != nil; e = e.parent {
		if e == ancestor {
			return true
		}
	}
	return false
}
