package text

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testSystem(t testing.TB) *System {
	t.Helper()
	s := New()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return s
}

func TestMeasureUnicodeAndLines(t *testing.T) {
	s := testSystem(t)
	w, h := s.Measure("Hello, 世界", 16, false)
	if w <= 0 || h < 16 {
		t.Fatalf("label dimensions %v, %v", w, h)
	}
	w1, h1 := s.Measure("café", 16, false)
	w2, h2 := s.Measure("cafe\u0301", 16, false)
	if w1 != w2 || h1 != h2 {
		t.Fatalf("NFC mismatch (%v,%v) (%v,%v)", w1, h1, w2, h2)
	}
	wa, ha := s.Measure("line", 16, false)
	wb, hb := s.Measure("line\r\nline", 16, false)
	if wa != wb || hb != ha*2 {
		t.Fatalf("multiline mismatch (%v,%v) (%v,%v)", wa, ha, wb, hb)
	}
	wt, _ := s.Measure("a\tb", 16, false)
	ws, _ := s.Measure("ab", 16, false)
	if wt <= ws {
		t.Fatal("tab did not advance")
	}
	if w, h := s.Measure("", 16, false); w != 0 || h <= 0 {
		t.Fatalf("empty label dimensions %v,%v", w, h)
	}
	if step := s.LineHeight(16, false); step < 24 {
		t.Fatalf("input line step too short: %v", step)
	}
}

func TestHitRuneOffsetsAndLongLines(t *testing.T) {
	s := testSystem(t)
	for _, text := range []string{"a中😀b", "cafe\u0301", "a\tb"} {
		w, _ := s.Measure(text, 14, false)
		if got := s.Hit(text, 14, false, w+20); got != len([]rune(text)) {
			t.Fatalf("end of %q at %d", text, got)
		}
		if got := s.Hit(text, 14, false, 0); got != 0 {
			t.Fatal("start", got)
		}
	}
	w, _ := s.Measure("A", 14, false)
	before := len(s.layouts)
	if got := s.Hit(strings.Repeat("A", 1<<20), 14, false, w*2.1); got != 2 {
		t.Fatal("long-line hit", got)
	}
	if len(s.layouts) != before {
		t.Fatal("hit testing populated prefix layouts")
	}
}

func TestDrawClipsAndCachesMasks(t *testing.T) {
	s := testSystem(t)
	dst := image.NewRGBA(image.Rect(7, 11, 240, 100))
	white := color.RGBA{255, 255, 255, 255}
	draw.Draw(dst, dst.Bounds(), image.NewUniform(white), image.Point{}, draw.Src)
	clip := image.Rect(30, 20, 160, 65)
	s.Draw(dst, clip, "Hello, 世界", 20, 12, 24, color.RGBA{10, 20, 30, 255}, false)
	changed := 0
	for y := dst.Bounds().Min.Y; y < dst.Bounds().Max.Y; y++ {
		for x := dst.Bounds().Min.X; x < dst.Bounds().Max.X; x++ {
			if dst.RGBAAt(x, y) != white {
				changed++
				if !image.Pt(x, y).In(clip) {
					t.Fatalf("paint escaped clip at %d,%d", x, y)
				}
			}
		}
	}
	if changed < 20 || s.glyphBytes == 0 {
		t.Fatalf("no visible cached text: %d pixels, %d mask bytes", changed, s.glyphBytes)
	}
	before := s.glyphBytes
	s.Draw(dst, clip, "Hello, 世界", 20, 12, 24, color.RGBA{100, 30, 10, 255}, false)
	if s.glyphBytes != before {
		t.Fatal("changing ink regenerated glyph masks")
	}
}

func TestClippedDrawMatchesFullImage(t *testing.T) {
	s := testSystem(t)
	for _, value := range []string{"Italic fj AV 中 café", "\u0301a\ttext\nsecond line", strings.Repeat("abc ", 100)} {
		for _, x := range []float32{-250.5, -20, 12.25, 290} {
			for _, clip := range []image.Rectangle{image.Rect(5, 9, 15, 89), image.Rect(110, 9, 125, 89), image.Rect(5, 22, 125, 31)} {
				full := image.NewRGBA(image.Rect(5, 9, 125, 89))
				part := image.NewRGBA(full.Rect)
				s.Draw(full, full.Rect, value, x, 12.5, 16, color.RGBA{R: 50, A: 200}, false)
				s.Draw(part, clip, value, x, 12.5, 16, color.RGBA{R: 50, A: 200}, false)
				cropped := image.NewRGBA(full.Rect)
				draw.Draw(cropped, clip, full, clip.Min, draw.Src)
				if !bytes.Equal(part.Pix, cropped.Pix) {
					t.Fatalf("clipped label differs x=%v clip=%v text=%q", x, clip, value)
				}
			}
		}
	}
}

