package platform

import "errors"

// Both GLSL profiles use top-left UV coordinates matching the Go image.
const FrameVertex = `
out vec2 frameUV;
void main() {
    vec2 corner = vec2(float((gl_VertexID << 1) & 2), float(gl_VertexID & 2));
    frameUV = vec2(corner.x, 1.0 - corner.y);
    gl_Position = vec4(corner * 2.0 - 1.0, 0.0, 1.0);
}
`

const FrameFragment = `
in vec2 frameUV;
uniform sampler2D frameTexture;
out vec4 fragmentColor;
`

const FrameFragmentDefault = `
void main() { fragmentColor = texture(frameTexture, frameUV); }
`

var ErrGPUInactive = errors.New("fego: GPU presentation is inactive")

type ShaderPresenter interface {
	SetFragmentShader(string) error
}
