package ui

import (
	"math"
	"strconv"
)

type Series struct {
	Name   string
	Values []float64
	Color  Color // zero selects the chart palette
}

// Chart owns a snapshot of its data. Update it on the UI thread, as with other
// controls. Non-finite samples are missing values and break line segments.
type Chart struct {
	*Element
	series    []Series
	bars      bool
	low, high float64
	count     int
	hasData   bool
}

func LineChart(series ...Series) *Chart { return newChart(false, series) }
func BarChart(series ...Series) *Chart  { return newChart(true, series) }

func newChart(bars bool, series []Series) *Chart {
	c := &Chart{bars: bars}
	c.Element = Canvas(560, 240, c.paintChart).MinWidth(180).MinHeight(120)
	return c.SetSeries(series...)
}

func finiteValue(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (c *Chart) SetSeries(series ...Series) *Chart {
	previous := c.series
	if cap(c.series) < len(series) || len(series) < cap(c.series)/4 {
		c.series = make([]Series, len(series))
	} else {
		c.series = c.series[:len(series)]
	}
	c.low, c.high, c.count, c.hasData = math.Inf(1), math.Inf(-1), 0, false
	for i, s := range series {
		var values []float64
		if i < len(previous) && len(s.Values) >= cap(previous[i].Values)/4 {
			values = previous[i].Values[:0]
		}
		s.Values = append(values, s.Values...)
		c.series[i] = s
		c.count = max(c.count, len(s.Values))
		for _, v := range s.Values {
			if finiteValue(v) {
				c.low, c.high = min(c.low, v), max(c.high, v)
				c.hasData = true
			}
		}
	}
	if len(previous) > len(series) {
		clear(previous[len(series):])
	}
	if !c.hasData {
		c.low, c.high = 0, 1
	} else if c.bars {
		c.low, c.high = min(0, c.low), max(0, c.high)
	}
	if c.low == c.high {
		padding := max(1, math.Abs(c.low)*.1)
		c.low = max(-math.MaxFloat64, c.low-padding)
		c.high = min(math.MaxFloat64, c.high+padding)
	}
	c.Refresh()
	return c
}

func (c *Chart) paintChart(p *Painter) {
	w, h := c.rect.W, c.rect.H
	p.RoundRect(Rect{W: w, H: h}, 12, p.theme.Surface)
	colors := [...]Color{p.theme.Accent, RGB(21, 153, 139), RGB(225, 145, 51), RGB(180, 100, 207)}
	legendX, legendY := float32(16), float32(12)
	for i, series := range c.series {
		if series.Name == "" {
			continue
		}
		tw, _ := p.Measure(series.Name, 12, false)
		if legendX+tw+22 > w-12 && legendX > 16 {
			legendX, legendY = 16, legendY+20
		}
		ink := series.Color
		if ink.A == 0 {
			ink = colors[i%len(colors)]
		}
		p.Circle(legendX+4, legendY+9, 4, ink)
		p.Label(series.Name, legendX+14, legendY, 12, p.theme.Text, false)
		legendX += tw + 32
	}
	plot := Rect{48, legendY + 32, max(0, w-64), max(0, h-legendY-66)}
	if plot.W < 1 || plot.H < 1 {
		return
	}
	if !c.hasData {
		tw, th := p.Measure("No data", 14, false)
		p.Label("No data", plot.X+(plot.W-tw)/2, plot.Y+(plot.H-th)/2, 14, p.theme.Muted, false)
		return
	}
	for i := range 5 {
		fraction := float64(i) / 4
		y := plot.Y + plot.H*float32(1-fraction)
		p.Line(plot.X, y, plot.X+plot.W, y, 1, p.theme.Border)
		// Weighted endpoints avoid overflow for a [-MaxFloat64, MaxFloat64] range.
		value := c.low*(1-fraction) + c.high*fraction
		label := strconv.FormatFloat(value, 'g', 3, 64)
		tw, th := p.Measure(label, 10, false)
		p.Label(label, plot.X-tw-8, y-th/2, 10, p.theme.Muted, false)
	}
	ticks := min(5, c.count)
	for i := range ticks {
		index := (c.count - 1) * i / max(1, ticks-1)
		x := c.x(index, plot)
		label := strconv.Itoa(index)
		tw, _ := p.Measure(label, 10, false)
		p.Label(label, x-tw/2, plot.Y+plot.H+8, 10, p.theme.Muted, false)
	}
	for i, series := range c.series {
		ink := series.Color
		if ink.A == 0 {
			ink = colors[i%len(colors)]
		}
		if c.bars {
			c.paintBars(p, plot, series.Values, i, ink)
		} else {
			c.paintLine(p, plot, series.Values, ink)
		}
	}
}

func (c *Chart) y(value float64, plot Rect) float32 {
	span := c.high - c.low
	f := (value - c.low) / span
	if math.IsInf(span, 0) {
		f = (value/2 - c.low/2) / (c.high/2 - c.low/2)
	}
	return plot.Y + plot.H*(1-float32(f))
}

func (c *Chart) x(index int, plot Rect) float32 {
	if c.count <= 1 {
		return plot.X + plot.W/2
	}
	return plot.X + plot.W*float32(index)/float32(c.count-1)
}

// Dense traces retain each pixel column's first, last, minimum and maximum
// samples; narrow spikes survive without painting a line for every sample.
func (c *Chart) paintLine(p *Painter, plot Rect, values []float64, ink Color) {
	type point struct {
		i int
		v float64
	}
	var first, last, low, high point
	bucket, haveBucket := 0, false
	previousX, previousY, connected := float32(0), float32(0), false
	flush := func() {
		if !haveBucket {
			return
		}
		points := [4]point{first, last, low, high}
		for i := 1; i < len(points); i++ {
			for j := i; j > 0 && points[j].i < points[j-1].i; j-- {
				points[j], points[j-1] = points[j-1], points[j]
			}
		}
		for i, value := range points {
			if i > 0 && value.i == points[i-1].i {
				continue
			}
			x, y := c.x(value.i, plot), c.y(value.v, plot)
			if connected {
				p.Line(previousX, previousY, x, y, 2, ink)
			}
			if !connected || c.count <= 80 {
				p.Circle(x, y, 3.2, p.theme.Surface)
				p.Circle(x, y, 2, ink)
			}
			previousX, previousY, connected = x, y, true
		}
		haveBucket = false
	}
	for i, value := range values {
		if !finiteValue(value) {
			flush()
			connected = false
			continue
		}
		nextBucket := int(c.x(i, plot) - plot.X)
		if haveBucket && nextBucket != bucket {
			flush()
		}
		at := point{i, value}
		if !haveBucket {
			first, last, low, high = at, at, at, at
			bucket, haveBucket = nextBucket, true
		} else {
			last = at
			if value < low.v {
				low = at
			}
			if value > high.v {
				high = at
			}
		}
	}
	flush()
}

func (c *Chart) paintBars(p *Painter, plot Rect, values []float64, series int, ink Color) {
	groups := min(c.count, max(1, int(plot.W/float32(max(4, len(c.series)*3)))))
	step := plot.W / float32(groups)
	width := step * .75 / float32(len(c.series))
	base := c.y(0, plot)
	for group := range groups {
		start, end := group*c.count/groups, (group+1)*c.count/groups
		low, high := math.Inf(1), math.Inf(-1)
		for i := start; i < min(end, len(values)); i++ {
			if value := values[i]; finiteValue(value) {
				low, high = min(low, value), max(high, value)
			}
		}
		if low > high {
			continue
		}
		top, bottom := min(base, c.y(high, plot)), max(base, c.y(low, plot))
		x := plot.X + float32(group)*step + step*.125 + float32(series)*width
		p.RoundRect(Rect{x, top, max(1, width-1), max(1, bottom-top)}, min(3, width/3), ink)
	}
}
