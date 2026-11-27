//go:build ((linux && !android) || windows || (darwin && !ios)) && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
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
	return s.open(create)
}

func (s *gpuState) open(create func() (glContext, error)) bool {
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
	ctx          glContext
	gl           glAPI
	program      uint32
	maxTexture   int32
	unpackLength int32
	size         image.Point
	texture      uint32
	// Scrolling copies through scratch: framebuffers[0] reads or draws the
	// frame texture, framebuffers[1] the scratch texture of scratchSize.
	framebuffers [2]uint32
	scratch      uint32
	scratchSize  image.Point
	blitArgs     [10]uintptr
	copyFailed   bool
	readBuffer   uint32
	// damage collects this frame's changed rects, top-left origin. A custom
	// shader may spread changes, and a new texture is all new: swap everything.
	damage     []image.Rectangle
	fullDamage bool
	custom     bool
	swapRects  []int32
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
	// The Linux presenter can copy the retained texture directly to the back
	// buffer. Only a custom effect, or a driver without blits, needs a shader.
	if g.blitFramebuffer == 0 {
		if err := p.shader(""); err != nil {
			return nil, err
		}
	}
	var vao, texture uint32
	g.genVertexArrays(1, &vao)
	g.bindVertexArray(vao)
	g.genTextures(1, &texture)
	g.bindTexture(0x0de1, texture)
	p.texture = texture
	p.textureParameters()
	if code := g.getError(); code != 0 {
		return nil, fmt.Errorf("initialize OpenGL: %#x", code)
	}
	return p, nil
}

func (p *glPresenter) textureParameters() {
	g := &p.gl
	for _, key := range []uint32{0x2800, 0x2801} {
		g.textureParameter(0x0de1, key, 0x2600)
	}
	for _, key := range []uint32{0x2802, 0x2803} {
		g.textureParameter(0x0de1, key, 0x812f)
	}
}

func (p *glPresenter) shader(source string) error {
	if (source == "" || source == FrameFragmentDefault) && p.gl.blitFramebuffer != 0 && !p.copyFailed {
		if p.program != 0 {
			p.gl.useProgram(0)
			p.gl.deleteProgram(p.program)
			p.program = 0
		}
		p.custom, p.fullDamage = false, true
		return nil
	}
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
	p.custom = source != FrameFragmentDefault
	p.fullDamage = true
	if p.program != 0 {
		g.deleteProgram(p.program)
	}
	p.program = program
	return nil
}

// A zero unpack row length lets tightly packed bands share the same GL state.
func (p *glPresenter) setUnpackLength(length int32) {
	if length != p.unpackLength {
		p.gl.pixelStore(0x0cf2, length)
		p.unpackLength = length
	}
}

func (p *glPresenter) resize(size image.Point, pixels unsafe.Pointer) {
	g := &p.gl
	// Trim a scroll texture that is more than twice the new window area.
	// Small resizes continue to reuse it.
	if int64(p.scratchSize.X)*int64(p.scratchSize.Y) > 2*int64(size.X)*int64(size.Y) {
		g.bindTexture(0x0de1, p.scratch)
		g.textureImage(0x0de1, 0, 0x8058, 0, 0, 0, 0x1908, 0x1401, nil)
		g.bindTexture(0x0de1, p.texture)
		p.scratchSize = image.Point{}
	}
	g.viewport(0, 0, int32(size.X), int32(size.Y))
	g.textureImage(0x0de1, 0, 0x8058, int32(size.X), int32(size.Y), 0, 0x1908, 0x1401, pixels)
	p.size = size
}

