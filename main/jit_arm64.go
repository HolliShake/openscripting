//go:build arm64

package main

import "math"

func emitNativeCode(ops []nativeIROp) ([]byte, bool) {
	var code []byte
	var zeroBranches []int
	stackDepth := 0
	for _, op := range ops {
		switch op.opcode {
		case OpLoadVar:
			rn := uint32(op.slot)
			code = appendARM64Word(code, 0xFD400000|(rn<<5)|uint32(stackDepth))
			stackDepth++
		case OpLoadNum, OpLoadConst:
			code = appendARM64Float(code, stackDepth, op.value)
			stackDepth++
		case OpUnaryPos:
		case OpUnaryNeg:
			reg := uint32(stackDepth - 1)
			code = appendARM64Word(code, 0x1E614000|(reg<<5)|reg)
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv:
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
				code = appendARM64Word(code, 0x1E602008|(right<<5)) // fcmp Dright, #0.0
				zeroBranches = append(zeroBranches, len(code))
				code = appendARM64Word(code, 0x54000000) // b.eq divide-by-zero fallback
				base = 0x1E601800
			}
			code = appendARM64Word(code, base|(right<<16)|(left<<5)|left)
			stackDepth--
		case OpReturn:
			code = appendARM64Word(code, 0xFD000040) // str D0, [X2]
			code = appendARM64Word(code, 0xD65F03C0) // ret
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
		default:
			return nil, false
		}
	}
	return nil, false
}

func appendARM64Float(code []byte, register int, value float64) []byte {
	bits := math.Float64bits(value)
	code = appendARM64Word(code, 0xD2800000|uint32(bits&0xFFFF)<<5|16)
	for shift, base := uint(16), uint32(0xF2A00000); shift < 64; shift, base = shift+16, base+0x200000 {
		code = appendARM64Word(code, base|uint32((bits>>shift)&0xFFFF)<<5|16)
	}
	return appendARM64Word(code, 0x9E670000|(16<<5)|uint32(register)) // fmov Dn, X16
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
