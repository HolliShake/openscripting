package main

import (
	"runtime"
	"strconv"
)

const nativeJITCallThreshold = 32

const nativeJITMaxStack = 5

type nativeIROp struct {
	opcode     int32
	slot       int32
	value      float64
	target     int
	stackDepth int
}

type nativeJITFunction struct {
	calls     uint32
	attempted bool
	code      nativeCode
}

type nativeCode interface {
	run(left, right float64) (float64, bool)
	release()
}

type nativeBranchPatch struct {
	offset        int
	target        int
	conditional   bool
	conditionCode uint32
}

func patchNativeBranches(code []byte, codeOffsets []int, patches []nativeBranchPatch, patch func([]byte, nativeBranchPatch, int) bool) bool {
	for _, branch := range patches {
		if branch.target < 0 || branch.target >= len(codeOffsets) {
			return false
		}
		if !patch(code, branch, codeOffsets[branch.target]) {
			return false
		}
	}
	return true
}

func nativeJITPlatformSupported() bool {
	isWindows := runtime.GOOS == "windows"
	isMacOS := runtime.GOOS == "darwin"
	isLinux := runtime.GOOS == "linux"
	is64Bit := strconv.IntSize == 64

	switch runtime.GOARCH {
	case "amd64":
		return nativeJITAllocatorAvailable && isWindows && is64Bit
	case "arm64":
		return nativeJITAllocatorAvailable && (isWindows || isMacOS) && is64Bit
	case "386":
		return nativeJITAllocatorAvailable && isWindows && !is64Bit
	case "arm":
		return nativeJITAllocatorAvailable && isLinux && !is64Bit
	default:
		return false
	}
}

func nativeFunctionCode(fn *Function) ([]byte, bool) {
	ops, valid := nativeFunctionIR(fn)
	if !valid {
		return nil, false
	}
	return emitNativeCode(ops)
}

func nativeFunctionIR(fn *Function) ([]nativeIROp, bool) {
	if fn == nil || fn.Async || fn.ClassConstructor || fn.Module != nil ||
		fn.ParamCount != 2 || fn.LocalCount != 2 || fn.VarArgIndex != -1 ||
		len(fn.CaptureMeta) != 0 || len(fn.TryCatchTable) != 0 {
		return nil, false
	}
	bytecode := fn.Bytecode
	offset := 0
	if len(bytecode) >= 4 && bytecode[0] == OpStoreLocal && bytecode[1] == 1 &&
		bytecode[2] == OpStoreLocal && bytecode[3] == 0 {
		offset = 4
	}
	if offset == len(bytecode) {
		return nil, false
	}

	var ops []nativeIROp
	bytecodeToOp := make(map[int]int, len(bytecode)-offset)
	var branchTargets []int
	for ip := offset; ip < len(bytecode); {
		bytecodeOffset := ip
		op := bytecode[ip]
		ip++
		bytecodeToOp[bytecodeOffset] = len(ops)
		switch op {
		case OpLoadVar:
			if ip >= len(bytecode) {
				return nil, false
			}
			slot := bytecode[ip]
			ip++
			if slot < 0 || slot > 1 {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op, slot: slot})
		case OpLoadNum:
			if ip+1 >= len(bytecode) {
				return nil, false
			}
			value := CombineFloat(bytecode[ip], bytecode[ip+1])
			ip += 2
			ops = append(ops, nativeIROp{opcode: op, value: value})
		case OpLoadConst:
			if ip >= len(bytecode) {
				return nil, false
			}
			index := int(bytecode[ip])
			ip++
			if index < 0 || index >= len(fn.Constants) || fn.Constants[index].kind != kNum {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op, value: fn.Constants[index].num})
		case OpUnaryPos:
		case OpUnaryNeg:
			ops = append(ops, nativeIROp{opcode: op})
		case OpUnaryBitNot:
			ops = append(ops, nativeIROp{opcode: op})
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv, OpBinMod,
			OpBinBitAnd, OpBinBitXor, OpBinBitOr, OpBinShiftLeft, OpBinShiftRight:
			ops = append(ops, nativeIROp{opcode: op})
		case OpCaseEqual, OpPopTop:
			ops = append(ops, nativeIROp{opcode: op})
		case OpJmp, OpPopJmpIfFalse, OpPopJmpIfTrue:
			if ip >= len(bytecode) {
				return nil, false
			}
			target := int(bytecode[ip])
			ip++
			if target <= bytecodeOffset || target > len(bytecode) {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op})
			branchTargets = append(branchTargets, target)
		case OpReturn:
			ops = append(ops, nativeIROp{opcode: op})
		default:
			return nil, false
		}
	}
	bytecodeToOp[len(bytecode)] = len(ops)
	branchIndex := 0
	for index := range ops {
		if ops[index].opcode == OpJmp || ops[index].opcode == OpPopJmpIfFalse || ops[index].opcode == OpPopJmpIfTrue {
			target, exists := bytecodeToOp[branchTargets[branchIndex]]
			if !exists {
				return nil, false
			}
			ops[index].target = target
			branchIndex++
		}
	}
	if !nativeIRHasValidControlFlow(ops) {
		return nil, false
	}
	return ops, true
}

func nativeIRHasValidControlFlow(ops []nativeIROp) bool {
	if len(ops) == 0 {
		return false
	}
	depths := make([]int, len(ops))
	for index := range depths {
		depths[index] = -1
	}
	depths[0] = 0
	work := []int{0}
	hasReturn := false
	for len(work) > 0 {
		index := work[len(work)-1]
		work = work[:len(work)-1]
		depth := depths[index]
		ops[index].stackDepth = depth
		op := ops[index]
		switch op.opcode {
		case OpLoadVar, OpLoadNum, OpLoadConst:
			depth++
		case OpUnaryPos, OpUnaryNeg, OpUnaryBitNot:
			if depth < 1 {
				return false
			}
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv, OpBinMod,
			OpBinBitAnd, OpBinBitXor, OpBinBitOr, OpBinShiftLeft, OpBinShiftRight:
			if depth < 2 {
				return false
			}
			depth--
		case OpCaseEqual:
			if depth < 2 {
				return false
			}
		case OpPopTop:
			if depth < 1 {
				return false
			}
			depth--
		case OpPopJmpIfFalse, OpPopJmpIfTrue:
			if depth < 1 {
				return false
			}
			depth--
		case OpReturn:
			if depth != 1 {
				return false
			}
			hasReturn = true
			continue
		default:
			return false
		}
		if depth > nativeJITMaxStack {
			return false
		}
		if op.opcode == OpJmp || op.opcode == OpPopJmpIfFalse || op.opcode == OpPopJmpIfTrue {
			if !nativeIRMergeDepth(depths, &work, op.target, depth) {
				return false
			}
			if op.opcode == OpJmp {
				continue
			}
		}
		if index+1 >= len(ops) || !nativeIRMergeDepth(depths, &work, index+1, depth) {
			return false
		}
	}
	return hasReturn
}

func nativeIRMergeDepth(depths []int, work *[]int, index, depth int) bool {
	if index < 0 || index >= len(depths) {
		return false
	}
	if depths[index] == -1 {
		depths[index] = depth
		*work = append(*work, index)
		return true
	}
	return depths[index] == depth
}