func (p *glPresenter) present(img *image.RGBA, damage []image.Rectangle) bool {
	if img == nil || img.Rect.Empty() || max(img.Rect.Dx(), img.Rect.Dy()) > int(p.maxTexture) || !p.ctx.MakeCurrent() {
		return false
	}
	size := img.Rect.Size()
	p.setUnpackLength(int32(img.Stride / 4))
	if size != p.size {
		p.resize(size, unsafe.Pointer(unsafe.SliceData(img.Pix)))
	} else {
		for _, r := range damage {
			r = r.Intersect(img.Rect)
			if !r.Empty() {
				offset := img.PixOffset(r.Min.X, r.Min.Y)
				p.upload(r.Sub(img.Rect.Min), &img.Pix[offset])
			}
		}
	}
	runtime.KeepAlive(img)
	p.fullDamage = true
	drawn := p.drawFrame()
	p.damage, p.fullDamage = p.damage[:0], false
	return drawn && p.ctx.Swap()
}

func (s *gpuState) presentRegions(size image.Point, paint func(func(*image.RGBA))) bool {
	p := s.renderer
	if p == nil {
		return false
	}
	if size.X < 1 || size.Y < 1 || max(size.X, size.Y) > int(p.maxTexture) || !p.ctx.MakeCurrent() {
		s.close()
		return false
	}
	if size != p.size {
		p.resize(size, nil)
		p.fullDamage = true
	}
	paint(p.uploadRegion)
	if !p.drawFrame() || !p.swap() {
		s.close()
		return false
	}
	return true
}

func (p *glPresenter) drawFrame() bool {
	if c, ok := p.ctx.(interface{ drawBackBuffer(*glPresenter) bool }); ok {
		return c.drawBackBuffer(p)
	}
	p.gl.drawArrays(4, 0, 3)
	return p.gl.getError() == 0
}

// Beyond this many rects the display server gets one bounding rect.
const maxSwapRects = 16

func (p *glPresenter) swap() bool {
	damage := p.damage
	p.damage = p.damage[:0]
	full := p.fullDamage || p.custom
	p.fullDamage = false
	swapper, ok := p.ctx.(interface{ SwapDamage([]int32) bool })
	if full || !ok || len(damage) == 0 {
		return p.ctx.Swap()
	}
	if len(damage) > maxSwapRects {
		for _, r := range damage[1:] {
			damage[0] = damage[0].Union(r)
		}
		damage = damage[:1]
	}
	rects := p.swapRects[:0]
	for _, r := range damage {
		rects = append(rects, int32(r.Min.X), int32(p.size.Y-r.Max.Y), int32(r.Dx()), int32(r.Dy()))
	}
	p.swapRects = rects
	return swapper.SwapDamage(rects)
}

func (p *glPresenter) uploadRegion(img *image.RGBA) {
	if img == nil {
		return
	}
	r := img.Rect.Intersect(image.Rectangle{Max: p.size})
	if r.Empty() {
		return
	}
	p.damage = append(p.damage, r)
	length := int32(0)
	if img.Stride != 4*r.Dx() {
		length = int32(img.Stride / 4)
	}
	p.setUnpackLength(length)
	p.upload(r, &img.Pix[img.PixOffset(r.Min.X, r.Min.Y)])
	runtime.KeepAlive(img)
}

func (p *glPresenter) upload(r image.Rectangle, pixels *byte) {
	p.blitArgs = [10]uintptr{0x0de1, 0, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()), 0x1908, 0x1401, uintptr(unsafe.Pointer(pixels))}
	purego.SyscallN(p.gl.textureSubImage, p.blitArgs[:9]...)
	runtime.KeepAlive(pixels)
}

// These integer-only GL calls run several times per scroll. Reuse their
// argument storage instead of boxing coordinates for each driver call.
func (p *glPresenter) bindFramebuffer(target, buffer uint32) {
	if target == 0x8ca8 && p.readBuffer == buffer { // GL_READ_FRAMEBUFFER
		return
	}
	if target != 0x8ca9 { // A draw-only bind leaves the read framebuffer alone.
		p.readBuffer = buffer
	}
	p.blitArgs[0], p.blitArgs[1] = uintptr(target), uintptr(buffer)
	purego.SyscallN(p.gl.bindFramebuffer, p.blitArgs[:2]...)
}

