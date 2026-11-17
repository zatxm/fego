// Package text lays out and paints UTF-8 labels using Go font rasterization.
// It supports kerning, Latin and system CJK fallback. Complex-script shaping,
// bidirectional reordering and color emoji are not implemented.
package text

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

const (
	maxGlyphs      = 4096
	maxGlyphBytes  = 8 << 20
	maxLayouts     = 128
	maxLayoutBytes = 128 << 10
	maxFaces       = 32
)

type source struct {
	font *sfnt.Font
	file *os.File
	name string
}

type faceKey struct {
	source *source
	size   fixed.Int26_6
}

type glyphKey struct {
	source *source
	size   fixed.Int26_6
	r      rune
}

type glyph struct {
	index      sfnt.GlyphIndex
	advance    fixed.Int26_6
	mask       *image.Alpha
	bounds     image.Rectangle
	rasterized bool
}

type coverageKey struct {
	r    rune
	bold bool
}

type layoutKey struct {
	text string
	size fixed.Int26_6
	bold bool
}

type positioned struct {
	source *source
	r      rune
	x, y   int64 // 1/64 pixel units; long documents must not overflow 26.6 positions.
}

type layout struct {
	glyphs []positioned
	w, h   int64
}

// System owns read-only system font handles and bounded reusable caches.
// Its methods may run concurrently; Close releases its font handles.
type System struct {
	mu            sync.Mutex
	closed        bool
	regular       [2]*source
	cjk           [2]*source
	cjkLoaded     [2]bool
	symbol        *source
	symbolLoaded  bool
	sources       []*source
	buffer        sfnt.Buffer
	faces         map[faceKey]font.Face
	glyphs        map[glyphKey]*glyph
	asciiFace     faceKey
	asciiGlyphs   [128]*glyph
	glyphBytes    int
	coverage      map[coverageKey]*source
	layouts       map[layoutKey]*layout
	layoutBytes   int
	numeric       layout
	numericKey    [32]byte
	numericLength int
	numericSize   fixed.Int26_6
	numericBold   bool
	numericGlyphs [32]positioned
	ink           color.RGBA
	paint         image.Uniform
}

var bundled = sync.OnceValue(func() [2]*sfnt.Font {
	regular, err := sfnt.Parse(goregular.TTF)
	if err != nil {
		panic(err) // The embedded font is a compile-time dependency.
	}
	bold, err := sfnt.Parse(gobold.TTF)
	if err != nil {
		panic(err)
	}
	return [2]*sfnt.Font{regular, bold}
})

// New selects the system's sans-serif fonts. CJK and symbol fonts are opened
// only when a label needs them. The bundled Go fonts work without system fonts.
func New() *System {
	s := &System{
		faces:    make(map[faceKey]font.Face),
		glyphs:   make(map[glyphKey]*glyph),
		coverage: make(map[coverageKey]*source),
		layouts:  make(map[layoutKey]*layout),
	}
	s.paint.C = &s.ink
	for i := range 2 {
		s.regular[i] = s.openCandidates(fontCandidates("latin", i == 1))
		if s.regular[i] == nil {
			s.regular[i] = &source{font: bundled()[i], name: "bundled Go"}
		}
	}
	return s
}

// Close releases font files and cached data. Further Measure and Draw calls are no-ops.
func (s *System) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, src := range s.sources {
		if err := src.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	s.sources = nil
	s.faces = nil
	s.glyphs = nil
	clear(s.asciiGlyphs[:])
	s.asciiFace = faceKey{}
	s.layouts = nil
	s.coverage = nil
	s.regular, s.cjk = [2]*source{}, [2]*source{}
	s.symbol = nil
	s.buffer = sfnt.Buffer{}
	s.numeric = layout{}
	clear(s.numericGlyphs[:])
	s.closed = true
	return errors.Join(errs...)
}

// Measure returns the advance width and line height in device pixels.
// Newlines create new lines. Font sizes outside (0, 512] are not rendered.
func (s *System) Measure(text string, size float32, bold bool) (w, h float32) {
	q := fontSize(size)
	if q == 0 {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, 0
	}
	l := s.layout(text, q, bold)
	return float32(l.w) / 64, float32(l.h) / 64
}

