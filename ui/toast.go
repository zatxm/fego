package ui

import (
	"image"
	"time"
)

const toastDuration = 4 * time.Second

type notification struct {
	title, description string
	action             *Element
	expires            time.Time
}

// ShowToast displays a brief notification at the bottom-right of the window.
// Pass an empty message to clear the notifications.
func (d *Document) ShowToast(message string) {
	d.ShowNotification(message, "")
}

// ToastAction is an optional button on a notification.
type ToastAction struct {
	Label string
	Run   func()
}

// ShowNotification displays a toast with a description and an optional action.
func (d *Document) ShowNotification(title, description string, actions ...ToastAction) {
	v := d.live
	if v == nil {
		return
	}
	if title == "" {
		v.clearToasts()
		return
	}
	var button *Element
	if len(actions) > 0 && actions[0].Run != nil {
		action := actions[0]
		button = PrimaryButton(action.Label, func() {
			v.dismissToast()
			action.Run()
		}).Height(28)
	}
	v.damageToasts()
	v.detachToastButton()
	v.toastCount = min(v.toastCount+1, len(v.toasts))
	copy(v.toasts[1:v.toastCount], v.toasts[:v.toastCount-1])
	v.toasts[0] = notification{title: title, description: description, action: button, expires: time.Now().Add(toastDuration)}
	v.attachToastButton()
	v.damageToasts()
	v.scheduleToast()
}

func (v *view) toastButton() *Element {
	if v.toastCount > 0 {
		return v.toasts[0].action
	}
	return nil
}

func (v *view) detachToastButton() {
	if button := v.toastButton(); button != nil {
		v.accessDirty = true
		v.forget(button)
		button.attach(nil)
	}
}

func (v *view) attachToastButton() {
	if button := v.toastButton(); button != nil {
		v.accessDirty = true
		button.attach(v)
		v.layoutToastButton()
	}
}

func (v *view) layoutToastButton() {
	if button := v.toastButton(); button != nil && !v.size.Empty() {
		r := v.toastBounds(0)
		w, h := v.measure(button)
		w = min(w, r.W*.42)
		v.layout(button, Rect{r.X + r.W - 12 - w, r.Y + (r.H-h)/2, w, h}, r)
	}
}

func (v *view) dismissToast() {
	if v.toastCount == 0 {
		return
	}
	v.damageToasts()
	v.detachToastButton()
	copy(v.toasts[:], v.toasts[1:v.toastCount])
	v.toastCount--
	v.toasts[v.toastCount] = notification{}
	if v.toastCount == 0 && v.toastCancel != nil {
		v.toastCancel()
		v.toastCancel = nil
	}
	v.attachToastButton()
	v.damageToasts()
}

func (v *view) toastBounds(i int) Rect {
	margin := float32(24)
	w, h := float32(v.size.Dx())/v.scale, float32(v.size.Dy())/v.scale
	if w < 600 {
		margin = 16
	}
	width := max(0, min(356, w-margin*2)-float32(i*12))
	height := float32(52)
	if v.toasts[0].description != "" {
		height = 68
	}
	return Rect{w - margin - width - float32(i*6), h - margin - height - float32(i*10), width, height}
}

func (v *view) damageToasts() {
	if v.toastCount == 0 || v.size.Empty() {
		return
	}
	var area image.Rectangle
	for i := range v.toastCount {
		area = area.Union(v.toastBounds(i).pixels(v.scale))
	}
	v.scrolling = nil
	v.damage(area.Inset(-int(7*v.scale + 1)))
	v.host.Invalidate()
}

func (v *view) clearToasts() {
	if v.toastCount == 0 {
		return
	}
	v.damageToasts()
	v.detachToastButton()
	v.toastCount = 0
	clear(v.toasts[:])
	if v.toastCancel != nil {
		v.toastCancel()
		v.toastCancel = nil
	}
}

func (v *view) scheduleToast() {
	host, ok := v.host.(delayer)
	if !ok || v.toastCount == 0 || v.toastCancel != nil {
		return
	}
	v.toastCancel = host.After(max(0, time.Until(v.toasts[v.toastCount-1].expires)), func() {
		v.toastCancel = nil
		now := time.Now()
		next := v.toastCount
		for next > 0 && !v.toasts[next-1].expires.After(now) {
			next--
		}
		if next != v.toastCount {
			v.damageToasts()
			if next == 0 {
				v.detachToastButton()
			}
			clear(v.toasts[next:v.toastCount])
			v.toastCount = next
			v.damageToasts()
		}
		v.scheduleToast()
	})
}

func (v *view) paintToasts(p *Painter) {
	if v.toastCount == 0 {
		return
	}
	t := &v.theme
	for i := v.toastCount - 1; i >= 0; i-- {
		r := v.toastBounds(i)
		if !r.pixels(v.scale).Inset(-int(7*v.scale + 1)).Overlaps(p.clip) {
			continue
		}
		if i == 0 {
			p.Shadow(r, 8)
		}
		p.frame(r, 8, t.Border, t.Surface)
		if i != 0 {
			continue
		}
		n, clip := v.toasts[i], p.clip
		right := r.X + r.W - 14
		if n.action != nil {
			right = n.action.rect.X - 8
		}
		p.clip = clip.Intersect(Rect{r.X + 14, r.Y + 1, max(0, right-(r.X+14)), r.H - 2}.pixels(v.scale))
		font := t.FontSize
		y := r.Y + (r.H-v.text.LineHeight(font, true))/2
		if n.description != "" {
			y = r.Y + 14
			p.Label(n.description, r.X+14, y+22, font-1, t.Muted, false)
		}
		p.Label(n.title, r.X+14, y, font, t.Text, true)
		p.clip = clip
		if n.action != nil {
			v.paint(p, n.action, false)
		}
	}
}
