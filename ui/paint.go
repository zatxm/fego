package ui

import (
	"encoding/binary"
	"image"
	"image/color"
	"math"
	"strings"
	"unicode/utf8"

	fonttext "github.com/zatxm/fego/v26/internal/text"
)

// Painter draws in logical pixels, clipped to its widget and the changed region.
type Painter struct {
	dst              *image.RGBA
	clip             image.Rectangle
	scale            float32
	text             *fonttext.System
	mono             **fonttext.System
	theme            Theme
	originX, originY float32
	bounds           Rect
	// solid is the latest opaque fill with nothing drawn over it since, so text
	// on it can copy pre-blended glyphs.
	solid      image.Rectangle
	solidColor Color
}

// Bounds is the canvas's local logical rectangle; Theme is its current palette.
func (p *Painter) Bounds() Rect { return p.bounds }
func (p *Painter) Theme() Theme { return p.theme }

func (p *Painter) fillPixels(r image.Rectangle, c Color) {
	r = r.Intersect(p.clip).Intersect(p.dst.Rect)
	if r.Empty() || c.A == 0 {
		return
	}
	if c.A < 255 && r.In(p.solid) {
		// Every covered pixel has the same destination. Blend the color once.
		a := uint32(c.A)
		inv := 255 - a
		base := p.solidColor
		c = RGB(uint8((uint32(c.R)*a+uint32(base.R)*inv+127)/255),
			uint8((uint32(c.G)*a+uint32(base.G)*inv+127)/255),
			uint8((uint32(c.B)*a+uint32(base.B)*inv+127)/255))
	}
	if c.A == 255 {
		if c == p.solidColor && r.In(p.solid) {
			return
		}
		p.solid, p.solidColor = r, c
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
	p.solid = image.Rectangle{}
	// Same per-pixel blend as pixel. A fixed color makes each channel a function
	// of its destination byte, so larger fills look results up instead.
	a := uint32(c.A)
	inv := 255 - a
	cr, cg, cb := uint32(c.R)*a+127, uint32(c.G)*a+127, uint32(c.B)*a+127
	if r.Dx()*r.Dy() >= 512 {
		var lut [4][256]uint8
		for v := range uint32(256) {
			lut[0][v] = uint8((cr + v*inv) / 255)
			lut[1][v] = uint8((cg + v*inv) / 255)
			lut[2][v] = uint8((cb + v*inv) / 255)
			lut[3][v] = uint8(a + (v*inv+127)/255)
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			row := p.dst.Pix[p.dst.PixOffset(r.Min.X, y):p.dst.PixOffset(r.Max.X, y)]
			for i := 0; i+3 < len(row); i += 4 {
				d := row[i : i+4 : i+4]
				d[0], d[1], d[2], d[3] = lut[0][d[0]], lut[1][d[1]], lut[2][d[2]], lut[3][d[3]]
			}
		}
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := p.dst.Pix[p.dst.PixOffset(r.Min.X, y):p.dst.PixOffset(r.Max.X, y)]
		for i := 0; i+3 < len(row); i += 4 {
			d := row[i : i+4 : i+4]
			d[0] = uint8((cr + uint32(d[0])*inv) / 255)
			d[1] = uint8((cg + uint32(d[1])*inv) / 255)
			d[2] = uint8((cb + uint32(d[2])*inv) / 255)
			d[3] = uint8(a + (uint32(d[3])*inv+127)/255)
		}
	}
}

// outsideRows conservatively rejects device rows [lo, hi] before rounding;
// paint bands repeat every primitive of a full-width canvas.
func (p *Painter) outsideRows(lo, hi float32) bool {
	return hi < float32(p.clip.Min.Y)-1 || lo > float32(p.clip.Max.Y)+1
}

func (p *Painter) pixel(x, y int, c Color) {
	p.solid = image.Rectangle{}
	i := p.dst.PixOffset(x, y)
	blendPixel(p.dst.Pix[i:i+4:i+4], c)
}

func blendPixel(d []byte, c Color) {
	if c.A == 255 {
		d[0], d[1], d[2], d[3] = c.R, c.G, c.B, 255
		return
	}
	ink := uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16 | 0xff000000
	blendPacked(d, spreadRGBA(ink), uint64(c.A))
}

// Separate channels into 16-bit lanes so the four integer blends share a
// multiply. Each rounded numerator fits in its lane, including the correction.
func spreadRGBA(pixel uint32) uint64 {
	const pairs = 0x00ff00ff
	return uint64(pixel&pairs) | uint64((pixel>>8)&pairs)<<32
}

func blendPacked(dst []byte, ink, alpha uint64) {
	const lanes = 0x00ff00ff00ff00ff
	base := spreadRGBA(binary.LittleEndian.Uint32(dst))
	v := ink*alpha + base*(255-alpha) + 0x0080008000800080
	v = ((v + ((v >> 8) & lanes)) >> 8) & lanes
	binary.LittleEndian.PutUint32(dst, uint32(v)|uint32(v>>24))
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
	if p.outsideRows(min(y0, y1), max(y0, y1)) {
		return
	}
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
		// At fractional scales the straight sides still have identical coverage
		// for every complete interior row. Paint each side as one vertical strip.
		end := y + 1
		if dy == 0 && float32(y) >= y0 {
			end = max(end, min(b.Max.Y, int(math.Floor(float64(y1-max(rad, 1))))))
		}
		inset := float32(0)
		if dy > 0 {
			inset = rad - float32(math.Sqrt(float64(max(0, rad*rad-dy*dy))))
		}
		lo, hi := max(b.Min.X, int(math.Ceil(float64(x0+inset)))), min(b.Max.X, int(math.Floor(float64(x1-inset))))
		if lo < hi {
			rowColor := c
			rowColor.A = uint8(float32(c.A) * min(float32(1), max(float32(0), min(py-y0+.5, y1-py+.5))))
			p.fillPixels(image.Rect(lo, y, hi, end), rowColor)
		}
		spans := [2][2]int{{b.Min.X, min(lo, b.Max.X)}, {max(hi, b.Min.X), b.Max.X}}
		for _, span := range spans {
			for x := span[0]; x < span[1]; x++ {
				px := float32(x) + .5
				dx := max(x0+rad-px, px-(x1-rad), 0)
				cov := min(float32(1), max(float32(0), rad+.5-float32(math.Sqrt(float64(dx*dx+dy*dy)))))
				if cov > 0 {
					cc := c
					cc.A = uint8(float32(c.A) * cov)
					if end == y+1 {
						p.pixel(x, y, cc)
					} else {
						p.fillPixels(image.Rect(x, y, x+1, end), cc)
					}
				}
			}
		}
		y = end - 1
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
	rad := width/2 + .5
	if c.A == 0 || rad <= 0 {
		return
	}
	padding := min(width, rad)
	if p.outsideRows(min(y1, y2)-padding, max(y1, y2)+padding) {
		return
	}
	r := image.Rect(int(math.Floor(float64(min(x1, x2)-padding))), int(math.Floor(float64(min(y1, y2)-padding))),
		int(math.Ceil(float64(max(x1, x2)+padding))), int(math.Ceil(float64(max(y1, y2)+padding)))).Intersect(p.clip).Intersect(p.dst.Rect)
	if r.Empty() {
		return
	}
	dx, dy := x2-x1, y2-y1
	if math.Abs(float64(dx)) <= 8 || math.Abs(float64(dy)) <= 8 {
		p.linePixels(r, x1, y1, dx, dy, width, c)
		return
	}
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
	radius := width/2 + .5
	inner := max(0, radius-1)
	inner2, outer2 := float32(inner*inner), float32(radius*radius)
	p.solid = image.Rectangle{}
	ink := uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16 | 0xff000000
	lanes := spreadRGBA(ink)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		py := float32(y) + .5
		yProjection := float32((py - y1) * dy)
		row := p.dst.Pix[p.dst.PixOffset(r.Min.X, y):p.dst.PixOffset(r.Max.X, y)]
		for i := 0; i+3 < len(row); i += 4 {
			px := float32(r.Min.X+i/4) + .5
			projection := float32((px-x1)*dx) + yProjection
			t := float32(0)
			if length > 0 && projection > 0 {
				t = 1
				if projection < length {
					t = projection / length
				}
			}
			a, b := px-(x1+float32(t*dx)), py-(y1+float32(t*dy))
			distance := float32(a*a) + float32(b*b)
			if !(distance < outer2) {
				continue
			}
			alpha := c.A
			if distance >= inner2 {
				coverage := radius - float32(math.Sqrt(float64(distance)))
				if !(coverage > 0) {
					continue
				}
				if coverage < 1 {
					alpha = uint8(float32(c.A) * coverage)
				}
			}
			if alpha == 255 {
				binary.LittleEndian.PutUint32(row[i:], ink)
			} else if alpha != 0 {
				blendPacked(row[i:], lanes, uint64(alpha))
			}
		}
	}
}

