//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"bufio"
	"bytes"
	"context"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestX11ABI(t *testing.T) {
	if unsafe.Sizeof(xEvent{}) != 192 || unsafe.Sizeof(xAny{}) != 40 || unsafe.Sizeof(xKeyEvent{}) != 96 || unsafe.Sizeof(xConfigureEvent{}) != 88 || unsafe.Sizeof(xImage{}) != 136 {
		t.Fatal("Xlib ABI struct sizes do not match 64-bit X11")
	}
	if unsafe.Offsetof(xKeyEvent{}.State) != 80 || unsafe.Offsetof(xClientEvent{}.Data) != 56 || unsafe.Offsetof(xImage{}.Red) != 56 || unsafe.Offsetof(xImage{}.BytesPerLine) != 44 {
		t.Fatal("Xlib ABI field offsets do not match 64-bit X11")
	}
}

func TestX11ImageBounds(t *testing.T) {
	for _, size := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {32768, 1}, {32767, 32767}, {math.MaxInt, 2}} {
		if xDimensions(size[0], size[1]) {
			t.Errorf("accepted dimensions %v", size)
		}
	}
	if !xDimensions(7680, 4320) {
		t.Fatal("8K window rejected")
	}
	for _, src := range []*image.RGBA{
		{Rect: image.Rect(0, 0, 2, 3), Stride: math.MaxInt, Pix: make([]byte, 8)},
		{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 15)},
		{Rect: image.Rect(0, 0, 2, 2), Stride: 7, Pix: make([]byte, 16)},
	} {
		if xValidRGBA(src) {
			t.Fatal("accepted invalid/overflowing pixel storage")
		}
	}
	if !xValidRGBA(image.NewRGBA(image.Rect(3, 4, 9, 10))) {
		t.Fatal("valid offset image rejected")
	}
}

