//go:build linux && (amd64 || arm64)

package platform

import (
	"image"
	"testing"
)

func BenchmarkATSPISnapshotUpdates(b *testing.B) {
	for _, field := range []string{"text", "bounds", "state"} {
		b.Run(field, func(b *testing.B) {
			a := &atspiBridge{}
			nodes := make([]AccessNode, 600)
			for i := range nodes {
				nodes[i] = AccessNode{ID: uint64(i + 1), Role: "text", Label: "before", Bounds: image.Rect(0, i, 40, i+1)}
			}
			a.update("title", image.Rect(0, 0, 800, 600), image.Point{}, nodes)
			frame := 0
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				frame++
				for i := range nodes {
					switch field {
					case "text":
						nodes[i].Label = []string{"before", "after"}[frame%2]
					case "bounds":
						nodes[i].Bounds = image.Rect(0, i-frame, 40, i+1-frame)
					case "state":
						nodes[i].Number = float64(frame)
					}
				}
				a.update("title", image.Rect(0, 0, 800, 600), image.Point{}, nodes)
			}
		})
	}
}
