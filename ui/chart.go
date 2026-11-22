package ui

import (
	"math"
	"slices"
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
	series                      []Series
	bars                        bool
	area, stacked               bool
	plot                        Rect
	selectedIndex, hoveredIndex int
	onSample                    func(int)
	areaPaths                   []Path
	low, high                   float64
	count                       int
	hasData                     bool
	// Reduced shapes are reused by every paint band until data or plot changes.
	version  uint64
	traceKey chartTraceKey
	traces   [][]tracePoint
	barRects [][]Rect
}

type chartTraceKey struct {
	version uint64
	plot    Rect
}

type tracePoint struct {
	x, y    float32
	connect bool
}

func LineChart(series ...Series) *Chart { return newChart(false, series) }
func BarChart(series ...Series) *Chart  { return newChart(true, series) }

// AreaChart fills the space between each series and the zero axis.
func AreaChart(series ...Series) *Chart {
	c := newChart(false, nil)
	c.area = true
	return c.SetSeries(series...)
}

// StackedBarChart stacks positive and negative values separately.
func StackedBarChart(series ...Series) *Chart {
	c := newChart(true, nil)
	c.stacked = true
	return c.SetSeries(series...)
}

// OnSample selects a sample when the plot is clicked or operated with arrows.
func (c *Chart) OnSample(fn func(index int)) *Chart {
	c.onSample = fn
	return c
}

func (c *Chart) sampleAt(x, y float32) int {
	if c.count == 0 || !c.plot.contains(x, y) {
		return -1
	}
	progress := float64((x - c.plot.X) / c.plot.W)
	if c.bars {
		groups := c.barGroups(c.plot)
		group := min(groups-1, int(progress*float64(groups)))
		return group*c.count/groups + ((group+1)*c.count/groups-group*c.count/groups)/2
	}
	return int(math.Round(progress * float64(c.count-1)))
}

func (c *Chart) selectSample(index int) {
	if index >= 0 && index < c.count && index != c.selectedIndex {
		c.selectedIndex = index
		c.Element.touch(false)
		if c.onSample != nil {
			c.onSample(index)
		}
	}
}

func (c *Chart) navigateSample(key string) {
	index := c.selectedIndex
	switch key {
	case "ArrowLeft", "ArrowUp":
		index--
	case "ArrowRight", "ArrowDown":
		index++
	case "Home":
		index = 0
	case "End":
		index = c.count - 1
	}
	c.selectSample(max(0, min(c.count-1, index)))
}

