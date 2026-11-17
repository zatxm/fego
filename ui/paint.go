package ui

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	fonttext "github.com/zatxm/fego/internal/text"
)

// Painter draws in logical pixels, clipped to its widget and the changed region.
type Painter struct {
	dst              *image.RGBA
	clip             image.Rectangle
	scale            float32
	text             *fonttext.System
	theme            Theme
	originX, originY float32
	bounds           Rect
	canvas           *Painter
}

// Bounds is the canvas's local logical rectangle; Theme is its current palette.
func (p *Painter) Bounds() Rect { return p.bounds }
func (p *Painter) Theme() Theme { return p.theme }

func (p *Painter) fillPixels(r image.Rectangle, c Color) {
	r = r.Intersect(p.clip).Intersect(p.dst.Rect)
	if r.Empty() || c.A == 0 {
		return
	}
	if c.A == 255 {
		row := p.dst.Pix[p.dst.PixOffset(r.Min.X, r.Min.Y):p.dst.PixOffset(r.Max.X, r.Min.Y)]
		row[0], row[1], row[2], row[3] = c.R, c.G, c.B, 255
		for n := 4; n < len(row); n *= 2 {
			copy(row[n:], row[:n])
		}
		for y := r.Min.Y + 1; y < r.Max.Y; y++ {
			copy(p.dst.Pix[p.dst.PixOffset(r.Min.X, y):], row)
		}
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			p.pixel(x, y, c)
		}
	}
}

