//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"sync"

	"github.com/ebitengine/purego"
)

// XRender presents damage through the display server without creating a client
// GL context. The temporary server pixmap holds at most one upload band.
type xRenderPresenter struct {
	format, src, dst, pix uintptr
	width, rows           int
}

var xr struct {
	once         sync.Once
	available    bool
	query        func(uintptr, string, *int32, *int32, *int32) int32
	createPixmap func(uintptr, uintptr, uint32, uint32, uint32) uintptr
	freePixmap   func(uintptr, uintptr) int32
	findVisual   func(uintptr, uintptr) uintptr
	picture      func(uintptr, uintptr, uintptr, uintptr, uintptr) uintptr
	freePicture  func(uintptr, uintptr)
	composite    func(uintptr, int32, uintptr, uintptr, uintptr, int32, int32, int32, int32, int32, int32, uint32, uint32)
}

func (p *xRenderPresenter) open(w *xWindow) bool {
	xr.once.Do(func() {
		x, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			return
		}
		lib, err := purego.Dlopen("libXrender.so.1", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			return
		}
		for _, s := range []struct {
			lib    uintptr
			name   string
			target any
		}{
			{x, "XQueryExtension", &xr.query}, {x, "XCreatePixmap", &xr.createPixmap}, {x, "XFreePixmap", &xr.freePixmap},
			{lib, "XRenderFindVisualFormat", &xr.findVisual}, {lib, "XRenderCreatePicture", &xr.picture},
			{lib, "XRenderFreePicture", &xr.freePicture}, {lib, "XRenderComposite", &xr.composite},
		} {
			addr, err := purego.Dlsym(s.lib, s.name)
			if err != nil {
				return
			}
			purego.RegisterFunc(s.target, addr)
		}
		xr.available = true
	})
	if !xr.available {
		return false
	}
	d := w.b.display
	var op, event, firstError int32
	for _, ext := range [...]string{"DRI3", "RENDER"} {
		if xr.query(d, ext, &op, &event, &firstError) == 0 {
			return false
		}
	}
	p.format = xr.findVisual(d, w.b.visual)
	if p.format == 0 {
		return false
	}
	p.dst = xr.picture(d, w.id, p.format, 0, 0)
	return p.dst != 0
}

func (p *xRenderPresenter) resize(w *xWindow, width int) {
	if p.width == width {
		return
	}
	d := w.b.display
	p.freeSource(d)
	p.width = width
	p.rows = max(1, min(w.height, 32767, xUploadBytes/(width*4)))
	p.pix = xr.createPixmap(d, w.id, uint32(width), uint32(p.rows), uint32(w.b.depth))
	p.src = xr.picture(d, p.pix, p.format, 0, 0)
}

func (p *xRenderPresenter) freeSource(d uintptr) {
	if p.src != 0 {
		xr.freePicture(d, p.src)
		xr.freePixmap(d, p.pix)
		p.src, p.pix = 0, 0
	}
}

func (p *xRenderPresenter) close(d uintptr) {
	if p.dst != 0 {
		p.freeSource(d)
		xr.freePicture(d, p.dst)
		*p = xRenderPresenter{}
	}
}
