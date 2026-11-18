package ui

import "time"

// tooltipDelay is how long the pointer rests on an element before its
// tooltip appears.
const tooltipDelay = 500 * time.Millisecond

type tooltip struct {
	target *Element
	shown  bool
	rect   Rect
	x, y   float32
	cancel func()
}

// delayer is implemented by hosts that can run work on the UI thread later.
// Hosts without it show tooltips immediately.
type delayer interface {
	After(time.Duration, func()) (cancel func())
}

// Tooltip shows s near the pointer after it rests on the element. Elements
// without their own tooltip inside it show the nearest ancestor's.
func (e *Element) Tooltip(s string) *Element {
	if e.tooltip == s {
		return e
	}
	e.tooltip = s
	if v := e.view; v != nil && v.tip.target == e && v.tip.shown {
		v.hideTip()
		if s != "" {
			v.showTip(e)
		}
	}
	return e
}

func tooltipAt(e *Element, x, y float32) *Element {
	if e.hidden || !e.clip.contains(x, y) {
		return nil
	}
	for i := len(e.children) - 1; i >= 0; i-- {
		if t := tooltipAt(e.children[i], x, y); t != nil {
			return t
		}
	}
	if e.tooltip != "" && e.rect.contains(x, y) {
		return e
	}
	return nil
}

// trackTip follows the pointer and schedules the tooltip of the element under it.
func (v *view) trackTip(root *Element, x, y float32) {
	t := tooltipAt(root, x, y)
	if t == v.tip.target {
		return
	}
	v.dismissTip()
	v.tip.target = t
	if t == nil {
		return
	}
	v.tip.x, v.tip.y = x, y
	if d, ok := v.host.(delayer); ok {
		v.tip.cancel = d.After(tooltipDelay, func() {
			if v.tip.target == t && t.view == v && !v.tip.shown {
				v.tip.cancel = nil
				v.showTip(t)
			}
		})
		return
	}
	v.showTip(t)
}

// dismissTip hides the tooltip until the pointer reaches another element.
func (v *view) dismissTip() {
	if v.tip.cancel != nil {
		v.tip.cancel()
		v.tip.cancel = nil
	}
	v.hideTip()
}

func (v *view) showTip(t *Element) {
	if v.text == nil || v.size.Empty() {
		return
	}
	size := v.theme.FontSize - 1
	tw, th := v.text.Measure(t.tooltip, size, false)
	w, h := tw+20, th+12
	width, height := float32(v.size.Dx())/v.scale, float32(v.size.Dy())/v.scale
	x, y := v.tip.x+4, v.tip.y+22
	if y+h > height-4 {
		// Flip above the pointer near the bottom edge.
		y = v.tip.y - h - 8
	}
	v.tip.rect = Rect{max(4, min(x, width-w-4)), max(4, y), w, h}
	v.tip.shown = true
	v.damageTip()
}

func (v *view) hideTip() {
	if v.tip.shown {
		v.tip.shown = false
		v.damageTip()
	}
}

func (v *view) damageTip() {
	v.scrolling = nil
	v.damage(v.tip.rect.pixels(v.scale).Inset(-2))
	v.host.Invalidate()
}

func (v *view) paintTip(p *Painter) {
	if !v.tip.shown || v.tip.target == nil {
		return
	}
	r := v.tip.rect
	p.RoundRect(r, 6, v.theme.Text)
	p.Label(v.tip.target.tooltip, r.X+10, r.Y+6, v.theme.FontSize-1, v.theme.Surface, false)
}