func (p *Painter) pixel(x, y int, c Color) {
	i := p.dst.PixOffset(x, y)
	d := p.dst.Pix[i : i+4]
	a := uint32(c.A)
	inv := 255 - a
	d[0] = uint8((uint32(c.R)*a + uint32(d[0])*inv + 127) / 255)
	d[1] = uint8((uint32(c.G)*a + uint32(d[1])*inv + 127) / 255)
	d[2] = uint8((uint32(c.B)*a + uint32(d[2])*inv + 127) / 255)
	d[3] = uint8(a + (uint32(d[3])*inv+127)/255)
}
func (p *Painter) Rect(r Rect, c Color) {
	r.X += p.originX
	r.Y += p.originY
	p.fillPixels(r.pixels(p.scale), c)
}
func (p *Painter) RoundRect(r Rect, radius float32, c Color) {
	r.X += p.originX
	r.Y += p.originY
	x0, y0, x1, y1 := r.X*p.scale, r.Y*p.scale, (r.X+r.W)*p.scale, (r.Y+r.H)*p.scale
	rad := min(radius*p.scale, (x1-x0)/2, (y1-y0)/2)
	b := r.pixels(p.scale).Intersect(p.clip).Intersect(p.dst.Rect)
	if b.Empty() || c.A == 0 {
		return
	}
	if rad <= 0 {
		p.fillPixels(b, c)
		return
	}
	aligned := x0 == float32(math.Trunc(float64(x0))) && x1 == float32(math.Trunc(float64(x1))) && y0 == float32(math.Trunc(float64(y0))) && y1 == float32(math.Trunc(float64(y1)))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		py := float32(y) + .5
		dy := max(rad-(py-y0), rad-(y1-py), 0)
		if aligned && dy == 0 {
			end := min(b.Max.Y, int(math.Floor(float64(y1-rad))))
			if end > y {
				p.fillPixels(image.Rect(b.Min.X, y, b.Max.X, end), c)
				y = end - 1
				continue
			}
		}
		inset := float32(0)
		if dy > 0 {
			inset = rad - float32(math.Sqrt(float64(max(0, rad*rad-dy*dy))))
		}
		lo, hi := max(b.Min.X, int(math.Ceil(float64(x0+inset)))), min(b.Max.X, int(math.Floor(float64(x1-inset))))
		if lo < hi {
			rowColor := c
			rowColor.A = uint8(float32(c.A) * min(float32(1), max(float32(0), min(py-y0+.5, y1-py+.5))))
			p.fillPixels(image.Rect(lo, y, hi, y+1), rowColor)
		}
		for _, span := range [][2]int{{b.Min.X, min(lo, b.Max.X)}, {max(hi, b.Min.X), b.Max.X}} {
			for x := span[0]; x < span[1]; x++ {
				px := float32(x) + .5
				dx := max(x0+rad-px, px-(x1-rad), 0)
				cov := min(float32(1), max(float32(0), rad+.5-float32(math.Sqrt(float64(dx*dx+dy*dy)))))
				if cov > 0 {
					cc := c
					cc.A = uint8(float32(c.A) * cov)
					p.pixel(x, y, cc)
				}
			}
		}
	}
}
func (p *Painter) Circle(x, y, r float32, c Color) {
	p.RoundRect(Rect{x - r, y - r, 2 * r, 2 * r}, r, c)
}
func (p *Painter) Line(x1, y1, x2, y2, width float32, c Color) {
	x1 = (x1 + p.originX) * p.scale
	y1 = (y1 + p.originY) * p.scale
	x2 = (x2 + p.originX) * p.scale
	y2 = (y2 + p.originY) * p.scale
	width *= p.scale
	r := image.Rect(int(math.Floor(float64(min(x1, x2)-width))), int(math.Floor(float64(min(y1, y2)-width))), int(math.Ceil(float64(max(x1, x2)+width))), int(math.Ceil(float64(max(y1, y2)+width)))).Intersect(p.clip).Intersect(p.dst.Rect)
	dx, dy := x2-x1, y2-y1
	if math.Abs(float64(dx)) <= 8 || math.Abs(float64(dy)) <= 8 {
		p.linePixels(r, x1, y1, dx, dy, width, c)
		return
	}
	rad := width/2 + .5
	for y := r.Min.Y; y < r.Max.Y; y++ {
		// A covered pixel must lie within this row's expanded segment span.
		py := float32(y) + .5
		a := x1 + (max(min(y1, y2), py-rad)-y1)*(dx/dy)
		b := x1 + (min(max(y1, y2), py+rad)-y1)*(dx/dy)
		left := max(r.Min.X, int(math.Floor(float64(min(a, b)-rad-1))))
		right := min(r.Max.X, int(math.Ceil(float64(max(a, b)+rad+1))))
		if left < right {
			p.linePixels(image.Rect(left, y, right, y+1), x1, y1, dx, dy, width, c)
		}
	}
}
func (p *Painter) linePixels(r image.Rectangle, x1, y1, dx, dy, width float32, c Color) {
	// Explicit rounding keeps ARM64 FMA and scalar edge coverage identical.
	length := float32(dx*dx) + float32(dy*dy)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			px, py := float32(x)+.5, float32(y)+.5
			t := float32(0)
			if length > 0 {
				t = max(0, min(1, (float32((px-x1)*dx)+float32((py-y1)*dy))/length))
			}
			a, b := px-(x1+float32(t*dx)), py-(y1+float32(t*dy))
			cov := max(float32(0), min(float32(1), width/2+.5-float32(math.Sqrt(float64(float32(a*a)+float32(b*b))))))
			if cov > 0 {
				cc := c
				cc.A = uint8(float32(c.A) * cov)
				p.pixel(x, y, cc)
			}
		}
	}
}
func (p *Painter) Label(s string, x, y, size float32, c Color, bold bool) {
	// Widget colors are straight alpha; image/draw text expects premultiplied ink.
	a := uint32(c.A)
	ink := color.RGBA{uint8(uint32(c.R) * a / 255), uint8(uint32(c.G) * a / 255), uint8(uint32(c.B) * a / 255), c.A}
	p.text.Draw(p.dst, p.clip, s, (x+p.originX)*p.scale, (y+p.originY)*p.scale, size*p.scale, ink, bold)
}

