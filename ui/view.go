package ui

import (
	"image"
	"math"
	"sync"
	"time"

	"github.com/zatxm/fego/v26"
	fonttext "github.com/zatxm/fego/v26/internal/text"
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
	return (&Document{root: root}).Theme(Light)
}
func (d *Document) Theme(theme Theme) *Document {
	if theme.InputBorder == (Color{}) {
		theme.InputBorder = theme.Border
	}
	if theme.Ring == (Color{}) {
		theme.Ring = theme.Accent
	}
	if theme.Sidebar == (Color{}) {
		theme.Sidebar = theme.Surface
	}
	if theme.Chart == ([5]Color{}) {
		theme.Chart = Light.Chart
		theme.Chart[0] = theme.Accent
	}

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
	v := &view{doc: d, root: d.root, host: host, theme: d.theme, text: fonttext.New(), needsLayout: true, focusVisible: true, hoveredRow: -1}
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
	hoveredRow              int
	focusVisible            bool
	scrollDrag              *Element
	scrollGrab              float32
	pointerX, pointerY      float32
	popup                   *popup
	popupState              popup
	popover                 popover
	dialog                  *Element
	dialogShade             uint8
	dialogFade              func()
	dialogPlacement         panelPlacement
	drawerDragging          bool
	drawerOrigin            float32
	returnFocus             *Element
	toasts                  [3]notification
	toastCount              int
	toastCancel             func()
	tip                     tooltip
	frames, paints          uint64
	accessDirty             bool
	access                  map[uint64]accessTarget
	accessNodes             []fego.AccessNode
	rowLabels               map[[2]uint64]string
	canvas                  *Painter
	scrolling               *pixelScroll
	scrollState             pixelScroll
	scrollCheck             *Element
	scrollCopyOK            bool
}

func (v *view) ResetPresenter() {
	v.region = nil
	if r, ok := v.host.(regionHost); ok && r.RetainsPixels() {
		v.region = r
	}
	v.size = image.Rectangle{}
}

func (v *view) Close() {
	v.closePopover()
	v.clearToasts()
	v.dismissTip()
	if v.dialogFade != nil {
		v.dialogFade()
		v.dialogFade = nil
	}
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
	v.accessDirty = true
	v.repaint(e, layout)
}

func (v *view) repaint(e *Element, layout bool) {
	v.scrolling = nil
	if layout {
		if c := v.popover.content; c != nil {
			for parent := e; parent != nil; parent = parent.parent {
				if parent == c {
					v.popover.layout = true
					v.host.Invalidate()
					return
				}
			}
		}
		v.scrollCheck = nil
		v.needsLayout = true
	} else if !v.size.Empty() {
		if e.parent != nil && e.parent.style.tone == groupSurface {
			e = e.parent
		}
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
		v.accessDirty = true
		v.scrolling = nil
		r := Rect{W: float32(w) / scale, H: float32(h) / scale}
		v.layout(v.root, r, r)
		if v.dialog != nil {
			dw, dh := v.measure(v.dialog)
			dw = min(dw, max(0, r.W-32))
			dh = min(dh, max(0, r.H-32))
			bounds := Rect{(r.W - dw) / 2, (r.H - dh) / 2, dw, dh}
			switch v.dialogPlacement {
			case rightPanel:
				dw = min(dw, r.W*.75)
				bounds = Rect{r.W - dw, 0, dw, r.H}
			case bottomPanel:
				dh = min(dh, r.H*.8)
				bounds = Rect{0, r.H - dh, r.W, dh}
			}
			v.layout(v.dialog, bounds, r)
		}
		v.layoutPopover()
		v.layoutToastButton()
		if v.popup != nil {
			v.placePopup(v.popup)
		}
		v.dirty = append(v.dirty[:0], v.size)
		v.needsLayout = false
	} else if v.popover.layout {
		v.layoutPopover()
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
	if v.accessDirty {
		v.updateCaret()
	}
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
		} else {
			v.damageCopiedOverlays(scroll, dst)
		}
	}
	if v.region != nil {
		if batch, ok := v.region.(interface{ PresentRegions(func(func(*image.RGBA))) }); ok {
			batch.PresentRegions(v.paintBands)
		} else {
			v.paintBands(v.region.PresentRegion)
		}
	} else {
		for _, d := range v.dirty {
			v.paintRegion(v.img, d)
		}
		damage := v.dirty
		if scroll != nil && !v.scrollPixels(scroll) {
			damage = v.dirty[:1]
			damage[0] = scroll.full
		}
		v.host.Present(v.img, damage)
	}
	v.paints += uint64(len(v.dirty))
	v.dirty = v.dirty[:0]
	if v.accessDirty {
		v.accessDirty = false
		v.updateCaret()
		v.updateAccessibility()
	}
}

