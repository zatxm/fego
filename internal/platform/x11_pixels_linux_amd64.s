//go:build linux && !android && amd64

#include "textflag.h"

// Four unaligned RGBA pixels per SSE2 vector. The X11 visual expects BGRX.
TEXT ·xCopyBlocks(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	PCMPEQL X3, X3
	MOVOU X3, X4
	PSLLL $24, X4         // opaque alpha
	PSRLL $24, X3
	MOVOU X3, X5
	PSLLL $8, X5          // green
	MOVOU X3, X6
	PSLLL $16, X6
	POR X6, X3            // red and blue
loop:
	MOVOU (SI), X0
	MOVOU X0, X1
	MOVOU X0, X2
	PSRLL $16, X0
	PSLLL $16, X1
	POR X1, X0
	PAND X3, X0
	PAND X5, X2
	POR X2, X0
	POR X4, X0
	MOVOU X0, (DI)
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $16, CX
	JNZ loop
	RET
