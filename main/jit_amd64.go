//go:build amd64

package main

import "math"

func emitNativeCode(ops []nativeIROp) ([]byte, bool) {
	var code []byte
	var divideByZeroBranches []int
	stackDepth := 0
	for _, op := range ops {
		switch op.opcode {
		case OpLoadVar:
			base := byte(1)
			if op.slot == 1 {
				base = 2
			}
			code = append(code, 0xF2, 0x0F, 0x10, byte(stackDepth<<3)|base)
			stackDepth++
		case OpLoadNum, OpLoadConst:
			code = appendAMD64Float(code, stackDepth, op.value)
			stackDepth++
		case OpUnaryPos:
		case OpUnaryNeg:
			register := byte(stackDepth - 1)
			code = append(code, 0x66, 0x48, 0x0F, 0x7E, 0xC0|(register<<3))
			code = append(code, 0x49, 0xBA, 0, 0, 0, 0, 0, 0, 0, 0x80, 0x4C, 0x31, 0xD0)
			code = append(code, 0x66, 0x48, 0x0F, 0x6E, 0xC0|(register<<3))
		case OpUnaryBitNot:
			register := byte(stackDepth - 1)
			code = append(code,
				0xF2, 0x48, 0x0F, 0x2C, 0xC0|register, // cvttsd2si rax, xmmN
				0x48, 0xF7, 0xD0, // not rax
				0xF2, 0x48, 0x0F, 0x2A, 0xC0|(register<<3), // cvtsi2sd xmmN, rax
			)
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv, OpBinMod,
			OpBinBitAnd, OpBinBitXor, OpBinBitOr, OpBinShiftLeft, OpBinShiftRight:
			if op.opcode == OpBinBitAnd || op.opcode == OpBinBitXor || op.opcode == OpBinBitOr ||
				op.opcode == OpBinShiftLeft || op.opcode == OpBinShiftRight {
				left := byte(stackDepth - 2)
				right := byte(stackDepth - 1)
				code = append(code,
					0xF2, 0x48, 0x0F, 0x2C, 0xC0|left, // cvttsd2si rax, xmmLeft
					0xF2, 0x4C, 0x0F, 0x2C, 0xD0|right, // cvttsd2si r10, xmmRight
				)
				switch op.opcode {
				case OpBinBitAnd:
					code = append(code, 0x4C, 0x21, 0xD0) // and rax, r10
				case OpBinBitXor:
					code = append(code, 0x4C, 0x31, 0xD0) // xor rax, r10
				case OpBinBitOr:
					code = append(code, 0x4C, 0x09, 0xD0) // or rax, r10
				case OpBinShiftLeft, OpBinShiftRight:
					code = append(code,
						0x49, 0x83, 0xFA, 0x3F, // cmp r10, 63; Go shifts >=64 produce zero
						0x77, 0x08, // ja zero result
						0x44, 0x89, 0xD1, // mov ecx, r10d
					)
					if op.opcode == OpBinShiftLeft {
						code = append(code, 0x48, 0xD3, 0xE0) // shl rax, cl
					} else {
						code = append(code, 0x48, 0xD3, 0xF8) // sar rax, cl
					}
					code = append(code, 0xEB, 0x03, 0x48, 0x31, 0xC0) // skip xor; zero rax
				}
				code = append(code, 0xF2, 0x48, 0x0F, 0x2A, 0xC0|(left<<3)) // cvtsi2sd xmmLeft, rax
				stackDepth--
				continue
			}
			left := byte(stackDepth - 2)
			right := byte(stackDepth - 1)
			operation := byte(0)
			switch op.opcode {
			case OpBinAdd:
				operation = 0x58
			case OpBinSub:
				operation = 0x5C
			case OpBinMul:
				operation = 0x59
			case OpBinDiv:
				operation = 0x5E
			case OpBinMod:
				code = append(code,
					0x66, 0x45, 0x0F, 0xEF, 0xFF,
					0x66, 0x41, 0x0F, 0x2E, 0xC0|(right<<3)|0x07,
					0x7A, 0x06,
					0x0F, 0x84, 0, 0, 0, 0,
				)
				divideByZeroBranches = append(divideByZeroBranches, len(code)-4)
			}
			if op.opcode == OpBinDiv {
				code = appendAMD64ZeroGuard(code, right)
				divideByZeroBranches = append(divideByZeroBranches, len(code)-4)
			}
			if op.opcode == OpBinMod {
				code = appendAMD64ZeroGuard(code, right)
				divideByZeroBranches = append(divideByZeroBranches, len(code)-4)
			}
			if op.opcode == OpBinMod {
				code = append(code,
					0x66, 0x0F, 0x28, 0xE8|left,
					0xF2, 0x0F, 0x5E, 0xE8|right,
					0xF2, 0x48, 0x0F, 0x2C, 0xC5,
					0xF2, 0x48, 0x0F, 0x2A, 0xE8,
					0xF2, 0x0F, 0x59, 0xE8|right,
					0xF2, 0x0F, 0x5C, 0xC0|(left<<3)|5,
				)
			} else {
				code = append(code, 0xF2, 0x0F, operation, 0xC0|(left<<3)|right)
			}
			stackDepth--
		case OpReturn:
			code = append(code, 0xF2, 0x41, 0x0F, 0x11, 0x00, 0xC3)
			if len(divideByZeroBranches) > 0 {
				failureOffset := len(code)
				code = append(code, 0x49, 0xC7, 0x01, 0, 0, 0, 0, 0xC3)
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

func appendAMD64ZeroGuard(code []byte, register byte) []byte {
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, 0xC0|(register<<3))
	code = append(code, 0x49, 0xBA, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F)
	code = append(code, 0x4C, 0x21, 0xD0, 0x48, 0x85, 0xC0, 0x0F, 0x84, 0, 0, 0, 0)
	return code
}

func appendAMD64Float(code []byte, register int, value float64) []byte {
	bits := math.Float64bits(value)
	code = append(code, 0x48, 0xB8)
	for shift := uint(0); shift < 64; shift += 8 {
		code = append(code, byte(bits>>shift))
	}
	return append(code, 0x66, 0x48, 0x0F, 0x6E, 0xC0|(byte(register)<<3))
}
