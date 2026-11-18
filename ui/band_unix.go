//go:build unix

package ui

import "syscall"

// Paint bands live outside the Go heap so a released band leaves RSS at once.
func bandAlloc(n int) []byte {
	b, err := syscall.Mmap(-1, 0, n, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return make([]byte, n)
	}
	return b
}

// Munmap ignores slices it did not map, such as the heap fallback.
func bandFree(b []byte) {
	if cap(b) > 0 {
		syscall.Munmap(b[:cap(b)])
	}
}
