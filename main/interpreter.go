package main

import (
	"fmt"
	"strings"
	"time"
	"unsafe"
)

type VM struct {
	Current *Coroutine
	Null    OSValue
	True    OSValue
	False   OSValue
}

var StackSize int32 = 1024

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
	bcode := frame.Function.Bytecode

	ip := frame.Ip
	sp := co.Sp
	stack := co.OperandStack
	bsize := int32(len(bcode))
	currentInstruction := ip

	readByte := func() int32 {
		if ip >= bsize {
			panic("unexpected end of bytecode")
		}

		value := bcode[ip]
		ip++
		return value
	}

	pop := func() OSValue {
		if sp <= 0 {
			panic("operand stack underflow")
		}

		sp--
		value := stack[sp]
		stack[sp] = OSValue{}
		return value
	}

	throwRuntimeError := func(message string) (handled bool) {
		instruction := currentInstruction
		handlers := frame.Function.TryCatchTable
		for index := len(handlers) - 1; index >= 0; index-- {
			handler := handlers[index]
			if instruction < handler.TryStart || instruction >= handler.TryEnd {
				continue
			}

			if handler.CatchLocal >= 0 && int(handler.CatchLocal) < len(frame.Env) && frame.Env[handler.CatchLocal] != nil {
				frame.Env[handler.CatchLocal].Value = NewStringValue(message)
			}
			clear(stack[:sp])
			sp = 0
			ip = handler.CatchStart
			return true
		}

		line := int32(0)
		for _, sourceLine := range frame.Function.Lines {
			if sourceLine.Index > instruction {
				break
			}
			line = sourceLine.Line
		}
		ThrowError(frame.Function.File, message, Position{Line: int(line), Column: 1})
		return false
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

	for ip < bsize {
		currentInstruction = ip
		op := bcode[ip]
		ip++

		switch op {
		case OpLoadNum:
			{
				a := readByte()
				b := readByte()

				push(OSValue{
					Type:   OSVNum,
					Number: CombineFloat(a, b),
				})
			}
		case OpLoadStr:
			{
				// TODO: Fix, slow iteration for string literal decoding
				size := int32(readByte())

				if ip+size > bsize {
					panic("invalid string literal: exceeds bytecode")
				}

				var builder strings.Builder
				builder.Grow(int(size))
				for range size {
					builder.WriteRune(rune(readByte()))
				}

				push(NewStringValue(builder.String()))
			}
		case OpLoadTrue:
			{
				push(vm.True)
			}
		case OpLoadFalse:
			{
				push(vm.False)
			}
		case OpLoadNull:
			{
				push(vm.Null)
			}
		case OpMakeArray:
			{
				size := readByte()

				if sp < size {
					panic("operand stack underflow for array creation")
				}

				elements := make([]OSValue, size)
				for i := size - 1; i >= 0; i-- {
					elements[i] = pop()
				}

				push(OSValue{
					Type:   OSVArray,
					Opaque: unsafe.Pointer(&elements),
				})
			}
		case OpLoadFunction:
			{
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
			}
		case OpLoadCap:
			{
				capIndex := int(readByte())
				parentCoro := co.Parent

				for parentCoro != nil && parentCoro.Frame.Function == frame.Function {
					parentCoro = parentCoro.Parent
				}

				if parentCoro == nil || capIndex >= len(parentCoro.Frame.Env) || parentCoro.Frame.Env[capIndex] == nil {
					panic(fmt.Sprintf("invalid captured variable index: %d", capIndex))
				}

				push(parentCoro.Frame.Env[capIndex].Value)
			}
		case OpLoadVar:
			{
				varIndex := int(readByte())

				if varIndex >= len(frame.Env) || frame.Env[varIndex] == nil {
					panic(fmt.Sprintf("invalid local variable index: %d of %d", varIndex, len(frame.Env)))
				}

				push(frame.Env[varIndex].Value)
			}
		case OpCall:
			{
				argCount := int32(readByte())

				// Expected stack layout:
				// [..., arg1, arg2, ..., argN, function]
				fnValue := pop()

				if fnValue.Type != OSVFunction || fnValue.Opaque == nil {
					if throwRuntimeError("attempted to call a non-function value " + fnValue.String()) {
						continue
					}
				}

				fn := (*Function)(fnValue.Opaque)

				if argCount < 0 || argCount > sp {
					if throwRuntimeError("invalid function argument count") {
						continue
					}
				}

				if fn == nil {
					if throwRuntimeError("attempted to call a nil function") {
						continue
					}
				}

				// Copy arguments in their original order before switching
				// to the new coroutine.
				argStart := sp - argCount
				args := stack[argStart:sp]
				sp = argStart

				// Save the caller AFTER consuming its arguments.
				saveState()

				caller := co
				callee := NewCoroutine(caller, fn, StackSize)

				if callee == nil {
					panic("failed to create coroutine")
				}

				if int32(len(callee.OperandStack)) < argCount {
					panic("callee operand stack is too small for arguments")
				}

				// Place arguments on the callee's stack.
				copy(callee.OperandStack[:argCount], args)
				callee.Sp = argCount
				clear(stack[argStart : argStart+argCount])

				vm.Current = callee
				return
			}
		case OpBinMul:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for arithmetic operation") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: lhs.Number * rhs.Number,
				})
			}
		case OpBinDiv:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for arithmetic operation") {
						continue
					}
				}

				if rhs.Number == 0 {
					if throwRuntimeError("division by zero") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: lhs.Number / rhs.Number,
				})
			}
		case OpBinMod:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for arithmetic operation") {
						continue
					}
				}

				if rhs.Number == 0 {
					if throwRuntimeError("division by zero") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: lhs.Number - float64(int64(lhs.Number/rhs.Number))*rhs.Number,
				})
			}
		case OpBinAdd:
			{
				rhs := pop()
				lhs := pop()

				if IsString(lhs) && IsString(rhs) {
					push(NewStringValue(lhs.stringValue() + rhs.stringValue()))
					continue
				} else if IsNumber(lhs) && IsNumber(rhs) {
					push(OSValue{
						Type:   OSVNum,
						Number: lhs.Number + rhs.Number,
					})
					continue
				}

				if throwRuntimeError("invalid operands for arithmetic operation") {
					continue
				}
			}
		case OpBinSub:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for arithmetic operation") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: lhs.Number - rhs.Number,
				})
			}
		case OpBinShiftLeft:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for bitwise operation") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: float64(int64(lhs.Number) << uint(int64(rhs.Number))),
				})
			}
		case OpBinShiftRight:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for bitwise operation") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: float64(int64(lhs.Number) >> uint(int64(rhs.Number))),
				})
			}
		case OpBinLess:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for comparison") {
						continue
					}
				}

				result := lhs.Number < rhs.Number

				if result {
					push(vm.True)
				} else {
					push(vm.False)
				}
			}
		case OpBinLessEqual:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for comparison") {
						continue
					}
				}

				result := lhs.Number <= rhs.Number

				if result {
					push(vm.True)
				} else {
					push(vm.False)
				}
			}
		case OpBinGreater:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for comparison") {
						continue
					}
				}

				result := lhs.Number > rhs.Number

				if result {
					push(vm.True)
				} else {
					push(vm.False)
				}
			}
		case OpBinGreaterEqual:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for comparison") {
						continue
					}
				}

				result := lhs.Number >= rhs.Number

				if result {
					push(vm.True)
				} else {
					push(vm.False)
				}
			}
		case OpBinEqual:
			{
				rhs := pop()
				lhs := pop()

				if IsEqual(lhs, rhs) {
					push(vm.True)
				} else {
					push(vm.False)
				}
			}
		case OpBinNotEqual:
			{
				rhs := pop()
				lhs := pop()

				if !IsEqual(lhs, rhs) {
					push(vm.True)
				} else {
					push(vm.False)
				}
			}
		case OpBinBitAnd:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for bitwise operation") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: float64(int64(lhs.Number) & int64(rhs.Number)),
				})
			}
		case OpBinBitXor:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for bitwise operation") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: float64(int64(lhs.Number) ^ int64(rhs.Number)),
				})
			}
		case OpBinBitOr:
			{
				rhs := pop()
				lhs := pop()

				if !(IsNumber(lhs) && IsNumber(rhs)) {
					if throwRuntimeError("invalid operands for bitwise operation") {
						continue
					}
				}

				push(OSValue{
					Type:   OSVNum,
					Number: float64(int64(lhs.Number) | int64(rhs.Number)),
				})
			}
		case OpStoreCap:
			{
				capIndex := int(readByte())
				srcIndex := int(readByte())

				parentCoro := co.Parent
				if parentCoro == nil || srcIndex >= len(parentCoro.Frame.Env) {
					panic(fmt.Sprintf("invalid captured variable index: %d", srcIndex))
				}

				cell := parentCoro.Frame.Env[srcIndex]
				if capIndex >= len(frame.Env) {
					panic(fmt.Sprintf("invalid local capture index: %d", capIndex))
				}
				frame.Env[capIndex] = cell
			}
		case OpStoreLocal:
			{
				localIndex := int(readByte())

				if localIndex >= len(frame.Env) || frame.Env[localIndex] == nil {
					panic(fmt.Sprintf("invalid local variable index: %d (frame.Env length: %d)", localIndex, len(frame.Env)))
				}

				frame.Env[localIndex].Value = pop()
			}

		case OpStdWrite:
			{
				value := pop()
				fmt.Printf("[%s] log: %s\n", time.Now().Format("2006-01-02 15:04:05"), value.String())
			}
		case OpJmp:
			{
				offset := int(readByte())
				ip = int32(offset)
			}
		case OpPopJmpIfFalse:
			{
				offset := int(readByte())
				expval := pop()
				if !CoerceToBool(expval) {
					ip = int32(offset)
				}
			}
		case OpPopJmpIfTrue:
			{
				offset := int(readByte())
				expval := pop()
				if CoerceToBool(expval) {
					ip = int32(offset)
				}
			}

		case OpJmpIfFalseOrPop, OpJmpIfTrueOrPop:
			{
				offset := int(readByte())

				if sp <= 0 {
					panic("operand stack underflow")
				}

				expval := stack[sp-1]
				truthy := CoerceToBool(expval)

				if op == OpJmpIfFalseOrPop && !truthy || op == OpJmpIfTrueOrPop && truthy {
					ip = int32(offset)
				} else {
					pop()
				}
			}
		case OpDupTop:
			{
				push(stack[sp-1])
			}
		case OpReturn:
			{
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
			}
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

	main := NewCoroutine(nil, program, StackSize)

	if main == nil {
		panic("failed to create main coroutine")
	}

	vm.Current = main

	interpreterLoop(state, &vm)
}
