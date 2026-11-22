package ui

import (
	"errors"
	"image"

	"github.com/zatxm/fego/v26"
)

// VideoPlayer presents a bounded decoded frame inside an ordinary element.
type VideoPlayer struct {
	*Element
	source    string
	pixelSize image.Point
	stop      func()
	onEnd     func(error)
	frame     *image.RGBA
}

// Video creates a player for a local file or, in a browser, a same-origin or
// CORS-enabled media URL. Decode resolution is capped at about 1 megapixel.
func Video(source string, width, height int) *VideoPlayer {
	if width < 1 || height < 1 {
		width, height = 640, 360
	}
	size := image.Pt(min(1280, width), min(800, height))
	v := &VideoPlayer{source: source, pixelSize: size}
	v.Element = Canvas(float32(width), float32(height), func(p *Painter) {
		bounds := p.Bounds()
		p.Rect(bounds, RGB(0, 0, 0))
		if v.frame != nil {
			w, h := float32(v.frame.Rect.Dx()), float32(v.frame.Rect.Dy())
			scale := min(bounds.W/w, bounds.H/h)
			w, h = w*scale, h*scale
			p.Image(v.frame, Rect{(bounds.W - w) / 2, (bounds.H - h) / 2, w, h})
		}
	}).Label("Video")
	return v
}

// Play starts at the beginning. Call it from a user gesture in the browser.
func (v *VideoPlayer) Play(w *fego.Window) error {
	if w == nil || v.source == "" {
		return errors.New("ui: video needs a window and source")
	}
	select {
	case <-w.Done():
		return errors.New("ui: video window is closed")
	default:
	}
	v.Stop()
	return v.play(w)
}

func (v *VideoPlayer) Stop() {
	if stop := v.stop; stop != nil {
		v.stop = nil
		stop()
	}
}

func (v *VideoPlayer) OnEnd(fn func(error)) *VideoPlayer { v.onEnd = fn; return v }

func (v *VideoPlayer) showFrame(frame *image.RGBA) {
	v.frame = frame
	v.Refresh()
}

func (v *VideoPlayer) videoEnded(err error) {
	v.Stop()
	if v.onEnd != nil {
		v.onEnd(err)
	}
}
