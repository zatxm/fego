//go:build !unix

package ui

func bandAlloc(n int) []byte { return make([]byte, n) }

func bandFree([]byte) {}
