//go:build (windows || (darwin && !ios)) && (amd64 || arm64)

package platform

import "unsafe"

// nativeAddress restores a pointer returned by a native API, outside the Go heap.
func nativeAddress(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}
