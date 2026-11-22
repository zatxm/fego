//go:build linux && !android && (amd64 || arm64)

package platform

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// MIT-SHM lets a local X server read uploads from shared memory instead of the
// socket. A window's segment has two halves; tiles alternate between them, and
// a half is rewritten only after the ShmCompletion of every upload from it.
// Remote servers, missing libXext and failed attaches use XPutImage instead.

type xShmInfo struct {
	Seg      uintptr
	ID       int32
	_        int32
	Addr     *byte
	ReadOnly int32
	_        int32
}

type xShm struct {
	info     *xShmInfo // C memory: Xlib keeps it in the image
	img      *xImage
	half     int
	next     int
	pending  [2]int
	putArgs  [11]uintptr
	waitArgs [3]uintptr
	event    xEvent
}

var xs struct {
	loaded         bool
	createImage    func(uintptr, uintptr, uint32, int32, *byte, *xShmInfo, uint32, uint32) *xImage
	query          func(uintptr) int32
	attach, detach func(uintptr, *xShmInfo) int32
	queryExtension func(uintptr, string, *int32, *int32, *int32) int32
	sync           func(uintptr, int32) int32
	checkTyped     uintptr
	shmget         func(int32, uintptr, int32) int32
	shmat          func(int32, unsafe.Pointer, int32) unsafe.Pointer
	shmdt          func(unsafe.Pointer) int32
	shmctl         func(int32, int32, unsafe.Pointer) int32
	putImage       uintptr
	rawSync        uintptr
}

// xShmDisabled lets tests exercise the socket upload path on a local server.
var xShmDisabled bool

func loadXShm() bool {
	if xs.loaded {
		return xs.putImage != 0
	}
	xs.loaded = true
	ext, err := purego.Dlopen("libXext.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return false
	}
	x11, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return false
	}
	libc, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return false
	}
	for _, f := range []struct {
		lib  uintptr
		name string
		dest any
	}{{ext, "XShmQueryExtension", &xs.query}, {ext, "XShmCreateImage", &xs.createImage}, {ext, "XShmAttach", &xs.attach}, {ext, "XShmDetach", &xs.detach}, {x11, "XQueryExtension", &xs.queryExtension}, {x11, "XSync", &xs.sync}, {libc, "shmget", &xs.shmget}, {libc, "shmat", &xs.shmat}, {libc, "shmdt", &xs.shmdt}, {libc, "shmctl", &xs.shmctl}} {
		p, err := purego.Dlsym(f.lib, f.name)
		if err != nil {
			return false
		}
		purego.RegisterFunc(f.dest, p)
	}
	// Waiting for uploads is a per-frame path; raw calls avoid wrapper allocations.
	if xs.checkTyped, err = purego.Dlsym(x11, "XCheckTypedEvent"); err != nil {
		return false
	}
	if xs.rawSync, err = purego.Dlsym(x11, "XSync"); err != nil {
		return false
	}
	put, err := purego.Dlsym(ext, "XShmPutImage")
	if err != nil {
		return false
	}
	xs.putImage = put
	return true
}

// shmAvailable reports whether this display supports MIT-SHM, once per backend.
func (b *xBackend) shmAvailable() bool {
	if b.shmState == 0 {
		b.shmState = -1
		var major, event, firstError int32
		if !xShmDisabled && loadXShm() && xs.query(b.display) != 0 && xs.queryExtension(b.display, "MIT-SHM", &major, &event, &firstError) != 0 {
			// ShmCompletion is the extension's first event.
			b.shmMajor, b.shmCompletion, b.shmState = uint8(major), event, 1
		}
	}
	return b.shmState > 0
}

// shmBuffers returns a segment whose halves hold capacity bytes, or nil.
func (w *xWindow) shmBuffers(capacity int) *xShm {
	if s := w.shm; s != nil && s.half == capacity {
		return s
	}
	w.releaseShm()
	if !w.b.shmAvailable() {
		return nil
	}
	s, ok := w.b.newShm(capacity)
	if !ok {
		// A server that cannot attach (remote or another IPC namespace) keeps using the socket.
		w.b.shmState = -1
		return nil
	}
	w.shm = s
	return s
}

