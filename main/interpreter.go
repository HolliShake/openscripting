package main

import (
	"fmt"
	"unsafe"
)

type VM struct {
	Current *Coroutine
	Null    OSValue
	True    OSValue
	False   OSValue
}

func interpreterLoop(state *State, vm *VM) {
	for vm.Current != nil && vm.Current.State != CoroutineStateDead {
		runCoroutine(state, vm, vm.Current)
	}
}

func runCoroutine(state *State, vm *VM, co *Coroutine) {
	if co == nil || co.Frame == nil || co.Frame.Function == nil {
		panic("invalid coroutine or call frame")
	}

	frame := co.Frame
	code := frame.Function.Bytecode

	ip := frame.Ip
	sp := co.Sp
	stack := co.OperandStack
	codeLen := int32(len(code))

	// Read the next bytecode operand safely.
	readByte := func() int32 {
		if ip >= codeLen {
			panic("unexpected end of bytecode")
		}

		value := code[ip]
		ip++
		return value
	}

	pop := func() OSValue {
		if sp <= 0 {
			panic("operand stack underflow")
		}

		sp--
		return stack[sp]
	}

	push := func(value OSValue) {
		if sp >= int32(len(stack)) {
			panic("operand stack overflow")
		}

		stack[sp] = value
		sp++
	}

	// Save the exact execution position and stack pointer.
	saveState := func() {
		frame.Ip = ip
		co.Frame = frame
		co.Sp = sp
	}

	for ip < codeLen {
		op := code[ip]
		ip++

		switch op {
		case OpLoadNum:
			a := readByte()
			b := readByte()

			push(OSValue{
				Type:   OSVInt,
				Number: CombineFloat(a, b),
			})

		case OpLoadStr:
			size := int32(readByte())

			if ip+size > codeLen {
				panic("invalid string literal: exceeds bytecode")
			}

			bytes := make([]int32, size)

			for i := int32(0); i < size; i++ {
				bytes[i] = readByte()
			}

			// Keep the string representation compatible with the
			// existing OSValue implementation.
			value := string(bytes)

			push(OSValue{
				Type:   OSVString,
				Opaque: unsafe.Pointer(&value),
			})

		case OpLoadNull:
			push(vm.Null)

		case OpLoadFunction:
			funcIndex := int(readByte())

			if funcIndex >= len(state.Functions) {
				panic(fmt.Sprintf("invalid function index: %d", funcIndex))
			}

			fn := state.Functions[funcIndex]

			if fn == nil {
				panic(fmt.Sprintf("function at index %d is nil", funcIndex))
			}

			push(OSValue{
				Type:   OSVFunction,
				Opaque: unsafe.Pointer(fn),
			})

		case OpLoadVar:
			{
				varIndex := int(readByte())

				if varIndex >= len(frame.Env) || frame.Env[varIndex] == nil {
					panic(fmt.Sprintf("invalid local variable index: %d of %d", varIndex, len(frame.Env)))
				}

				push(frame.Env[varIndex].Value)
			}

		case OpCall:
			argCount := int32(readByte())

			// Expected stack layout:
			// [..., arg1, arg2, ..., argN, function]
			fnValue := pop()

			if fnValue.Type != OSVFunction || fnValue.Opaque == nil {
				panic("attempted to call a non-function value " + fnValue.String())
			}

			fn := (*Function)(fnValue.Opaque)

			if argCount < 0 || argCount > sp {
				panic("invalid function argument count")
			}

			if fn == nil {
				panic("attempted to call a nil function")
			}

			// Copy arguments in their original order before switching
			// to the new coroutine.
			argStart := sp - argCount
			args := make([]OSValue, argCount)

			copy(args, stack[argStart:sp])

			// Remove arguments from the caller's operand stack.
			sp = argStart

			// Save the caller AFTER consuming its arguments.
			saveState()

			caller := co
			callee := NewCoroutine(caller, fn, 255)

			if callee == nil {
				panic("failed to create coroutine")
			}

			if int32(len(callee.OperandStack)) < argCount {
				panic("callee operand stack is too small for arguments")
			}

			// Place arguments on the callee's stack.
			copy(callee.OperandStack[:argCount], args)
			callee.Sp = argCount

			vm.Current = callee
			return

		case OpBinAdd:
			{
				// Stack layout: [..., left, right]
				rhs := pop()
				lhs := pop()

				push(OSValue{
					Type:   OSVInt,
					Number: lhs.Number + rhs.Number,
				})
			}
		case OpBinSub:
			{
				// Stack layout: [..., left, right]
				rhs := pop()
				lhs := pop()

				push(OSValue{
					Type:   OSVInt,
					Number: lhs.Number - rhs.Number,
				})
			}
		case OpBinLessEqual:
			{
				// Stack layout: [..., left, right]
				rhs := pop()
				lhs := pop()
				if lhs.Number <= rhs.Number {
					push(vm.True)
				} else {
					push(vm.False)
				}
			}
		case OpStoreLocal:
			localIndex := int(readByte())

			if localIndex >= len(frame.Env) || frame.Env[localIndex] == nil {
				panic(fmt.Sprintf("invalid local variable index: %d", localIndex))
			}

			frame.Env[localIndex].Value = pop()

		case OpStdWrite:
			value := pop()
			fmt.Println("log:", value.String())

		case OpJmp:
			{
				offset := int(readByte())
				ip = int32(offset)
			}
		case OpPopJmpIfFalse:
			{
				offset := int(readByte())
				condition := pop()
				if !CoerceToBool(condition) {
					ip = int32(offset)
				} else {
					ip++
				}
			}
		case OpReturn:
			var returnValue OSValue

			if sp > 0 {
				returnValue = pop()
			} else {
				returnValue = vm.Null
			}

			// Save the completed coroutine's final state.
			saveState()

			co.State = CoroutineStateDead

			caller := co.Parent

			if caller == nil {
				// The root coroutine has finished.
				vm.Current = nil
				return
			}

			// Restore the caller and push the return value onto
			// its operand stack.
			vm.Current = caller

			if caller.Sp < 0 || caller.Sp >= int32(len(caller.OperandStack)) {
				panic("invalid caller stack pointer on return")
			}

			caller.OperandStack[caller.Sp] = returnValue
			caller.Sp++

			return

		default:
			panic(fmt.Sprintf("unknown opcode %v at offset %d", op, ip-1))
		}
	}

	// Persist state when bytecode reaches its end without OpReturn.
	saveState()

	// Treat reaching the end of bytecode as coroutine completion.
	co.State = CoroutineStateDead

	if co.Parent == nil {
		vm.Current = nil
	} else {
		vm.Current = co.Parent
	}
}

func Run(state *State, program *Function) {
	if state == nil {
		panic("state cannot be nil")
	}

	if program == nil {
		panic("program function cannot be nil")
	}

	vm := VM{
		Null:  OSValue{Type: OSVNull},
		True:  OSValue{Type: OSVBool, Number: 1},
		False: OSValue{Type: OSVBool, Number: 0},
	}

	main := NewCoroutine(nil, program, 255)

	if main == nil {
		panic("failed to create main coroutine")
	}

	vm.Current = main

	interpreterLoop(state, &vm)
}
