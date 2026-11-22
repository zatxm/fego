//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"bytes"
	"encoding/binary"
	"image"
	"testing"
)

func xCopyPixelsScalar(dst []byte, stride int, src *image.RGBA, r image.Rectangle, bigEndian bool) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		s := src.Pix[src.PixOffset(r.Min.X, y):][:r.Dx()*4]
		d := dst[(y-src.Rect.Min.Y)*stride+(r.Min.X-src.Rect.Min.X)*4:][:len(s)]
		for i := 0; i < len(s); i += 4 {
			if bigEndian {
				d[i], d[i+1], d[i+2], d[i+3] = 255, s[i], s[i+1], s[i+2]
			} else {
				d[i], d[i+1], d[i+2], d[i+3] = s[i+2], s[i+1], s[i], 255
			}
		}
	}
}

func TestX11PixelCopySubimage(t *testing.T) {
	parent := image.NewRGBA(image.Rect(-3, 4, 104, 29))
	for i := range parent.Pix {
		parent.Pix[i] = byte(i*37 + i/7)
	}
	src := parent.SubImage(image.Rect(11, 9, 97, 27)).(*image.RGBA)
	stride := src.Rect.Dx()*4 + 13
	for _, big := range []bool{false, true} {
		for width := 0; width <= src.Rect.Dx(); width++ {
			r := image.Rect(src.Rect.Min.X, src.Rect.Min.Y+1, src.Rect.Min.X+width, src.Rect.Max.Y-1)
			want := bytes.Repeat([]byte{77}, stride*src.Rect.Dy()+7)
			got := bytes.Clone(want)
			xCopyPixelsScalar(want[3:], stride, src, r, big)
			xCopyPixels(got[3:], stride, src, r, big)
			if !bytes.Equal(got, want) {
				t.Fatalf("copy differs: bigEndian=%v width=%d", big, width)
			}
		}
	}
}

func TestX11PixelCopyBitMapping(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	values := []uint64{0, ^uint64(0)}
	for bit := range 64 {
		values = append(values, uint64(1)<<bit)
	}
	for _, value := range values {
		binary.LittleEndian.PutUint64(src.Pix, value)
		for _, big := range []bool{false, true} {
			var got, want [8]byte
			xCopyPixels(got[:], 8, src, src.Rect, big)
			xCopyPixelsScalar(want[:], 8, src, src.Rect, big)
			if got != want {
				t.Fatalf("pixel bit mapping differs: input=%016x bigEndian=%v got=%x want=%x", value, big, got, want)
			}
		}
	}
}

func BenchmarkX11CopyPixels(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for i := range src.Pix {
		src.Pix[i] = byte(i)
	}
	dst := make([]byte, len(src.Pix))
	for _, tc := range []struct {
		name string
		copy func([]byte, int, *image.RGBA, image.Rectangle, bool)
	}{{"scalar", xCopyPixelsScalar}, {"word", xCopyPixels}} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(src.Pix)))
			b.ReportAllocs()
			for b.Loop() {
				tc.copy(dst, src.Stride, src, src.Rect, false)
			}
		})
	}
}

func TestX11UploadCapacityReleasesFullFrame(t *testing.T) {
	full := 800 * 600 * 4
	small := xUploadCapacity(full, 800*28*4)
	if small >= full/4 || small < 800*28*4 {
		t.Fatal("full frame retained", small)
	}
	if got := xUploadCapacity(small, small/2); got != small {
		t.Fatal("small size changes churn allocations", got)
	}
	if got := xUploadCapacity(small, full); got != full {
		t.Fatal("full repaint did not grow", got)
	}
	if got := xUploadCapacity(full, 4); got != 4096 {
		t.Fatal("tiny tile capacity", got)
	}
}

func TestX11UploadCapacityAvoidsAlternatingAllocationChurn(t *testing.T) {
	full := 800 * 600 * 4
	w := &xWindow{imageBytes: full}
	for i := 0; i < 20; i++ {
		if w.uploadCapacity(4096) != full || w.uploadCapacity(full) != full {
			t.Fatal("alternating uploads reallocated")
		}
	}
	for i := 0; i < 7; i++ {
		if w.uploadCapacity(4096) != full {
			t.Fatal("shrank before consecutive small frames")
		}
	}
	if w.uploadCapacity(4096) != 4096 {
		t.Fatal("steady partial updates never released full buffer")
	}
}

func TestX11UploadCapacityReleasesPersistentFormDamage(t *testing.T) {
	w := &xWindow{imageBytes: xUploadBytes}
	needed := 768 * 48 * 4
	for range 7 {
		if got := w.uploadCapacity(needed); got != xUploadBytes {
			t.Fatal("buffer shrank during a transient resize", got)
		}
	}
	w.imageBytes = w.uploadCapacity(needed)
	if w.imageBytes != needed {
		t.Fatal("steady form updates retained the initial buffer", w.imageBytes)
	}
	for range 20 {
		if got := w.uploadCapacity(needed); got != needed {
			t.Fatal("steady form damage churned the upload buffer", got)
		}
	}
}