// Hit returns a rune offset at the nearest grapheme boundary of a single line.
// It advances once through the text instead of shaping every growing prefix.
func (s *System) Hit(text string, size float32, bold bool, x float32) int {
	q := fontSize(size)
	if q == 0 || x <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0
	}
	var it norm.Iter
	it.InitString(norm.NFC, text)
	position := int64(0)
	offset, byteOffset := 0, 0
	var previous *source
	var previousIndex sfnt.GlyphIndex
	weight := 0
	if bold {
		weight = 1
	}
	for !it.Done() {
		segment := it.Next()
		before := position
		for _, r := range string(segment) {
			if r == '\r' {
				continue
			}
			if r == '\t' {
				space := s.glyph(glyphKey{s.regular[weight], q, ' '}).advance
				if step := int64(space) * 4; step > 0 {
					position = (position/step + 1) * step
				}
				previous = nil
				continue
			}
			src := s.selectSource(r, bold)
			g := s.glyph(glyphKey{src, q, r})
			if previous == src {
				kern, _ := src.font.Kern(&s.buffer, previousIndex, g.index, q, font.HintingFull)
				position += int64(kern)
			}
			position += int64(g.advance)
			previous, previousIndex = src, g.index
		}
		if float64(x)*64 < float64(before+position)/2 {
			return offset
		}
		end := it.Pos()
		offset += utf8.RuneCountInString(text[byteOffset:end])
		byteOffset = end
	}
	return offset
}

// LineHeight is the stable baseline step for separately painted input rows.
// It leaves room for common Latin/CJK faces without loading fallback fonts.
func (s *System) LineHeight(size float32, bold bool) float32 {
	_, height := s.Measure("Ag", size, bold)
	if height == 0 {
		return 0
	}
	return max(height, size*1.5)
}

// Draw draws a label with its line box's top-left at x,y. It never writes
// outside clip or dst.Bounds. Glyph masks are cached independently of color.
func (s *System) Draw(dst *image.RGBA, clip image.Rectangle, text string, x, y, size float32, ink color.RGBA, bold bool) {
	q := fontSize(size)
	clip = clip.Intersect(dst.Bounds())
	if q == 0 || clip.Empty() || ink.A == 0 || !finite(x) || !finite(y) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	l := s.layout(text, q, bold)
	s.ink = ink
	for _, p := range l.glyphs {
		gx := x + float32(p.x)/64
		gy := y + float32(p.y)/64
		// Reject offscreen glyphs before converting unbounded coordinates to int.
		if gx < float32(clip.Min.X)-size*2 || gx > float32(clip.Max.X)+size*2 ||
			gy < float32(clip.Min.Y)-size*2 || gy > float32(clip.Max.Y)+size*2 {
			continue
		}
		key := glyphKey{p.source, q, p.r}
		g := s.glyph(key)
		if !g.rasterized {
			s.rasterize(key, g)
		}
		if g.mask == nil {
			continue
		}
		r := g.bounds.Add(image.Pt(int(math.Round(float64(gx))), int(math.Round(float64(gy)))))
		visible := r.Intersect(clip)
		if !visible.Empty() {
			draw.DrawMask(dst, visible, &s.paint, image.Point{}, g.mask, visible.Min.Sub(r.Min), draw.Over)
		}
	}
}

func finite(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }

func fontSize(size float32) fixed.Int26_6 {
	if !finite(size) || size <= 0 || size > 512 {
		return 0
	}
	return max(1, fixed.Int26_6(math.Round(float64(size)*64)))
}

func (s *System) face(key faceKey) font.Face {
	if f := s.faces[key]; f != nil {
		return f
	}
	if len(s.faces) == maxFaces {
		clear(s.faces)
	}
	f, _ := opentype.NewFace(key.source.font, &opentype.FaceOptions{
		Size: float64(key.size) / 64, DPI: 72, Hinting: font.HintingFull,
	})
	s.faces[key] = f
	return f
}

func (s *System) glyph(key glyphKey) *glyph {
	// UI labels repeatedly reuse a small alphabet at the same font size.
	ascii := key.r >= 0 && key.r < 128
	if ascii {
		face := faceKey{key.source, key.size}
		if s.asciiFace != face {
			s.asciiFace = face
			clear(s.asciiGlyphs[:])
		}
		if g := s.asciiGlyphs[key.r]; g != nil {
			return g
		}
	}
	if g := s.glyphs[key]; g != nil {
		if ascii {
			s.asciiGlyphs[key.r] = g
		}
		return g
	}
	if len(s.glyphs) >= maxGlyphs {
		clear(s.glyphs)
		clear(s.asciiGlyphs[:])
		s.glyphBytes = 0
	}
	index, _ := key.source.font.GlyphIndex(&s.buffer, key.r)
	advance, _ := key.source.font.GlyphAdvance(&s.buffer, index, key.size, font.HintingFull)
	g := &glyph{index: index, advance: advance}
	s.glyphs[key] = g
	if ascii {
		s.asciiGlyphs[key.r] = g
	}
	return g
}

