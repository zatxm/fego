//go:build ios && cgo

package text

/*
#cgo LDFLAGS: -framework CoreText -framework CoreFoundation
#include <CoreText/CoreText.h>
#include <limits.h>
#include <stdbool.h>

static int installedFont(int bold, int cjk, char *path, char *name) {
	CTFontRef font = cjk
		? CTFontCreateWithName(bold ? CFSTR("PingFangSC-Semibold") : CFSTR("PingFangSC-Regular"), 16, NULL)
		: CTFontCreateUIFontForLanguage(bold ? kCTFontUIFontEmphasizedSystem : kCTFontUIFontSystem, 16, NULL);
	if (!font) return 0;
	UniChar ch = cjk ? 0x4e2d : 'A';
	CGGlyph glyph;
	CFURLRef url = CTFontCopyAttribute(font, kCTFontURLAttribute);
	CFStringRef ps = CTFontCopyPostScriptName(font);
	int ok = CTFontGetGlyphsForCharacters(font, &ch, &glyph, 1) && url && ps &&
		CFURLGetFileSystemRepresentation(url, true, (UInt8 *)path, PATH_MAX) &&
		CFStringGetCString(ps, name, 256, kCFStringEncodingUTF8);
	if (ps) CFRelease(ps);
	if (url) CFRelease(url);
	CFRelease(font);
	return ok;
}
*/
import "C"

func init() {
	systemLatin = func(s *System, bold bool) *source { return installedAppleFont(s, bold, 0) }
	systemCJK = func(s *System, bold bool) *source { return installedAppleFont(s, bold, 1) }
}

func installedAppleFont(s *System, bold bool, cjk C.int) *source {
	var path [C.PATH_MAX]C.char
	var name [256]C.char
	weight := C.int(0)
	if bold {
		weight = 1
	}
	if C.installedFont(weight, cjk, &path[0], &name[0]) == 0 {
		return nil
	}
	return s.openNamedFont(C.GoString(&path[0]), C.GoString(&name[0]))
}
