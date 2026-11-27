//go:build ((linux && !android) || windows || (darwin && !ios)) && (amd64 || arm64)

package platform

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

type glContext interface {
	MakeCurrent() bool
	Swap() bool
	Proc(string) uintptr
	Close()
}

type glAPI struct {
	getString        func(uint32) *byte
	getInteger       func(uint32, *int32)
	getError         func() uint32
	viewport         func(int32, int32, int32, int32)
	createShader     func(uint32) uint32
	shaderSource     func(uint32, int32, **byte, *int32)
	compileShader    func(uint32)
	getShader        func(uint32, uint32, *int32)
	shaderLog        func(uint32, int32, *int32, *byte)
	deleteShader     func(uint32)
	createProgram    func() uint32
	attachShader     func(uint32, uint32)
	linkProgram      func(uint32)
	getProgram       func(uint32, uint32, *int32)
	programLog       func(uint32, int32, *int32, *byte)
	deleteProgram    func(uint32)
	useProgram       func(uint32)
	uniformLocation  func(uint32, string) int32
	uniform1i        func(int32, int32)
	genVertexArrays  func(int32, *uint32)
	bindVertexArray  func(uint32)
	genTextures      func(int32, *uint32)
	bindTexture      func(uint32, uint32)
	textureParameter func(uint32, uint32, int32)
	textureImage     func(uint32, int32, int32, int32, int32, int32, uint32, uint32, unsafe.Pointer)
	textureSubImage  uintptr
	pixelStore       func(uint32, int32)
	drawArrays       func(uint32, int32, int32)
	// Optional: retained-texture scrolling falls back to repainting without them.
	genFramebuffers    func(int32, *uint32)
	bindFramebuffer    uintptr
	framebufferTexture func(uint32, uint32, uint32, uint32, int32)
	framebufferStatus  func(uint32) uint32
	blitFramebuffer    uintptr
}

func loadGL(ctx glContext) (glAPI, error) {
	var g glAPI
	for _, entry := range []struct {
		name string
		fn   any
	}{
		{"glGetString", &g.getString},
		{"glGetIntegerv", &g.getInteger},
		{"glGetError", &g.getError},
		{"glViewport", &g.viewport},
		{"glCreateShader", &g.createShader},
		{"glShaderSource", &g.shaderSource},
		{"glCompileShader", &g.compileShader},
		{"glGetShaderiv", &g.getShader},
		{"glGetShaderInfoLog", &g.shaderLog},
		{"glDeleteShader", &g.deleteShader},
		{"glCreateProgram", &g.createProgram},
		{"glAttachShader", &g.attachShader},
		{"glLinkProgram", &g.linkProgram},
		{"glGetProgramiv", &g.getProgram},
		{"glGetProgramInfoLog", &g.programLog},
		{"glDeleteProgram", &g.deleteProgram},
		{"glUseProgram", &g.useProgram},
		{"glGetUniformLocation", &g.uniformLocation},
		{"glUniform1i", &g.uniform1i},
		{"glGenVertexArrays", &g.genVertexArrays},
		{"glBindVertexArray", &g.bindVertexArray},
		{"glGenTextures", &g.genTextures},
		{"glBindTexture", &g.bindTexture},
		{"glTexParameteri", &g.textureParameter},
		{"glTexImage2D", &g.textureImage},
		{"glPixelStorei", &g.pixelStore},
		{"glDrawArrays", &g.drawArrays},
	} {
		address := ctx.Proc(entry.name)
		if address == 0 {
			return glAPI{}, fmt.Errorf("OpenGL entry point %s unavailable", entry.name)
		}
		purego.RegisterFunc(entry.fn, address)
	}
	g.textureSubImage = ctx.Proc("glTexSubImage2D")
	if g.textureSubImage == 0 {
		return glAPI{}, fmt.Errorf("OpenGL entry point glTexSubImage2D unavailable")
	}
	// Only X11 uses retained-texture scrolling. Other backends repaint on scroll.
	if runtime.GOOS != "linux" {
		return g, nil
	}
	g.bindFramebuffer = ctx.Proc("glBindFramebuffer")
	if g.bindFramebuffer == 0 {
		return g, nil
	}
	optional := []struct {
		name string
		fn   any
	}{
		{"glGenFramebuffers", &g.genFramebuffers},
		{"glFramebufferTexture2D", &g.framebufferTexture},
		{"glCheckFramebufferStatus", &g.framebufferStatus},
	}
	for _, entry := range optional {
		address := ctx.Proc(entry.name)
		if address == 0 {
			return g, nil
		}
		purego.RegisterFunc(entry.fn, address)
	}
	g.blitFramebuffer = ctx.Proc("glBlitFramebuffer")
	return g, nil
}

func glString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for n < 4096 && *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
