//go:build ((linux && !android) || windows || (darwin && !ios)) && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"strings"
	"unsafe"
)

type gpuState struct {
	renderer  *glPresenter
	requested bool
}

func (s *gpuState) set(enabled bool, create func() (glContext, error)) bool {
	if enabled == s.requested {
		return false
	}
	s.requested = enabled
	if !enabled {
		s.close()
		return true
	}
	ctx, err := create()
	if err != nil {
		return false
	}
	renderer, err := newGLPresenter(ctx)
	if err != nil {
		ctx.Close()
		return false
	}
	s.renderer = renderer
	return true
}

func (s *gpuState) close() {
	if s.renderer != nil {
		s.renderer.ctx.Close()
		s.renderer = nil
	}
}

func (s *gpuState) present(img *image.RGBA, damage []image.Rectangle) bool {
	if s.renderer == nil {
		return false
	}
	if s.renderer.present(img, damage) {
		return true
	}
	s.close()
	return false
}

func (s *gpuState) shader(source string) error {
	if s.renderer == nil || !s.renderer.ctx.MakeCurrent() {
		return ErrGPUInactive
	}
	return s.renderer.shader(source)
}

type glPresenter struct {
	ctx        glContext
	gl         glAPI
	program    uint32
	maxTexture int32
	size       image.Point
}

func newGLPresenter(ctx glContext) (*glPresenter, error) {
	if !ctx.MakeCurrent() {
		return nil, ErrGPUInactive
	}
	g, err := loadGL(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(glString(g.getString(0x1f01)))
	for _, software := range []string{"llvmpipe", "softpipe", "swrast", "software", "gdi generic"} {
		if strings.Contains(name, software) {
			return nil, ErrGPUInactive
		}
	}
	p := &glPresenter{ctx: ctx, gl: g}
	g.getInteger(0x0d33, &p.maxTexture)
	if err := p.shader(""); err != nil {
		return nil, err
	}
	var vao, texture uint32
	g.genVertexArrays(1, &vao)
	g.bindVertexArray(vao)
	g.genTextures(1, &texture)
	g.bindTexture(0x0de1, texture)
	for _, key := range []uint32{0x2800, 0x2801} {
		g.textureParameter(0x0de1, key, 0x2600)
	}
	for _, key := range []uint32{0x2802, 0x2803} {
		g.textureParameter(0x0de1, key, 0x812f)
	}
	if code := g.getError(); code != 0 {
		return nil, fmt.Errorf("initialize OpenGL: %#x", code)
	}
	return p, nil
}

func (p *glPresenter) shader(source string) error {
	if source == "" {
		source = FrameFragmentDefault
	}
	g := p.gl
	shaders := [2]uint32{}
	defer func() {
		for _, shader := range shaders {
			if shader != 0 {
				g.deleteShader(shader)
			}
		}
	}()
	for i, body := range []string{FrameVertex, FrameFragment + source} {
		shaders[i] = g.createShader(0x8b31 - uint32(i))
		text := []byte("#version 330 core\n" + body + "\x00")
		pointer := unsafe.SliceData(text)
		g.shaderSource(shaders[i], 1, &pointer, nil)
		g.compileShader(shaders[i])
		runtime.KeepAlive(text)
		var ok int32
		g.getShader(shaders[i], 0x8b81, &ok)
		if ok == 0 {
			var log [2048]byte
			g.shaderLog(shaders[i], int32(len(log)), nil, &log[0])
			return errors.New(glString(&log[0]))
		}
	}
	program := g.createProgram()
	for _, shader := range shaders {
		g.attachShader(program, shader)
	}
	g.linkProgram(program)
	var ok int32
	g.getProgram(program, 0x8b82, &ok)
	if ok == 0 {
		var log [2048]byte
		g.programLog(program, int32(len(log)), nil, &log[0])
		g.deleteProgram(program)
		return errors.New(glString(&log[0]))
	}
	g.useProgram(program)
	g.uniform1i(g.uniformLocation(program, "frameTexture"), 0)
	if p.program != 0 {
		g.deleteProgram(p.program)
	}
	p.program = program
	return nil
}

func (p *glPresenter) present(img *image.RGBA, damage []image.Rectangle) bool {
	if img == nil || img.Rect.Empty() || max(img.Rect.Dx(), img.Rect.Dy()) > int(p.maxTexture) || !p.ctx.MakeCurrent() {
		return false
	}
	g := p.gl
	size := img.Rect.Size()
	g.pixelStore(0x0cf2, int32(img.Stride/4))
	if size != p.size {
		g.viewport(0, 0, int32(size.X), int32(size.Y))
		g.textureImage(0x0de1, 0, 0x8058, int32(size.X), int32(size.Y), 0, 0x1908, 0x1401, unsafe.Pointer(unsafe.SliceData(img.Pix)))
		p.size = size
	} else {
		for _, r := range damage {
			r = r.Intersect(img.Rect)
			if !r.Empty() {
				offset := img.PixOffset(r.Min.X, r.Min.Y)
				g.textureSubImage(0x0de1, 0, int32(r.Min.X-img.Rect.Min.X), int32(r.Min.Y-img.Rect.Min.Y), int32(r.Dx()), int32(r.Dy()), 0x1908, 0x1401, unsafe.Pointer(&img.Pix[offset]))
			}
		}
	}
	runtime.KeepAlive(img)
	g.drawArrays(4, 0, 3)
	return g.getError() == 0 && p.ctx.Swap()
}
