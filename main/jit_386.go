//go:build 386

package main

import "math"

func emitNativeCode(ops []nativeIROp) ([]byte, bool) {
	code := []byte{0x55, 0x89, 0xE5, 0x83, 0xEC, 0x08} // push ebp; mov ebp, esp; reserve rounding slot
	var divideByZeroBranches []int
	stackDepth := 0
	for _, op := range ops {
		switch op.opcode {
		case OpLoadVar:
			argumentOffset := byte(8 + op.slot*4)
			code = append(code, 0x8B, 0x45, argumentOffset, 0xDD, 0x00) // load pointer; fld qword [eax]
			stackDepth++
		case OpLoadNum, OpLoadConst:
			bits := math.Float64bits(op.value)
			code = append(code, 0x68)
			code = appendUint32(code, uint32(bits>>32))
			code = append(code, 0x68)
			code = appendUint32(code, uint32(bits))
			code = append(code, 0xDD, 0x04, 0x24, 0x83, 0xC4, 0x08) // fld [esp]; add esp, 8
			stackDepth++
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
			stackDepth--
		case OpReturn:
			code = append(code, 0x8B, 0x45, 0x10, 0xDD, 0x18) // fstp qword [result]
			code = append386Return(code)
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
		default:
			return nil, false
		}
	}
	return nil, false
}

func append386Return(code []byte) []byte {
	return append(code, 0x89, 0xEC, 0x5D, 0xC2, 0x10, 0x00) // mov esp, ebp; pop ebp; ret 16
}

func appendUint32(code []byte, value uint32) []byte {
	return append(code, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}
