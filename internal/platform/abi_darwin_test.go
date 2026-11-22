//go:build darwin && !ios && (amd64 || arm64)

package platform

import "unsafe"

// Objective-C struct arguments and returns must follow the native ABI.
var (
	_ [16]byte = [unsafe.Sizeof(NSPoint{})]byte{}
	_ [16]byte = [unsafe.Sizeof(NSSize{})]byte{}
	_ [32]byte = [unsafe.Sizeof(NSRect{})]byte{}
	_ [16]byte = [unsafe.Sizeof(NSRange{})]byte{}
	_ [80]byte = [unsafe.Sizeof(cfSourceContext{})]byte{}
	_ [72]byte = [unsafe.Offsetof(cfSourceContext{}.Perform)]byte{}
)
