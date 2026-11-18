package ui

import (
	"image"
	"math"
	"sync"
	"time"

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
	if r, ok := host.(regionHost); ok && r.RetainsPixels() {
		v.region = r
	}
	d.live = v
	d.root.attach(v)
	return v
}

// regionHost windows retain presented pixels and send Paint after losing them.
type regionHost interface {
	RetainsPixels() bool
	PresentRegion(*image.RGBA)
}

// Bound region scratch memory; full repaints are painted and uploaded in bands.
// The size matches one X11 upload request; smaller bands repeat more paint work.
const regionBandBytes = 512<<10 - 64

// A band above this size is kept only while frames keep needing it: a frame
// that paints only small regions or a pause without frames releases it.
const (
	smallBandBytes = 64 << 10
	bandIdle       = 250 * time.Millisecond
)

type view struct {
	doc                     *Document
	root                    *Element
	host                    fego.Host
	theme                   Theme
	text                    *fonttext.System
	img                     *image.RGBA // full surface, only for hosts without region
	region                  regionHost
	band                    image.RGBA
	bandMem                 []byte
	bandNeed                int
	bandMu                  sync.Mutex // the idle timer releases bandMem off the UI thread
	bandTimer               *time.Timer
	size                    image.Rectangle
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
	rowLabels               map[[2]uint64]string
	scrolling               *pixelScroll
	scrollState             pixelScroll
}

func (v *view) Close() {
	v.rowLabels = nil
	v.img = nil
	v.bandMu.Lock()
	if v.bandTimer != nil {
		v.bandTimer.Stop()
	}
	v.releaseBand()
	v.bandMu.Unlock()
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
	} else if !v.size.Empty() {
		v.damage(e.rect.pixels(v.scale).Inset(-e.paintPadding(v.scale)))
	}
	v.host.Invalidate()
}
func (v *view) damage(r image.Rectangle) {
	r = r.Intersect(v.size)
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
	if v.size.Dx() != w || v.size.Dy() != h || v.scale != scale {
		v.size = image.Rect(0, 0, w, h)
		v.img = nil
		if v.region == nil {
			v.img = image.NewRGBA(v.size)
		}
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
		v.dirty = append(v.dirty[:0], v.size)
		v.needsLayout = false
	}
	if force && v.region != nil {
		// A retained window asks to paint only after it lost pixels.
		v.scrolling = nil
		v.dirty = append(v.dirty[:0], v.size)
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
		dst := scroll.rect.Intersect(scroll.rect.Add(image.Pt(0, scroll.dy)))
		if v.region == nil {
			movePixels(v.img, scroll.rect, scroll.dy)
		} else if !v.scrollPixels(scroll) {
			// Without a native copy the exposed strips cannot reuse retained pixels.
			dst = image.Rectangle{}
		}
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
		if dst.Empty() {
			v.dirty = append(v.dirty[:0], full)
		}
	}
	if v.region != nil {
		v.bandMu.Lock()
	}
	for _, d := range v.dirty {
		if v.region == nil {
			v.paintRegion(v.img, d)
			continue
		}
		rows := max(1, regionBandBytes/(4*d.Dx()))
		for y := d.Min.Y; y < d.Max.Y; y += rows {
			band := v.scratch(image.Rect(d.Min.X, y, d.Max.X, min(y+rows, d.Max.Y)))
			v.paintRegion(band, band.Rect)
			v.region.PresentRegion(band)
		}
	}
	if v.region != nil {
		if cap(v.bandMem) > smallBandBytes {
			if v.bandNeed <= smallBandBytes {
				v.releaseBand()
			} else if v.bandTimer == nil {
				v.bandTimer = time.AfterFunc(bandIdle, v.trimBand)
			} else {
				v.bandTimer.Reset(bandIdle)
			}
		}
		v.bandNeed = 0
		v.bandMu.Unlock()
	}
	if v.region == nil {
		damage := v.dirty
		if scroll != nil && !v.scrollPixels(scroll) {
			damage = v.dirty[:1]
			damage[0] = scroll.full
		}
		v.host.Present(v.img, damage)
	}
	v.paints += uint64(len(v.dirty))
	v.dirty = v.dirty[:0]
	v.updateCaret()
	v.updateAccessibility()
}

func (v *view) scrollPixels(scroll *pixelScroll) bool {
	host, ok := v.host.(interface {
		ScrollPixels(image.Rectangle, image.Point) bool
	})
	return ok && host.ScrollPixels(scroll.rect, image.Pt(0, scroll.dy))
}

// scratch reuses one band-sized image whose bounds are window pixels.
func (v *view) scratch(r image.Rectangle) *image.RGBA {
	n := 4 * r.Dx() * r.Dy()
	v.bandNeed = max(v.bandNeed, n)
	if cap(v.bandMem) < n {
		v.releaseBand()
		v.bandMem = bandAlloc((n + 4095) &^ 4095)
	}
	v.band.Pix, v.band.Stride, v.band.Rect = v.bandMem[:n], 4*r.Dx(), r
	return &v.band
}

func (v *view) trimBand() {
	v.bandMu.Lock()
	if cap(v.bandMem) > smallBandBytes {
		v.releaseBand()
	}
	v.bandMu.Unlock()
}

// releaseBand runs with bandMu held.
func (v *view) releaseBand() {
	bandFree(v.bandMem)
	v.bandMem, v.band = nil, image.RGBA{}
}

func (v *view) paintRegion(dst *image.RGBA, d image.Rectangle) {
	w, h, scale := v.size.Dx(), v.size.Dy(), v.scale
	p := &Painter{dst: dst, clip: d, scale: scale, text: v.text, theme: v.theme}
	p.fillPixels(d, v.theme.Background)
	v.paint(p, v.root, false)
	if v.dialog != nil {
		p.fillPixels(v.size, RGBA(8, 16, 32, 96))
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
		if v.region != nil && e.Width > 0 && e.Height > 0 && !v.size.Empty() && !v.needsLayout {
			// Repaint only what a retained window lost; a pending copy cannot reuse it.
			v.scrolling = nil
			v.damage(image.Rect(int(e.X), int(e.Y), int(e.X)+e.Width, int(e.Y)+e.Height))
			v.frame(false)
			return
		}
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
