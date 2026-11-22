package platform

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNativeTextLimitsBeforeConversion(t *testing.T) {
	text := "a中🧪"
	if value, ok := nativeUTF8Text([]byte(text)); !ok || value != text {
		t.Fatal("UTF8 text", value, ok)
	}
	if value, ok := nativeUTF16Text(utf16.Encode([]rune(text))); !ok || value != text {
		t.Fatal("UTF16 text", value, ok)
	}
	if _, ok := nativeUTF8Text([]byte{0xff}); ok {
		t.Fatal("invalid native UTF8 accepted")
	}
	tooManyUTF8Runes := []byte(strings.Repeat("a", nativeTextMaxRunes+1))
	tooManyUTF16Runes := make([]uint16, nativeTextMaxRunes+1)
	tooManyUTF8Bytes := make([]byte, nativeTextMaxBytes+1)
	tooManyUTF16Bytes := make([]uint16, nativeTextMaxBytes/2+1)
	// AllocsPerRun includes background allocations; one sample can fail
	// even when the rejection path itself does not allocate.
	const allocationSamples = 16
	for _, data := range [][]byte{tooManyUTF8Runes, tooManyUTF8Bytes} {
		if _, ok := nativeUTF8Text(data); ok {
			t.Fatal("oversized UTF8 accepted")
		}
		if allocations := testing.AllocsPerRun(allocationSamples, func() { nativeUTF8Text(data) }); allocations != 0 {
			t.Fatalf("rejected UTF8 allocated %v objects", allocations)
		}
	}
	for _, data := range [][]uint16{tooManyUTF16Runes, tooManyUTF16Bytes} {
		if _, ok := nativeUTF16Text(data); ok {
			t.Fatal("oversized UTF16 accepted")
		}
		if allocations := testing.AllocsPerRun(allocationSamples, func() { nativeUTF16Text(data) }); allocations != 0 {
			t.Fatalf("rejected UTF16 allocated %v objects", allocations)
		}
	}
	// A surrogate pair counts as one rune, including at the raw-byte limit.
	pairs := make([]uint16, nativeTextMaxRunes*2)
	for i := 0; i < len(pairs); i += 2 {
		pairs[i], pairs[i+1] = 0xd83e, 0xddea
	}
	if got, ok := nativeUTF16Text(pairs); !ok || len(got) != nativeTextMaxBytes {
		t.Fatal("valid surrogate-pair boundary rejected", len(got), ok)
	}
}