func (p *Painter) Measure(s string, size float32, bold bool) (float32, float32) {
	return p.text.Measure(s, size, bold)
}
func (p *Painter) Image(img image.Image, r Rect) {
	if img == nil {
		return
	}
	r.X += p.originX
	r.Y += p.originY
	dst := r.pixels(p.scale)
	clip := dst.Intersect(p.clip).Intersect(p.dst.Rect)
	if clip.Empty() {
		return
	}
	src := img.Bounds()
	if rgba, ok := img.(*image.RGBA); ok && !src.Empty() && clip.Dx() >= src.Dx() && clip.Dy() >= src.Dy() && rgba.Opaque() {
		// Opaque upscaling can reuse painted rows without blending another background.
		previousY := src.Min.Y - 1
		var previous []byte
		for y := clip.Min.Y; y < clip.Max.Y; y++ {
			sy := src.Min.Y + (y-dst.Min.Y)*src.Dy()/dst.Dy()
			row := p.dst.Pix[p.dst.PixOffset(clip.Min.X, y):p.dst.PixOffset(clip.Max.X, y)]
			if sy == previousY {
				copy(row, previous)
			} else {
				source := rgba.Pix[(sy-src.Min.Y)*rgba.Stride:]
				for x := clip.Min.X; x < clip.Max.X; x++ {
					sx := (x - dst.Min.X) * src.Dx() / dst.Dx()
					copy(row[(x-clip.Min.X)*4:], source[sx*4:sx*4+4])
				}
				previousY = sy
			}
			previous = row
		}
		return
	}
	for y := clip.Min.Y; y < clip.Max.Y; y++ {
		sy := src.Min.Y + (y-dst.Min.Y)*src.Dy()/max(dst.Dy(), 1)
		for x := clip.Min.X; x < clip.Max.X; x++ {
			sx := src.Min.X + (x-dst.Min.X)*src.Dx()/max(dst.Dx(), 1)
			var c color.NRGBA
			switch img := img.(type) {
			case *image.NRGBA:
				c = img.NRGBAAt(sx, sy)
			case *image.RGBA:
				v := img.RGBAAt(sx, sy)
				c = color.NRGBA(v)
				if v.A != 0 && v.A != 255 {
					c.R = uint8(uint32(v.R) * 0xffff / uint32(v.A) >> 8)
					c.G = uint8(uint32(v.G) * 0xffff / uint32(v.A) >> 8)
					c.B = uint8(uint32(v.B) * 0xffff / uint32(v.A) >> 8)
				}
			default:
				c = color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
			}
			if c.A == 255 {
				i := p.dst.PixOffset(x, y)
				copy(p.dst.Pix[i:i+4], []byte{c.R, c.G, c.B, 255})
			} else if c.A != 0 {
				p.pixel(x, y, Color{R: c.R, G: c.G, B: c.B, A: c.A})
			}
		}
	}
}
func (p *Painter) Shadow(r Rect, radius float32) {
	for i := 5; i >= 1; i-- {
		d := float32(i)
		p.RoundRect(Rect{r.X - d, r.Y - d + 2, r.W + d*2, r.H + d*2}, radius+d, RGBA(22, 40, 76, 3))
	}
}

func mix(a, b Color, t float32) Color {
	return Color{R: uint8(float32(a.R)*(1-t) + float32(b.R)*t), G: uint8(float32(a.G)*(1-t) + float32(b.G)*t), B: uint8(float32(a.B)*(1-t) + float32(b.B)*t), A: uint8(float32(a.A)*(1-t) + float32(b.A)*t)}
}

