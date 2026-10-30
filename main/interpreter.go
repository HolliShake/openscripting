package main

import (
	"fmt"
	"log"
	"strings"
	"unicode/utf8"
	"unsafe"
)

var StackSize int32 = 1024

/******************* Cold-path helpers ***/

// These are kept out of line so that boxing their arguments for fmt does not
// make locals of the hot interpreter loop escape (the old "funcIndex escapes
// to heap", "sp escapes to heap", "ip - 1 escapes to heap" lines), and so the
// *strings.Builder inside OSValue.String is not "moved to heap" in
// runCoroutine's frame.

// strKey identifies one string literal in the bytecode: the function it lives
// in plus the position of its length word.
type strKey struct {
	fn *Function
	ip int32
}

type VM struct {
	Current *Coroutine
	Pending []*Coroutine
	Null    OSValue
	True    OSValue
	False   OSValue

	// strs caches decoded string literals. Previously every OpLoadStr /
	// OpGetAttr executed in a loop built a brand new string on the heap.
	// Strings are immutable, so sharing is safe. The cache lives and dies
	// with the VM, so it can never outlive a run.
	strs map[strKey]string
}

//go:noinline
func panicInt(format string, a int) { panic(fmt.Sprintf(format, a)) }

//go:noinline
func panicInt2(format string, a, b int) { panic(fmt.Sprintf(format, a, b)) }

//go:noinline
func valueString(v OSValue) string { return v.String() }

// writeLog avoids boxing the string into a []any for log.Println.
//
//go:noinline
func writeLog(s string) {
	if strings.HasSuffix(s, "\n") { // log.Println would have emitted a blank line too
		s += "\n"
	}
	_ = log.Output(3, s)
}

func interpreterLoop(state *State, vm *VM) {
	for {
		if vm.Current != nil && vm.Current.State != CoroutineStateDead {
			runCoroutine(state, vm, vm.Current)
			continue
		}

		vm.Current = nil
		for len(vm.Pending) > 0 {
			next := vm.Pending[0]
			vm.Pending[0] = nil
			vm.Pending = vm.Pending[1:]
			if next != nil && next.State != CoroutineStateDead && next.Frame.Function != nil {
				vm.Current = next
				break
			}
		}

		if vm.Current == nil {
			return
		}
	}
}

func enqueueMicrotask(vm *VM, coroutine *Coroutine) {
	if coroutine != nil {
		vm.Pending = append(vm.Pending, coroutine)
	}
}