func (v *view) paintBands(upload func(*image.RGBA)) {
	v.bandMu.Lock()
	for _, d := range v.dirty {
		rows := max(1, regionBandBytes/(4*d.Dx()))
		for y := d.Min.Y; y < d.Max.Y; y += rows {
			band := v.scratch(image.Rect(d.Min.X, y, d.Max.X, min(y+rows, d.Max.Y)))
			v.paintRegion(band, band.Rect)
			upload(band)
		}
	}
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

func (v *view) damageCopiedOverlays(scroll *pixelScroll, dst image.Rectangle) {
	e := scroll.element
	if scroll.eraseRow >= 0 {
		// The old hover highlight moved with the copied pixels.
		y := e.rect.Y + e.headerHeight() + float32(scroll.eraseRow)*e.rowHeight() - e.scroll
		row := Rect{e.rect.X, y, e.rect.W, e.rowHeight()}.pixels(v.scale).Intersect(dst)
		if !row.Empty() {
			v.dirty = append(v.dirty, row)
		}
	}
	if scroll.copyThumb {
		old := e.rowScrollThumb(scroll.start).pixels(v.scale).Add(image.Pt(0, scroll.dy))
		next := e.rowScrollThumb(e.scroll).pixels(v.scale)
		if area := old.Union(next).Intersect(dst); !area.Empty() {
			v.dirty = append(v.dirty, area)
		}
	}
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
	scale := v.scale
	p := &Painter{dst: dst, clip: d, scale: scale, text: v.text, theme: v.theme}
	p.fillPixels(d, v.theme.Background)
	v.paint(p, v.root, false)
	p.fillPixels(d, RGBA(0, 0, 0, v.dialogShade))
	if v.dialog != nil {
		v.paint(p, v.dialog, false)
	}
	if v.popover.content != nil {
		v.paint(p, v.popover.content, false)
	}
	if v.popup != nil {
		v.paintPopup(p)
	}
	v.paintToasts(p)
	v.paintTip(p)
}

// Stats helps tests distinguish event-driven repainting from an idle frame loop.
type Stats struct{ Frames, PaintedRegions uint64 }

func (d *Document) Stats() Stats {
	if d.live == nil {
		return Stats{}
	}
	return Stats{d.live.frames, d.live.paints}
}

type panelPlacement uint8

const (
	centerPanel panelPlacement = iota
	rightPanel
	bottomPanel
)

func (d *Document) ShowDialog(content *Element) { d.showDialog(content, centerPanel) }

func (d *Document) showDialog(content *Element, placement panelPlacement) {
	v := d.live
	if v == nil {
		return
	}
	if content.parent != nil || content.view != nil {
		panic("ui: dialog must be detached")
	}
	v.closePopover()
	v.closePopup()
	opening := v.dialog == nil
	if opening {
		v.returnFocus = v.focused
		v.forget(v.root)
	} else {
		v.forget(v.dialog)
		v.dialog.attach(nil)
	}
	v.dialog = content
	v.dialogPlacement = placement
	v.drawerDragging = false
	content.attach(v)
	v.focused = nil
	if opening {
		v.fadeDialogShade(128)
	}
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
	v.drawerDragging = false
	v.focused = nil
	if e := v.returnFocus; e != nil && e.view == v {
		v.focus(e)
	}
	v.returnFocus = nil
	v.invalidate(v.root, false)
	v.fadeDialogShade(0)
}

const dialogFadeDuration = 150 * time.Millisecond

func (v *view) fadeDialogShade(target uint8) {
	if v.dialogFade != nil {
		v.dialogFade()
		v.dialogFade = nil
	}
	apply := func(shade uint8) {
		v.dialogShade = shade
		v.scrolling = nil
		v.damage(v.size)
		v.host.Invalidate()
	}
	clock, animated := v.host.(animationClock)
	start := v.dialogShade
	// Paint the new modal state even when the previous transition never drew.
	if !animated || start == target {
		apply(target)
		return
	}
	v.dialogFade = Animate(clock, dialogFadeDuration, EaseInOut, func(progress float64) {
		shade := uint8(math.Round(float64(start) + float64(int(target)-int(start))*progress))
		if shade != v.dialogShade || progress == 1 {
			apply(shade)
		}
	})
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
			v.dismissTip()
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