func TestChineseSystemFallback(t *testing.T) {
	s := testSystem(t)
	src := s.selectSource('中', false)
	if !s.contains(src, '中') {
		t.Skip("host has no supported system CJK font; portable fallback still tested")
	}
	if src.file == nil {
		t.Fatal("Chinese should resolve to a real system font")
	}
	for _, r := range "中文控件" {
		g := s.glyph(glyphKey{src, fontSize(18), r})
		if g.index == 0 {
			t.Fatalf("%s is missing %c", src.name, r)
		}
		s.rasterize(glyphKey{src, fontSize(18), r}, g)
		if g.mask == nil || len(g.mask.Pix) == 0 {
			t.Fatalf("%c produced no glyph mask", r)
		}
	}
	t.Logf("CJK font: %s", src.name)
}

func TestCacheBudgets(t *testing.T) {
	s := testSystem(t)
	for i := range maxLayouts * 2 {
		s.Measure(fmt.Sprintf("status %d", i), 14, false)
	}
	if len(s.layouts) > maxLayouts || s.layoutBytes > maxLayoutBytes {
		t.Fatalf("layout budget exceeded: %d entries, %d bytes", len(s.layouts), s.layoutBytes)
	}
	for r := rune(0x4e00); r < 0x4e00+maxGlyphs+20; r++ {
		s.Measure(string(r), 16, false)
	}
	if len(s.glyphs) > maxGlyphs || len(s.coverage) > maxGlyphs {
		t.Fatalf("glyph budget exceeded: %d glyphs, %d coverage entries", len(s.glyphs), len(s.coverage))
	}
	for size := float32(1); size < maxFaces+5; size++ {
		s.Measure("M", size, false)
	}
	if len(s.faces) > maxFaces {
		t.Fatalf("face budget exceeded: %d", len(s.faces))
	}
	dst := image.NewRGBA(image.Rect(0, 0, 700, 800))
	for r := rune('!'); r <= '~'; r++ {
		s.Draw(dst, dst.Bounds(), string(r), 0, 0, 512, color.RGBA{A: 255}, false)
	}
	if s.glyphBytes > maxGlyphBytes {
		t.Fatalf("glyph mask budget exceeded: %d", s.glyphBytes)
	}
	// An oversized string is laid out for this call, never retained in the cache.
	long := strings.Repeat("a", maxLayoutBytes)
	s.Measure(long, 14, false)
	if _, retained := s.layouts[layoutKey{long, fontSize(14), false}]; retained {
		t.Fatal("oversized layout retained")
	}
}

func TestASCIIGlyphCacheInvalidates(t *testing.T) {
	s := testSystem(t)
	key := glyphKey{s.regular[0], fontSize(14), 'A'}
	first := s.glyph(key)
	if s.glyph(key) != first {
		t.Fatal("ASCII glyph was not reused")
	}
	other := key
	other.size = fontSize(23)
	if s.glyph(other) == first || s.glyph(key) != first {
		t.Fatal("font-size change reused the wrong glyph")
	}
	other.source = s.regular[1]
	if s.glyph(other) != s.glyphs[other] {
		t.Fatal("font source differs from the bounded glyph cache")
	}
	for r := rune(128); r < 128+maxGlyphs; r++ {
		s.glyph(glyphKey{key.source, key.size, r})
	}
	if s.asciiGlyphs['A'] != nil || s.glyph(key) == first {
		t.Fatal("glyph-count eviction retained a stale ASCII glyph")
	}
	s.glyphBytes = maxGlyphBytes
	g := s.glyph(key)
	s.rasterize(key, g)
	if s.asciiGlyphs['A'] != nil || s.glyphs[key] != g || s.glyph(key) != g {
		t.Fatal("mask-byte eviction left a stale ASCII glyph")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, g := range s.asciiGlyphs {
		if g != nil {
			t.Fatal("closed text system retained a glyph")
		}
	}
}

func TestConcurrentAndClose(t *testing.T) {
	s := New()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			dst := image.NewRGBA(image.Rect(0, 0, 200, 60))
			for range 20 {
				s.Measure("Safe 中文", 16, true)
				s.Draw(dst, dst.Bounds(), "Safe 中文", 0, 0, 16, color.RGBA{A: 255}, true)
			}
		})
	}
	workers.Wait()
	s.Measure("123.45", 16, false)
	files := make([]*os.File, 0, len(s.sources))
	for _, src := range s.sources {
		files = append(files, src.file)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if _, err := file.Stat(); err == nil {
			t.Fatal("font file left open")
		}
	}
	if w, h := s.Measure("closed", 16, false); w != 0 || h != 0 {
		t.Fatalf("closed system measured %v,%v", w, h)
	}
	if s.regular != [2]*source{} || s.cjk != [2]*source{} || s.symbol != nil ||
		!reflect.ValueOf(s.buffer).IsZero() || s.numeric.glyphs != nil {
		t.Fatal("closed system retained font or scratch data")
	}
	for _, glyph := range s.numericGlyphs {
		if glyph.source != nil {
			t.Fatal("closed numeric layout retained a font")
		}
	}
}