func (s *System) rasterize(key glyphKey, g *glyph) {
	r, mask, at, _, _ := s.face(faceKey{key.source, key.size}).Glyph(fixed.Point26_6{}, key.r)
	g.rasterized = true
	if mask == nil || r.Empty() {
		return
	}
	n := r.Dx() * r.Dy()
	if s.glyphBytes+n > maxGlyphBytes {
		clear(s.glyphs)
		clear(s.asciiGlyphs[:])
		s.glyphBytes = 0
		s.glyphs[key] = g
	}
	g.mask = image.NewAlpha(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(g.mask, g.mask.Bounds(), mask, at, draw.Src)
	g.bounds = r
	s.glyphBytes += len(g.mask.Pix)
}

func (s *System) layout(text string, size fixed.Int26_6, bold bool) *layout {
	key := layoutKey{text, size, bold}
	if len(text) == s.numericLength && size == s.numericSize && bold == s.numericBold && text == string(s.numericKey[:s.numericLength]) {
		return &s.numeric
	}
	if l := s.layouts[key]; l != nil {
		return l
	}
	// NFC makes decomposed Latin accents use the same glyph as precomposed text.
	normal := text
	numeric := numericLabel(text)
	if !numeric && !norm.NFC.IsNormalString(normal) {
		normal = norm.NFC.String(normal)
	}
	weight := 0
	if bold {
		weight = 1
	}
	base := s.face(faceKey{s.regular[weight], size}).Metrics()
	var l *layout
	if numeric {
		s.numeric = layout{glyphs: s.numericGlyphs[:0]}
		l = &s.numeric
	} else {
		l = &layout{}
	}
	x, top := int64(0), int64(0)
	ascent, descent := base.Ascent, base.Descent
	lineStart := 0
	var previous *source
	var prevIndex sfnt.GlyphIndex
	finishLine := func() {
		for i := lineStart; i < len(l.glyphs); i++ {
			l.glyphs[i].y = top + int64(ascent)
		}
		l.w = max(l.w, x)
		top += int64(max(base.Height, ascent+descent))
		x, lineStart = 0, len(l.glyphs)
		ascent, descent = base.Ascent, base.Descent
		previous = nil
	}
	for _, r := range normal {
		switch r {
		case '\r':
			continue
		case '\n', '\u2028', '\u2029':
			finishLine()
			continue
		case '\t':
			space := s.glyph(glyphKey{s.regular[weight], size, ' '}).advance
			if step := int64(space) * 4; step > 0 {
				x = (x/step + 1) * step
			}
			previous = nil
			continue
		}
		src := s.selectSource(r, bold)
		g := s.glyph(glyphKey{src, size, r})
		if previous == src {
			kern, _ := src.font.Kern(&s.buffer, prevIndex, g.index, size, font.HintingFull)
			x += int64(kern)
		}
		metrics := s.face(faceKey{src, size}).Metrics()
		ascent, descent = max(ascent, metrics.Ascent), max(descent, metrics.Descent)
		l.glyphs = append(l.glyphs, positioned{source: src, r: r, x: x})
		x += int64(g.advance)
		previous, prevIndex = src, g.index
	}
	finishLine()
	l.h = top
	if numeric {
		copy(s.numericKey[:], text)
		s.numericLength, s.numericSize, s.numericBold = len(text), size, bold
		return l
	}
	n := len(text) + cap(l.glyphs)*32 + 80
	if n <= maxLayoutBytes {
		if len(s.layouts) >= maxLayouts || s.layoutBytes+n > maxLayoutBytes {
			clear(s.layouts)
			s.layoutBytes = 0
		}
		key.text = strings.Clone(text)
		s.layouts[key] = l
		s.layoutBytes += n
	}
	return l
}

// Rapidly changing counters reuse positions without evicting cached UI labels.
func numericLabel(text string) bool {
	if len(text) == 0 || len(text) > 32 {
		return false
	}
	digit := false
	for i := range len(text) {
		c := text[i]
		if c >= '0' && c <= '9' {
			digit = true
			continue
		}
		switch c {
		case '.', ',', '+', '-', ':', '/', '%', ' ':
		default:
			return false
		}
	}
	return digit
}

func (s *System) selectSource(r rune, bold bool) *source {
	key := coverageKey{r, bold}
	if src := s.coverage[key]; src != nil {
		return src
	}
	weight := 0
	if bold {
		weight = 1
	}
	src := s.regular[weight]
	if !s.contains(src, r) {
		if !s.cjkLoaded[weight] {
			s.cjk[weight] = s.openCandidates(fontCandidates("cjk", bold))
			if bold && s.cjk[weight] == nil {
				if !s.cjkLoaded[0] {
					s.cjk[0] = s.openCandidates(fontCandidates("cjk", false))
					s.cjkLoaded[0] = true
				}
				s.cjk[weight] = s.cjk[0]
			}
			s.cjkLoaded[weight] = true
		}
		if s.contains(s.cjk[weight], r) {
			src = s.cjk[weight]
		} else {
			if !s.symbolLoaded {
				s.symbol = s.openCandidates(fontCandidates("symbols", false))
				s.symbolLoaded = true
			}
			if s.contains(s.symbol, r) {
				src = s.symbol
			}
		}
	}
	if len(s.coverage) >= maxGlyphs {
		clear(s.coverage)
	}
	s.coverage[key] = src
	return src
}

func (s *System) contains(src *source, r rune) bool {
	if src == nil {
		return false
	}
	index, err := src.font.GlyphIndex(&s.buffer, r)
	return err == nil && index != 0
}

type candidate struct {
	path  string
	index int
}

func (s *System) openCandidates(candidates []candidate) *source {
	for _, c := range candidates {
		f, err := os.Open(c.path)
		if err != nil {
			continue
		}
		collection, err := sfnt.ParseCollectionReaderAt(f)
		if err != nil || c.index >= collection.NumFonts() {
			f.Close()
			continue
		}
		parsed, err := collection.Font(c.index)
		if err != nil {
			f.Close()
			continue
		}
		src := &source{font: parsed, file: f, name: c.path}
		s.sources = append(s.sources, src)
		return src
	}
	return nil
}

func fontCandidates(kind string, bold bool) []candidate {
	suffix := "Regular"
	if bold {
		suffix = "Bold"
	}
	switch runtime.GOOS {
	case "windows":
		root := os.Getenv("WINDIR")
		if root == "" {
			root = `C:\Windows`
		}
		root = filepath.Join(root, "Fonts")
		switch kind {
		case "latin":
			name := "segoeui.ttf"
			if bold {
				name = "segoeuib.ttf"
			}
			return []candidate{{filepath.Join(root, name), 0}}
		case "cjk":
			name := "msyh.ttc"
			if bold {
				name = "msyhbd.ttc"
			}
			return []candidate{{filepath.Join(root, name), 0}, {filepath.Join(root, "simsun.ttc"), 0}}
		case "symbols":
			return []candidate{{filepath.Join(root, "seguisym.ttf"), 0}}
		}
	case "darwin":
		switch kind {
		case "latin":
			name := "Arial.ttf"
			if bold {
				name = "Arial Bold.ttf"
			}
			return []candidate{{"/System/Library/Fonts/Supplemental/" + name, 0}}
		case "cjk":
			return []candidate{{"/System/Library/Fonts/PingFang.ttc", 0}, {"/System/Library/Fonts/STHeiti Light.ttc", 0}}
		case "symbols":
			return []candidate{{"/System/Library/Fonts/Apple Symbols.ttf", 0}}
		}
	default:
		switch kind {
		case "latin":
			dejavu := "DejaVuSans.ttf"
			if bold {
				dejavu = "DejaVuSans-Bold.ttf"
			}
			return []candidate{
				{"/usr/share/fonts/truetype/noto/NotoSans-" + suffix + ".ttf", 0},
				{"/usr/share/fonts/noto/NotoSans-" + suffix + ".ttf", 0},
				{"/usr/share/fonts/truetype/dejavu/" + dejavu, 0},
				{"/usr/share/fonts/dejavu-sans-fonts/" + dejavu, 0},
			}
		case "cjk":
			return []candidate{
				{"/usr/share/fonts/opentype/noto/NotoSansCJK-" + suffix + ".ttc", 2},
				{"/usr/share/fonts/noto-cjk/NotoSansCJK-" + suffix + ".ttc", 2},
				{"/usr/share/fonts/google-noto-cjk/NotoSansCJK-" + suffix + ".ttc", 2},
				{"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc", 0},
				{"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf", 0},
			}
		case "symbols":
			return []candidate{{"/usr/share/fonts/truetype/noto/NotoSansSymbols2-Regular.ttf", 0}}
		}
	}
	return nil
}
