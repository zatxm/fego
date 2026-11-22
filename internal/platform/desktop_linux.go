//go:build !android

package platform

import (
	"image"
	"strings"
	"unsafe"
)

func (b *xBackend) Configure(_ string, _ string, _ *image.NRGBA) error { return nil }

func (w *xWindow) setIdentity(o Options) {
	b := w.b
	if id := strings.ReplaceAll(o.AppID, "\x00", ""); id != "" {
		class := []byte(id + "\x00" + id + "\x00")
		xl.changeProperty(b.display, w.id, 67, 31, 8, 0, unsafe.Pointer(&class[0]), int32(len(class)))
	}
	if o.Icon == nil {
		return
	}
	width, height := o.Icon.Bounds().Dx(), o.Icon.Bounds().Dy()
	argb := make([]uintptr, 2+width*height)
	argb[0], argb[1] = uintptr(width), uintptr(height)
	for i := range width * height {
		p := o.Icon.Pix[4*i:]
		argb[2+i] = uintptr(p[3])<<24 | uintptr(p[0])<<16 | uintptr(p[1])<<8 | uintptr(p[2])
	}
	atom := xl.internAtom(b.display, "_NET_WM_ICON", 0)
	xl.changeProperty(b.display, w.id, atom, 6, 32, 0, unsafe.Pointer(&argb[0]), int32(len(argb)))
}

func (w *xWindow) Show() {
	xl.mapWindow(w.b.display, w.id)
	xl.flush(w.b.display)
}
func (w *xWindow) Hide() {
	xl.unmapWindow(w.b.display, w.id)
	xl.flush(w.b.display)
}
func (w *xWindow) Activate() {
	b := w.b
	xl.mapWindow(b.display, w.id)
	event := xClientEvent{
		xAny:    xAny{Kind: xClientMessage, Display: b.display, Window: w.id},
		Message: xl.internAtom(b.display, "_NET_ACTIVE_WINDOW", 0),
		Format:  32, Data: [5]int64{2},
	}
	var raw xEvent
	*(*xClientEvent)(unsafe.Pointer(&raw)) = event
	xl.sendEvent(b.display, b.root, 0, (1<<19)|(1<<20), &raw)
	xl.flush(b.display)
}
