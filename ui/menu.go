package ui

import "github.com/zatxm/fego/v26"

const popupRowHeight = 30

type popup struct {
	owner                    *Element
	menu                     []MenuEntry
	current, selected, first int
	rect                     Rect
}

func (p *popup) count() int {
	if p.menu != nil {
		return len(p.menu)
	}
	return len(p.owner.options)
}

func (p *popup) item(i int) MenuEntry {
	if p.menu != nil {
		return p.menu[i]
	}
	return MenuEntry{MenuItem: MenuItem{Label: p.owner.options[i]}}
}

func (p *popup) rows() int { return max(1, int((p.rect.H-8)/popupRowHeight)) }

func (p *popup) row(i int) Rect {
	return Rect{p.rect.X + 4, p.rect.Y + 4 + float32(i-p.first)*popupRowHeight, p.rect.W - 8, popupRowHeight}
}

func (v *view) openPopup(owner *Element, menu []MenuEntry) {
	v.closePopover()
	v.closePopup()
	p := &v.popupState
	*p = popup{owner: owner, menu: menu, current: -1, selected: -1}
	if p.count() == 0 {
		*p = popup{}
		return
	}
	if owner.kind == kSelect {
		p.current = owner.selected
	}
	v.popup = p
	v.placePopup(p)
	if p.current >= 0 {
		v.highlightPopup(p.current)
	} else {
		v.movePopup(1)
	}
	v.damagePopup(p)
}

func (v *view) placePopup(p *popup) {
	bounds := Rect{W: float32(v.size.Dx()) / v.scale, H: float32(v.size.Dy()) / v.scale}
	w := max(p.owner.rect.W, 128)
	h := float32(min(p.count(), 10)*popupRowHeight + 8)
	p.rect = popupBounds(p.owner.rect, bounds, w, h)
	p.first = min(p.first, max(0, p.count()-p.rows()))
}

func (v *view) damagePopup(p *popup) {
	v.accessDirty = true
	v.scrolling = nil
	v.damage(p.rect.pixels(v.scale).Inset(-int(8*v.scale + 1)))
	v.host.Invalidate()
}

func (v *view) closePopup() {
	if p := v.popup; p != nil {
		v.popup = nil
		v.damagePopup(p)
		*p = popup{}
	}
}

func (v *view) choosePopup(i int) {
	p := v.popup
	if i < 0 || i >= p.count() || p.item(i).Disabled {
		return
	}
	owner, item := p.owner, p.item(i)
	v.closePopup()
	if owner.kind == kSelect {
		owner.SetSelected(i)
		if owner.onSelect != nil {
			owner.onSelect(i)
		}
	} else if item.Action != nil {
		item.Action()
	}
}

func (v *view) highlightPopup(i int) {
	p := v.popup
	if i == p.selected || i < 0 || i >= p.count() || p.item(i).Disabled {
		return
	}
	previous, first := p.selected, p.first
	p.selected = i
	v.accessDirty = true
	if i < first {
		p.first = i
	} else if i >= first+p.rows() {
		p.first = i - p.rows() + 1
	}
	if first != p.first {
		v.damagePopup(p)
		return
	}
	if previous >= first {
		v.damage(p.row(previous).pixels(v.scale))
	}
	v.damage(p.row(i).pixels(v.scale))
	v.host.Invalidate()
}

func (v *view) movePopup(step int) {
	p := v.popup
	i := p.selected
	if i < 0 && step < 0 {
		i = 0
	}
	for range p.count() {
		i = (i + step + p.count()) % p.count()
		if !p.item(i).Disabled {
			v.highlightPopup(i)
			return
		}
	}
}

func (v *view) popupInput(ev fego.Event, x, y float32) bool {
	p := v.popup
	switch ev.Kind {
	case fego.KeyDown:
		switch ev.Key {
		case "Escape":
			v.closePopup()
		case "Tab":
			v.closePopup()
			v.focusNext(ev.Shift)
		case "ArrowDown":
			v.movePopup(1)
		case "ArrowUp":
			v.movePopup(-1)
		case "Enter", "Space":
			v.choosePopup(p.selected)
		}
	case fego.PointerDown:
		if !p.rect.contains(x, y) {
			v.closePopup()
		}
	case fego.PointerMove, fego.PointerUp:
		i := p.first + int((y-p.rect.Y-4)/popupRowHeight)
		if i >= p.first && i < p.count() && p.row(i).contains(x, y) {
			if ev.Kind == fego.PointerUp {
				v.choosePopup(i)
			} else {
				v.highlightPopup(i)
			}
		}
	case fego.Scroll:
		step := int(ev.DY / popupRowHeight)
		if step == 0 && ev.DY != 0 {
			step = 1
			if ev.DY < 0 {
				step = -1
			}
		}
		first := max(0, min(max(0, p.count()-p.rows()), p.first+step))
		if first != p.first {
			p.first = first
			v.damagePopup(p)
		}
	case fego.Text:
	default:
		return false
	}
	return true
}

func (v *view) paintPopup(p *Painter) {
	menu, t := v.popup, v.theme
	r, clip := menu.rect, p.clip
	if !r.pixels(v.scale).Inset(-int(8*v.scale + 1)).Overlaps(clip) {
		return
	}
	if !clip.In(r.pixels(v.scale).Inset(8)) {
		p.Shadow(r, 8)
	}
	p.frame(r, 8, t.Border, t.Surface)
	p.clip = clip.Intersect(r.pixels(v.scale))
	font := t.FontSize
	line := v.text.LineHeight(font, false)
	for i := menu.first; i < min(menu.count(), menu.first+menu.rows()); i++ {
		row := menu.row(i)
		if !row.pixels(v.scale).Overlaps(p.clip) {
			continue
		}
		item, ink := menu.item(i), t.Text
		if item.SeparatorBefore {
			p.Rect(Rect{row.X, row.Y, row.W, 1}, t.Border)
			row.Y += 2
			row.H -= 2
		}
		if item.Disabled {
			ink = t.Muted
		} else if i == menu.selected {
			p.RoundRect(row, 4, t.Raised)
		}
		if i == menu.current {
			p.icon(checkIcon, row.X+row.W-12, row.Y+row.H/2, t.Text)
		}
		rowClip := p.clip
		x, right := row.X+8, row.X+row.W-8
		y := row.Y + (row.H-line)/2
		if item.Icon != "" {
			p.Label(item.Icon, x, y, font, t.Muted, false)
			x += 22
		}
		if item.Shortcut != "" {
			tw, _ := v.text.Measure(item.Shortcut, font-2, false)
			p.Label(item.Shortcut, right-tw, y, font-2, t.Muted, false)
			right -= tw + 8
		} else if menu.owner.kind == kSelect {
			right -= 20
		}
		p.clip = rowClip.Intersect(Rect{x, row.Y, max(0, right-x), row.H}.pixels(v.scale))
		p.Label(item.Label, x, y, font, ink, false)
		p.clip = rowClip
	}
	p.clip = clip
}
