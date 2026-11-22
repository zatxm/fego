package ui

import (
	"image"
	"image/color"
	"math"
	"sync"

	"golang.org/x/image/vector"
)

type pathCommand struct {
	verb   byte
	points [3]Point
}

// Point is a position in local logical pixels.
type Point struct{ X, Y float32 }

// Path is a reusable sequence of vector contours.
type Path struct {
	commands []pathCommand
	min, max Point
}

func (s *Path) Reset() { s.commands = s.commands[:0] }

func (s *Path) add(verb byte, points ...Point) {
	for _, p := range points {
		if !finiteValue(float64(p.X)) || !finiteValue(float64(p.Y)) || max(absFloat(p.X), absFloat(p.Y)) > 1<<20 {
			return
		}
	}
	if len(s.commands) == 0 {
		if verb != 'M' {
			return
		}
		s.min, s.max = points[0], points[0]
	}
	command := pathCommand{verb: verb}
	copy(command.points[:], points)
	s.commands = append(s.commands, command)
	for _, p := range points {
		s.min.X, s.max.X = min(s.min.X, p.X), max(s.max.X, p.X)
		s.min.Y, s.max.Y = min(s.min.Y, p.Y), max(s.max.Y, p.Y)
	}
}

func (s *Path) MoveTo(x, y float32)         { s.add('M', Point{x, y}) }
func (s *Path) LineTo(x, y float32)         { s.add('L', Point{x, y}) }
func (s *Path) QuadTo(x1, y1, x, y float32) { s.add('Q', Point{x1, y1}, Point{x, y}) }
func (s *Path) CubicTo(x1, y1, x2, y2, x, y float32) {
	s.add('C', Point{x1, y1}, Point{x2, y2}, Point{x, y})
}
func (s *Path) Close() { s.add('Z') }

var pathRasterizers = sync.Pool{New: func() any { return new(vector.Rasterizer) }}

// FillPath fills the path with the non-zero winding rule.
func (p *Painter) FillPath(s *Path, ink Color) {
	if s == nil || len(s.commands) < 2 || ink.A == 0 {
		return
	}
	x0, y0 := (p.originX+s.min.X)*p.scale, (p.originY+s.min.Y)*p.scale
	x1, y1 := (p.originX+s.max.X)*p.scale, (p.originY+s.max.Y)*p.scale
	clip := p.clip.Intersect(p.dst.Bounds())
	clip.Min.X = int(max(float32(clip.Min.X), float32(math.Floor(float64(x0)))))
	clip.Min.Y = int(max(float32(clip.Min.Y), float32(math.Floor(float64(y0)))))
	clip.Max.X = int(min(float32(clip.Max.X), float32(math.Ceil(float64(x1)))))
	clip.Max.Y = int(min(float32(clip.Max.Y), float32(math.Ceil(float64(y1)))))
	if clip.Empty() {
		return
	}
	raster := pathRasterizers.Get().(*vector.Rasterizer)
	defer pathRasterizers.Put(raster)
	rows := max(1, regionBandBytes/(4*clip.Dx()))
	for y := clip.Min.Y; y < clip.Max.Y; y += rows {
		p.fillPathBand(raster, s, ink, image.Rect(clip.Min.X, y, clip.Max.X, min(clip.Max.Y, y+rows)))
	}
	p.solid = image.Rectangle{}
}

func (p *Painter) fillPathBand(raster *vector.Rasterizer, s *Path, ink Color, clip image.Rectangle) {
	raster.Reset(clip.Dx(), clip.Dy())
	originX, originY := p.originX*p.scale-float32(clip.Min.X), p.originY*p.scale-float32(clip.Min.Y)
	open := false
	for _, cmd := range s.commands {
		a, b, c := cmd.points[0], cmd.points[1], cmd.points[2]
		ax, ay := a.X*p.scale+originX, a.Y*p.scale+originY
		bx, by := b.X*p.scale+originX, b.Y*p.scale+originY
		switch cmd.verb {
		case 'M':
			if open {
				raster.ClosePath()
			}
			raster.MoveTo(ax, ay)
			open = true
		case 'L':
			raster.LineTo(ax, ay)
		case 'Q':
			raster.QuadTo(ax, ay, bx, by)
		case 'C':
			raster.CubeTo(ax, ay, bx, by, c.X*p.scale+originX, c.Y*p.scale+originY)
		case 'Z':
			raster.ClosePath()
			open = false
		}
	}
	if open {
		raster.ClosePath()
	}
	raster.Draw(p.dst, clip, image.NewUniform(color.NRGBA(ink)), image.Point{})
}

// StrokePath draws round joins and caps at the requested width.
func (p *Painter) StrokePath(s *Path, width float32, ink Color) {
	if s == nil || width <= 0 || !finiteValue(float64(width)) || ink.A == 0 {
		return
	}
	var from, start Point
	line := func(to Point) {
		p.Line(from.X, from.Y, to.X, to.Y, width, ink)
		from = to
	}
	for _, cmd := range s.commands {
		a, b, c := cmd.points[0], cmd.points[1], cmd.points[2]
		switch cmd.verb {
		case 'M':
			from, start = a, a
		case 'L':
			line(a)
		case 'Z':
			line(start)
		case 'Q', 'C':
			origin := from
			cubic := cmd.verb == 'C'
			length := math.Hypot(float64(a.X-origin.X), float64(a.Y-origin.Y)) + math.Hypot(float64(b.X-a.X), float64(b.Y-a.Y))
			if cubic {
				length += math.Hypot(float64(c.X-b.X), float64(c.Y-b.Y))
			}
			steps := int(min(128, max(4, math.Sqrt(length*float64(p.scale))*2)))
			for i := 1; i <= steps; i++ {
				t := float32(i) / float32(steps)
				q := lerpPoint(lerpPoint(origin, a, t), lerpPoint(a, b, t), t)
				if cubic {
					q = lerpPoint(q, lerpPoint(lerpPoint(a, b, t), lerpPoint(b, c, t), t), t)
				}
				line(q)
			}
		}
	}
}

func lerpPoint(a, b Point, t float32) Point {
	return Point{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}
}

func absFloat(n float32) float32 { return max(n, -n) }
