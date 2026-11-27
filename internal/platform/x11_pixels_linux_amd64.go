//go:build linux && !android && amd64

package platform

//go:noescape
func xCopyBlocks(dst, src *byte, n int)

func xCopyBGRX(dst, src []byte) {
	n := len(src) &^ 15
	if n != 0 {
		xCopyBlocks(&dst[0], &src[0], n)
	}
	xCopyWords(dst[n:], src[n:])
}
