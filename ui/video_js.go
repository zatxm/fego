package ui

import (
	"errors"
	"image"
	"syscall/js"

	"github.com/zatxm/fego/v26"
)

func (v *VideoPlayer) play(w *fego.Window) error {
	document := js.Global().Get("document")
	video := document.Call("createElement", "video")
	if video.Get("requestVideoFrameCallback").IsUndefined() {
		return errors.New("ui: browser has no video frame callback")
	}
	video.Set("crossOrigin", "anonymous")
	video.Set("playsInline", true)
	video.Set("src", v.source)
	canvas := document.Call("createElement", "canvas")
	// This canvas only decodes media frames and is never attached to the DOM.
	ctx := canvas.Call("getContext", "2d", map[string]any{"willReadFrequently": true, "alpha": false})
	if ctx.IsNull() {
		return errors.New("ui: browser cannot read video frames")
	}
	width, height := v.pixelSize.X, v.pixelSize.Y
	canvas.Set("width", width)
	canvas.Set("height", height)
	var frame, ended, failed js.Func
	handle := 0
	stopped := make(chan struct{})
	v.stop = func() {
		close(stopped)
		video.Call("cancelVideoFrameCallback", handle)
		video.Call("removeEventListener", "ended", ended)
		video.Call("removeEventListener", "error", failed)
		video.Call("pause")
		video.Call("removeAttribute", "src")
		video.Call("load")
		frame.Release()
		ended.Release()
		failed.Release()
	}
	frame = js.FuncOf(func(js.Value, []js.Value) any {
		sw, sh := video.Get("videoWidth").Int(), video.Get("videoHeight").Int()
		if sw > 0 && sh > 0 {
			f := min(float64(width)/float64(sw), float64(height)/float64(sh))
			dw, dh := max(1, int(float64(sw)*f)), max(1, int(float64(sh)*f))
			bounds := image.Rect(0, 0, dw, dh)
			if v.frame == nil || v.frame.Rect != bounds {
				v.frame = image.NewRGBA(bounds)
			}
			ctx.Call("drawImage", video, 0, 0, dw, dh)
			data := ctx.Call("getImageData", 0, 0, dw, dh).Get("data")
			js.CopyBytesToGo(v.frame.Pix, data)
			v.Refresh()
		}
		handle = video.Call("requestVideoFrameCallback", frame).Int()
		return nil
	})
	ended = js.FuncOf(func(js.Value, []js.Value) any { v.videoEnded(nil); return nil })
	failed = js.FuncOf(func(js.Value, []js.Value) any {
		v.videoEnded(errors.New("ui: browser could not decode video"))
		return nil
	})
	video.Call("addEventListener", "ended", ended)
	video.Call("addEventListener", "error", failed)
	handle = video.Call("requestVideoFrameCallback", frame).Int()
	rejected := js.FuncOf(func(_ js.Value, args []js.Value) any {
		select {
		case <-stopped:
		default:
			v.videoEnded(errors.New(args[0].Get("message").String()))
		}
		return nil
	})
	var settled js.Func
	settled = js.FuncOf(func(js.Value, []js.Value) any { rejected.Release(); settled.Release(); return nil })
	video.Call("play").Call("catch", rejected).Call("finally", settled)
	done := w.Done()
	go func() {
		select {
		case <-done:
			select {
			case <-stopped:
			default:
				v.Stop()
			}
		case <-stopped:
		}
	}()
	return nil
}