func (v *view) paint(p *Painter, e *Element, disabled bool) {
	if e.hidden || !e.rect.pixels(v.scale).Inset(-e.paintPadding(v.scale)).Overlaps(p.clip) {
		return
	}
	oldClip := p.clip
	v.paintFocus(p, e)
	p.clip = p.clip.Intersect(e.clip.pixels(v.scale))
	defer func() { p.clip = oldClip }()
	disabled = disabled || e.disabled
	t := v.theme
	r := e.rect
	s := &e.style
	font := v.font(e)
	fg := t.Text
	if s.fgSet {
		fg = s.fg
	}
	if disabled {
		fg = mix(fg, t.Surface, .55)
		t.Accent = mix(t.Accent, t.Surface, .55)
	}
	bg := s.bg
	if !s.bgSet {
		if s.shadow {
			bg = t.Surface
		} else {
			bg = Color{}
		}
	}
	border := t.Border
	if s.borderSet && s.border.A != 0 {
		border = s.border
	}
	if !disabled && v.hover == e {
		border = mix(border, t.Accent, .45)
	}
	if v.focused == e {
		border = t.Accent
	}
	if s.shadow {
		saved := p.clip
		clips := []image.Rectangle{oldClip}
		inset := max(0, s.radius) + 2
		if bg.A == 255 && r.W > 2*inset && r.H > 2*inset {
			// The opaque background overwrites this interior after the shadow.
			cut := image.Rect(int(math.Ceil(float64((r.X+inset)*p.scale))), int(math.Ceil(float64((r.Y+inset)*p.scale))), int(math.Floor(float64((r.X+r.W-inset)*p.scale))), int(math.Floor(float64((r.Y+r.H-inset)*p.scale)))).Intersect(oldClip)
			if !cut.Empty() {
				clips = []image.Rectangle{image.Rect(oldClip.Min.X, oldClip.Min.Y, oldClip.Max.X, cut.Min.Y), image.Rect(oldClip.Min.X, cut.Max.Y, oldClip.Max.X, oldClip.Max.Y), image.Rect(oldClip.Min.X, cut.Min.Y, cut.Min.X, cut.Max.Y), image.Rect(cut.Max.X, cut.Min.Y, oldClip.Max.X, cut.Max.Y)}
			}
		}
		for _, clip := range clips {
			if !clip.Empty() {
				p.clip = clip
				p.Shadow(r, s.radius)
			}
		}
		p.clip = saved
	}
	if p.clip.Empty() {
		return
	}
	if s.borderSet {
		p.RoundRect(r, s.radius, border)
		p.RoundRect(Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}, max(0, s.radius-1), bg)
	} else if bg.A != 0 {
		p.RoundRect(r, s.radius, bg)
	}
	label := func(text string, box Rect, color Color, bold bool, center bool) {
		tw, th := v.text.Measure(text, font, bold)
		x := box.X
		if center {
			x += (box.W - tw) / 2
		}
		p.Label(text, x, box.Y+(box.H-th)/2, font, color, bold)
	}
	switch e.kind {
	case kBox:
		for _, c := range e.children {
			v.paint(p, c, disabled)
		}
		if e.scrollable && e.maxScroll() > 0 {
			thumb := max(24, r.H*r.H/e.contentH)
			y := r.Y + (r.H-thumb)*e.scroll/e.maxScroll()
			p.RoundRect(Rect{r.X + r.W - 5, y, 3, thumb}, 2, mix(t.Border, t.Muted, .3))
		}
	case kText:
		label(e.text, r, fg, s.bold, false)
	case kRichText:
		x := r.X
		for _, sp := range e.spans {
			c := fg
			if sp.Color.A != 0 {
				c = sp.Color
			}
			if sp.Link != "" {
				c = t.Accent
			}
			p.Label(sp.Text, x, r.Y, font, c, sp.Bold || s.bold)
			sw, _ := v.text.Measure(sp.Text, font, sp.Bold || s.bold)
			x += sw
		}
	case kButton:
		b, c := t.Surface, fg
		if e.primary {
			b, c = t.Accent, t.AccentText
		}
		if e.tab {
			b = Color{}
			if e.primary {
				b, c = mix(t.Surface, t.Accent, .12), t.Accent
			}
		}
		if s.bgSet {
			b = bg
		}
		if disabled {
			b, c = t.Raised, fg
		} else if v.pressed == e {
			if e.tab {
				b = mix(t.Surface, t.Accent, .18)
			} else {
				b = mix(b, t.Text, .14)
			}
		} else if v.hover == e {
			if e.tab {
				b = mix(t.Surface, t.Accent, .08)
			} else if e.primary {
				b = mix(b, t.Text, .06)
			} else {
				b = mix(b, t.Accent, .08)
			}
		}
		outlined := !e.primary && !e.tab
		if outlined {
			p.RoundRect(r, s.radius, border)
			r = Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}
		}
		radius := s.radius
		if outlined {
			radius--
		}
		if b.A != 0 {
			p.RoundRect(r, max(0, radius), b)
		}
		label(e.text, r, c, true, true)
	case kInput, kEditor:
		bc := border
		if v.focused == e {
			bc = t.Accent
		}
		p.RoundRect(r, s.radius, bc)
		p.RoundRect(Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}, max(0, s.radius-1), t.Surface)
		inner := Rect{r.X + 12, r.Y + 10, r.W - 24, r.H - 20}
		if e.kind == kEditor {
			inner.X += 34
			inner.W -= 34
			p.Rect(Rect{r.X + 1, r.Y + 1, 34, r.H - 2}, t.Raised)
		}
		p.clip = p.clip.Intersect(inner.pixels(v.scale))
		text := e.text
		if e.password {
			text = strings.Repeat("•", len([]rune(text)))
		}
		ink := fg
		if text == "" {
			text = e.placeholder
			ink = t.Muted
		}
		v.paintSelection(p, e, inner, font)
		lineH := v.text.LineHeight(font, false)
		i := 0
		for line := range strings.SplitSeq(text, "\n") {
			y := inner.Y + float32(i)*lineH - e.scroll
			if y > inner.Y+inner.H {
				break
			}
			if y+lineH >= inner.Y {
				p.Label(line, inner.X-e.scrollX, y, font, ink, false)
				if e.kind == kEditor {
					saved := p.clip
					p.clip = oldClip.Intersect(r.pixels(v.scale))
					p.Label(strconv.Itoa(i+1), r.X+8, y, font-2, t.Muted, false)
					p.clip = saved
				}
			}
			i++
		}
		if v.focused == e {
			x, y := v.caretPosition(e, inner)
			p.Rect(Rect{x, y, 1.4, font * 1.25}, t.Accent)
		}
	case kCheck, kRadio, kSwitch:
		cx, cy := r.X+10, r.Y+r.H/2
		textX := r.X + 30
		if e.kind == kSwitch {
			b := t.Border
			if e.checked {
				b = t.Accent
			}
			p.RoundRect(Rect{r.X, cy - 11, 40, 22}, 11, b)
			if !e.checked {
				p.RoundRect(Rect{r.X + 1, cy - 10, 38, 20}, 10, t.Raised)
			}
			knob := r.X + 11
			if e.checked {
				knob = r.X + 29
			}
			p.Circle(knob, cy, 8, RGB(255, 255, 255))
			textX = r.X + 52
		} else if e.kind == kRadio {
			if e.checked {
				border = t.Accent
			}
			p.Circle(cx, cy, 9, border)
			p.Circle(cx, cy, 7, t.Surface)
			if e.checked {
				p.Circle(cx, cy, 4, t.Accent)
			}
		} else {
			b := border
			if e.checked {
				b = t.Accent
			}
			p.RoundRect(Rect{r.X, cy - 9, 18, 18}, 5, b)
			if e.checked {
				p.Line(r.X+4, cy, r.X+8, cy+4, 1.8, t.AccentText)
				p.Line(r.X+8, cy+4, r.X+14, cy-4, 1.8, t.AccentText)
			} else {
				p.RoundRect(Rect{r.X + 1, cy - 8, 16, 16}, 4, t.Surface)
			}
		}
		label(e.text, Rect{textX, r.Y, r.W - (textX - r.X), r.H}, fg, s.bold, false)
	case kSlider:
		cy := r.Y + r.H/2
		track := Rect{r.X + 8, cy - 3, r.W - 16, 6}
		fraction := float32((e.value - e.minimum) / (e.maximum - e.minimum))
		if math.IsInf(e.maximum-e.minimum, 0) {
			fraction = float32((e.value/2 - e.minimum/2) / (e.maximum/2 - e.minimum/2))
		}
		p.RoundRect(track, 3, t.Raised)
		p.RoundRect(Rect{track.X, track.Y, track.W * fraction, track.H}, 3, t.Accent)
		x := track.X + track.W*fraction
		p.Circle(x, cy, 8, t.Accent)
		p.Circle(x, cy, 5.5, RGB(255, 255, 255))
	case kProgress:
		p.RoundRect(r, r.H/2, t.Raised)
		p.RoundRect(Rect{r.X, r.Y, r.W * float32(e.value), r.H}, r.H/2, t.Accent)
	case kSelect:
		p.RoundRect(r, s.radius, border)
		p.RoundRect(Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}, s.radius-1, t.Surface)
		str := e.placeholder
		if e.selected >= 0 && e.selected < len(e.options) {
			str = e.options[e.selected]
		}
		label(str, Rect{r.X + 12, r.Y, r.W - 36, r.H}, fg, false, false)
		x, y := r.X+r.W-18, r.Y+r.H/2
		p.Line(x-4, y-2, x, y+2, 1.5, t.Muted)
		p.Line(x, y+2, x+4, y-2, 1.5, t.Muted)
	case kSeparator:
		p.Rect(r, t.Border)
	case kList, kTable, kTree:
		v.paintRows(p, e, fg)
	case kImage:
		if e.bitmap != nil {
			p.Image(e.bitmap, r)
		}
	case kCanvas:
		if e.draw != nil {
			if p.canvas == nil {
				p.canvas = &Painter{}
			}
			canvas := p.canvas
			*canvas = *p
			canvas.canvas = nil
			canvas.originX, canvas.originY = r.X, r.Y
			canvas.bounds = Rect{W: r.W, H: r.H}
			e.draw(canvas)
		}
	}
}