func newChart(bars bool, series []Series) *Chart {
	c := &Chart{bars: bars, selectedIndex: -1, hoveredIndex: -1}
	c.Element = chartCanvas(560, 240, c.paintChart).MinWidth(180).MinHeight(120).Label("Chart")
	c.Element.own().onPoint = func(x, y float32) { c.selectSample(c.sampleAt(x, y)) }
	c.Element.own().onNavigate = c.navigateSample
	c.Element.own().onHover = func(x, y float32) {
		if i := c.sampleAt(x, y); i != c.hoveredIndex {
			c.hoveredIndex = i
			c.Element.touch(false)
		}
	}
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
	c.version++
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
		if len(series) < len(c.areaPaths) {
			clear(c.areaPaths[len(series):])
		}
	}
	if c.selectedIndex >= c.count {
		c.selectedIndex = -1
	}
	if c.hoveredIndex >= c.count {
		c.hoveredIndex = -1
	}
	if c.stacked && c.hasData {
		c.low, c.high = 0, 0
		for i := range c.count {
			low, high := float64(0), float64(0)
			for _, series := range c.series {
				if i < len(series.Values) && finiteValue(series.Values[i]) {
					v := series.Values[i]
					low = max(-math.MaxFloat64, low+min(0, v))
					high = min(math.MaxFloat64, high+max(0, v))
				}
			}
			c.low, c.high = min(c.low, low), max(c.high, high)
		}
	}
	if !c.hasData {
		c.low, c.high = 0, 1
	} else if c.bars || c.area {
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
	legendTop := drawChartLegend(p, len(c.series), func(i int) (string, Color) {
		series := c.series[i]
		return series.Name, chartInk(p.theme, series.Color, i)
	})
	plot := Rect{32, 24, max(0, w-64), max(0, min(h-16, legendTop)-58)}
	c.plot = plot
	if plot.W < 1 || plot.H < 1 {
		return
	}
	if !c.hasData {
		tw, th := p.Measure("No data", 14, false)
		p.Label("No data", plot.X+(plot.W-tw)/2, plot.Y+(plot.H-th)/2, 14, p.theme.Muted, false)
		return
	}
	grid := p.theme.Border
	grid.A /= 2
	for i := range 5 {
		y := plot.Y + plot.H*float32(i)/4
		p.Rect(Rect{plot.X, y, plot.W, 1}, grid)
	}
	ticks := min(6, c.count)
	for i := range ticks {
		index := (c.count - 1) * i / max(1, ticks-1)
		x := c.x(index, plot)
		if c.bars {
			x = plot.X + plot.W*(float32(index)+.5)/float32(c.count)
		}
		label := strconv.Itoa(index)
		tw, _ := p.Measure(label, 12, false)
		p.Label(label, x-tw/2, plot.Y+plot.H+10, 12, p.theme.Muted, false)
	}
	active := c.hoveredIndex
	if active < 0 {
		active = c.selectedIndex
	}
	hasActive := active >= 0 && active < c.count
	activeX := float32(0)
	if hasActive {
		activeX = c.x(active, plot)
		if c.bars {
			groups := c.barGroups(plot)
			step := plot.W / float32(groups)
			activeX = plot.X + (float32(active*groups/c.count)+.5)*step
			p.Rect(Rect{activeX - step/2, plot.Y, step, plot.H}, p.theme.Raised)
		} else {
			p.Rect(Rect{activeX, plot.Y, 1, plot.H}, p.theme.Border)
		}
	}
	if c.traceKey != (chartTraceKey{c.version, plot}) {
		c.traceKey = chartTraceKey{c.version, plot}
		if c.bars {
			c.barRects = resizeShapes(c.barRects, len(c.series))
		} else {
			c.traces = resizeShapes(c.traces, len(c.series))
			if c.area {
				c.areaPaths = slices.Grow(c.areaPaths[:0], len(c.series))[:len(c.series)]
			}
		}
		for i, series := range c.series {
			if c.stacked {
				c.barRects[i] = c.barRects[i][:0]
			} else if c.bars {
				c.barRects[i] = c.barShapes(c.barRects[i][:0], plot, series.Values, i)
			} else {
				c.traces[i] = c.trace(c.traces[i][:0], plot, series.Values)
				if c.area {
					c.areaPath(&c.areaPaths[i], c.traces[i], c.y(0, plot))
				}
			}
		}
		if c.stacked {
			c.stackShapes(plot)
		}
	}
	radius := float32(0)
	if c.bars {
		radius = min(4, c.barWidth(plot)/3)
	}
	for i, series := range c.series {
		ink := chartInk(p.theme, series.Color, i)
		if c.bars {
			for _, r := range c.barRects[i] {
				p.RoundRect(r, radius, ink)
			}
		} else {
			if c.area {
				fill := ink
				fill.A = uint8(int(fill.A) * 2 / 5)
				p.FillPath(&c.areaPaths[i], fill)
			}
			paintTrace(p, c.traces[i], ink)
			if hasActive && active < len(series.Values) && finiteValue(series.Values[active]) {
				y := c.y(series.Values[active], plot)
				p.Circle(activeX, y, 5, p.theme.Surface)
				p.Circle(activeX, y, 4, ink)
			}
		}
	}
	if hasActive {
		var entries [8]chartTooltipEntry
		k := 0
		for i, series := range c.series {
			if active >= len(series.Values) || !finiteValue(series.Values[active]) {
				continue
			}
			name := series.Name
			if name == "" {
				name = "Value"
				if len(c.series) > 1 {
					name += " " + strconv.Itoa(i+1)
				}
			}
			entries[k] = chartTooltipEntry{name, strconv.FormatFloat(series.Values[active], 'g', 6, 64), chartInk(p.theme, series.Color, i)}
			k++
			if k == len(entries) {
				break
			}
		}
		if k != 0 {
			drawChartTooltip(p, activeX, plot.Y+4, strconv.Itoa(active), entries[:k])
		}
	}
}

