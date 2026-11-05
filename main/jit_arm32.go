//go:build arm.7

package main

import "math"

const nativeJITEmitterAvailable = true

func emitNativeCode(ops []nativeIROp) ([]byte, bool) {
	code := []byte{
		0xF0, 0x41, 0x2D, 0xE9, // push {r4-r8, lr}
		0x00, 0x40, 0xA0, 0xE1, // mov r4, r0
		0x01, 0x50, 0xA0, 0xE1, // mov r5, r1
		0x02, 0x60, 0xA0, 0xE1, // mov r6, r2
		0x03, 0x70, 0xA0, 0xE1, // mov r7, r3
	}
	codeOffsets := make([]int, len(ops)+1)
	type branchPatch struct {
		offset int
		target int
	}
	var branches []branchPatch
	var divideByZeroBranches []int
	for index, op := range ops {
		codeOffsets[index] = len(code)
		depth := op.stackDepth
		switch op.opcode {
		case OpLoadVar:
			base := uint32(4)
			if op.slot == 1 {
				base = 5
			}
			code = appendARM32Word(code, 0xED900B00|((uint32(depth)&0xF)<<12)|((uint32(depth)&0x10)<<18)|(base<<16)) // load the argument's floating-point value
		case OpLoadNum, OpLoadConst:
			bits := math.Float64bits(op.value)
			code = appendARM32Word(code, arm32MoveLow16(8, uint16(bits)))
			code = appendARM32Word(code, arm32MoveHigh16(8, uint16(bits>>16)))
			code = appendARM32Word(code, arm32MoveLow16(12, uint16(bits>>32)))
			code = appendARM32Word(code, arm32MoveHigh16(12, uint16(bits>>48)))
			code = appendARM32Word(code, arm32VMOVCoreToDouble(depth, 8, 12))
		case OpUnaryPos:
		case OpUnaryNeg:
			reg := uint32(depth)
			code = appendARM32Word(code, 0xEEB10B40|((reg&0xF)<<12)|((reg&0x10)<<18)|((reg&0xF)<<0)|((reg&0x10)<<1)) // negate the floating-point value in place
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv:
			left, right := uint32(depth-2), uint32(depth-1)
			base := uint32(0)
			switch op.opcode {
			case OpBinAdd:
				base = 0xEE300B00
			case OpBinSub:
				base = 0xEE300B40
			case OpBinMul:
				base = 0xEE200B00
			case OpBinDiv:
				code = appendARM32Word(code, arm32CompareFloat64Zero(right))
				code = appendARM32Word(code, 0xEEF1FA10) // transfer floating-point comparison flags to CPU flags
				offset := len(code)
				code = appendARM32Word(code, 0x0A000000) // beq divide-by-zero fallback
				divideByZeroBranches = append(divideByZeroBranches, offset)
				base = 0xEE800B00
			}
			code = appendARM32Word(code, arm32VFPBinary(base, left, left, right))
		case OpCaseEqual:
			left, right := uint32(depth-2), uint32(depth-1)
			code = appendARM32Word(code, arm32CompareFloat64(left, right))
			code = appendARM32Word(code, 0xEEF1FA10) // transfer floating-point comparison flags to CPU flags
			code = appendARM32Word(code, 0xE3A0C000) // mov r12, #0
			code = appendARM32Word(code, 0xE3008000) // set the low word for the floating-point true value
			code = appendARM32Word(code, 0xE3438FF0) // set the high word for the floating-point true value
			code = appendARM32Word(code, 0x13A08000) // clear the result when values differ
			code = appendARM32Word(code, 0x63A08000) // clear the result for unordered comparisons
			code = appendARM32Word(code, arm32VMOVCoreToDouble(int(right), 12, 8))
		case OpPopTop:
		case OpJmp:
			offset := len(code)
			code = appendARM32Word(code, 0xEA000000)
			branches = append(branches, branchPatch{offset: offset, target: op.target})
		case OpPopJmpIfFalse, OpPopJmpIfTrue:
			code = appendARM32Word(code, arm32CompareFloat64Zero(uint32(depth-1)))
			code = appendARM32Word(code, 0xEEF1FA10) // transfer floating-point comparison flags to CPU flags
			if op.opcode == OpPopJmpIfFalse {
				code = appendARM32Word(code, 0x6A000000) // unordered values such as NaN are truthy
				offset := len(code)
				code = appendARM32Word(code, 0x0A000000)
				branches = append(branches, branchPatch{offset: offset, target: op.target})
			} else {
				for _, condition := range []uint32{6, 1} { // unordered and nonzero values are truthy
					offset := len(code)
					code = appendARM32Word(code, (condition<<28)|0x0A000000)
					branches = append(branches, branchPatch{offset: offset, target: op.target})
				}
			}
		case OpReturn:
			code = appendARM32Word(code, 0xED860B00) // store the returned floating-point value
			code = appendARM32Word(code, 0xE8BD81F0) // pop {r4-r8, pc}
		default:
			return nil, false
		}
	}
	codeOffsets[len(ops)] = len(code)
	code = appendARM32Word(code, 0xE8BD81F0) // safe end target
	for _, patch := range branches {
		if patch.target < 0 || patch.target >= len(codeOffsets) {
			return nil, false
		}
		putARM32Word(code, patch.offset, arm32Branch(code[patch.offset:patch.offset+4], codeOffsets[patch.target]-patch.offset))
	}
	if len(divideByZeroBranches) > 0 {
		failureOffset := len(code)
		code = appendARM32Word(code, 0xE3A0C000) // mov r12, #0
		code = appendARM32Word(code, 0xE587C000) // str r12, [r7]
		code = appendARM32Word(code, 0xE8BD81F0) // pop {r4-r8, pc}
		for _, offset := range divideByZeroBranches {
			putARM32Word(code, offset, arm32Branch(code[offset:offset+4], failureOffset-offset))
		}
	}
	return code, true
}

