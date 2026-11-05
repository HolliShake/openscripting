package main

import "math"

const nativeJITEmitterAvailable = true

func emitNativeCode(ops []nativeIROp) ([]byte, bool) {
	code := []byte{0x55, 0x89, 0xE5, 0x83, 0xEC, 0x08} // push ebp; mov ebp, esp; reserve rounding slot
	var divideByZeroBranches []int
	var branchPatches []nativeBranchPatch
	codeOffsets := make([]int, len(ops)+1)
	appendBranch := func(instruction []byte, target int) {
		code = append(code, instruction...)
		branchPatches = append(branchPatches, nativeBranchPatch{offset: len(code) - 4, target: target})
	}
	for index, op := range ops {
		codeOffsets[index] = len(code)
		switch op.opcode {
		case OpLoadVar:
			argumentOffset := byte(8 + op.slot*4)
			code = append(code, 0x8B, 0x45, argumentOffset, 0xDD, 0x00) // load pointer and floating-point value
		case OpLoadNum, OpLoadConst:
			bits := math.Float64bits(op.value)
			code = append(code, 0x68)
			code = appendUint32(code, uint32(bits>>32))
			code = append(code, 0x68)
			code = appendUint32(code, uint32(bits))
			code = append(code, 0xDD, 0x04, 0x24, 0x83, 0xC4, 0x08) // load floating-point value and release temporary stack space
		case OpUnaryPos:
		case OpUnaryNeg:
			code = append(code, 0xD9, 0xE0) // negate the top floating-point value
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv, OpBinMod:
			if op.opcode == OpBinDiv || op.opcode == OpBinMod {
				code = append(code, 0xD9, 0xE4, 0xDF, 0xE0, 0x9E, 0x7A, 0x06, 0x0F, 0x84, 0, 0, 0, 0)
				divideByZeroBranches = append(divideByZeroBranches, len(code)-4)
			}
			if op.opcode == OpBinMod {
				code = append(code, 0xD9, 0xC9) // swap the top two floating-point values
				loopOffset := len(code)
				code = append(code, 0xD9, 0xF8, 0xDF, 0xE0, 0x9E, 0x7A, byte(loopOffset-(len(code)+2)))
				code = append(code, 0xDD, 0xD9) // discard the divisor and keep the remainder on top
			} else {
				switch op.opcode {
				case OpBinAdd:
					code = append(code, 0xDE, 0xC1) // add values and pop one stack entry
				case OpBinSub:
					code = append(code, 0xDE, 0xE9) // subtract values and pop one stack entry
				case OpBinMul:
					code = append(code, 0xDE, 0xC9) // multiply values and pop one stack entry
				case OpBinDiv:
					code = append(code, 0xDE, 0xF9) // divide values and pop one stack entry
				}
			}
			code = append(code, 0xDD, 0x1C, 0x24, 0xDD, 0x04, 0x24) // round through a float64 scratch value
		case OpCaseEqual:
			code = append(code,
				0xDF, 0xE9, // compare values and pop the right operand
				0x0F, 0x94, 0xC0, // record whether values are equal
				0x0F, 0x9B, 0xC2, // record ordered comparison; NaN is not equal
				0x20, 0xD0, // and al, dl
				0x50, 0xDB, 0x04, 0x24, 0x83, 0xC4, 0x04, // convert the comparison result to floating point
			)
		case OpPopTop:
			code = append(code, 0xDD, 0xD8) // discard the top floating-point value
		case OpJmp:
			appendBranch([]byte{0xE9, 0, 0, 0, 0}, op.target)
		case OpPopJmpIfFalse, OpPopJmpIfTrue:
			code = append(code, 0xD9, 0xE4, 0xDF, 0xE0, 0x9E, 0xDD, 0xD8) // test floating-point truthiness and discard the condition
			if op.opcode == OpPopJmpIfFalse {
				code = append(code, 0x7A, 0x06) // skip the zero check for NaN, which is truthy
				appendBranch([]byte{0x0F, 0x84, 0, 0, 0, 0}, op.target)
			} else {
				appendBranch([]byte{0x0F, 0x8A, 0, 0, 0, 0}, op.target)
				appendBranch([]byte{0x0F, 0x85, 0, 0, 0, 0}, op.target)
			}
		case OpReturn:
			code = append(code, 0x8B, 0x45, 0x10, 0xDD, 0x18) // store the returned floating-point value
			code = append386Return(code)
		default:
			return nil, false
		}
	}
	codeOffsets[len(ops)] = len(code)
	code = append(code, 0xC3)
	if !patchNativeBranches(code, codeOffsets, branchPatches, func(code []byte, patch nativeBranchPatch, targetOffset int) bool {
		return patch386Relative32(code, patch.offset, targetOffset)
	}) {
		return nil, false
	}
	if len(divideByZeroBranches) > 0 {
		failureOffset := len(code)
		code = append(code, 0x8B, 0x45, 0x14, 0xC7, 0x00, 0, 0, 0, 0)
		code = append386Return(code)
		for _, branch := range divideByZeroBranches {
			if !patch386Relative32(code, branch, failureOffset) {
				return nil, false
			}
		}
	}
	return code, true
}

func patch386Relative32(code []byte, displacementOffset, targetOffset int) bool {
	if displacementOffset < 0 || displacementOffset+4 > len(code) {
		return false
	}
	displacement := int32(targetOffset - (displacementOffset + 4))
	code[displacementOffset] = byte(displacement)
	code[displacementOffset+1] = byte(displacement >> 8)
	code[displacementOffset+2] = byte(displacement >> 16)
	code[displacementOffset+3] = byte(displacement >> 24)
	return true
}

func append386Return(code []byte) []byte {
	return append(code, 0x89, 0xEC, 0x5D, 0xC2, 0x10, 0x00) // mov esp, ebp; pop ebp; ret 16
}

func appendUint32(code []byte, value uint32) []byte {
	return append(code, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}