func TestX11InitialDeviceSize(t *testing.T) {
	if os.Getenv("FEGO_X11_TEST") != "1" {
		t.Skip("set FEGO_X11_TEST=1 on a dedicated X display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t.Setenv("GDK_SCALE", "1.5")
	native, err := New(func() {})
	if err != nil {
		t.Fatal(err)
	}
	b := native.(*xBackend)
	defer b.release()
	var initial Event
	_, err = b.Open(Options{Title: "Initial device size", Width: 320, Height: 200}, func(event Event) { initial = event })
	if err != nil {
		t.Fatal(err)
	}
	if initial.Kind != Resize || initial.Width != 480 || initial.Height != 300 || initial.Scale != 1.5 {
		t.Fatal("first frame would use logical instead of device dimensions", initial)
	}
}

// Without FEGO_GL_TEST the display must lack DRI3 (e.g. Xvfb) so the start fails.
func TestX11DeferredGPUStart(t *testing.T) {
	if os.Getenv("FEGO_X11_TEST") != "1" {
		t.Skip("set FEGO_X11_TEST=1 on a dedicated X display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	native, err := New(func() {})
	if err != nil {
		t.Fatal(err)
	}
	b := native.(*xBackend)
	defer b.release()
	paints := 0
	window, err := b.Open(Options{Title: "Deferred GPU", Width: 64, Height: 48}, func(e Event) {
		if e.Kind == Paint {
			paints++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	w := window.(*xWindow)
	if w.SetGPUEnabled(true) || w.GPUActive() || b.eglPending {
		t.Fatal("requesting the GPU loaded the driver on the UI thread")
	}
	w.StartGPU()
	if !b.eglPending {
		t.Fatal("StartGPU did not begin in the background")
	}
	// The event loop finishes the start after the worker wakes it.
	for deadline := time.Now().Add(10 * time.Second); b.eglPending; {
		if time.Now().After(deadline) {
			t.Fatal("EGL start did not finish")
		}
		if err := b.wait(10 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("FEGO_GL_TEST") == "1" {
		if !w.GPUActive() || paints != 1 {
			t.Fatalf("active=%v paints=%d after the background start", w.GPUActive(), paints)
		}
		if !w.SetGPUEnabled(false) || w.GPUActive() {
			t.Error("disabling a started GPU failed")
		}
		return
	}
	if w.GPUActive() || !w.gpuFailed || paints != 0 {
		t.Fatal("failed start did not keep the CPU presenter")
	}
	if w.AwaitGPU() || b.eglPending {
		t.Error("failed start was retried without re-enabling")
	}
	if !w.SetGPUEnabled(false) || w.SetGPUEnabled(true) || w.gpuFailed || w.AwaitGPU() || !w.gpuFailed {
		t.Error("re-enabling did not retry the start")
	}
}

func TestX11CoalescesResizes(t *testing.T) {
	if os.Getenv("FEGO_X11_TEST") != "1" {
		t.Skip("set FEGO_X11_TEST=1 on a dedicated X display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	lib, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	var resize func(uintptr, uintptr, uint32, uint32) int32
	var syncX func(uintptr, int32) int32
	purego.RegisterLibFunc(&resize, lib, "XResizeWindow")
	purego.RegisterLibFunc(&syncX, lib, "XSync")
	for _, repaints := range []bool{true, false} {
		native, err := New(func() {})
		if err != nil {
			t.Fatal(err)
		}
		b := native.(*xBackend)
		var w *xWindow
		var resizes []Event
		var paints []image.Rectangle
		window, err := b.Open(Options{Title: "Coalesced resize", Width: 120, Height: 80}, func(e Event) {
			switch e.Kind {
			case Resize:
				resizes = append(resizes, e)
				if repaints && w != nil {
					w.PresentRegion(image.NewRGBA(image.Rect(0, 0, e.Width, e.Height)))
				}
			case Paint:
				paints = append(paints, image.Rect(int(e.X), int(e.Y), int(e.X)+e.Width, int(e.Y)+e.Height))
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		w = window.(*xWindow)
		pump := func() {
			syncX(b.display, 0)
			for b.pending() {
				var e xEvent
				xl.nextEvent(b.display, &e)
				b.event(&e)
			}
			b.emitResizes()
		}
		for deadline := time.Now().Add(5 * time.Second); !w.configured; pump() {
			if time.Now().After(deadline) {
				t.Fatal("window was not mapped")
			}
		}
		if len(resizes) != 2 || (len(paints) == 0) == !repaints {
			t.Fatalf("repaints=%v: first map sent %d Resize and paints %v", repaints, len(resizes), paints)
		}
		resizes, paints = nil, nil
		for _, size := range [][2]uint32{{130, 90}, {150, 110}, {140, 120}} {
			resize(b.display, w.id, size[0], size[1])
		}
		pump()
		if len(resizes) != 1 || resizes[0].Width != 140 || resizes[0].Height != 120 {
			t.Fatalf("queued resizes were not coalesced to the last size: %v", resizes)
		}
		if repaints && len(paints) != 0 {
			t.Errorf("exposure already repainted on Resize was painted again: %v", paints)
		}
		if !repaints && (len(paints) != 1 || !image.Rect(120, 0, 140, 120).In(paints[0])) {
			t.Errorf("exposed area not reported after Resize: %v", paints)
		}
		b.release()
	}
}

func TestX11DamageCopy(t *testing.T) {
	src := image.NewRGBA(image.Rect(10, 20, 13, 23))
	src.SetRGBA(11, 21, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	for _, big := range []bool{false, true} {
		dst := bytes.Repeat([]byte{77}, 3*16)
		xCopyPixels(dst, 16, src, image.Rect(11, 21, 12, 22), big)
		want := []byte{30, 20, 10, 255}
		if big {
			want = []byte{255, 10, 20, 30}
		}
		if !bytes.Equal(dst[20:24], want) {
			t.Fatalf("pixel=%v want %v", dst[20:24], want)
		}
		for i, b := range dst {
			if (i < 20 || i >= 24) && b != 77 {
				t.Fatalf("damage copy changed byte %d outside damage", i)
			}
		}
	}
}

func TestX11ScaleAndKeys(t *testing.T) {
	for _, tc := range []struct {
		resource, override string
		want               float32
	}{{"Xft.dpi:\t144", "", 1.5}, {"Xft.dpi: NaN", "", 1}, {"Xft.dpi: 96", "2", 2}, {"", "NaN", 1}, {"Xft.dpi: 100000", "", 1}} {
		if got := xScale(tc.resource, tc.override); got != tc.want {
			t.Errorf("scale %v want %v", got, tc.want)
		}
	}
	for key, want := range map[uintptr]string{0xff09: "Tab", 0xff51: "ArrowLeft", 0xffc0: "F3", 'A': "a", 0x01004e2d: "中", 0xffe5: ""} {
		if got := xKeyName(key); got != want {
			t.Errorf("key %#x = %q want %q", key, got, want)
		}
	}
}

// FEGO_X11_TEST=1 DISPLAY=:... go test ./internal/platform -run TestX11Native -v
var raceEnabled bool

func TestX11Native(t *testing.T) {
	if os.Getenv("FEGO_X11_TEST") != "1" {
		t.Skip("set FEGO_X11_TEST=1 on a dedicated X display")
	}
	for _, shm := range []bool{true, false} {
		t.Run(map[bool]string{true: "shm", false: "socket"}[shm], func(t *testing.T) {
			xShmDisabled = !shm
			defer func() { xShmDisabled = false }()
			testX11Native(t, shm)
		})
	}
}

func testX11Native(t *testing.T, shm bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var b *xBackend
	var w *xWindow
	ready := make(chan struct{}, 1)
	jobs := make(chan func(), 4)
	native, err := New(func() {
		for {
			select {
			case job := <-jobs:
				job()
			default:
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	b = native.(*xBackend)
	defer b.release()
	events := map[EventKind]int{}
	var lastPaint Event
	window, err := b.Open(Options{Title: "Fego native test", Width: 160, Height: 100}, func(e Event) {
		events[e.Kind]++
		if e.Kind == Paint {
			lastPaint = e
		}
		if e.Kind == Paint {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	w = window.(*xWindow)
	lib, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	var syncX func(uintptr, int32) int32
	var getImage func(uintptr, uintptr, int32, int32, uint32, uint32, uintptr, int32) *xImage
	var getPixel func(*xImage, int32, int32) uintptr
	var keycode func(uintptr, uintptr) uint8
	var focus func(uintptr, uintptr, int32, uintptr) int32
	purego.RegisterLibFunc(&syncX, lib, "XSync")
	purego.RegisterLibFunc(&getImage, lib, "XGetImage")
	purego.RegisterLibFunc(&getPixel, lib, "XGetPixel")
	purego.RegisterLibFunc(&keycode, lib, "XKeysymToKeycode")
	purego.RegisterLibFunc(&focus, lib, "XSetInputFocus")
	go func() {
		<-ready
		jobs <- func() {
			img := image.NewRGBA(image.Rect(0, 0, 160, 100))
			for y := 0; y < 100; y++ {
				for x := 0; x < 160; x++ {
					img.SetRGBA(x, y, color.RGBA{R: 200, G: 30, B: 40, A: 255})
				}
			}
			w.Present(img, []image.Rectangle{img.Bounds()})
			if shm != (w.shm != nil) && (!shm || b.shmAvailable()) {
				t.Errorf("shared-memory upload active=%v, requested %v", w.shm != nil, shm)
			}
			if w.shm != nil {
				if n := testing.AllocsPerRun(25, func() { w.shmPut(w.shm, 0, 0, 1, 1) }); n != 0 {
					t.Errorf("XShmPutImage argument conversion allocated %v times", n)
				}
			} else if n := testing.AllocsPerRun(25, func() { w.putImage(0, 0, 1, 1) }); n != 0 {
				t.Errorf("XPutImage argument conversion allocated %v times", n)
			}
			damage := []image.Rectangle{img.Bounds()}
			// Shared-memory waits make several pooled purego calls per present.
			pooled := raceEnabled && w.shm != nil
			if n := testing.AllocsPerRun(25, func() { w.Present(img, damage) }); n != 0 && !pooled {
				t.Errorf("steady native Present allocated %v times", n)
			}
			fullCapacity := w.imageBytes
			img.SetRGBA(3, 4, color.RGBA{R: 1, G: 2, B: 240, A: 255})
			img.SetRGBA(4, 4, color.RGBA{R: 255, A: 255})
			w.Present(img, []image.Rectangle{image.Rect(3, 4, 4, 5)})
			for i := 0; i < 7; i++ {
				w.Present(img, []image.Rectangle{image.Rect(3, 4, 4, 5)})
			}
			if w.imageBytes >= fullCapacity {
				t.Error("partial upload retained full-window conversion buffer")
			}
			syncX(b.display, 0)
			actual := getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
			if actual == nil {
				t.Error("XGetImage failed")
			} else {
				if got := getPixel(actual, 3, 4) & 0xffffff; got != 0x0102f0 {
					t.Errorf("damaged pixel=%#x", got)
				}
				if got := getPixel(actual, 4, 4) & 0xffffff; got != 0xc81e28 {
					t.Errorf("pixel outside damage=%#x", got)
				}
				xl.destroyImage(actual)
			}
			if !w.ScrollPixels(img.Rect, image.Pt(0, 8)) {
				t.Error("native scroll copy rejected")
			}
			top := image.Rect(0, 0, 160, 8)
			for y := 0; y < 8; y++ {
				for x := 0; x < 160; x++ {
					img.SetRGBA(x, y, color.RGBA{G: 200, A: 255})
				}
			}
			w.Present(img, []image.Rectangle{top})
			syncX(b.display, 0)
			actual = getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
			if actual == nil {
				t.Error("scroll readback failed")
			} else {
				if got := getPixel(actual, 3, 12) & 0xffffff; got != 0x0102f0 {
					t.Errorf("copied scroll pixel=%#x", got)
				}
				if got := getPixel(actual, 3, 4) & 0xffffff; got != 0x00c800 {
					t.Errorf("exposed strip pixel=%#x", got)
				}
				xl.destroyImage(actual)
			}
			before := events[Paint]
			var expose xEvent
			*(*xAny)(unsafe.Pointer(&expose)) = xAny{Kind: xGraphicsExpose, Display: b.display, Window: w.id}
			b.event(&expose)
			if events[Paint] != before+1 {
				t.Error("lost scroll source did not request repaint")
			}
			// An Expose series becomes one Paint carrying the union of lost areas.
			for i, r := range []image.Rectangle{image.Rect(5, 6, 15, 26), image.Rect(40, 2, 44, 9)} {
				expose = xEvent{}
				*(*xAny)(unsafe.Pointer(&expose)) = xAny{Kind: xExpose, Display: b.display, Window: w.id}
				expose[5] = uint64(uint32(r.Min.X)) | uint64(uint32(r.Min.Y))<<32
				expose[6] = uint64(uint32(r.Dx())) | uint64(uint32(r.Dy()))<<32
				expose[7] = uint64(1 - i)
				b.event(&expose)
			}
			if events[Paint] != before+2 || lastPaint.X != 5 || lastPaint.Y != 2 || lastPaint.Width != 39 || lastPaint.Height != 24 {
				t.Errorf("expose series paint=%+v", lastPaint)
			}
			// A cropped image with a nonzero origin still targets client coordinates.
			parent := image.NewRGBA(image.Rect(10, 20, 174, 124))
			cropped := parent.SubImage(image.Rect(12, 22, 172, 122)).(*image.RGBA)
			cropped.SetRGBA(30, 40, color.RGBA{R: 45, G: 67, B: 89, A: 255})
			w.Present(cropped, []image.Rectangle{image.Rect(30, 40, 31, 41)})
			syncX(b.display, 0)
			actual = getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
			if actual == nil {
				t.Error("cropped upload readback failed")
			} else {
				if got := getPixel(actual, 18, 18) & 0xffffff; got != 0x2d4359 {
					t.Errorf("cropped pixel=%#x", got)
				}
				xl.destroyImage(actual)
			}
			// Region bounds are window coordinates, unlike a full surface's origin.
			region := image.NewRGBA(image.Rect(50, 60, 53, 62))
			region.SetRGBA(51, 61, color.RGBA{R: 98, G: 76, B: 54, A: 255})
			w.PresentRegion(region)
			if n := testing.AllocsPerRun(25, func() { w.PresentRegion(region) }); n != 0 && !pooled {
				t.Errorf("PresentRegion allocated %v times", n)
			}
			syncX(b.display, 0)
			actual = getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
			if actual == nil {
				t.Error("region readback failed")
			} else {
				if got := getPixel(actual, 51, 61) & 0xffffff; got != 0x624c36 {
					t.Errorf("region pixel=%#x", got)
				}
				xl.destroyImage(actual)
			}
			if !shm {
				w.preferXRender = true
				if w.SetGPUEnabled(true) {
					if !w.GPUActive() || w.graphics.renderer != nil {
						t.Error("XRender should retain regions")
					}
					w.Present(img, []image.Rectangle{img.Rect})
					w.PresentRegion(region)
					actual = getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
					if actual == nil {
						t.Error("XRender readback failed")
					} else {
						if getPixel(actual, 51, 61)&0xffffff != 0x624c36 || getPixel(actual, 54, 61)&0xffffff != 0xc81e28 {
							t.Error("XRender lost a partial update or surrounding pixels")
						}
						xl.destroyImage(actual)
					}
					if !w.SetGPUEnabled(false) || w.GPUActive() || w.render.pix != 0 {
						t.Error("XRender resources retained after switching to CPU")
					}
					t.Log("XRender damage readback and live renderer switch verified")
				} else if w.GPUActive() || w.graphics.renderer != nil {
					t.Error("XRender fallback did not retain the CPU presenter")
				}
				w.preferXRender = false
				if os.Getenv("FEGO_GL_TEST") == "1" {
					w.SetGPUEnabled(true)
					if !w.AwaitGPU() || !w.GPUActive() {
						t.Error("hardware OpenGL did not initialize")
					} else {
						func() {
							if !w.PresentGPURegions(img.Rect.Size(), func(upload func(*image.RGBA)) {
								// Mix tightly packed and strided bands in the same frame.
								upload(img.SubImage(image.Rect(0, 0, 160, 53)).(*image.RGBA))
								upload(img.SubImage(image.Rect(0, 53, 80, 100)).(*image.RGBA))
								upload(img.SubImage(image.Rect(80, 53, 160, 100)).(*image.RGBA))
							}) || !w.PresentGPURegions(img.Rect.Size(), func(upload func(*image.RGBA)) { upload(region) }) {
								t.Error("OpenGL region upload or swap failed")
							}
							// Presents use EGL's own connection; wait for them before reading back.
							syncX(b.eglXDisplay, 0)
							actual := getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
							if actual == nil {
								t.Error("OpenGL readback failed")
							} else {
								if getPixel(actual, 51, 61)&0xffffff != 0x624c36 || getPixel(actual, 54, 61)&0xffffff != 0xc81e28 {
									t.Errorf("OpenGL region or retained background mismatch: %#x, %#x", getPixel(actual, 51, 61), getPixel(actual, 54, 61))
								}
								xl.destroyImage(actual)
							}
							// After a custom effect, resetting the default must reveal the
							// original retained texture even with no new uploads.
							for _, tc := range []struct {
								body  string
								pixel uintptr
							}{
								{"void main() { fragmentColor=vec4(1,0,1,1); }", 0xff00ff}, {"", 0x624c36},
							} {
								if err := w.SetFragmentShader(tc.body); err != nil || !w.PresentGPURegions(img.Rect.Size(), func(func(*image.RGBA)) {}) {
									t.Errorf("present shader switch: %v", err)
									return
								}
								syncX(b.eglXDisplay, 0)
								read := getImage(b.display, w.id, 51, 61, 1, 1, ^uintptr(0), 2)
								if read == nil {
									t.Error("read switched shader")
									return
								}
								pixel := getPixel(read, 0, 0) & 0xffffff
								xl.destroyImage(read)
								if pixel != tc.pixel {
									t.Errorf("switched shader pixel %#x, want %#x", pixel, tc.pixel)
									return
								}
							}
							// Scrolling copies retained texture pixels; only the frame is swapped again.
							for _, step := range []struct{ dy, from, to int32 }{{-7, 61, 54}, {7, 54, 61}} {
								if !w.ScrollPixels(image.Rect(0, 10, 160, 90), image.Pt(0, int(step.dy))) ||
									!w.PresentGPURegions(img.Rect.Size(), func(func(*image.RGBA)) {}) {
									t.Error("OpenGL scroll copy rejected")
									break
								}
								syncX(b.eglXDisplay, 0)
								actual := getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
								if actual == nil {
									t.Error("OpenGL scroll readback failed")
									break
								}
								moved, vacated, outside := getPixel(actual, 51, step.to)&0xffffff, getPixel(actual, 51, step.from)&0xffffff, getPixel(actual, 51, 5)&0xffffff
								if moved != 0x624c36 || vacated != 0xc81e28 || outside != 0x00c800 {
									t.Errorf("OpenGL scroll %d moved %#x vacated %#x outside %#x", step.dy, moved, vacated, outside)
								}
								xl.destroyImage(actual)
							}
							checkGLScrollResize(t, w, img)
							// Exercise both roles of the full-size scroll textures. The newly
							// exposed edge keeps its old pixels until the caller uploads it.
							gradient := image.NewRGBA(img.Rect)
							for y := range gradient.Rect.Dy() {
								for x := range gradient.Rect.Dx() {
									gradient.SetRGBA(x, y, color.RGBA{R: uint8(y), G: uint8(x), A: 255})
								}
							}
							for _, dy := range []int{-9, 12} {
								if !w.PresentGPURegions(img.Rect.Size(), func(upload func(*image.RGBA)) { upload(gradient) }) ||
									!w.ScrollPixels(img.Rect, image.Pt(0, dy)) || !w.PresentGPURegions(img.Rect.Size(), func(func(*image.RGBA)) {}) {
									t.Error("full-window scroll")
									return
								}
								syncX(b.eglXDisplay, 0)
								read := getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
								if read == nil {
									t.Error("read full-window scroll")
									return
								}
								for y := 0; y < img.Rect.Dy(); y++ {
									sourceY := y - dy
									if sourceY < 0 || sourceY >= img.Rect.Dy() {
										sourceY = y
									}
									if got := getPixel(read, 37, int32(y)) & 0xffffff; got != uintptr(sourceY<<16|37<<8) {
										t.Errorf("scroll %d row %d pixel %#x", dy, y, got)
										break
									}
								}
								xl.destroyImage(read)
							}
						}()
						w.SetGPUEnabled(false)
					}
				}
			}
			if w.surfaceSize != (image.Point{}) {
				t.Error("region upload left a full-surface size for partial presents")
			}
			w.Present(img, []image.Rectangle{img.Rect})
			if w.imageBytes < 160*100*4 {
				t.Error("full repaint failed to regrow upload buffer")
			}
			now := time.Now()
			w.uploadIdleAt = now.Add(xUploadIdleTime)
			if next := b.reclaimUploads(now); next != xUploadIdleTime || w.imageBytes == 0 {
				t.Error("large upload buffer released before the idle deadline")
			}
			if next := b.reclaimUploads(now.Add(xUploadIdleTime)); next >= 0 || w.imageBytes != 0 || w.img != nil || w.shm != nil {
				t.Error("idle upload buffer was retained")
			}

			tall, wide := image.Rect(20, 10, 23, 90), image.Rect(10, 30, 150, 32)
			for y := tall.Min.Y; y < tall.Max.Y; y++ {
				for x := tall.Min.X; x < tall.Max.X; x++ {
					img.SetRGBA(x, y, color.RGBA{R: 11, G: 22, B: 33, A: 255})
				}
			}
			for y := wide.Min.Y; y < wide.Max.Y; y++ {
				for x := wide.Min.X; x < wide.Max.X; x++ {
					img.SetRGBA(x, y, color.RGBA{R: 44, G: 55, B: 66, A: 255})
				}
			}
			w.Present(img, []image.Rectangle{tall, wide})
			for i := 0; i < 7; i++ {
				w.Present(img, []image.Rectangle{tall, wide})
			}
			if w.imageBytes != 4096 {
				t.Error("mixed tile orientations expanded staging buffer", w.imageBytes)
			}
			syncX(b.display, 0)
			actual = getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
			if actual == nil {
				t.Error("mixed tile readback failed")
			} else {
				for point, want := range map[image.Point]uintptr{image.Pt(21, 60): 0x0b1621, image.Pt(100, 31): 0x2c3742, image.Pt(159, 99): 0xc81e28} {
					if got := getPixel(actual, int32(point.X), int32(point.Y)) & 0xffffff; got != want {
						t.Errorf("tile %v=%#x want %#x", point, got, want)
					}
				}
				xl.destroyImage(actual)
			}
			largeParent := image.NewRGBA(image.Rect(7, 11, 16014, 115))
			large := largeParent.SubImage(image.Rect(9, 13, 16010, 113)).(*image.RGBA)
			for y := 0; y < large.Rect.Dy(); y++ {
				for x := 0; x < large.Rect.Dx(); x++ {
					i := large.PixOffset(large.Rect.Min.X+x, large.Rect.Min.Y+y)
					large.Pix[i], large.Pix[i+1], large.Pix[i+2], large.Pix[i+3] = uint8(y), uint8(x), 89, 255
				}
			}
			w.Present(large, []image.Rectangle{large.Rect.Inset(-5)})
			if w.imageBytes > xUploadBytes {
				t.Error("upload conversion buffer exceeded its budget", w.imageBytes)
			}
			syncX(b.display, 0)
			actual = getImage(b.display, w.id, 0, 0, 160, 100, ^uintptr(0), 2)
			if actual == nil {
				t.Error("split upload readback failed")
			} else {
				for _, y := range []int32{0, 7, 8, 15, 16, 31, 32, 79, 80, 99} {
					want := uintptr(y)<<16 | 12<<8 | 89
					if got := getPixel(actual, 12, y) & 0xffffff; got != want {
						t.Errorf("split upload boundary %d: %#x want %#x", y, got, want)
					}
				}
				xl.destroyImage(actual)
			}
			w.SetClipboard("中文 clipboard 🙂")
			if got := w.Clipboard(); got != "中文 clipboard 🙂" {
				t.Errorf("clipboard=%q", got)
			}
			if w.ic != 0 {
				t.Error("window allocated an input context before text input was enabled")
			}
			caret := image.Rect(10, 10, 11, 28)
			w.TextInput(true, caret)
			im, ic := b.im, w.ic
			w.TextInput(false, image.Rectangle{})
			w.TextInput(true, caret)
			if b.im != im || w.ic != ic {
				t.Error("refocusing text input did not reuse its input context")
			}
			focus(b.display, w.id, 1, 0)
			for _, kind := range []int32{xButtonPress, xButtonRelease, xMotion, xKeyPress, xKeyRelease} {
				var raw xEvent
				e := (*xKeyEvent)(unsafe.Pointer(&raw))
				*e = xKeyEvent{xAny: xAny{Kind: kind, Display: b.display, Window: w.id}, Root: b.root, X: 20, Y: 30, Code: 1, SameScreen: 1}
				mask := int64(1 << 6)
				if kind == xButtonPress {
					mask = 4
				}
				if kind == xButtonRelease {
					mask = 8
				}
				if kind == xKeyPress || kind == xKeyRelease {
					e.Code = uint32(keycode(b.display, 'q'))
					mask = 1
					if kind == xKeyRelease {
						mask = 2
					}
				}
				xl.sendEvent(b.display, w.id, 0, mask, &raw)
			}
			w.SetCursor("text")
			w.SetSize(180, 120)
			// Xwayland's window manager can deliver ConfigureNotify after XSync.
			deadline := time.Now().Add(time.Second)
			for {
				for xl.pending(b.display) > 0 {
					var e xEvent
					xl.nextEvent(b.display, &e)
					b.event(&e)
				}
				remaining := time.Until(deadline)
				if w.width == 180 && w.height == 120 || remaining <= 0 {
					break
				}
				if err := b.wait(remaining); err != nil {
					t.Error(err)
					break
				}
			}
			if w.width != 180 || w.height != 120 {
				t.Errorf("native resize=%dx%d", w.width, w.height)
			}
			if w.shm != nil && w.shm.pending != [3]int{} {
				t.Errorf("shared-memory completions not consumed: %v", w.shm.pending)
			}
			if shm && b.shmAvailable() {
				// A server that cannot attach (remote, other IPC namespace) must not stop the app.
				info := (*xShmInfo)(unsafe.Pointer(xl.calloc(unsafe.Sizeof(xShmInfo{}), 1)))
				info.ID = -1
				if b.shmAttach(info) || b.err != nil {
					t.Errorf("failed attach accepted or fatal: %v", b.err)
				}
				xl.free(unsafe.Pointer(info))
			}
			w.Close()
			w.Close()
			b.Quit()
		}
		b.Wake()
	}()
	if err := b.Run(); err != nil {
		t.Fatal(err)
	}
	if events[Paint] == 0 || events[Resize] == 0 {
		t.Fatalf("missing native paint/resize events: %v", events)
	}
	for _, kind := range []EventKind{PointerDown, PointerUp, PointerMove, KeyDown, KeyUp, Text, Focus} {
		if events[kind] == 0 {
			t.Errorf("native event %d not delivered: %v", kind, events)
		}
	}
	if len(b.windows) != 0 || w.img != nil || w.shm != nil || w.imageBytes != 0 || w.gc != 0 || w.ic != 0 || w.access != nil {
		t.Fatal("native resources retained after close")
	}
	b.Wake()
}

func TestX11ClipboardAcknowledgesINCRWithOneDelete(t *testing.T) {
	get, remove := xl.getProperty, xl.deleteProperty
	t.Cleanup(func() { xl.getProperty, xl.deleteProperty = get, remove })
	b := &xBackend{incr: 1, transfer: &xClipboardRead{window: &xWindow{}}}
	xl.getProperty = func(_, _, _ uintptr, _, _ int64, deleteAfterRead int32, _ uintptr, kind *uintptr, format *int32, count, _ *uint64, _ **byte) int32 {
		if deleteAfterRead != 1 {
			t.Error("reading the INCR header did not acknowledge it")
		}
		*kind, *format, *count = b.incr, 32, 1
		return 0
	}
	xl.deleteProperty = func(_, _, _ uintptr) int32 {
		t.Error("deleting again can discard the first data chunk")
		return 0
	}

	b.readClipboardProperty()
	if !b.transfer.incremental || b.transfer.done || b.transfer.failed {
		t.Fatal("INCR transfer did not start")
	}
}

func TestX11ClipboardInterop(t *testing.T) {
	path := os.Getenv("FEGO_XCLIP")
	if os.Getenv("FEGO_X11_TEST") != "1" || path == "" {
		t.Skip("set FEGO_X11_TEST=1 and FEGO_XCLIP on a dedicated X display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	jobs := make(chan func(), 1)
	native, err := New(func() {
		select {
		case f := <-jobs:
			f()
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	b := native.(*xBackend)
	defer b.release()
	window, err := b.Open(Options{Title: "Fego clipboard test", Width: 64, Height: 64}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	w := window.(*xWindow)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	text := strings.Repeat("中文 clipboard 🙂\n", 30000)
	owner := exec.CommandContext(ctx, path, "-selection", "clipboard", "-in", "-loops", "1", "-verbose")
	owner.Stdin = strings.NewReader(text)
	stderr, err := owner.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer owner.Process.Kill()
	scanner := bufio.NewScanner(stderr)
	ready := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "Waiting for") {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("xclip did not claim the clipboard")
	}
	if got := w.Clipboard(); got != text {
		t.Fatalf("external INCR clipboard: got %d bytes, want %d", len(got), len(text))
	}
	if err := owner.Wait(); err != nil {
		t.Fatal(err)
	}
	w.SetClipboard(text)
	go func() {
		got, err := exec.CommandContext(ctx, path, "-selection", "clipboard", "-out").Output()
		jobs <- func() {
			if err != nil {
				t.Error(err)
			} else if string(got) != text {
				t.Errorf("outgoing clipboard: got %d bytes, want %d", len(got), len(text))
			}
			b.Quit()
		}
		b.Wake()
	}()
	if err := b.Run(); err != nil {
		t.Fatal(err)
	}
}

// Query the driver after replacing the frame texture and after the next scroll.
func checkGLScrollResize(t *testing.T, w *xWindow, img *image.RGBA) {
	t.Helper()
	p := w.graphics.renderer
	var level func(uint32, int32, uint32, *int32)
	purego.RegisterFunc(&level, p.ctx.Proc("glGetTexLevelParameteriv"))
	scratchSize := func() image.Point {
		var width, height int32
		p.gl.bindTexture(0x0de1, p.scratch)
		level(0x0de1, 0, 0x1000, &width)
		level(0x0de1, 0, 0x1001, &height)
		p.gl.bindTexture(0x0de1, p.texture)
		return image.Pt(int(width), int(height))
	}

	small := image.Rect(0, 0, 40, 25)
	if scratchSize() != image.Pt(160, 73) || !w.PresentGPURegions(small.Size(), func(upload func(*image.RGBA)) {
		upload(img.SubImage(small).(*image.RGBA))
	}) {
		t.Error("OpenGL scroll texture or frame resize failed")
		return
	}
	if size := scratchSize(); size != (image.Point{}) {
		t.Errorf("OpenGL retained %v scroll texture after window shrink", size)
	}
	if !w.ScrollPixels(small, image.Pt(0, 4)) || scratchSize() != small.Size() {
		t.Error("OpenGL did not regrow its scroll texture")
	}
}

func TestEGLBufferAgeRepairBounds(t *testing.T) {
	var full, now, last, earlier eglDamage
	full.add(image.Rect(0, 0, 800, 600))
	now.add(image.Rect(2, 8, 9, 12))
	last.add(image.Rect(230, 300, 250, 326))
	earlier.add(image.Rect(510, 40, 535, 60))
	c := eglContext{previous: [2]eglDamage{last, earlier}}
	two := now
	two.add(last.rects[0])
	three := two
	three.add(earlier.rects[0])
	for age, want := range []eglDamage{full, now, two, three, full} {
		if got := c.repairDamage(now, full, int32(age)); got != want {
			t.Fatalf("age %d: repair %v, want %v", age, got, want)
		}
	}
	c.previous[1] = eglDamage{}
	if c.repairDamage(now, full, 3) != full {
		t.Fatal("uninitialized back buffer")
	}
	if three.count != 3 {
		t.Fatal("distant edits merged")
	}
}
