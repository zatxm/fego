package ui

import (
	"math"
	"strconv"
)

type Slice struct {
	Label string
	Value float64
	Color Color
}

// PieChart displays positive finite values and reports slice selection by index.
func PieChart(slices []Slice, onSelect func(int)) *Element {
	type wedge struct {
		Slice
		end   float64
		index int
	}
	var parts []wedge
	largest := float64(0)
	for _, part := range slices {
		if finiteValue(part.Value) {
			largest = max(largest, part.Value)
		}
	}
	total := float64(0)
	if largest > 0 {
		for i, part := range slices {
			if part.Value > 0 && finiteValue(part.Value) {
				total += part.Value / largest
				parts = append(parts, wedge{part, total, i})
			}
		}
	}
	for i := range parts {
		parts[i].end /= total
	}
	selected, hovered := -1, -1
	var paths []Path
	var size Rect
	var cx, cy, radius float32
	canvas := chartCanvas(420, 280, func(p *Painter) {
		bounds, theme := p.Bounds(), p.Theme()
		if len(parts) == 0 {
			p.Label("No data", 12, 12, 14, theme.Muted, false)
			return
		}
		legendTop := drawChartLegend(p, len(parts), func(i int) (string, Color) {
			return parts[i].Label, chartInk(theme, parts[i].Color, i)
		})
		if bounds != size || len(paths) == 0 {
			size = bounds
			cx, cy = bounds.W/2, legendTop/2
			radius = max(0, min(cx, cy)-20)
			paths = make([]Path, len(parts))
			start := float64(0)
			for i, part := range parts {
				path := &paths[i]
				path.MoveTo(cx, cy)
				steps := int(max(2, math.Ceil((part.end-start)*128)))
				for j := range steps + 1 {
					offset := start + (part.end-start)*float64(j)/float64(steps)
					s, c := math.Sincos(offset*2*math.Pi - math.Pi/2)
					path.LineTo(cx+radius*float32(c), cy+radius*float32(s))
				}
				path.Close()
				start = part.end
			}
		}
		for i, part := range parts {
			p.FillPath(&paths[i], chartInk(theme, part.Color, i))
		}
		active := hovered
		if active < 0 {
			active = selected
		}
		if active >= 0 {
			part := parts[active]
			ink := chartInk(theme, part.Color, active)
			p.StrokePath(&paths[active], 2, ink)
			entry := chartTooltipEntry{"Value", strconv.FormatFloat(part.Value, 'g', 6, 64), ink}
			drawChartTooltip(p, cx, cy-radius/2, part.Label, []chartTooltipEntry{entry})
		}
	}).Label("Pie chart")
	selectPart := func(i int) {
		if i < 0 || i >= len(parts) || selected == i {
			return
		}
		selected = i
		canvas.touch(false)
		if onSelect != nil {
			onSelect(parts[i].index)
		}
	}
	partAt := func(x, y float32) int {
		dx, dy := float64(x-cx), float64(y-cy)
		if radius <= 0 || dx*dx+dy*dy > float64(radius*radius) {
			return -1
		}
		turn := math.Atan2(dx, -dy) / (2 * math.Pi)
		if turn < 0 {
			turn++
		}
		for i, part := range parts {
			if turn < part.end {
				return i
			}
		}
		return -1
	}
	canvas.own().onPoint = func(x, y float32) { selectPart(partAt(x, y)) }
	canvas.own().onHover = func(x, y float32) {
		if i := partAt(x, y); i != hovered {
			hovered = i
			if canvas.view != nil {
				canvas.view.repaint(canvas, false)
			}
		}
	}
	canvas.own().onNavigate = func(key string) {
		switch key {
		case "ArrowLeft", "ArrowUp":
			selectPart(max(0, selected-1))
		case "ArrowRight", "ArrowDown":
			selectPart(min(len(parts)-1, selected+1))
		case "Home":
			selectPart(0)
		case "End":
			selectPart(len(parts) - 1)
		}
	}
	return canvas
}
