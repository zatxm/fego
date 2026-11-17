package ui

import (
	"fmt"
	"testing"

	"github.com/zatxm/fego"
)

func BenchmarkListScrolling(b *testing.B) {
	rows := List(100000, func(i int) string {
		return fmt.Sprintf("Item %06d   Native UI row   %.2f", i, float64((int64(i)*7919)%100000)/100)
	}).Fill().FontSize(13)
	h := newScrollScreen(View(rows), 1, true)
	defer h.Close()
	h.Width, h.Height = 800, 600
	rows.Refresh()
	h.Step()
	delta := float32(8)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if rows.scroll >= 8000 {
			delta = -8
		} else if rows.scroll <= 0 {
			delta = 8
		}
		h.Send(fego.Event{Kind: fego.Scroll, X: 20, Y: 20, DY: delta})
	}
}
