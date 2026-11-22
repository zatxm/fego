//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"image"
	"os"
	"runtime"
	"testing"
)

func BenchmarkX11Present(b *testing.B) {
	if os.Getenv("FEGO_X11_TEST") != "1" {
		b.Skip("set FEGO_X11_TEST=1 on a dedicated X display")
	}
	for _, mode := range []string{"full", "strip"} {
		b.Run(mode, func(b *testing.B) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			native, err := New(nil)
			if err != nil {
				b.Fatal(err)
			}
			backend := native.(*xBackend)
			defer backend.release()
			window, err := backend.Open(Options{Title: "Upload benchmark", Width: 800, Height: 600}, func(Event) {})
			if err != nil {
				b.Fatal(err)
			}
			w := window.(*xWindow)
			defer w.Close()
			img := image.NewRGBA(image.Rect(0, 0, 800, 600))
			for i := 3; i < len(img.Pix); i += 4 {
				img.Pix[i] = 255
			}
			damage := []image.Rectangle{img.Rect}
			if mode == "strip" {
				damage[0].Max.Y = 8
			}
			for range 10 {
				w.Present(img, damage)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				w.Present(img, damage)
			}
			b.StopTimer()
			b.ReportMetric(float64(w.imageBytes), "native-buffer-B")
		})
	}
}
