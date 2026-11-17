package ui

import (
	"image"
	"math"

	"github.com/zatxm/fego"
	fonttext "github.com/zatxm/fego/internal/text"
)

type Document struct {
	root  *Element
	theme Theme
	live  *view
}

func View(root *Element) *Document {
	if root == nil || root.parent != nil || root.view != nil {
		panic("ui: View needs a root element")
	}
	return &Document{root: root, theme: Light}
}
func (d *Document) Theme(theme Theme) *Document {
	d.theme = theme
	if d.live != nil {
		d.live.theme = theme
		d.live.invalidate(d.root, true)
	}
	return d
}
func (d *Document) Mount(host fego.Host) fego.View {
	if d.live != nil || d.root.view != nil || d.root.parent != nil {
		panic("ui: a Document belongs to one window")
	}
	v := &view{doc: d, root: d.root, host: host, theme: d.theme, text: fonttext.New(), needsLayout: true}
	d.live = v
	d.root.attach(v)
	return v
}

type view struct {
	doc                     *Document
	root                    *Element
	host                    fego.Host
	theme                   Theme
	text                    *fonttext.System
	img                     *image.RGBA
	scale                   float32
	dirty                   []image.Rectangle
	needsLayout             bool
	hover, pressed, focused *Element
	scrollDrag              *Element
	scrollGrab              float32
	pointerX, pointerY      float32
	popup                   *popup
	dialog                  *Element
	returnFocus             *Element
	toast                   string
	frames, paints          uint64
	access                  map[uint64]accessTarget
	accessNodes             []fego.AccessNode
	scrolling               *pixelScroll
	scrollState             pixelScroll
}

func (v *view) Close() {
	v.root.attach(nil)
	if v.dialog != nil {
		v.dialog.attach(nil)
	}
	v.doc.live = nil
	v.text.Close()
}
func (v *view) invalidate(e *Element, layout bool) {
	v.scrolling = nil
	if layout {
		v.needsLayout = true
	} else if v.img != nil {
		v.damage(e.rect.pixels(v.scale).Inset(-e.paintPadding(v.scale)))
	}
	v.host.Invalidate()
}
func (v *view) damage(r image.Rectangle) {
	if v.img == nil {
		return
	}
	r = r.Intersect(v.img.Bounds())
	if r.Empty() {
		return
	}
	for i, d := range v.dirty {
		if d.Inset(-1).Overlaps(r) {
			v.dirty[i] = d.Union(r)
			return
		}
	}
	if len(v.dirty) >= 8 {
		for _, d := range v.dirty {
			r = r.Union(d)
		}
		v.dirty = v.dirty[:0]
	}
	v.dirty = append(v.dirty, r)
}
func (v *view) frame(force bool) {
	w, h, scale := v.host.Size()
	if w < 1 || h < 1 {
		return
	}
	if w > 16384 || h > 16384 || int64(w)*int64(h) > 64<<20 {
		panic("ui: window exceeds pixel budget")
	}
	if scale <= 0 || math.IsNaN(float64(scale)) || math.IsInf(float64(scale), 0) {
		scale = 1
	}
	if v.img == nil || v.img.Rect.Dx() != w || v.img.Rect.Dy() != h || v.scale != scale {
		v.img = image.NewRGBA(image.Rect(0, 0, w, h))
		v.scale = scale
		v.needsLayout = true
	}
	if v.needsLayout {
		v.scrolling = nil
		r := Rect{W: float32(w) / scale, H: float32(h) / scale}
		v.layout(v.root, r, r)
		if v.dialog != nil {
			dw, dh := v.measure(v.dialog)
			dw = min(dw, r.W-32)
			dh = min(dh, r.H-32)
			v.layout(v.dialog, Rect{(r.W - dw) / 2, (r.H - dh) / 2, dw, dh}, r)
		}
		v.dirty = append(v.dirty[:0], v.img.Rect)
		v.needsLayout = false
	}
	if len(v.dirty) == 0 {
		if force {
			v.host.Present(v.img, []image.Rectangle{v.img.Rect})
		}
		return
	}
	v.frames++
	v.updateCaret()
	scroll := v.scrolling
	v.scrolling = nil
	if scroll != nil {
		dst := movePixels(v.img, scroll.rect, scroll.dy)
		full := scroll.full
		v.dirty = v.dirty[:0]
		for _, r := range []image.Rectangle{
			image.Rect(full.Min.X, full.Min.Y, full.Max.X, dst.Min.Y),
			image.Rect(full.Min.X, dst.Max.Y, full.Max.X, full.Max.Y),
			image.Rect(full.Min.X, dst.Min.Y, dst.Min.X, dst.Max.Y),
			image.Rect(dst.Max.X, dst.Min.Y, full.Max.X, dst.Max.Y),
		} {
			if !r.Empty() {
				v.dirty = append(v.dirty, r)
			}
		}
	}
	for _, d := range v.dirty {
		p := &Painter{dst: v.img, clip: d, scale: scale, text: v.text, theme: v.theme}
		p.fillPixels(d, v.theme.Background)
		v.paint(p, v.root, false)
		if v.dialog != nil {
			p.fillPixels(v.img.Rect, RGBA(8, 16, 32, 96))
			v.paint(p, v.dialog, false)
		}
		if v.popup != nil {
			v.paintPopup(p)
		}
		if v.toast != "" {
			tw, th := v.text.Measure(v.toast, v.theme.FontSize, true)
			rr := Rect{(float32(w)/scale - tw - 36) / 2, float32(h)/scale - th - 42, tw + 36, th + 24}
			p.RoundRect(rr, 10, v.theme.Text)
			p.Label(v.toast, rr.X+18, rr.Y+12, v.theme.FontSize, v.theme.Surface, true)
		}
	}
	damage := v.dirty
	if scroll != nil {
		host, ok := v.host.(interface {
			ScrollPixels(image.Rectangle, image.Point) bool
		})
		if !ok || !host.ScrollPixels(scroll.rect, image.Pt(0, scroll.dy)) {
			damage = v.dirty[:1]
			damage[0] = scroll.full
		}
	}
	v.host.Present(v.img, damage)
	v.paints += uint64(len(v.dirty))
	v.dirty = v.dirty[:0]
	v.updateCaret()
	v.updateAccessibility()
}

