//go:build 386

package main

import "math"

const nativeJITEmitterAvailable = true

func emitNativeCode(ops []nativeIROp) ([]byte, bool) {
	code := []byte{0x55, 0x89, 0xE5, 0x83, 0xEC, 0x08} // push ebp; mov ebp, esp; reserve rounding slot
	var divideByZeroBranches []int
	type branchPatch struct {
		displacement int
		target       int
	}
	var branchPatches []branchPatch
	codeOffsets := make([]int, len(ops)+1)
	for index, op := range ops {
		codeOffsets[index] = len(code)
		switch op.opcode {
		case OpLoadVar:
			argumentOffset := byte(8 + op.slot*4)
			code = append(code, 0x8B, 0x45, argumentOffset, 0xDD, 0x00) // load pointer; fld qword [eax]
		case OpLoadNum, OpLoadConst:
			bits := math.Float64bits(op.value)
			code = append(code, 0x68)
			code = appendUint32(code, uint32(bits>>32))
			code = append(code, 0x68)
			code = appendUint32(code, uint32(bits))
			code = append(code, 0xDD, 0x04, 0x24, 0x83, 0xC4, 0x08) // fld [esp]; add esp, 8
		case OpUnaryPos:
		case OpUnaryNeg:
			code = append(code, 0xD9, 0xE0) // fchs
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv, OpBinMod:
			if op.opcode == OpBinDiv || op.opcode == OpBinMod {
				code = append(code, 0xD9, 0xE4, 0xDF, 0xE0, 0x9E, 0x7A, 0x06, 0x0F, 0x84, 0, 0, 0, 0)
				divideByZeroBranches = append(divideByZeroBranches, len(code)-4)
			}
			if op.opcode == OpBinMod {
				code = append(code, 0xD9, 0xC9) // fxch st(1)
				loopOffset := len(code)
				code = append(code, 0xD9, 0xF8, 0xDF, 0xE0, 0x9E, 0x7A, byte(loopOffset-(len(code)+2)))
				code = append(code, 0xDD, 0xD9) // fstp st(1), leaving remainder on top
			} else {
				switch op.opcode {
				case OpBinAdd:
					code = append(code, 0xDE, 0xC1) // faddp st(1), st(0)
				case OpBinSub:
					code = append(code, 0xDE, 0xE9) // fsubp st(1), st(0)
				case OpBinMul:
					code = append(code, 0xDE, 0xC9) // fmulp st(1), st(0)
				case OpBinDiv:
					code = append(code, 0xDE, 0xF9) // fdivp st(1), st(0)
				}
			}
			code = append(code, 0xDD, 0x1C, 0x24, 0xDD, 0x04, 0x24) // round through float64 scratch
		case OpCaseEqual:
			code = append(code,
				0xDF, 0xE9, // fucomip st(0), st(1); pop rhs
				0x0F, 0x94, 0xC0, // sete al
				0x0F, 0x9B, 0xC2, // setnp dl; NaN is not equal
				0x20, 0xD0, // and al, dl
				0x50, 0xDB, 0x04, 0x24, 0x83, 0xC4, 0x04, // push eax; fild dword [esp]; add esp, 4
			)
		case OpPopTop:
			code = append(code, 0xDD, 0xD8) // fstp st(0)
		case OpJmp:
			code = append(code, 0xE9, 0, 0, 0, 0)
			branchPatches = append(branchPatches, branchPatch{displacement: len(code) - 4, target: op.target})
		case OpPopJmpIfFalse, OpPopJmpIfTrue:
			code = append(code, 0xD9, 0xE4, 0xDF, 0xE0, 0x9E, 0xDD, 0xD8) // ftst; fnstsw ax; sahf; pop condition
			if op.opcode == OpPopJmpIfFalse {
				code = append(code, 0x7A, 0x06) // jp past jz: unordered numeric NaN is truthy
				code = append(code, 0x0F, 0x84, 0, 0, 0, 0)
				branchPatches = append(branchPatches, branchPatch{displacement: len(code) - 4, target: op.target})
			} else {
				code = append(code, 0x0F, 0x8A, 0, 0, 0, 0) // jp target
				branchPatches = append(branchPatches, branchPatch{displacement: len(code) - 4, target: op.target})
				code = append(code, 0x0F, 0x85, 0, 0, 0, 0) // jne target
				branchPatches = append(branchPatches, branchPatch{displacement: len(code) - 4, target: op.target})
			}
		case OpReturn:
			code = append(code, 0x8B, 0x45, 0x10, 0xDD, 0x18) // fstp qword [result]
			code = append386Return(code)
		default:
			return nil, false
		}
	}
	codeOffsets[len(ops)] = len(code)
	code = append(code, 0xC3)
	for _, patch := range branchPatches {
		if patch.target < 0 || patch.target >= len(codeOffsets) {
			return nil, false
		}
		displacement := int32(codeOffsets[patch.target] - (patch.displacement + 4))
		code[patch.displacement] = byte(displacement)
		code[patch.displacement+1] = byte(displacement >> 8)
		code[patch.displacement+2] = byte(displacement >> 16)
		code[patch.displacement+3] = byte(displacement >> 24)
	}
	if len(divideByZeroBranches) > 0 {
		failureOffset := len(code)
		code = append(code, 0x8B, 0x45, 0x14, 0xC7, 0x00, 0, 0, 0, 0)
		code = append386Return(code)
		for _, branch := range divideByZeroBranches {
			displacement := int32(failureOffset - (branch + 4))
			code[branch] = byte(displacement)
			code[branch+1] = byte(displacement >> 8)
			code[branch+2] = byte(displacement >> 16)
			code[branch+3] = byte(displacement >> 24)
		}
	}
	return code, true
}

func append386Return(code []byte) []byte {
	return append(code, 0x89, 0xEC, 0x5D, 0xC2, 0x10, 0x00) // mov esp, ebp; pop ebp; ret 16
}

func appendUint32(code []byte, value uint32) []byte {
	return append(code, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}