func runCoroutine(state *State, vm *VM, co *Coroutine) {
	if co == nil || co.Frame.Function == nil {
		panic("invalid coroutine or call frame")
	}

	frame := &co.Frame
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

	readString := func() string {
		key := strKey{frame.Function, ip}
		size := readByte()

		if size < 0 || ip+size > bsize {
			panic("invalid string literal: exceeds bytecode")
		}

		if s, ok := vm.strs[key]; ok {
			ip += size
			return s
		}

		if size == 0 {
			vm.strs[key] = ""
			return ""
		}

		// The bytecode length is a rune count, so size the backing bytes from
		// the encoded runes to avoid append growth for multibyte text.
		runes := bcode[ip : ip+size]
		byteSize := 0

		for _, value := range runes {
			byteSize += utf8.RuneLen(rune(value))
		}

		buf := make([]byte, byteSize)
		byteOffset := 0

		for _, value := range runes {
			byteOffset += utf8.EncodeRune(buf[byteOffset:], rune(value))
		}

		ip += size

		s := unsafe.String(unsafe.SliceData(buf), byteSize)
		vm.strs[key] = s
		return s
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

	peek := func() OSValue {
		if sp <= 0 {
			panic("operand stack underflow")
		}
		return stack[sp-1]
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
				frame.Env[handler.CatchLocal].Value = OSValue{
					Type:   OSVString,
					Number: float64(len(message)),
					Opaque: unsafe.Pointer(unsafe.StringData(message)),
				}
			}
			clear(stack[:sp])
			sp = 0
			ip = handler.CatchStart
			return true
		}

		if promise := co.AsyncResult; promise != nil {
			promise.StringRoot = message
			rejectPromise(vm, promise, OSValue{
				Type:   OSVString,
				Number: float64(len(message)),
				Opaque: unsafe.Pointer(unsafe.StringData(message)),
			})
			co.State = CoroutineStateDead
			asyncCaller := co.AsyncCaller
			co.Release()
			vm.Current = asyncCaller
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
			stack = growStack(co)
		}

		stack[sp] = value
		sp++
	}

	// Save the exact execution position and stack pointer.
	saveState := func() {
		frame.Ip = ip
		co.Sp = sp
	}

	if co.ResumeReady {
		value := co.ResumeValue
		rejected := co.ResumeReject
		co.ResumeValue = OSValue{}
		co.ResumeReady = false
		co.ResumeReject = false
		if rejected {
			message := value.StringValue()
			if throwRuntimeError(message) {
				if co.State == CoroutineStateDead {
					return
				}
			} else {
				panic(message)
			}
		} else {
			push(value)
		}
	}

	for ip < bsize {
		if co.State == CoroutineStateDead {
			return
		}
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
				value := readString()

				push(OSValue{
					Type:   OSVString,
					Number: float64(len(value)),
					Opaque: unsafe.Pointer(unsafe.StringData(value)),
				})
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
		case OpExtendArray:
			{
				src := pop()
				dst := pop()

				if dst.Type != OSVArray || dst.Opaque == nil {
					panic("invalid destination array for OpExtendArray")
				}

				if src.Type != OSVArray || src.Opaque == nil {
					panic("invalid source array for OpExtendArray")
				}

				dstArray := (*Array)(dst.Opaque)
				srcArray := (*Array)(src.Opaque)
				dstArray.Elements = append(dstArray.Elements, srcArray.Elements...)
				push(dst)
			}
		case OpMakeArray:
			{
				size := readByte()

				if size < 0 || sp < size {
					panic("operand stack underflow for array creation")
				}

				// Take the elements straight off the operand stack: one
				// copy instead of `size` pop() calls.
				array := &Array{Elements: make([]OSValue, size)}
				copy(array.Elements, stack[sp-size:sp])
				clear(stack[sp-size : sp])
				sp -= size

				push(OSValue{
					Type:   OSVArray,
					Opaque: unsafe.Pointer(array),
				})
			}
		case OpExtendObject:
			{
				src := pop()
				dst := pop()

				if dst.Type != OSVObject || dst.Opaque == nil {
					panic("invalid destination object for OpExtendObject")
				}

				if src.Type != OSVObject || src.Opaque == nil {
					panic("invalid source object for OpExtendObject")
				}

				dstObj := (*Object)(dst.Opaque)
				srcObj := (*Object)(src.Opaque)

				for _, key := range srcObj.Keys {
					dstObj.Set(key, srcObj.Values[key])
				}

				push(dst)
			}
		case OpMakeObject:
			{
				size := readByte()

				if size < 0 || sp < 2*size {
					panic("operand stack underflow for object creation")
				}

				base := sp - 2*size
				obj := &Object{Values: make(map[string]OSValue, size)}
				for i := int32(0); i < size; i++ {
					key := stack[base+2*i]
					if key.Type != OSVString {
						panic("object keys must be strings")
					}
					obj.Set(key.StringValue(), stack[base+2*i+1])
				}
				clear(stack[base:sp])
				sp = base

				push(OSValue{
					Type:   OSVObject,
					Opaque: unsafe.Pointer(obj),
				})
			}
		case OpGetAttr:
			{
				top := pop()
				attr := readString()

				if top.Type == OSVPromise {
					if attr == "then" {
						push(BindPromiseThenMethod(top))
						continue
					}
					if attr == "catch" {
						push(BindPromiseCatchMethod(top))
						continue
					}
				}

				if top.Type != OSVObject || top.Opaque == nil {
					if throwRuntimeError("attempted to get attribute of a non-object value " + valueString(top)) {
						continue
					}
				}

				obj := (*Object)(top.Opaque)

				if value, exists := obj.Values[attr]; exists {
					push(value)
				} else {
					push(vm.Null)
				}
			}
		case OpSetAttr:
			{
				attr := readString()
				val := pop()
				obj := pop()

				if obj.Type != OSVObject || obj.Opaque == nil {
					if throwRuntimeError("attempted to set attribute of a non-object value " + valueString(obj)) {
						continue
					}
				}

				object := (*Object)(obj.Opaque)
				object.Set(attr, val)
			}
		case OpGetIndex:
			{
				idx := pop()
				obj := pop()

				if obj.Type == OSVObject && obj.Opaque != nil {
					if idx.Type != OSVString {
						if throwRuntimeError("object index must be a string") {
							continue
						}
					}

					object := (*Object)(obj.Opaque)
					if value, exists := object.Values[idx.StringValue()]; exists {
						push(value)
					} else {
						push(vm.Null)
					}
					continue
				}

				if obj.Type != OSVArray || obj.Opaque == nil {
					if throwRuntimeError("attempted to index a non-array value " + valueString(obj)) {
						continue
					}
				}

				arr := (*Array)(obj.Opaque)

				if idx.Type != OSVNum {
					if throwRuntimeError("array index must be a number") {
						continue
					}
				}

				i := int32(CoerceToNumber(idx))
				if i < 0 || i >= int32(len(arr.Elements)) {
					if throwRuntimeError("array index out of bounds") {
						continue
					}
				}

				push(arr.Elements[i])
			}
		case OpSetIndex:
			{
				val := pop()
				idx := pop()
				obj := pop()

				if obj.Type != OSVArray || obj.Opaque == nil {
					if throwRuntimeError("attempted to index a non-array value " + valueString(obj)) {
						continue
					}
				}

				arr := (*Array)(obj.Opaque)

				if idx.Type != OSVNum {
					if throwRuntimeError("array index must be a number") {
						continue
					}
				}

				i := int32(CoerceToNumber(idx))
				if i < 0 || i >= int32(len(arr.Elements)) {
					if throwRuntimeError("array index out of bounds") {
						continue
					}
				}

				arr.Elements[i] = val
			}
		case OpRot2:
			{
				// A, B -> B, A
				b := stack[sp-1]
				a := stack[sp-2]
				//========================//
				stack[sp-2] = b
				stack[sp-1] = a
			}
		case OpRot3:
			{
				// A, B, C -> C, A, B
				c := stack[sp-1]
				b := stack[sp-2]
				a := stack[sp-3]
				//========================//
				stack[sp-3] = c
				stack[sp-2] = a
				stack[sp-1] = b
			}
		case OpRot4:
			{
				// A, B, C, D -> D, A, B, C
				d := stack[sp-1]
				c := stack[sp-2]
				b := stack[sp-3]
				a := stack[sp-4]
				//========================//
				stack[sp-4] = d
				stack[sp-3] = a
				stack[sp-2] = b
				stack[sp-1] = c
			}
		case OpLoadFunction:
			{
				funcIndex := int(readByte())

				if funcIndex >= len(state.Functions) {
					panicInt("invalid function index: %d", funcIndex)
				}

				fn := state.Functions[funcIndex]
				fn = fn.Clone()

				// Save captured variables from the current environment into the function's capture metadata.
				env := co.Frame.Env
				for i := range fn.CaptureMeta {
					capMeta := &fn.CaptureMeta[i]
					capMeta.Cell = env[capMeta.Src]
				}

				if fn == nil {
					panicInt("function at index %d is nil", funcIndex)
				}

				push(OSValue{
					Type:   OSVFunction,
					Opaque: unsafe.Pointer(fn),
				})
			}
		case OpLoadVar:
			{
				varIndex := int(readByte())

				if varIndex >= len(frame.Env) || frame.Env[varIndex] == nil {
					panicInt2("invalid local variable index: %d of %d", varIndex, len(frame.Env))
				}

				push(frame.Env[varIndex].Value)
			}
		case OpInc, OpDec:
			{
				value := peek()
				if !IsNumber(value) {
					if throwRuntimeError("increment and decrement require a number") {
						continue
					}
				}

				if op == OpInc {
					value.Number++
				} else {
					value.Number--
				}
				stack[sp-1] = value
			}
		case OpPostInc, OpPostDec:
			{
				value := pop()
				if !IsNumber(value) {
					if throwRuntimeError("increment and decrement require a number") {
						continue
					}
				}

				old := value

				if op == OpPostInc {
					value.Number++
				} else {
					value.Number--
				}
				push(value)
				push(old)

			}
		case OpCall:
			{
				argCount := int32(readByte())

				// Expected stack layout:
				// [..., arg1, arg2, ..., argN, function]
				fnValue := pop()

				if fnValue.Type == OSVNativeMethod {
					// Handle native method binding here if necessary.
					nativeMethod := (*NativeMethod)(fnValue.Opaque)
					if nativeMethod == nil {
						if throwRuntimeError("attempted to call a nil native method") {
							continue
						}
					}

					if nativeMethod.Paramc != argCount {
						if throwRuntimeError("invalid argument count for native method") {
							continue
						}
					}

					if argCount < 0 || argCount > sp {
						if throwRuntimeError("invalid native method argument count") {
							continue
						}
					}

					argStart := sp - int32(argCount)
					result := nativeMethod.Callable(nativeMethod.This, vm, stack[argStart:sp])
					clear(stack[argStart:sp])
					sp = argStart
					push(result)
					continue
				}

				if fnValue.Type != OSVFunction || fnValue.Opaque == nil {
					if throwRuntimeError("attempted to call a non-function value " + valueString(fnValue)) {
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

				vaIndex := fn.VarArgIndex

				if (vaIndex == -1 && argCount != fn.ParamCount) || (vaIndex != -1 && argCount < vaIndex) {
					message := fmt.Sprintf("function expects %d arguments, got %d", fn.ParamCount, argCount)
					if throwRuntimeError(message) {
						continue
					}
					panic(message)
				}

				// Copy arguments in their original order before switching
				// to the new coroutine.
				argStart := sp - argCount
				args := stack[argStart:sp]
				sp = argStart

				// Save the caller AFTER consuming its arguments.
				saveState()

				caller := co
				callee := NewCoroutine(caller, fn, InitialStack(argCount))

				if callee == nil {
					panic("failed to create coroutine")
				}

				if int32(len(callee.OperandStack)) < argCount {
					panic("callee operand stack is too small for arguments")
				}

				// Unpack captured variables into the callee's environment.
				for i := range fn.CaptureMeta {
					capMeta := &fn.CaptureMeta[i]
					callee.Frame.Env[capMeta.Dst] = capMeta.Cell
				}

				// A variadic parameter occupies one local and receives the
				// remaining arguments as an array.
				calleeArgCount := argCount

				if vaIndex >= 0 {
					variadicArgs := make([]OSValue, argCount-vaIndex)
					copy(variadicArgs, args[vaIndex:])

					copy(callee.OperandStack[:vaIndex], args[:vaIndex])
					callee.OperandStack[vaIndex] = OSValue{
						Type:   OSVArray,
						Opaque: unsafe.Pointer(&Array{Elements: variadicArgs}),
					}
					calleeArgCount = vaIndex + 1
				} else {
					copy(callee.OperandStack[:argCount], args)
				}

				callee.Sp = calleeArgCount
				clear(stack[argStart : argStart+argCount])

				if fn.Async {
					promise := &Promise{Coroutine: callee}
					callee.Parent = SnapshotLexicalChain(caller)
					callee.Caller = nil
					callee.AsyncCaller = caller
					callee.AsyncResult = promise
					push(OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)})
					saveState()
					vm.Current = callee
					return
				}

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
					value := lhs.StringValue() + rhs.StringValue()
					push(OSValue{
						Type:   OSVString,
						Number: float64(len(value)),
						Opaque: unsafe.Pointer(unsafe.StringData(value)),
					})
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

				if lhs.Number < rhs.Number {
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

				if lhs.Number <= rhs.Number {
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

				if lhs.Number > rhs.Number {
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

				if lhs.Number >= rhs.Number {
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
		case OpDestructArray:
			{
				size := int(readByte())
				aobj := pop()

				if aobj.Type != OSVArray {
					if throwRuntimeError("expected array for destructuring") {
						continue
					}
				}

				if aobj.Opaque == nil {
					if throwRuntimeError("expected array for destructuring") {
						continue
					}
				}

				elements := (*Array)(aobj.Opaque).Elements
				if len(elements) < size {
					if throwRuntimeError("array size mismatch for destructuring") {
						continue
					}
				}

				for i := size - 1; i >= 0; i-- {
					push(elements[i])
				}
			}
		case OpStoreLocal:
			{
				localIndex := int(readByte())

				if localIndex >= len(frame.Env) || frame.Env[localIndex] == nil {
					panicInt2("invalid local variable index: %d (frame.Env length: %d)", localIndex, len(frame.Env))
				}

				frame.Env[localIndex].Value = pop()
			}
		case OpPopTop:
			{
				pop()
			}

		case OpStdWrite:
			{
				value := pop()
				writeLog(valueString(value))
			}
		case OpJmp:
			{
				offset := int(readByte())
				ip = int32(offset)
			}
		case OpAbsJmp:
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

				expval := peek()
				truthy := CoerceToBool(expval)

				if op == OpJmpIfFalseOrPop && !truthy || op == OpJmpIfTrueOrPop && truthy {
					ip = int32(offset)
				} else {
					pop()
				}
			}
		case OpDupTop:
			{
				push(peek())
			}
		case OpDup2:
			{
				v1 := stack[sp-1]
				v2 := stack[sp-2]
				push(v2)
				push(v1)
			}

		case OpAwait:
			{
				value := pop()
				if value.Type == OSVPromise {
					if value.Opaque == nil {
						if throwRuntimeError("attempted to await an invalid promise") {
							continue
						}
					}

					promise := (*Promise)(value.Opaque)
					reaction := PromiseReaction{Awaiter: co}
					if promise.Resolved {
						schedulePromiseReaction(vm, promise, reaction)
					} else {
						promise.Reactions = append(promise.Reactions, reaction)
					}
					if asyncCaller := co.AsyncCaller; asyncCaller != nil {
						co.AsyncCaller = nil
						vm.Current = asyncCaller
					} else {
						vm.Current = nil
					}
					saveState()
					return
				}
				push(value)
			}
		case OpReturn:
			{
				var returnValue OSValue

				if sp > 0 {
					returnValue = pop()
				} else {
					returnValue = vm.Null
				}

				if sp != 0 {
					panicInt("stack not empty on return (stack pointer: %d)", int(sp))
				}

				// Save the completed coroutine's final state.
				saveState()

				co.State = CoroutineStateDead
				if promise := co.AsyncResult; promise != nil {
					resolvePromise(vm, promise, returnValue)
				}
				if asyncCaller := co.AsyncCaller; asyncCaller != nil {
					co.AsyncCaller = nil
					vm.Current = asyncCaller
					co.Release()
					return
				}

				caller := co.Caller

				if caller == nil {
					// The root coroutine has finished.
					vm.Current = nil
					co.Release()
					return
				}

				// Restore the caller and push the return value onto
				// its operand stack.
				vm.Current = caller

				if caller.Sp < 0 {
					panic("invalid caller stack pointer on return")
				}
				if caller.Sp >= int32(len(caller.OperandStack)) {
					growStack(caller)
				}

				caller.OperandStack[caller.Sp] = returnValue
				caller.Sp++

				// Nothing references the finished coroutine any more; drop its
				// stack, cells and links so they can be collected.
				co.Release()

				return
			}
		default:
			panicInt2("unknown opcode %v at offset %d", int(op), int(ip-1))
		}
	}

	// Persist state when bytecode reaches its end without OpReturn.
	saveState()

	// Treat reaching the end of bytecode as coroutine completion.
	co.State = CoroutineStateDead
	returnValue := vm.Null
	if promise := co.AsyncResult; promise != nil {
		resolvePromise(vm, promise, returnValue)
	}

	parent := co.Caller
	if parent == nil {
		co.Release()
		vm.Current = nil
		return
	}
	if parent.Sp >= int32(len(parent.OperandStack)) {
		growStack(parent)
	}
	parent.OperandStack[parent.Sp] = returnValue
	parent.Sp++
	co.Release()
	vm.Current = parent
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
		strs:  make(map[strKey]string, 16),
	}

	main := NewCoroutine(nil, program, InitialStack(0))

	if main == nil {
		panic("failed to create main coroutine")
	}

	vm.Current = main

	interpreterLoop(state, &vm)
}