func TestInvalidCoordinatesAndSizes(t *testing.T) {
	s := testSystem(t)
	dst := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for _, size := range []float32{-1, 0, 513, float32(math.Inf(1)), float32(math.NaN())} {
		if w, h := s.Measure("x", size, false); w != 0 || h != 0 {
			t.Fatalf("invalid size %v yielded %v,%v", size, w, h)
		}
		s.Draw(dst, dst.Bounds(), "x", 0, 0, size, color.RGBA{A: 255}, false)
	}
	for _, x := range []float32{float32(math.Inf(1)), float32(math.NaN()), math.MaxFloat32} {
		s.Draw(dst, dst.Bounds(), "x", x, 0, 16, color.RGBA{A: 255}, false)
	}
	for _, b := range dst.Pix {
		if b != 0 {
			t.Fatal("invalid geometry painted pixels")
		}
	}
}

func TestCachedOperationsDontAllocate(t *testing.T) {
	s := testSystem(t)
	dst := image.NewRGBA(image.Rect(0, 0, 240, 40))
	render := func() {
		s.Measure("Cached 中文", 14, false)
		s.Draw(dst, dst.Bounds(), "Cached 中文", 0, 0, 14, color.RGBA{A: 255}, false)
	}
	render()
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatalf("cached label allocated %v times", n)
	}
}

func BenchmarkCachedMeasure(b *testing.B) {
	s := testSystem(b)
	s.Measure("Performance 性能", 14, false)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.Measure("Performance 性能", 14, false)
	}
}

func TestNumericLayoutsMatchGeneralShaping(t *testing.T) {
	s := testSystem(t)
	for _, text := range []string{"12345", "-123.45", "+1,234.00%", "12:34:56", "2026/11/10", "12345678901234567890123456789012"} {
		for _, bold := range []bool{false, true} {
			for _, size := range []float32{11, 16, 23.5} {
				n := s.layout(text, fontSize(size), bold)
				positions := append([]positioned(nil), n.glyphs...)
				width, height := n.w, n.h
				reference := s.layout("\n"+text, fontSize(size), bold)
				if width != reference.w || height*2 != reference.h || len(positions) != len(reference.glyphs) {
					t.Fatal("numeric metrics differ", text, size, bold)
				}
				for i, p := range reference.glyphs {
					p.y -= height
					if p != positions[i] {
						t.Fatal("numeric glyph differs", text, i)
					}
				}
			}
		}
	}
	values := []string{"123.45", "-987.65", "0.00"}
	img := image.NewRGBA(image.Rect(0, 0, 160, 40))
	render := func() {
		for _, value := range values {
			s.Measure(value, 14, false)
			s.Draw(img, img.Rect, value, 0, 0, 14, color.RGBA{A: 255}, false)
		}
	}
	render()
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatal("changing numeric text allocated", n)
	}
}

func BenchmarkNumericMeasureDraw(b *testing.B) {
	s := testSystem(b)
	img := image.NewRGBA(image.Rect(0, 0, 80, 24))
	values := []string{"123.45", "-987.65", "0.00"}
	for _, v := range values {
		s.Draw(img, img.Rect, v, 0, 0, 14, color.RGBA{A: 255}, false)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		v := values[i%len(values)]
		i++
		s.Measure(v, 14, false)
		s.Draw(img, img.Rect, v, 0, 0, 14, color.RGBA{A: 255}, false)
	}
}

func BenchmarkCachedDraw(b *testing.B) {
	s := testSystem(b)
	dst := image.NewRGBA(image.Rect(0, 0, 240, 40))
	s.Draw(dst, dst.Bounds(), "Performance 性能", 0, 0, 14, color.RGBA{A: 255}, false)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.Draw(dst, dst.Bounds(), "Performance 性能", 0, 0, 14, color.RGBA{A: 255}, false)
	}
}