func arm32VFPBinary(base, dest, left, right uint32) uint32 {
	return base | ((dest & 0xF) << 12) | ((dest & 0x10) << 18) |
		((left & 0xF) << 16) | ((left & 0x10) << 3) |
		(right & 0xF) | ((right & 0x10) << 1)
}

func arm32CompareFloat64(left, right uint32) uint32 {
	return 0xEEB40B40 | ((left & 0xF) << 12) | ((left & 0x10) << 18) |
		(right & 0xF) | ((right & 0x10) << 1)
}

func arm32CompareFloat64Zero(reg uint32) uint32 {
	return 0xEEB50B40 | ((reg & 0xF) << 12) | ((reg & 0x10) << 18)
}

func arm32VMOVCoreToDouble(reg, low, high int) uint32 {
	return 0xEC400B10 | (uint32(high) << 16) | (uint32(low) << 12) |
		((uint32(reg) & 0x10) << 1) | (uint32(reg) & 0xF)
}

func arm32MoveLow16(reg int, value uint16) uint32 {
	return 0xE3000000 | (uint32(value&0xF000) << 4) | (uint32(reg) << 12) | uint32(value&0x0FFF)
}

func arm32MoveHigh16(reg int, value uint16) uint32 {
	return 0xE3400000 | (uint32(value&0xF000) << 4) | (uint32(reg) << 12) | uint32(value&0x0FFF)
}

func arm32Branch(existing []byte, byteDelta int) uint32 {
	word := uint32(existing[0]) | uint32(existing[1])<<8 | uint32(existing[2])<<16 | uint32(existing[3])<<24
	words := int32((byteDelta - 8) / 4)
	return (word & 0xFF000000) | (uint32(words) & 0x00FFFFFF)
}

func appendARM32Word(code []byte, word uint32) []byte {
	return append(code, byte(word), byte(word>>8), byte(word>>16), byte(word>>24))
}

func putARM32Word(code []byte, offset int, word uint32) {
	code[offset] = byte(word)
	code[offset+1] = byte(word >> 8)
	code[offset+2] = byte(word >> 16)
	code[offset+3] = byte(word >> 24)
}
