//go:build windows && (amd64 || arm64)

package platform

import (
	"testing"
	"unsafe"
)

// These are compile-time assertions, including when cross-compiling.
var (
	_ [40]byte = [unsafe.Sizeof(winMinMaxInfo{})]byte{}
	_ [80]byte = [unsafe.Sizeof(winClass{})]byte{}
	_ [48]byte = [unsafe.Sizeof(winMessage{})]byte{}
	_ [72]byte = [unsafe.Sizeof(winPaint{})]byte{}
	_ [52]byte = [unsafe.Sizeof(winBitmap{})]byte{}
	_ [16]byte = [unsafe.Offsetof(winMessage{}.WParam)]byte{}
	_ [36]byte = [unsafe.Offsetof(winMessage{}.Point)]byte{}
)

func TestWindowsTextAndKeys(t *testing.T) {
	if got := winUTF16("a汉🧪"); len(got) != 5 || got[len(got)-1] != 0 {
		t.Fatalf("UTF16 %v", got)
	}
	for key, want := range map[uintptr]string{0x41: "a", 0x39: "9", 0x25: "ArrowLeft", 0x09: "Tab", 0x0d: "Enter"} {
		if got := winKey(key); got != want {
			t.Errorf("key %x: %q", key, got)
		}
	}
	if indexZero([]uint16{3, 7, 0, 9}) != 2 || indexZero([]uint16{3}) != -1 {
		t.Fatal("clipboard terminator")
	}
}