func (v *view) paintFocus(p *Painter, e *Element) {
	if v.focused == e {
		t := v.theme
		p.RoundRect(Rect{e.rect.X - 3, e.rect.Y - 3, e.rect.W + 6, e.rect.H + 6}, e.style.radius+3, RGBA(t.Accent.R, t.Accent.G, t.Accent.B, 32))
	}
}

func (v *view) paintRows(p *Painter, e *Element, fg Color) {
	originalClip := p.clip
	r, t, font := e.rect, v.theme, v.font(e)
	p.RoundRect(r, e.style.radius, t.Surface)
	header, rowH := e.headerHeight(), e.rowHeight()
	y0 := r.Y + header
	p.clip = p.clip.Intersect(Rect{r.X, y0, r.W, r.H - header}.pixels(v.scale))
	first := max(0, int(e.scroll/rowH))
	last := min(e.rowCount(), int((e.scroll+r.H-header)/rowH)+1)
	_, th := v.text.Measure("Ag", font, false)
	for i := first; i < last; i++ {
		bodyClip := p.clip
		y := y0 + float32(i)*rowH - e.scroll
		rr := Rect{r.X, y, r.W, rowH}
		p.clip = p.clip.Intersect(rr.pixels(v.scale))
		if p.clip.Empty() {
			p.clip = bodyClip
			continue
		}
		if e.selected == i {
			p.Rect(rr, mix(t.Surface, t.Accent, .12))
			p.Rect(Rect{r.X, y, 3, rowH}, t.Accent)
		} else if i%2 != 0 && e.kind == kTable {
			p.Rect(rr, mix(t.Surface, t.Background, .4))
		}
		ty := y + (rowH-th)/2
		switch e.kind {
		case kList:
			p.Label(e.listText(i), r.X+12, ty, font, fg, false)
		case kTree:
			tr := e.visibleTree[i]
			x := r.X + 12 + float32(tr.depth)*18
			if len(tr.node.Children) > 0 {
				if e.expanded[tr.node] {
					p.Line(x, y+15, x+4, y+19, 1.4, t.Muted)
					p.Line(x+4, y+19, x+8, y+15, 1.4, t.Muted)
				} else {
					p.Line(x+2, y+13, x+6, y+17, 1.4, t.Muted)
					p.Line(x+6, y+17, x+2, y+21, 1.4, t.Muted)
				}
			}
			p.Label(tr.node.Label, x+18, ty, font, fg, false)
		case kTable:
			index := i
			if len(e.order) > 0 {
				index = e.order[i]
			}
			x := r.X
			for col, c := range e.columns {
				width := v.columnWidth(e, col)
				if col < len(e.rows[index]) {
					saved := p.clip
					p.clip = p.clip.Intersect(Rect{x, y, width, rowH}.pixels(v.scale))
					p.Label(e.rows[index][col], x+14, ty, font, fg, false)
					p.clip = saved
				}
				x += width
				_ = c
			}
		}
		p.clip = bodyClip
	}
	p.clip = originalClip
	if e.kind == kTable {
		p.clip = p.clip.Intersect(Rect{r.X, r.Y, r.W, header}.pixels(v.scale))
		p.RoundRect(Rect{r.X, r.Y, r.W, header + e.style.radius}, e.style.radius, t.Raised)
		p.Rect(Rect{r.X, r.Y + header - 1, r.W, 1}, t.Border)
		x := r.X
		for i, c := range e.columns {
			p.Label(c.Title, x+14, r.Y+12, font-1, t.Muted, true)
			x += v.columnWidth(e, i)
		}
		p.clip = originalClip
	}
	if total := float32(e.rowCount()) * rowH; total > r.H-header {
		trackH := r.H - header
		thumb := max(24, trackH*trackH/total)
		y := y0 + (trackH-thumb)*e.scroll/max(1, e.maxScroll())
		p.RoundRect(Rect{r.X + r.W - 5, y, 3, thumb}, 2, mix(t.Border, t.Muted, .3))
	}
}
func (v *view) columnWidth(e *Element, i int) float32 {
	if e.columns[i].Width > 0 {
		return e.columns[i].Width
	}
	fixed := float32(0)
	flex := 0
	for _, c := range e.columns {
		if c.Width > 0 {
			fixed += c.Width
		} else {
			flex++
		}
	}
	return max(40, (e.rect.W-fixed)/float32(max(1, flex)))
}

