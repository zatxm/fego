//go:build linux && !android && arm64

package platform

func xCopyBGRX(dst, src []byte) { xCopyWords(dst, src) }