func (b *xBackend) newShm(half int) (*xShm, bool) {
	const ipcPrivate, ipcCreate, ipcRemove = 0, 0o1000, 0
	id := xs.shmget(ipcPrivate, uintptr(2*half), ipcCreate|0o600)
	if id < 0 {
		return nil, false
	}
	addr := xs.shmat(id, nil, 0)
	if uintptr(addr) == ^uintptr(0) {
		xs.shmctl(id, ipcRemove, nil)
		return nil, false
	}
	info := (*xShmInfo)(unsafe.Pointer(xl.calloc(unsafe.Sizeof(xShmInfo{}), 1)))
	if info == nil {
		xs.shmdt(addr)
		xs.shmctl(id, ipcRemove, nil)
		return nil, false
	}
	info.ID, info.Addr = id, (*byte)(addr)
	attached := b.shmAttach(info)
	// The segment disappears once both this process and the server detach it.
	xs.shmctl(id, ipcRemove, nil)
	var img *xImage
	if attached {
		img = xs.createImage(b.display, b.visual, uint32(b.depth), 2, nil, info, 1, 1)
	}
	if img == nil || img.BitsPerPixel != 32 || img.Red != 0xff0000 || img.Green != 0xff00 || img.Blue != 0xff {
		if img != nil {
			xl.destroyImage(img)
		}
		if attached {
			xs.detach(b.display, info)
			xs.sync(b.display, 0)
		}
		xs.shmdt(addr)
		xl.free(unsafe.Pointer(info))
		return nil, false
	}
	return &xShm{info: info, img: img, half: half}, true
}

// shmAttach reports attach errors instead of letting the error handler stop the app.
func (b *xBackend) shmAttach(info *xShmInfo) bool {
	b.shmProbing, b.shmFailed = true, false
	ok := xs.attach(b.display, info) != 0
	xs.sync(b.display, 0)
	b.shmProbing = false
	return ok && !b.shmFailed
}

func (w *xWindow) releaseShm() {
	s := w.shm
	if s == nil {
		return
	}
	w.shm = nil
	s.img.Data = nil
	xl.destroyImage(s.img)
	xs.detach(w.b.display, s.info)
	// The server must stop reading before this process unmaps the segment.
	xs.sync(w.b.display, 0)
	xs.shmdt(unsafe.Pointer(s.info.Addr))
	xl.free(unsafe.Pointer(s.info))
}

// shmTile returns the next free half, waiting for the server only if both are in use.
func (w *xWindow) shmTile(s *xShm) []byte {
	for range 100 {
		if s.pending[s.next] == 0 {
			break
		}
		// After XSync the server has processed earlier puts; their completions are queued.
		s.waitArgs = [3]uintptr{w.b.display, 0, 0}
		purego.SyscallN(xs.rawSync, s.waitArgs[:2]...)
		w.b.drainShmCompletions(s)
	}
	return unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(s.info.Addr), s.next*s.half)), s.half)
}

func (b *xBackend) drainShmCompletions(s *xShm) {
	for {
		s.waitArgs = [3]uintptr{b.display, uintptr(b.shmCompletion), uintptr(unsafe.Pointer(&s.event))}
		if r, _, _ := purego.SyscallN(xs.checkTyped, s.waitArgs[:]...); int32(r) == 0 {
			return
		}
		b.shmEvent(&s.event)
	}
}

func (b *xBackend) shmEvent(raw *xEvent) {
	if w := b.windows[(*xAny)(unsafe.Pointer(raw)).Window]; w != nil {
		w.shmCompleted(uintptr(raw[6]), uintptr(raw[7]))
	}
}

func (w *xWindow) shmPut(s *xShm, x, y int32, width, height uint32) {
	s.img.Data = (*byte)(unsafe.Add(unsafe.Pointer(s.info.Addr), s.next*s.half))
	s.putArgs = [11]uintptr{w.b.display, w.id, w.gc, uintptr(unsafe.Pointer(s.img)), 0, 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1}
	purego.SyscallN(xs.putImage, s.putArgs[:]...)
	s.pending[s.next]++
	s.next = 1 - s.next
	runtime.KeepAlive(s)
}

// shmCompleted counts an upload the server finished reading. Completions for a
// replaced segment carry another segment id and are ignored.
func (w *xWindow) shmCompleted(seg, offset uintptr) {
	if s := w.shm; s != nil && s.info.Seg == seg && offset%uintptr(s.half) == 0 && offset/uintptr(s.half) < 2 {
		if h := offset / uintptr(s.half); s.pending[h] > 0 {
			s.pending[h]--
		}
	}
}
