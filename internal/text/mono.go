package text

import (
	"sync"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/sfnt"
)

var monoFonts = sync.OnceValue(func() [2]*source {
	var result [2]*source
	for weight, data := range [][]byte{gomono.TTF, gomonobold.TTF} {
		f, err := sfnt.Parse(data)
		if err != nil {
			panic(err) // Both faces are compiled into the executable.
		}
		result[weight] = &source{font: f, name: "Go Mono"}
	}
	return result
})

// NewMonospace uses Go Mono with the same CJK and symbol fallback as New.
func NewMonospace() *System {
	s := New()
	s.regular = monoFonts()
	return s
}

func NewMonospaceWith(regular, bold *sfnt.Font) *System {
	if bold == nil {
		bold = regular
	}
	s := New()
	s.regular = [2]*source{{font: regular}, {font: bold}}
	return s
}