// Stats helps tests distinguish event-driven repainting from an idle frame loop.
type Stats struct{ Frames, PaintedRegions uint64 }

func (d *Document) Stats() Stats {
	if d.live == nil {
		return Stats{}
	}
	return Stats{d.live.frames, d.live.paints}
}
func (d *Document) ShowDialog(content *Element) {
	v := d.live
	if v == nil {
		return
	}
	if content.parent != nil || content.view != nil {
		panic("ui: dialog must be detached")
	}
	if v.dialog != nil {
		v.forget(v.dialog)
		v.dialog.attach(nil)
	}
	if v.dialog == nil {
		v.returnFocus = v.focused
	}
	v.forget(v.root)
	v.dialog = content
	content.attach(v)
	v.focused = nil
	v.invalidate(v.root, true)
}
func (d *Document) CloseDialog() {
	v := d.live
	if v == nil || v.dialog == nil {
		return
	}
	v.forget(v.dialog)
	v.dialog.attach(nil)
	v.dialog = nil
	v.focused = nil
	if e := v.returnFocus; e != nil && e.view == v {
		v.focus(e)
	}
	v.returnFocus = nil
	v.invalidate(v.root, true)
}
func (d *Document) ShowToast(message string) {
	if d.live != nil {
		d.live.toast = message
		d.live.invalidate(d.root, false)
	}
}

// Queued invalidations may follow an input event that already presented its changes.
func (v *view) PaintIfDirty() { v.frame(false) }

func (v *view) Event(e fego.Event) {
	switch e.Kind {
	case fego.Paint:
		v.frame(true)
	case fego.Resize:
		v.needsLayout = true
		v.frame(true)
	case fego.Focus:
		if !e.Focused {
			v.pressed = nil
			v.scrollDrag = nil
		}
		if v.focused != nil {
			v.invalidate(v.focused, false)
		}
	case fego.AccessAction:
		v.accessAction(e)
	default:
		v.input(e)
	}
}
