package platform

import (
	"unicode/utf16"
	"unicode/utf8"
)

const (
	nativeTextMaxBytes = 4 << 20
	nativeTextMaxRunes = 1 << 20
)

// Validate borrowed native text before allocating Go strings or rune slices.
func nativeUTF8Text(data []byte) (string, bool) {
	if len(data) > nativeTextMaxBytes || !utf8.Valid(data) || utf8.RuneCount(data) > nativeTextMaxRunes {
		return "", false
	}
	return string(data), true
}

func nativeUTF16Text(data []uint16) (string, bool) {
	if len(data) > nativeTextMaxBytes/2 {
		return "", false
	}
	count := 0
	for i := 0; i < len(data); i++ {
		if count == nativeTextMaxRunes {
			return "", false
		}
		if data[i] >= 0xd800 && data[i] <= 0xdbff && i+1 < len(data) && data[i+1] >= 0xdc00 && data[i+1] <= 0xdfff {
			i++
		}
		count++
	}
	return string(utf16.Decode(data)), true
}

// nativeTextChange computes a rune-indexed replacement without allocating rune
// copies of the editor contents. Both strings have already been UTF-8 validated.
func nativeTextChange(old, value string) (start, end int, inserted string) {
	for len(old) != 0 && len(value) != 0 {
		r, n := utf8.DecodeRuneInString(old)
		s, m := utf8.DecodeRuneInString(value)
		if r != s {
			break
		}
		old, value = old[n:], value[m:]
		start++
	}
	for len(old) != 0 && len(value) != 0 {
		r, n := utf8.DecodeLastRuneInString(old)
		s, m := utf8.DecodeLastRuneInString(value)
		if r != s {
			break
		}
		old, value = old[:len(old)-n], value[:len(value)-m]
	}
	return start, start + utf8.RuneCountInString(old), value
}
