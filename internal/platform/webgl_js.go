package platform

import (
	"errors"
	"syscall/js"
)

type webGL struct {
	canvas, gl, texture, program js.Value
	width, height                int
	dirty                        bool
	onLoss                       js.Func
}

func newWebGL() *webGL {
	canvas := js.Global().Get("document").Call("createElement", "canvas")
	canvas.Get("style").Set("cssText", "position:absolute;inset:0;width:100%;height:100%;pointer-events:none;")
	canvas.Call("setAttribute", "aria-hidden", "true")
	gl := canvas.Call("getContext", "webgl2", map[string]any{
		"alpha": false, "depth": false, "stencil": false, "antialias": false,
		"failIfMajorPerformanceCaveat": true,
	})
	if gl.IsNull() {
		return nil
	}
	g := &webGL{canvas: canvas, gl: gl}
	if err := g.shader(""); err != nil {
		g.close()
		return nil
	}
	g.texture = gl.Call("createTexture")
	gl.Call("bindTexture", 0x0de1, g.texture)
	for _, key := range []int{0x2800, 0x2801} {
		gl.Call("texParameteri", 0x0de1, key, 0x2600)
	}
	for _, key := range []int{0x2802, 0x2803} {
		gl.Call("texParameteri", 0x0de1, key, 0x812f)
	}
	return g
}

func (g *webGL) resize(w, h int) bool {
	const target = 0x0de1
	if w*h == 0 {
		return true
	}
	limit := g.gl.Call("getParameter", 0x0d33).Int()
	if max(w, h) > limit {
		return false
	}
	g.canvas.Set("width", w)
	g.canvas.Set("height", h)
	g.gl.Call("viewport", 0, 0, w, h)
	g.gl.Call("texImage2D", target, 0, 0x8058, w, h, 0, 0x1908, 0x1401, js.Null())
	g.width, g.height = w, h
	return g.gl.Call("getError").Int() == 0
}

func (g *webGL) upload(pixels js.Value, x, y, w, h int) {
	if g.width == 0 || x+w > g.width || y+h > g.height {
		return
	}
	g.gl.Call("texSubImage2D", 0x0de1, 0, x, y, w, h, 0x1908, 0x1401, pixels)
	g.dirty = true
}

func (g *webGL) shader(source string) error {
	if source == "" {
		source = FrameFragmentDefault
	}
	gl := g.gl
	program := gl.Call("createProgram")
	for _, shader := range []struct {
		kind int
		src  string
	}{
		{0x8b31, "#version 300 es\n" + FrameVertex},
		{0x8b30, "#version 300 es\nprecision highp float;\n" + FrameFragment + source},
	} {
		handle := gl.Call("createShader", shader.kind)
		gl.Call("shaderSource", handle, shader.src)
		gl.Call("compileShader", handle)
		if !gl.Call("getShaderParameter", handle, 0x8b81).Bool() {
			message := gl.Call("getShaderInfoLog", handle).String()
			gl.Call("deleteShader", handle)
			gl.Call("deleteProgram", program)
			return errors.New(message)
		}
		gl.Call("attachShader", program, handle)
		gl.Call("deleteShader", handle)
	}
	gl.Call("linkProgram", program)
	if !gl.Call("getProgramParameter", program, 0x8b82).Bool() {
		message := gl.Call("getProgramInfoLog", program).String()
		gl.Call("deleteProgram", program)
		return errors.New(message)
	}
	gl.Call("useProgram", program)
	gl.Call("uniform1i", gl.Call("getUniformLocation", program, "frameTexture"), 0)
	if !g.program.IsUndefined() {
		gl.Call("deleteProgram", g.program)
	}
	g.program = program
	g.dirty = true
	return nil
}

func (g *webGL) lost() bool { return g.gl.Call("isContextLost").Bool() }
func (g *webGL) close() {
	if !g.onLoss.Value.IsUndefined() {
		g.canvas.Call("removeEventListener", "webglcontextlost", g.onLoss)
		g.onLoss.Release()
	}
	g.canvas.Call("remove")
	if ext := g.gl.Call("getExtension", "WEBGL_lose_context"); !ext.IsNull() {
		ext.Call("loseContext")
	}
}

func (w *webWindow) SetGPUEnabled(enabled bool) bool {
	if w.gpuRequested == enabled {
		return false
	}
	w.gpuRequested = enabled
	w.closeGPU()
	if enabled {
		if g := newWebGL(); g != nil {
			if !g.resize(w.width, w.height) {
				g.close()
				return false
			}
			w.gpu = g
			// The underlying canvas still receives input at its CSS size.
			w.canvas.Set("width", 1)
			w.canvas.Set("height", 1)
			w.data = js.Undefined()
			g.onLoss = js.FuncOf(func(js.Value, []js.Value) any {
				if w.gpu == g && !w.closed {
					w.closeGPU()
					w.send(Event{Kind: Paint})
				}
				return nil
			})
			g.canvas.Call("addEventListener", "webglcontextlost", g.onLoss)
			w.root.Call("appendChild", g.canvas)
		}
	}
	return true
}

func (w *webWindow) GPUActive() bool { return w.gpu != nil && !w.gpu.lost() }
func (w *webWindow) SetFragmentShader(source string) error {
	if !w.GPUActive() {
		return ErrGPUInactive
	}
	if err := w.gpu.shader(source); err != nil {
		return err
	}
	w.backend.Wake()
	return nil
}
func (w *webWindow) closeGPU() {
	if w.gpu != nil {
		w.gpu.close()
		w.gpu = nil
		if !w.closed {
			w.canvas.Set("width", w.width)
			w.canvas.Set("height", w.height)
		}
	}
}
func (w *webWindow) flushGPU() {
	if w.gpu == nil {
		return
	}
	if w.gpu.lost() {
		w.closeGPU()
		w.send(Event{Kind: Paint})
		return
	}
	if w.gpu.dirty {
		w.gpu.dirty = false
		w.gpu.gl.Call("drawArrays", 4, 0, 3)
	}
}