func (c *Chart) areaPath(path *Path, trace []tracePoint, bottom float32) {
	path.Reset()
	for i, pt := range trace {
		if !pt.connect {
			path.MoveTo(pt.x, bottom)
		}
		path.LineTo(pt.x, pt.y)
		if i+1 == len(trace) || !trace[i+1].connect {
			path.LineTo(pt.x, bottom)
			path.Close()
		}
	}
}

func (c *Chart) stackShapes(plot Rect) {
	groups := c.barGroups(plot)
	step := plot.W / float32(groups)
	for group := range groups {
		lo, hi := group*c.count/groups, (group+1)*c.count/groups
		positive, negative := float64(0), float64(0)
		for i, series := range c.series {
			mean := float64(0)
			n := 0
			for _, v := range series.Values[min(lo, len(series.Values)):min(hi, len(series.Values))] {
				if finiteValue(v) {
					n++
					mean += v / float64(hi-lo)
				}
			}
			if n == 0 || mean == 0 {
				continue
			}
			base := positive
			if mean < 0 {
				base = negative
			}
			end := max(-math.MaxFloat64, min(math.MaxFloat64, base+mean))
			if mean < 0 {
				negative = end
			} else {
				positive = end
			}
			a, b := c.y(max(c.low, min(c.high, base)), plot), c.y(max(c.low, min(c.high, end)), plot)
			c.barRects[i] = append(c.barRects[i], Rect{plot.X + (float32(group)+.125)*step, min(a, b), max(1, .75*step-1), max(1, max(a, b)-min(a, b))})
		}
	}
}

// resizeShapes keeps per-series buffers and drops ones for removed series.
func resizeShapes[T any](shapes [][]T, n int) [][]T {
	if cap(shapes) < n {
		shapes = make([][]T, n)
	}
	clear(shapes[n:cap(shapes)])
	return shapes[:n]
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

func paintTrace(p *Painter, trace []tracePoint, ink Color) {
	var previous tracePoint
	for i, point := range trace {
		if point.connect {
			p.Line(previous.x, previous.y, point.x, point.y, 2, ink)
		}
		if !point.connect && (i+1 == len(trace) || !trace[i+1].connect) {
			p.Circle(point.x, point.y, 2, ink)
		}
		previous = point
	}
}

// Dense traces retain the first, last, minimum and maximum in each narrow
// band. At high sample density, two-pixel bands avoid redundant subpixel lines.
func (c *Chart) trace(trace []tracePoint, plot Rect, values []float64) []tracePoint {
	type point struct {
		i int
		v float64
	}
	var first, last, low, high point
	bucket, haveBucket := 0, false
	connected := false
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
			trace = append(trace, tracePoint{x, y, connected})
			connected = true
		}
		haveBucket = false
	}
	columnWidth := float32(1)
	if float32(len(values)) > 2*plot.W {
		columnWidth = 2
	}
	for i, value := range values {
		if !finiteValue(value) {
			flush()
			connected = false
			continue
		}
		nextBucket := int((c.x(i, plot) - plot.X) / columnWidth)
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
	return trace
}

func (c *Chart) barGroups(plot Rect) int {
	return min(c.count, max(1, int(plot.W/float32(max(4, len(c.series)*3)))))
}

func (c *Chart) barWidth(plot Rect) float32 {
	if c.stacked {
		return plot.W / float32(c.barGroups(plot)) * .75
	}
	return plot.W / float32(c.barGroups(plot)) * .75 / float32(len(c.series))
}

func (c *Chart) barShapes(rects []Rect, plot Rect, values []float64, series int) []Rect {
	groups := c.barGroups(plot)
	step := plot.W / float32(groups)
	width := c.barWidth(plot)
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
		rects = append(rects, Rect{x, top, max(1, width-1), max(1, bottom-top)})
	}
	return rects
}
