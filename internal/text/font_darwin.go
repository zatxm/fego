//go:build darwin && !ios

package text

import (
	"bytes"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

type macFonts struct {
	uiFont    func(kind uint32, size float64, language uintptr) uintptr
	attribute func(font, key uintptr) uintptr
	name      func(font uintptr) uintptr
	path      func(url uintptr, resolve uint8, data *byte, capacity int) uint8
	string    func(text uintptr, data *byte, capacity int, encoding uint32) uint8
	release   func(uintptr)
	urlKey    uintptr
}

var macSystemFonts = sync.OnceValue(func() *macFonts {
	ct, err := purego.Dlopen("/System/Library/Frameworks/CoreText.framework/CoreText", purego.RTLD_LOCAL|purego.RTLD_NOW)
	if err != nil {
		return nil
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LOCAL|purego.RTLD_NOW)
	if err != nil {
		return nil
	}
	urlKey, err := purego.Dlsym(ct, "kCTFontURLAttribute")
	if err != nil {
		return nil
	}
	memcpy, err := purego.Dlsym(purego.RTLD_DEFAULT, "memcpy")
	if err != nil {
		return nil
	}
	// dlsym returns the address of the exported CFStringRef. Read its value in C.
	f := &macFonts{}
	purego.SyscallN(memcpy, uintptr(unsafe.Pointer(&f.urlKey)), urlKey, unsafe.Sizeof(f.urlKey))
	purego.RegisterLibFunc(&f.uiFont, ct, "CTFontCreateUIFontForLanguage")
	purego.RegisterLibFunc(&f.attribute, ct, "CTFontCopyAttribute")
	purego.RegisterLibFunc(&f.name, ct, "CTFontCopyPostScriptName")
	purego.RegisterLibFunc(&f.path, cf, "CFURLGetFileSystemRepresentation")
	purego.RegisterLibFunc(&f.string, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&f.release, cf, "CFRelease")
	return f
})

func init() { systemLatin = macUIFont }

func macUIFont(s *System, bold bool) *source {
	f := macSystemFonts()
	if f == nil {
		return nil
	}
	kind := uint32(2) // kCTFontUIFontSystem
	if bold {
		kind = 3 // kCTFontUIFontEmphasizedSystem
	}
	font := f.uiFont(kind, 16, 0)
	if font == 0 {
		return nil
	}
	defer f.release(font)
	url := f.attribute(font, f.urlKey)
	if url == 0 {
		return nil
	}
	defer f.release(url)
	name := f.name(font)
	if name == 0 {
		return nil
	}
	defer f.release(name)
	var path [1024]byte
	var postscript [256]byte
	const utf8Encoding = 0x08000100
	if f.path(url, 1, &path[0], len(path)) == 0 || f.string(name, &postscript[0], len(postscript), utf8Encoding) == 0 {
		return nil
	}
	filename, _, _ := bytes.Cut(path[:], []byte{0})
	ps, _, _ := bytes.Cut(postscript[:], []byte{0})
	return s.openNamedFont(string(filename), string(ps))
}
