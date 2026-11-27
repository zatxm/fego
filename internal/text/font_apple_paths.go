//go:build darwin || ios

package text

import (
	"os"

	"golang.org/x/image/font/sfnt"
)

func (s *System) openNamedFont(filename, postscript string) *source {
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
			if n, _ := f.Name(&s.buffer, sfnt.NameIDPostScript); n == postscript || collection.NumFonts() == 1 {
				src := &source{font: f, file: file, name: filename, index: i}
				s.sources = append(s.sources, src)
				return src
			}
		}
	}
	file.Close()
	return nil
}