// UseMonospace selects the document's monospace face for subsequent Label and
// Measure calls in this canvas draw. The default face is Go Mono.
func (p *Painter) UseMonospace() {
	if *p.mono == nil {
		*p.mono = fonttext.NewMonospace()
	}
	p.text = *p.mono
}

func (p *Painter) Label(s string, x, y, size float32, c Color, bold bool) {
	// Widget colors are straight alpha; image/draw text expects premultiplied ink.
	a := uint32(c.A)
	ink := color.RGBA{uint8(uint32(c.R) * a / 255), uint8(uint32(c.G) * a / 255), uint8(uint32(c.B) * a / 255), c.A}
	under := color.RGBA{p.solidColor.R, p.solidColor.G, p.solidColor.B, 255}
	p.text.DrawOn(p.dst, p.clip, s, (x+p.originX)*p.scale, (y+p.originY)*p.scale, size*p.scale, ink, bold, p.solid, under)
	p.solid = image.Rectangle{}
}

func (p *Painter) Measure(s string, size float32, bold bool) (float32, float32) {
	return p.text.Measure(s, size, bold)
}
func (p *Painter) Image(img image.Image, r Rect) {
	if img == nil {
		return
	}
	p.solid = image.Rectangle{}
	r.X += p.originX
	r.Y += p.originY
	dst := r.pixels(p.scale)
	clip := dst.Intersect(p.clip).Intersect(p.dst.Rect)
	if clip.Empty() {
		return
	}
	src := img.Bounds()
	if rgba, ok := img.(*image.RGBA); ok && !src.Empty() && dst.Dx() >= src.Dx() && dst.Dy() >= src.Dy() && rgba.Opaque() {
		// Opaque upscaling can reuse painted rows without blending another background.
		// Sampling depends on dst only, so any clip, including a paint band, matches.
		previousY := src.Min.Y - 1
		var previous []byte
		for y := clip.Min.Y; y < clip.Max.Y; y++ {
			sy := src.Min.Y + (y-dst.Min.Y)*src.Dy()/dst.Dy()
			row := p.dst.Pix[p.dst.PixOffset(clip.Min.X, y):p.dst.PixOffset(clip.Max.X, y)]
			if sy == previousY {
				copy(row, previous)
			} else {
				source := rgba.Pix[(sy-src.Min.Y)*rgba.Stride:]
				// Advance the nearest-neighbor sample with an integer remainder.
				// Bands and clipped draws start at the same sample as a full repaint.
				sw, dw := src.Dx(), dst.Dx()
				start := (clip.Min.X - dst.Min.X) * sw
				sx, rem := start/dw, start%dw
				if sw == dw {
					copy(row, source[4*sx:])
				} else {
					for i := 0; i+3 < len(row); i += 4 {
						copy(row[i:i+4], source[4*sx:4*sx+4])
						rem += sw
						if rem >= dw {
							rem -= dw
							sx++
						}
					}
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
		p.RoundRect(Rect{r.X - d, r.Y - d + 2, r.W + d*2, r.H + d*2}, radius+d, RGBA(0, 0, 0, 3))
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
	r := e.rect
	caption := e.captionControl()
	var title Rect
	if caption != nil {
		r = floatingFrame(r)
		title = v.floatingTitleBounds(p, caption, r)
	}
	v.paintFocus(p, e, r, title)
	p.clip = p.clip.Intersect(e.clip.pixels(v.scale))
	fieldClip := p.clip
	defer func() { p.clip = oldClip }()
	disabled = disabled || e.disabled
	t := &v.theme
	primary := t.Accent
	s := &e.style
	font := v.font(e)
	fg, bg := t.Text, Color{}
	switch s.tone {
	case mutedText:
		fg = t.Muted
	case dangerText:
		fg = t.Danger
	case primaryText:
		fg = t.AccentText
	case sidebarSurface:
		bg = t.Sidebar
	case mutedSurface:
		bg = t.Raised
	case primarySurface:
		bg = primary
	case subtleSurface:
		bg = t.Raised
	case groupSurface, segmentSurface:
		bg = fieldSurface(t, e)
	case commandSurface:
		bg = t.Surface
	case sheetSurface, drawerSurface, dialogSurface:
		bg = t.Background
	}
	if s.fgSet {
		fg = s.fg
	}
	if disabled {
		if e.kind != kButton || e.pagination != 0 {
			fg = mix(fg, t.Surface, .55)
		}
		primary = mix(primary, t.Surface, .55)
	}
	if s.bgSet {
		bg = s.bg
	} else if s.card && s.tone != dialogSurface {
		bg = t.Surface
	}
	border := t.Border
	if e.kind == kInput || e.kind == kEditor || e.kind == kSelect ||
		(e.kind == kButton && e.style.button == outlinedButton && !e.primary) || s.tone == groupSurface || e.kind == kCheck || e.kind == kRadio || e.kind == kSwitch {
		border = t.InputBorder
	}
	if s.borderSet && s.border.A != 0 {
		border = s.border
	}
	if !disabled && v.hover == e && e.onHover == nil {
		border = mix(border, t.Ring, .45)
	}
	if v.focused == e && (v.focusVisible || e.kind == kInput || e.kind == kEditor) {
		border = t.Ring
	}
	if s.card {
		clipped := p.clip
		p.clip = oldClip.Intersect(Rect{r.X - 3, r.Y + r.H - 1, r.W + 6, 7}.pixels(v.scale))
		p.RoundRect(Rect{r.X - 2, r.Y + 2, r.W + 4, r.H + 4}, s.radius+2, RGBA(0, 0, 0, 10))
		p.clip = clipped
	}
	if p.clip.Empty() {
		return
	}
	// A transparent border color removes the border instead of filling the
	// element with the theme border under a translucent background.
	if s.tone == groupSurface && v.focusVisible && v.focused != nil && v.focused.parent == e {
		border = t.Ring
	}
	if s.tone == groupSurface || s.tone == segmentSurface {
		p.frame(r, s.radius, border, bg)
	} else if s.card && !s.borderSet || s.borderSet && s.border.A != 0 {
		inner := Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}
		// A damaged area inside the opaque center cannot contain border pixels.
		// Skip painting the border underneath pixels the surface replaces.
		if s.card && bg.A == 255 && p.clip.In(inner.pixels(p.scale).Inset(int(math.Ceil(float64((s.radius+1)*p.scale))))) {
			p.fillPixels(p.clip, bg)
		} else {
			p.RoundRect(r, s.radius, border)
			p.RoundRect(inner, max(0, s.radius-1), bg)
		}
	} else if s.tone == drawerSurface {
		p.frame(Rect{r.X - 1, r.Y, r.W + 2, r.H + 16}, s.radius, t.Border, bg)
		p.RoundRect(Rect{r.X + (r.W-100)/2, r.Y + 14, 100, 8}, 4, t.Raised)
	} else if bg.A != 0 {
		p.RoundRect(r, s.radius, bg)
		if s.tone == sidebarSurface {
			p.Rect(Rect{r.X + r.W - 1, r.Y, 1, r.H}, t.Border)
		} else if s.tone == sheetSurface {
			p.Rect(Rect{r.X, r.Y, 1, r.H}, t.Border)
		}
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
	case kVirtualList:
		p.Rect(r, t.Surface)
		for _, c := range e.children {
			v.paint(p, c, disabled)
		}
		if e.maxScroll() > 0 {
			_, thumb := scrollbar(e)
			p.RoundRect(Rect{thumb.X + 5, thumb.Y, 3, thumb.H}, 2, t.Border)
		}
	case kBox, kVirtualRow:
		if e.kind == kVirtualRow {
			l := e.parent.virtual
			index := int(e.rowIndex)
			_, selected := l.chosen[l.key(index)]
			if selected || v.hover == e || l.header(index) {
				p.Rect(r, t.Raised)
			}
		}
		for _, c := range e.children {
			v.paint(p, c, disabled)
		}
		if e.scrollable && e.maxScroll() > 0 {
			thumb := max(24, r.H*r.H/e.contentH)
			y := r.Y + (r.H-thumb)*e.scroll/e.maxScroll()
			p.RoundRect(Rect{r.X + r.W - 5, y, 3, thumb}, 2, mix(t.Border, t.Muted, .3))
		}
	case kText:
		label(v.labelText(e), r, fg, s.bold, false)
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
		if e.pagination != 0 {
			v.paintPageButton(p, e, r, fg, border, disabled)
			break
		}
		v.paintButton(p, e, r, fg, border, disabled)
		if s.button == segmentButton && r.X > e.parent.rect.X+1 {
			p.Rect(Rect{r.X, r.Y, 1, r.H}, t.InputBorder)
		}
	case kInput, kEditor:
		if e.otpLength > 0 {
			inner := r
			inner.W = e.otpSlot(e.otpLength-1).X + e.otpSlot(0).W - r.X
			p.frame(inner, s.radius, border, fieldSurface(t, e))
			for i := range e.otpLength {
				slot := e.otpSlot(i)
				if i > 0 {
					p.Rect(Rect{slot.X, slot.Y + 1, 1, slot.H - 2}, border)
				}
				if v.focused == e && i == min(e.cursor, e.otpLength-1) {
					p.frame(slot, 4, t.Ring, fieldSurface(t, e))
					if i == len(e.text) {
						p.Rect(v.inputCaretRect(e, slot.X+slot.W/2, slot.Y), t.Text)
					}
				}
				if i >= min(e.anchor, e.cursor) && i < max(e.anchor, e.cursor) {
					p.RoundRect(Rect{slot.X + 3, slot.Y + 3, slot.W - 6, slot.H - 6}, 4, t.Raised)
				}
				if i < len(e.text) {
					label(e.text[i:i+1], slot, fg, false, true)
				}
			}
			break
		}
		bc := border
		if v.focused == e {
			bc = t.Ring
		}
		if e.parent == nil || e.parent.style.tone != groupSurface && e.parent.style.tone != commandSurface {
			p.frame(r, s.radius, bc, fieldSurface(t, e))
		}
		inner := v.inputInner(e)
		if e.kind == kEditor {
			p.Rect(Rect{r.X + 1, r.Y + 1, 48, r.H - 2}, t.Raised)
			v.paintSource(p, e, inner, font, fg)
			break
		}
		p.clip = p.clip.Intersect(inner.pixels(v.scale))
		text, password := e.text, e.password
		ink := fg
		if text == "" {
			text = e.emptyFieldText()
			if v.showsFieldHint(e) {
				text = e.placeholder
			}
			password = false
			ink = t.Muted
		}
		v.paintSelection(p, e, inner, font)
		lineH := v.inputLineHeight(font)
		i := 0
		for line := range strings.SplitSeq(text, "\n") {
			y := inner.Y + float32(i)*lineH - e.scroll
			if y > inner.Y+inner.H {
				break
			}
			if y+lineH >= inner.Y {
				p.Label(inputDisplayLine(line, password), inner.X-e.scrollX, y, font, ink, false)
			}
			i++
		}
		if v.focused == e {
			x, y := v.caretPosition(e, inner)
			p.Rect(v.inputCaretRect(e, x, y), t.Accent)
		}
	case kCheck, kRadio, kSwitch:
		cx, cy := r.X+8, r.Y+r.H/2
		textX := r.X + 24
		if e.kind == kSwitch {
			track, thumb := t.InputBorder, t.Background
			bounds := switchTrack(r)
			knob := bounds.X + 9
			dark := 299*int(t.Background.R)+587*int(t.Background.G)+114*int(t.Background.B) < 128000
			if e.checked {
				track = t.Accent
				knob = bounds.X + bounds.W - 9
				if dark {
					thumb = t.AccentText
				}
			} else if dark {
				track.A = uint8(uint16(track.A) * 4 / 5)
				thumb = t.Text
			}
			if disabled {
				track.A /= 2
				thumb = mix(t.Background, thumb, .5)
			}
			p.RoundRect(bounds, bounds.H/2, track)
			p.Circle(knob, cy, 8, thumb)
			textX = r.X + switchTrackWidth + 10
		} else if e.kind == kRadio {
			p.Circle(cx, cy, 8, border)
			p.Circle(cx, cy, 7, fieldSurface(t, e))
			if e.checked {
				p.Circle(cx, cy, 4, primary)
			}
		} else {
			b := border
			if e.checked {
				b = primary
			}
			p.RoundRect(Rect{r.X, cy - 8, 16, 16}, 4, b)
			if e.checked {
				p.Line(r.X+4, cy, r.X+7, cy+3, 1.8, t.AccentText)
				p.Line(r.X+7, cy+3, r.X+12, cy-3, 1.8, t.AccentText)
			} else {
				p.RoundRect(Rect{r.X + 1, cy - 7, 14, 14}, 3, fieldSurface(t, e))
			}
		}
		label(e.text, Rect{textX, r.Y, r.W - (textX - r.X), r.H}, fg, s.bold, false)
	case kSlider:
		cy := r.Y + r.H/2
		track := Rect{r.X + 8, cy - 3, r.W - 16, 6}
		fraction := sliderFraction(e, e.value)
		start, end := float32(0), fraction
		if interval := e.interval; interval != nil {
			start, end = fraction, sliderFraction(e, interval.high)
		}
		p.RoundRect(track, 3, t.Raised)
		p.RoundRect(Rect{track.X + start*track.W, track.Y, track.W * (end - start), track.H}, 3, primary)
		thumb := func(at float32) {
			x := track.X + track.W*at
			p.Circle(x, cy, 8, primary)
			p.Circle(x, cy, 7, RGB(255, 255, 255))
		}
		thumb(fraction)
		if e.interval != nil {
			thumb(end)
		}
	case kProgress:
		p.RoundRect(r, r.H/2, mix(backgroundUnder(t, e), t.Accent, .2))
		p.RoundRect(Rect{r.X, r.Y, r.W * float32(e.value), r.H}, r.H/2, primary)
	case kSelect:
		if e.floatingLabel == "" || caption != nil {
			p.frame(r, s.radius, border, fieldSurface(t, e))
		}
		str, selected := e.selectLabel(e.selected)
		ink := t.Muted
		if selected {
			ink = fg
		}
		box := selectTextBox(r)
		saved := p.clip
		if e.floatingLabel != "" {
			p.clip = p.clip.Intersect(box.pixels(v.scale))
		}
		label(str, box, ink, false, false)
		p.clip = saved
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
			canvas := v.canvas
			if canvas == nil {
				canvas = &Painter{}
			}
			v.canvas = nil // Keep this painter private to the active draw callback.
			*canvas = *p
			canvas.originX, canvas.originY = r.X, r.Y
			canvas.bounds = Rect{W: r.W, H: r.H}
			e.draw(canvas)
			*canvas = Painter{}
			v.canvas = canvas
			p.solid = image.Rectangle{}
		}
	}
	if title.W > 0 {
		p.clip = fieldClip
		p.Rect(Rect{title.X - 4, r.Y, title.W + 8, 1}, fieldSurface(t, e))
		p.clip = fieldClip.Intersect(title.pixels(v.scale))
		ink := t.Muted
		if disabled || caption.disabled {
			ink = mix(ink, t.Surface, .45)
		}
		p.Label(caption.floatingLabel, title.X, title.Y, min(float32(12), v.font(caption)), ink, false)
	}
}

const switchTrackWidth = 32

func switchTrack(r Rect) Rect {
	return Rect{r.X, r.Y + r.H/2 - 9, switchTrackWidth, 18}
}

func (v *view) paintFocus(p *Painter, e *Element, r, title Rect) {
	group := e.style.tone == groupSurface && v.focused != nil && v.focused.parent == e
	if (v.focused == e || group) && v.focusVisible {
		t := v.theme
		radius := e.style.radius
		if e.kind == kSwitch {
			r = switchTrack(r)
			radius = r.H / 2
		}
		if e.otpLength > 0 || e.parent != nil && (e.parent.style.tone == commandSurface || e.parent.style.tone == groupSurface) {
			return
		}
		ring := Rect{r.X - 3, r.Y - 3, r.W + 6, r.H + 6}
		ink := RGBA(t.Ring.R, t.Ring.G, t.Ring.B, 128)
		if title.W > 0 {
			// Leave the original parent surface visible in the notch above the frame.
			gap := (Rect{title.X - 4, r.Y - 3, title.W + 8, 3}).pixels(v.scale).Intersect(p.clip)
			if !gap.Empty() {
				clip := p.clip
				for _, part := range [...]image.Rectangle{
					image.Rect(clip.Min.X, clip.Min.Y, gap.Min.X, clip.Max.Y),
					image.Rect(gap.Max.X, clip.Min.Y, clip.Max.X, clip.Max.Y),
					image.Rect(gap.Min.X, gap.Max.Y, gap.Max.X, clip.Max.Y),
				} {
					p.clip = part
					if !part.Empty() {
						p.RoundRect(ring, radius+3, ink)
					}
				}
				p.clip = clip
				return
			}
		}
		p.RoundRect(ring, radius+3, ink)
	}
}

func (v *view) paintRows(p *Painter, e *Element, fg Color) {
	originalClip := p.clip
	r, t, font := e.rect, v.theme, v.font(e)
	if t.Surface != p.solidColor || !p.clip.In(p.solid) {
		p.RoundRect(r, e.style.radius, t.Surface)
	}
	header, rowH := e.headerHeight(), e.rowHeight()
	count := e.rowCount()
	menuList := e.style.tone == commandSurface || e.style.tone == suggestionSurface
	hovered := -1
	if v.hover == e && (e.kind == kTable || menuList) {
		hovered = v.hoveredRow
	}
	y0 := r.Y + header
	p.clip = p.clip.Intersect(Rect{r.X, y0, r.W, r.H - header}.pixels(v.scale))
	first := max(0, int((e.scroll+float32(p.clip.Min.Y)/p.scale-y0)/rowH)-1)
	last := min(count, int((e.scroll+float32(p.clip.Max.Y)/p.scale-y0)/rowH)+2)
	var th, flex float32
	if !p.clip.Empty() && first < last {
		_, th = v.text.Measure("Ag", font, false)
	} else {
		last = first
	}
	if e.kind == kTable {
		flex = flexibleColumnWidth(e)
	}
	for i := first; i < last; i++ {
		bodyClip := p.clip
		y := y0 + float32(i)*rowH - e.scroll
		rr := Rect{r.X, y, r.W, rowH}
		p.clip = p.clip.Intersect(rr.pixels(v.scale))
		if p.clip.Empty() {
			p.clip = bodyClip
			continue
		}
		if e.selected == i || hovered == i {
			fill := t.Raised
			if e.selected != i {
				fill = mix(t.Surface, t.Raised, .5)
			}
			if menuList {
				p.RoundRect(rr, 4, fill)
			} else {
				p.Rect(rr, fill)
			}
		}
		if e.kind == kTable && i+1 < e.rowCount() {
			p.Rect(Rect{rr.X, rr.Y + rr.H - 1, rr.W, 1}, t.Border)
		}
		ty := y + (rowH-th)/2
		switch e.kind {
		case kList:
			name := e.listText(i)
			if e.style.tone == suggestionSurface && name == e.text {
				p.icon(checkIcon, rr.X+rr.W-12, rr.Y+rr.H/2, fg)
			}
			x := r.X + 12
			if menuList {
				x = r.X + 8
			}
			p.Label(name, x, ty, font, fg, false)
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
				width := c.Width
				if width <= 0 {
					width = flex
				}
				if col < len(e.rows[index]) {
					saved := p.clip
					p.clip = p.clip.Intersect(Rect{x, y, width, rowH}.pixels(v.scale))
					p.Label(e.rows[index][col], x+14, ty, font, fg, false)
					p.clip = saved
				}
				x += width
			}
		}
		p.clip = bodyClip
	}
	p.clip = originalClip
	headerClip := p.clip.Intersect(Rect{r.X, r.Y, r.W, header}.pixels(v.scale))
	if e.kind == kTable && !headerClip.Empty() {
		p.clip = headerClip
		p.Rect(Rect{r.X, r.Y + header - 1, r.W, 1}, t.Border)
		x := r.X
		for _, c := range e.columns {
			p.Label(c.Title, x+14, r.Y+12, font-1, t.Muted, true)
			width := c.Width
			if width <= 0 {
				width = flex
			}
			x += width
		}
		p.clip = originalClip
	}
	if thumb := e.rowScrollThumb(e.scroll); thumb.H > 0 {
		p.RoundRect(thumb, 2, mix(t.Border, t.Muted, .3))
	}
}

func (e *Element) rowScrollThumb(scroll float32) Rect {
	r, header := e.rect, e.headerHeight()
	track := r.H - header
	total := float32(e.rowCount()) * e.rowHeight()
	if total <= track {
		return Rect{}
	}
	thumb := max(24, track*track/total)
	y := r.Y + header + (track-thumb)*scroll/max(1, e.maxScroll())
	return Rect{r.X + r.W - 5, y, 3, thumb}
}

func flexibleColumnWidth(e *Element) float32 {
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
	lineH := v.inputLineHeight(font)
	for line := range strings.SplitSeq(e.text, "\n") {
		y := inner.Y + float32(row)*lineH - e.scroll
		if y*p.scale > float32(p.clip.Max.Y) || lineStart >= end {
			break
		}
		count := utf8.RuneCountInString(line)
		a, b := max(start, lineStart), min(end, lineStart+count)
		if a < b && (y+lineH)*p.scale >= float32(p.clip.Min.Y) {
			before := inputDisplayLine(line[:runeByteOffset(line, a-lineStart)], e.password)
			through := inputDisplayLine(line[:runeByteOffset(line, b-lineStart)], e.password)
			x, endX := v.inputAdvance(before, font), v.inputAdvance(through, font)
			p.Rect(Rect{inner.X + x - e.scrollX, y, endX - x, lineH}, mix(v.theme.Surface, v.theme.Accent, .2))
		}
		lineStart += count + 1
		row++
	}
}
