package text

/*
#cgo LDFLAGS: -framework CoreText -framework CoreFoundation
#include <CoreText/CoreText.h>
#include <limits.h>
#include <stdbool.h>

static int chineseFont(int bold, char *path, char *name) {
	CTFontRef font = CTFontCreateWithName(bold ? CFSTR("PingFangSC-Semibold") : CFSTR("PingFangSC-Regular"), 16, NULL);
	if (!font) return 0;
	UniChar ch = 0x4e2d;
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

import (
	"os"

	"golang.org/x/image/font/sfnt"
)

func init() { systemCJK = installedChineseFont }

func installedChineseFont(s *System, bold bool) *source {
	var path [C.PATH_MAX]C.char
	var name [256]C.char
	weight := C.int(0)
	if bold {
		weight = 1
	}
	if C.chineseFont(weight, &path[0], &name[0]) == 0 {
		return nil
	}
	filename, postscript := C.GoString(&path[0]), C.GoString(&name[0])
	file, err := os.Open(filename)
	if err != nil {
		return nil
	}
	collection, err := sfnt.ParseCollectionReaderAt(file)
	if err == nil {
		for i := 0; i < collection.NumFonts(); i++ {
			f, err := collection.Font(i)
			if err != nil {
				continue
			}
			if n, _ := f.Name(&s.buffer, sfnt.NameIDPostScript); n == postscript {
				src := &source{font: f, file: file, name: filename, index: i}
				s.sources = append(s.sources, src)
				return src
			}
		}
	}
	file.Close()
	return nil
}