func (v *view) paintSelection(p *Painter, e *Element, inner Rect, font float32) {
	if v.focused != e || e.cursor == e.anchor {
		return
	}
	start, end := min(e.cursor, e.anchor), max(e.cursor, e.anchor)
	lineStart, row := 0, 0
	lineH := v.text.LineHeight(font, false)
	for line := range strings.SplitSeq(e.text, "\n") {
		y := inner.Y + float32(row)*lineH - e.scroll
		if y*p.scale > float32(p.clip.Max.Y) || lineStart >= end {
			break
		}
		count := utf8.RuneCountInString(line)
		a, b := max(start, lineStart), min(end, lineStart+count)
		if a < b && (y+lineH)*p.scale >= float32(p.clip.Min.Y) {
			before := line[:runeByteOffset(line, a-lineStart)]
			selected := line[runeByteOffset(line, a-lineStart):runeByteOffset(line, b-lineStart)]
			if e.password {
				before = strings.Repeat("•", a-lineStart)
				selected = strings.Repeat("•", b-a)
			}
			x, _ := v.text.Measure(before, font, false)
			w, _ := v.text.Measure(selected, font, false)
			p.Rect(Rect{inner.X + x - e.scrollX, y, w, lineH}, mix(v.theme.Surface, v.theme.Accent, .2))
		}
		lineStart += count + 1
		row++
	}
}
