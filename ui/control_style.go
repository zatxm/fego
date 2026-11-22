package ui

type buttonStyle uint8

const (
	outlinedButton buttonStyle = iota
	tabButton
	segmentButton
	ghostButton
	linkButton
	accordionButton
	sidebarButton
	dayButton
	todayButton
	outsideDayButton
)

func (b buttonStyle) inset() float32 {
	switch b {
	case outlinedButton:
		return 16
	case linkButton:
		return 1
	default:
		return 8
	}
}

type controlIcon uint8

const (
	noIcon controlIcon = iota
	chevronDown
	chevronUp
	chevronLeft
	chevronRight
	closeIcon
	calendarIcon
	searchIcon
	checkIcon
)

func iconButton(icon controlIcon, name string, click func()) *Element {
	b := Button("", click).Label(name).Size(32, 32)
	b.style.icon = icon
	b.style.button = ghostButton
	return b
}

func (p *Painter) icon(icon controlIcon, x, y float32, ink Color) {
	switch icon {
	case chevronDown:
		p.Line(x-4, y-2, x, y+2, 1.5, ink)
		p.Line(x, y+2, x+4, y-2, 1.5, ink)
	case chevronUp:
		p.Line(x-4, y+2, x, y-2, 1.5, ink)
		p.Line(x, y-2, x+4, y+2, 1.5, ink)
	case chevronLeft:
		p.Line(x+2, y-4, x-2, y, 1.5, ink)
		p.Line(x-2, y, x+2, y+4, 1.5, ink)
	case chevronRight:
		p.Line(x-2, y-4, x+2, y, 1.5, ink)
		p.Line(x+2, y, x-2, y+4, 1.5, ink)
	case closeIcon:
		p.Line(x-4, y-4, x+4, y+4, 1.5, ink)
		p.Line(x-4, y+4, x+4, y-4, 1.5, ink)
	case checkIcon:
		p.Line(x-5, y, x-1, y+4, 1.5, ink)
		p.Line(x-1, y+4, x+5, y-3, 1.5, ink)
	case calendarIcon:
		p.Line(x-6, y-5, x+6, y-5, 1, ink)
		p.Line(x-6, y+6, x+6, y+6, 1, ink)
		p.Line(x-6, y-5, x-6, y+6, 1, ink)
		p.Line(x+6, y-5, x+6, y+6, 1, ink)
		p.Line(x-5, y-1, x+5, y-1, 1.3, ink)
		p.Line(x-3, y-7, x-3, y-3, 1.3, ink)
		p.Line(x+3, y-7, x+3, y-3, 1.3, ink)
	case searchIcon:
		p.Circle(x-1.5, y-1.5, 4.5, ink)
		p.Circle(x-1.5, y-1.5, 3.2, p.theme.Surface)
		p.Line(x+2, y+2, x+6, y+6, 1.3, ink)
	}
}

func (p *Painter) frame(r Rect, radius float32, border, fill Color) {
	inner := Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2}
	if fill.A == 255 && inner.W > 2*radius && inner.H > 2 {
		// A text-only update entirely inside the opaque center needs no border.
		center := Rect{inner.X + radius + p.originX, inner.Y + p.originY, inner.W - 2*radius, inner.H}
		if p.clip.In(center.pixels(p.scale).Inset(1)) {
			p.fillPixels(p.clip, fill)
			return
		}
	}
	p.RoundRect(r, radius, border)
	p.RoundRect(inner, max(0, radius-1), fill)
}

func backgroundUnder(t *Theme, e *Element) Color {
	for p := e.parent; p != nil; p = p.parent {
		if p.style.bgSet && p.style.bg.A == 255 {
			return p.style.bg
		}
		switch p.style.tone {
		case sidebarSurface:
			return t.Sidebar
		case subtleSurface:
			return t.Raised
		case sheetSurface, drawerSurface, dialogSurface:
			return t.Background
		case commandSurface:
			return t.Surface
		}
		if p.style.card {
			return t.Surface
		}
	}
	return t.Background
}

func fieldSurface(t *Theme, e *Element) Color {
	if t.InputBorder.A < 255 {
		ink := RGB(t.InputBorder.R, t.InputBorder.G, t.InputBorder.B)
		return mix(backgroundUnder(t, e), ink, float32(t.InputBorder.A)*(.3/255))
	}
	return t.Surface
}

func (v *view) paintButton(p *Painter, e *Element, r Rect, ink, border Color, disabled bool) {
	t, s := &v.theme, &e.style
	fill := Color{}
	outline := false
	switch s.button {
	case outlinedButton:
		fill, outline = fieldSurface(t, e), !e.primary
		if e.primary {
			fill, ink = t.Accent, t.AccentText
		}
	case tabButton:
		ink = t.Muted
		if e.primary {
			fill, ink = t.Background, t.Text
			if t.InputBorder.A < 255 {
				fill, outline = fieldSurface(t, e), true
				border = t.InputBorder
			}
		}
	case segmentButton, sidebarButton:
		if e.primary {
			fill = t.Raised
		}
	case linkButton, outsideDayButton:
		ink = t.Muted
	case todayButton:
		fill = t.Raised
	}
	if (s.button == dayButton || s.button == todayButton || s.button == outsideDayButton) && e.primary {
		fill, ink = t.Accent, t.AccentText
	}
	if s.bgSet {
		fill = s.bg
	}
	selected := e.primary && (s.button == outlinedButton || s.button == dayButton || s.button == todayButton || s.button == outsideDayButton)
	hover := !disabled && (v.hover == e || v.pressed == e)
	if disabled {
		base := backgroundUnder(t, e)
		ink = mix(base, ink, .5)
		border.A /= 2
		if fill.A != 0 {
			fill = mix(base, fill, .5)
		}
	} else if hover {
		switch {
		case selected:
			opacity := float32(.9)
			if v.pressed == e {
				opacity = .8
			}
			fill = mix(t.Surface, fill, opacity)
		case s.button == linkButton || s.button == accordionButton:
			ink = t.Text
		case s.button == tabButton:
			ink = t.Text
		default:
			fill = t.Raised
		}
	}
	if outline {
		p.frame(r, s.radius, border, fill)
	} else if fill.A != 0 {
		p.RoundRect(r, s.radius, fill)
	}
	font := v.font(e)
	tw, th := v.text.Measure(e.text, font, s.bold)
	x, y := r.X+(r.W-tw)/2, r.Y+r.H/2
	switch s.button {
	case linkButton, sidebarButton, accordionButton:
		x = r.X + s.button.inset()
	}
	if icon := s.icon; icon != noIcon {
		ix := r.X + r.W/2
		if e.text != "" {
			if s.button == accordionButton {
				ix = r.X + r.W - 12
			} else {
				x -= 9
				ix = x + tw + 12
			}
		}
		if s.button == accordionButton && e.primary {
			icon = chevronUp
		}
		p.icon(icon, ix, y, ink)
	}
	if e.text != "" {
		p.Label(e.text, x, y-th/2, font, ink, s.bold)
		if hover && (s.button == linkButton || s.button == accordionButton) {
			p.Rect(Rect{x, y + th/2, tw, 1}, ink)
		}
	}
}
