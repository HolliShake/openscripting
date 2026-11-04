package main

const nativeJITCallThreshold = 32

const nativeJITMaxStack = 5

type nativeIROp struct {
	opcode int32
	slot   int32
	value  float64
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

func (vm *VM) tryNative(fn *Function, args []OSValue) (OSValue, bool) {
	if vm.nativeJIT == nil {
		vm.nativeJIT = make(map[*Function]nativeJITFunction)
	}
	tier := vm.nativeJIT[fn]
	if tier.code == nil && !tier.attempted {
		tier.calls++
		if tier.calls >= nativeJITCallThreshold {
			tier.attempted = true
			if instructions, eligible := nativeFunctionCode(fn); eligible {
				tier.code, _ = allocateNativeCode(instructions)
			}
		}
		vm.nativeJIT[fn] = tier
	}
	if tier.code == nil || len(args) != 2 || args[0].Type != OSVNum || args[1].Type != OSVNum {
		return OSValue{}, false
	}
	result, ok := tier.code.run(args[0].Number, args[1].Number)
	if !ok {
		return OSValue{}, false
	}
	return OSValue{Type: OSVNum, Number: result}, true
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
	stackDepth := 0
	for ip := offset; ip < len(bytecode); {
		op := bytecode[ip]
		ip++
		switch op {
		case OpLoadVar:
			if ip >= len(bytecode) || stackDepth == nativeJITMaxStack {
				return nil, false
			}
			slot := bytecode[ip]
			ip++
			if slot < 0 || slot > 1 {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op, slot: slot})
			stackDepth++
		case OpLoadNum:
			if ip+1 >= len(bytecode) || stackDepth == nativeJITMaxStack {
				return nil, false
			}
			value := CombineFloat(bytecode[ip], bytecode[ip+1])
			ip += 2
			ops = append(ops, nativeIROp{opcode: op, value: value})
			stackDepth++
		case OpLoadConst:
			if ip >= len(bytecode) || stackDepth == nativeJITMaxStack {
				return nil, false
			}
			index := int(bytecode[ip])
			ip++
			if index < 0 || index >= len(fn.Constants) || fn.Constants[index].kind != kNum {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op, value: fn.Constants[index].num})
			stackDepth++
		case OpUnaryPos:
			if stackDepth < 1 {
				return nil, false
			}
		case OpUnaryNeg:
			if stackDepth < 1 {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op})
		case OpBinAdd, OpBinSub, OpBinMul, OpBinDiv, OpBinMod:
			if stackDepth < 2 {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op})
			stackDepth--
		case OpReturn:
			if ip != len(bytecode) || stackDepth != 1 {
				return nil, false
			}
			ops = append(ops, nativeIROp{opcode: op})
			return ops, true
		default:
			return nil, false
		}
	}
	return nil, false
}

func (vm *VM) closeNative() {
	for function, tier := range vm.nativeJIT {
		if tier.code != nil {
			tier.code.release()
		}
		delete(vm.nativeJIT, function)
	}
}
