package ui

// Chart surfaces, grid opacity, text and legend markers follow the shadcn
// Neutral chart components and use the same tokens as the surrounding controls.
func chartCanvas(w, h float32, draw func(*Painter)) *Element {
	e := Canvas(w, h, draw).Radius(14)
	e.style.card = true
	return e
}

func chartInk(theme Theme, color Color, index int) Color {
	if color.A != 0 {
		return color
	}
	return theme.Chart[index%len(theme.Chart)]
}

func drawChartLegend(p *Painter, n int, item func(int) (string, Color)) float32 {
	w, h := p.Bounds().W, p.Bounds().H
	top := h - 7
	next := n
	for next > 0 && top-20 >= h*.7 {
		end, start := next, next
		width := float32(0)
		for start > 0 {
			name, _ := item(start - 1)
			if name == "" {
				start--
				continue
			}
			tw, _ := p.Measure(name, 12, false)
			if width != 0 && width+tw+32 > w-24 {
				break
			}
			width += tw + 28
			start--
		}
		next = start
		if width == 0 {
			continue
		}
		top -= 20
		x := max(12, (w-width+12)/2)
		for i := start; i < end; i++ {
			name, ink := item(i)
			if name == "" {
				continue
			}
			tw, _ := p.Measure(name, 12, false)
			p.RoundRect(Rect{x, top + 5, 8, 8}, 2, ink)
			p.Label(name, x+14, top, 12, p.theme.Text, false)
			x += tw + 28
		}
	}
	return top
}

type chartTooltipEntry struct {
	name, value string
	color       Color
}

func drawChartTooltip(p *Painter, x, y float32, label string, entries []chartTooltipEntry) {
	bounds, theme := p.Bounds(), p.theme
	width := float32(128)
	if tw, _ := p.Measure(label, 12, true); tw+20 > width {
		width = tw + 20
	}
	for _, entry := range entries {
		name, _ := p.Measure(entry.name, 12, false)
		value, _ := p.Measure(entry.value, 12, true)
		width = max(width, name+value+58)
	}
	width = min(width, bounds.W-16)
	height := min(bounds.H-16, float32(29+20*len(entries)))
	if width <= 0 || height <= 0 {
		return
	}
	box := Rect{max(8, min(bounds.W-width-8, x+10)), max(8, min(bounds.H-height-8, y)), width, height}
	border := theme.Border
	border.A /= 2
	p.Shadow(box, 8)
	p.RoundRect(box, 8, border)
	p.RoundRect(Rect{box.X + 1, box.Y + 1, box.W - 2, box.H - 2}, 7, theme.Background)
	p.Label(label, box.X+10, box.Y+8, 12, theme.Text, true)
	for i, entry := range entries {
		y := box.Y + 27 + 20*float32(i)
		if y+17 > box.Y+box.H {
			break
		}
		p.RoundRect(Rect{box.X + 10, y + 3, 10, 10}, 2, entry.color)
		p.Label(entry.name, box.X+26, y, 12, theme.Muted, false)
		tw, _ := p.Measure(entry.value, 12, true)
		p.Label(entry.value, max(box.X+26, box.X+box.W-tw-10), y, 12, theme.Text, true)
	}
}
