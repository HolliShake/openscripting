//go:build arm64

package main

import "math"

const nativeJITEmitterAvailable = true

func emitNativeCode(ops []nativeIROp) ([]byte, bool) {
	var code []byte
	var zeroBranches []int
	var branchPatches []nativeBranchPatch
	codeOffsets := make([]int, len(ops)+1)
	for index, op := range ops {
		codeOffsets[index] = len(code)
		stackDepth := op.stackDepth
		switch op.opcode {
		case OpLoadVar:
			rn := uint32(op.slot)
			code = appendARM64Word(code, 0xFD400000|(rn<<5)|uint32(stackDepth))
		case OpLoadNum, OpLoadConst:
			code = appendARM64Float(code, stackDepth, op.value)
		case OpUnaryPos:
		case OpUnaryNeg:
			reg := uint32(stackDepth - 1)
			code = appendARM64Word(code, 0x1E614000|(reg<<5)|reg)
		case OpUnaryBitNot:
			reg := uint32(stackDepth - 1)
			code = appendARM64Word(code, 0x9E780000|(reg<<5)|9) // convert the value to a signed integer
			code = appendARM64Word(code, 0xAA2903E9)            // invert all integer bits
			code = appendARM64Word(code, 0x9E620000|(9<<5)|reg) // convert the result back to floating point
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv,
			OpBinBitAnd, OpBinBitXor, OpBinBitOr:
			left := uint32(stackDepth - 2)
			right := uint32(stackDepth - 1)
			base := uint32(0)
			switch op.opcode {
			case OpBinAdd:
				base = 0x1E602800
			case OpBinSub:
				base = 0x1E603800
			case OpBinMul:
				base = 0x1E600800
			case OpBinDiv:
				code = appendARM64Word(code, 0x1E602008|(right<<5)) // compare the divisor with zero
				zeroBranches = append(zeroBranches, len(code))
				code = appendARM64Word(code, 0x54000000) // branch to the divide-by-zero fallback
				base = 0x1E601800
			case OpBinBitAnd, OpBinBitXor, OpBinBitOr:
				code = appendARM64Word(code, 0x9E780000|(left<<5)|9)   // convert the left value to a signed integer
				code = appendARM64Word(code, 0x9E780000|(right<<5)|10) // convert the right value to a signed integer
				integerOpcode := uint32(0x8A0A0129)                    // apply the bitwise operation
				if op.opcode == OpBinBitXor {
					integerOpcode = 0xCA0A0129 // eor X9, X9, X10
				} else if op.opcode == OpBinBitOr {
					integerOpcode = 0xAA0A0129 // orr X9, X9, X10
				}
				code = appendARM64Word(code, integerOpcode)
				code = appendARM64Word(code, 0x9E620000|(9<<5)|left) // convert the result back to floating point
				continue
			}
			code = appendARM64Word(code, base|(right<<16)|(left<<5)|left)
		case OpCaseEqual:
			left := uint32(stackDepth - 2)
			right := uint32(stackDepth - 1)
			code = appendARM64Word(code, 0x1E602000|(right<<16)|(left<<5)) // compare the floating-point values
			code = appendARM64Word(code, 0x9A9F17E9)                       // record equality; unordered comparisons are false
			code = appendARM64Word(code, 0x9E630000|(9<<5)|right)          // convert the boolean result to floating point
		case OpPopTop:
		case OpJmp:
			offset := len(code)
			code = appendARM64Word(code, 0x14000000)
			branchPatches = append(branchPatches, nativeBranchPatch{offset: offset, target: op.target})
		case OpPopJmpIfFalse, OpPopJmpIfTrue:
			register := uint32(stackDepth - 1)
			code = appendARM64Word(code, 0x1E602008|(register<<5)) // compare the value with zero
			if op.opcode == OpPopJmpIfFalse {
				offset := len(code)
				code = appendARM64Word(code, 0x54000000) // unordered NaN is truthy
				branchPatches = append(branchPatches, nativeBranchPatch{offset: offset, target: op.target, conditional: true})
			} else {
				for _, condition := range []uint32{6, 1} { // unordered and nonzero values are truthy
					offset := len(code)
					code = appendARM64Word(code, 0x54000000|condition)
					branchPatches = append(branchPatches, nativeBranchPatch{offset: offset, target: op.target, conditional: true, conditionCode: condition})
				}
			}
		case OpReturn:
			code = appendARM64Word(code, 0xFD000040) // store the returned floating-point value
			code = appendARM64Word(code, 0xD65F03C0) // return to the caller
		default:
			return nil, false
		}
	}
	codeOffsets[len(ops)] = len(code)
	code = appendARM64Word(code, 0xD65F03C0)
	if !patchNativeBranches(code, codeOffsets, branchPatches, func(code []byte, patch nativeBranchPatch, targetOffset int) bool {
		words := int32((targetOffset - patch.offset) / 4)
		instruction := readARM64Word(code, patch.offset)
		if patch.conditional {
			instruction = (instruction & 0xFF00001F) | (uint32(words&0x7FFFF) << 5) | patch.conditionCode
		} else {
			instruction = (instruction & 0xFC000000) | (uint32(words) & 0x03FFFFFF)
		}
		putARM64Word(code, patch.offset, instruction)
		return true
	}) {
		return nil, false
	}
	if len(zeroBranches) > 0 {
		failureOffset := len(code)
		code = appendARM64Word(code, 0xB900007F) // str WZR, [X3]
		code = appendARM64Word(code, 0xD65F03C0)
		for _, branchOffset := range zeroBranches {
			words := int32((failureOffset - branchOffset) / 4)
			instruction := uint32(0x54000000) | (uint32(words&0x7FFFF) << 5)
			putARM64Word(code, branchOffset, instruction)
		}
	}
	return code, true
}

func appendARM64Float(code []byte, register int, value float64) []byte {
	bits := math.Float64bits(value)
	code = appendARM64Word(code, 0xD2800000|uint32(bits&0xFFFF)<<5|16)
	for shift, base := uint(16), uint32(0xF2A00000); shift < 64; shift, base = shift+16, base+0x200000 {
		code = appendARM64Word(code, base|uint32((bits>>shift)&0xFFFF)<<5|16)
	}
	return appendARM64Word(code, 0x9E670000|(16<<5)|uint32(register)) // move the bit pattern into the floating-point register
}

func appendARM64Word(code []byte, word uint32) []byte {
	return append(code, byte(word), byte(word>>8), byte(word>>16), byte(word>>24))
}

func putARM64Word(code []byte, offset int, word uint32) {
	code[offset] = byte(word)
	code[offset+1] = byte(word >> 8)
	code[offset+2] = byte(word >> 16)
	code[offset+3] = byte(word >> 24)
}

func readARM64Word(code []byte, offset int) uint32 {
	return uint32(code[offset]) | uint32(code[offset+1])<<8 | uint32(code[offset+2])<<16 | uint32(code[offset+3])<<24
}