func (p *glPresenter) blit(src, dst image.Rectangle) {
	p.blitArgs = [10]uintptr{
		uintptr(src.Min.X), uintptr(src.Min.Y), uintptr(src.Max.X), uintptr(src.Max.Y),
		uintptr(dst.Min.X), uintptr(dst.Min.Y), uintptr(dst.Max.X), uintptr(dst.Max.Y),
		0x4000, 0x2600, // GL_COLOR_BUFFER_BIT, GL_NEAREST
	}
	purego.SyscallN(p.gl.blitFramebuffer, p.blitArgs[:]...)
}

// scroll moves retained frame pixels within r by delta on the GPU. A blit
// between overlapping areas of one texture is undefined, so it goes through
// a scratch texture.
func (s *gpuState) scroll(r image.Rectangle, delta image.Point) bool {
	p := s.renderer
	if p == nil || p.copyFailed || p.gl.blitFramebuffer == 0 || delta.X != 0 || delta.Y == 0 || !r.In(image.Rectangle{Max: p.size}) {
		return false
	}
	dst := r.Intersect(r.Add(delta))
	if dst.Empty() || !p.ctx.MakeCurrent() {
		return false
	}
	src := dst.Sub(delta)
	g := &p.gl
	const read, draw, color, texture2D = 0x8ca8, 0x8ca9, 0x8ce0, 0x0de1
	if p.framebuffers[0] == 0 {
		g.genFramebuffers(2, &p.framebuffers[0])
	}
	if p.scratch == 0 {
		g.genTextures(1, &p.scratch)
		g.bindTexture(texture2D, p.scratch)
		p.textureParameters()
		g.bindTexture(texture2D, p.texture)
	}
	whole := r == (image.Rectangle{Max: p.size})
	size := src.Size()
	if whole {
		size = p.size
	}
	grew := size.X > p.scratchSize.X || size.Y > p.scratchSize.Y
	if grew {
		p.scratchSize = image.Pt(max(size.X, p.scratchSize.X), max(size.Y, p.scratchSize.Y))
		g.bindTexture(texture2D, p.scratch)
		g.textureImage(texture2D, 0, 0x8058, int32(p.scratchSize.X), int32(p.scratchSize.Y), 0, 0x1908, 0x1401, nil)
		g.bindTexture(texture2D, p.texture)
	}
	frame, scratch := p.framebuffers[0], p.framebuffers[1]
	p.bindFramebuffer(read, frame)
	p.bindFramebuffer(draw, scratch)
	if grew {
		// Attachments survive unbinding. Validate them when allocating storage,
		// so steady scrolling only needs to bind and copy.
		g.framebufferTexture(read, color, texture2D, p.texture, 0)
		g.framebufferTexture(draw, color, texture2D, p.scratch, 0)
		if g.framebufferStatus(read) != 0x8cd5 || g.framebufferStatus(draw) != 0x8cd5 {
			p.bindFramebuffer(0x8d40, 0)
			p.copyFailed = true
			g.getError() // A failed copy must not poison the fallback repaint.
			return false
		}
	}
	if whole && p.scratchSize == p.size {
		// A full-window scroll can make the destination the next frame
		// texture. Preserve the exposed edge for callers that repaint later.
		p.blit(src, dst)
		edge := r
		if delta.Y > 0 {
			edge.Max.Y = dst.Min.Y
		} else {
			edge.Min.Y = dst.Max.Y
		}
		p.blit(edge, edge)
		p.texture, p.scratch = p.scratch, p.texture
		p.framebuffers[0], p.framebuffers[1] = scratch, frame
		g.bindTexture(texture2D, p.texture)
	} else {
		scratchRect := image.Rectangle{Max: src.Size()}
		p.blit(src, scratchRect)
		p.bindFramebuffer(read, scratch)
		p.bindFramebuffer(draw, frame)
		p.blit(scratchRect, dst)
	}
	p.bindFramebuffer(0x8d40, 0)
	if g.getError() != 0 {
		p.copyFailed = true
		return false
	}
	p.damage = append(p.damage, dst)
	return true
}
