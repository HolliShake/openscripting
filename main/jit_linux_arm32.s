//go:build linux && arm.7

#include "textflag.h"

TEXT ·callARM32(SB),NOSPLIT,$0-20
	MOVW	code+0(FP), R12
	MOVW	left+4(FP), R0
	MOVW	right+8(FP), R1
	MOVW	result+12(FP), R2
	MOVW	status+16(FP), R3
	SUB	$8, R13
	MOVW	R4, 0(R13)
	MOVW	R14, 4(R13)
	MOVW	R14, R4
	BL	(R12)
	MOVW	R4, R14
	MOVW	0(R13), R4
	ADD	$8, R13
	RET
