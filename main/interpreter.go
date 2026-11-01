package main

import (
	"fmt"
	"log"
	"math/big"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
	"unsafe"
)

var StackSize int = 1024

const (
	msgUnderflow   = "operand stack underflow"
	msgArith       = "invalid operands for arithmetic operation"
	msgBitwise     = "invalid operands for bitwise operation"
	msgCompare     = "invalid operands for comparison"
	msgIncDec      = "increment and decrement require a number"
	msgDestructArr = "expected array for destructuring"
	msgIndexNum    = "array index must be a number"
	msgIndexBounds = "array index out of bounds"
)

/******************* Cold-path helpers ***/

// These are kept out of line so that boxing their arguments for fmt does not
// make locals of the hot interpreter loop escape, and so the *strings.Builder
// inside OSValue.String is not "moved to heap" in runCoroutine's frame.

type VM struct {
	// State provides compiled functions and import tables required by opcode execution.
	State *State
	// Current is the coroutine currently owned by the interpreter loop.
	Current *Coroutine
	// Pending is the microtask FIFO for async callbacks and Promise resumptions.
	Pending []*Coroutine
	// HTTPApps keeps live server states reachable until their listeners are closed.
	HTTPApps []*httpAppState
	// HTTPRequests transfers accepted requests from listener goroutines to VM-thread handlers.
	HTTPRequests chan *httpIncoming
	// HTTPDone reports listener shutdowns so the event loop can decrement active server count.
	HTTPDone chan *httpAppState
	// HTTPFetches transfers worker results back to the VM thread for Promise settlement.
	HTTPFetches chan httpFetchResult
	// SocketResults transfers accepted connections from listener workers to the VM thread.
	SocketResults chan socketResult
	// SocketActive counts listeners and connections that keep the event loop alive.
	SocketActive int
	// SocketPending counts accept operations still running in listener workers.
	SocketPending int
	// HTTPActive counts servers keeping the event loop alive.
	HTTPActive int
	// HTTPFetchActive counts in-flight fetch workers keeping the event loop alive.
	HTTPFetchActive int
	// Null is the canonical null value used when bytecode or natives produce no value.
	Null OSValue
	// True is the canonical true value, avoiding repeated boxing in opcode execution.
	True OSValue
	// False is the canonical false value, avoiding repeated boxing in opcode execution.
	False OSValue
	// NativeError is the error channel from a native callable to the interpreter's throw path.
	NativeError string
	// RejectedPromises retains rejected promises until microtasks drain, allowing late catch registration.
	RejectedPromises []*Promise

	// pendingHead is the index of the next microtask to run in Pending.
	// Consuming with an index (instead of Pending = Pending[1:]) avoids
	// slowly losing the slice's front capacity. Anything that enqueues
	// should still just append to Pending.
	pendingHead int

	// strs caches decoded string literals, one table per distinct bytecode
	// array, indexed by the ip of the literal's length word. It is keyed by
	// the address of the bytecode (not the *Function) so that Clone()d
	// closures share a single table instead of allocating a new one each.
	// Strings are immutable, so sharing is safe. The cache lives and dies
	// with the VM, so it can never outlive a run.
	strs map[unsafe.Pointer][]string

	// One-entry cache in front of strs: calls bounce between a few
	// functions, so most lookups hit here and skip the map hash.
	lastKey unsafe.Pointer
	lastTab []string
}

// strTable returns the literal cache for the given bytecode.
func (vm *VM) strTable(bcode []int32) []string {
	key := unsafe.Pointer(unsafe.SliceData(bcode))
	if key == vm.lastKey && vm.lastTab != nil {
		return vm.lastTab
	}
	t := vm.strs[key]
	if t == nil {
		t = make([]string, len(bcode))
		vm.strs[key] = t
	}
	vm.lastKey, vm.lastTab = key, t
	return t
}

//go:noinline
func valueString(v OSValue) string { return v.String() }

func bigIntValue(value *big.Int) OSValue {
	return OSValue{Type: OSVBigInt, Opaque: unsafe.Pointer(value)}
}

func materializeConstant(value constVal) OSValue {
	switch value.kind {
	case kNum:
		return OSValue{Type: OSVNum, Number: value.num}
	case kStr:
		return OSValue{Type: OSVString, Number: float64(len(value.str)), Opaque: unsafe.Pointer(unsafe.StringData(value.str))}
	case kBool:
		if value.b {
			return OSValue{Type: OSVBool, Number: 1}
		}
		return OSValue{Type: OSVBool}
	case kNull:
		return OSValue{}
	case kArray:
		elements := make([]OSValue, len(value.values))
		for index, element := range value.values {
			elements[index] = materializeConstant(element)
		}
		return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
	case kObject:
		object := &Object{Keys: make([]string, 0, len(value.keys)), Values: make(map[string]OSValue, len(value.keys))}
		for index, key := range value.keys {
			object.Set(key, materializeConstant(value.values[index]))
		}
		return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
	default:
		panic("invalid constant-pool value")
	}
}

func isBigIntPair(left, right OSValue) bool {
	return left.Type == OSVBigInt && left.Opaque != nil && right.Type == OSVBigInt && right.Opaque != nil
}

//go:noinline
func argcMessage(expected, got int) string {
	return fmt.Sprintf("function expects %d arguments, got %d", expected, got)
}

// writeLog avoids boxing the string into a []any for log.Println.
//
//go:noinline
func writeLog(s string) {
	if strings.HasSuffix(s, "\n") { // log.Println would have emitted a blank line too
		s += "\n"
	}
	_ = log.Output(3, s)
}

// operand reads the operand word at ip (the caller advances ip).
func operand(bcode []int32, ip, bsize int) int {
	if ip >= bsize {
		panic("unexpected end of bytecode")
	}
	return int(bcode[ip])
}

// decodeRunes turns a rune-per-word literal into a string with exactly one
// allocation. The bytecode length is a rune count, so the byte size is
// computed up front. Pure-ASCII literals (the common case) take a straight
// byte-copy path. Invalid runes encode as U+FFFD (3 bytes), matching what
// utf8.EncodeRune writes, so the buffer can never be undersized.
func decodeRunes(runes []int32) string {
	if len(runes) == 0 {
		return ""
	}

	byteSize := 0
	for _, value := range runes {
		if uint32(value) < utf8.RuneSelf {
			byteSize++
		} else if n := utf8.RuneLen(rune(value)); n > 0 {
			byteSize += n
		} else {
			byteSize += 3 // utf8.RuneError
		}
	}

	buf := make([]byte, byteSize)

	if byteSize == len(runes) {
		// Every rune fit in one byte: all ASCII.
		for i, value := range runes {
			buf[i] = byte(value)
		}
		return unsafe.String(unsafe.SliceData(buf), byteSize)
	}

	byteOffset := 0
	for _, value := range runes {
		byteOffset += utf8.EncodeRune(buf[byteOffset:], rune(value))
	}

	return unsafe.String(unsafe.SliceData(buf), byteSize)
}

// readString reads the string literal whose length word is at ip. It returns
// the string, the (possibly newly looked up) literal table and the ip after
// the literal.
func readString(vm *VM, bcode []int32, ip, bsize int, strTab []string) (string, []string, int) {
	if ip >= bsize {
		panic("unexpected end of bytecode")
	}

	at := ip
	size := int(bcode[ip])
	ip++

	if size < 0 || ip+size > bsize {
		panic("invalid string literal: exceeds bytecode")
	}

	if strTab == nil {
		strTab = vm.strTable(bcode)
	}

	if s := strTab[at]; s != "" || size == 0 {
		return s, strTab, ip + size
	}

	s := decodeRunes(bcode[ip : ip+size])
	strTab[at] = s
	return s, strTab, ip + size
}

// raise delivers a runtime error: to the innermost enclosing try/catch of the
// current frame, or by rejecting the async promise of the coroutine. It
// returns the new ip and sp (handled by a catch), or dead == true when the
// coroutine was killed by rejecting its promise. When nobody handles the
// error it panics through ThrowError.
//
//go:noinline
func raise(vm *VM, co *Coroutine, instruction int, stack []OSValue, sp int, message string) (newIp, newSp int, dead bool) {
	frame := &co.Frame
	line := 0
	for _, sourceLine := range frame.Function.Lines {
		if sourceLine.Index > instruction {
			break
		}
		line = sourceLine.Line
	}
	handlers := frame.Function.TryCatchTable
	for index := len(handlers) - 1; index >= 0; index-- {
		handler := handlers[index]
		if instruction < handler.TryStart || instruction >= handler.TryEnd {
			continue
		}

		if handler.CatchLocal >= 0 && int(handler.CatchLocal) < len(frame.Env) && frame.Env[handler.CatchLocal] != nil {
			frame.Env[handler.CatchLocal].Value = newErrorValue(message, int64(line+1))
		}
		clear(stack[:sp])
		return handler.CatchStart, 0, false
	}

	if promise := co.AsyncResult; promise != nil {
		promise.StringRoot = message
		promise.StackTrace = captureStackTrace(co, instruction)
		rejectPromise(vm, promise, newErrorValue(message, int64(line+1)))
		co.State = CoroutineStateDead
		asyncCaller := co.AsyncCaller
		co.Release()
		vm.Current = asyncCaller
		return 0, 0, true
	}
	if boundary := co.SyncBoundary; boundary != nil {
		boundary.StringRoot = message
		boundary.StackTrace = captureStackTrace(co, instruction)
		rejectPromise(vm, boundary, newErrorValue(message, int64(line+1)))
		co.State = CoroutineStateDead
		if root := boundary.Coroutine; root != nil && root != co {
			root.State = CoroutineStateDead
			root.Release()
		}
		co.Release()
		return 0, 0, true
	}

	position := Position{Line: int(line), Column: 1}
	formatError(frame.Function.File, position, message)
	printStackTrace(captureStackTrace(co, instruction))
	os.Exit(ExitFailure)
	panic(message)
}

func captureStackTrace(co *Coroutine, instruction int) []StackTraceFrame {
	frames := make([]StackTraceFrame, 0, 8)
	seen := make(map[*CallFrame]struct{}, 8)
	appendFrame := func(frame *CallFrame, offset int) {
		for frame != nil {
			if _, exists := seen[frame]; exists {
				return
			}
			seen[frame] = struct{}{}
			if frame.Function != nil {
				line := 0
				for _, sourceLine := range frame.Function.Lines {
					if sourceLine.Index > offset {
						break
					}
					line = sourceLine.Line
				}
				frames = append(frames, StackTraceFrame{
					File:     frame.Function.File,
					Function: frame.Function.Name,
					Line:     line + 1,
				})
			}
			offset = frame.Ip - 1
			frame = frame.Caller
		}
	}
	if co != nil {
		appendFrame(&co.Frame, instruction)
		if co.AsyncCaller != nil {
			appendFrame(&co.AsyncCaller.Frame, co.AsyncCaller.Frame.Ip-1)
		}
	}
	return frames
}

func printStackTrace(frames []StackTraceFrame) {
	if len(frames) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "Stack trace:")
	for _, frame := range frames {
		name := frame.Function
		if name == "" {
			name = "<anonymous>"
		}
		file := "<unknown>"
		if frame.File != nil && frame.File.Name != "" {
			file = frame.File.Name
		}
		fmt.Fprintf(os.Stderr, "  at %s (%s:%d)\n", name, file, frame.Line)
	}
}

func reportUnhandledPromises(vm *VM) bool {
	unhandled := false
	for _, promise := range vm.RejectedPromises {
		if promise == nil || promise.Handled || !promise.Resolved || !promise.Rejected {
			continue
		}
		unhandled = true
		message, ok := errorMessage(promise.Value)
		if !ok {
			message = promise.Value.String()
		}
		if len(promise.StackTrace) > 0 {
			frame := promise.StackTrace[0]
			formatError(frame.File, Position{Line: frame.Line - 1}, "unhandled promise rejection: ", message)
		} else {
			fmt.Fprintf(os.Stderr, "unhandled promise rejection: %s\n", message)
		}
		printStackTrace(promise.StackTrace)
		promise.Handled = true
	}
	vm.RejectedPromises = nil
	return unhandled
}

func interpreterLoop(state *State, vm *VM) {
	for {
		if vm.Current != nil && vm.Current.State != CoroutineStateDead {
			runCoroutine(state, vm, vm.Current)
			continue
		}

		vm.Current = nil
		for vm.pendingHead < len(vm.Pending) {
			next := vm.Pending[vm.pendingHead]
			vm.Pending[vm.pendingHead] = nil
			vm.pendingHead++
			if next != nil && next.State != CoroutineStateDead && next.Frame.Function != nil {
				vm.Current = next
				break
			}
		}

		// Reclaim the consumed prefix of the queue.
		if vm.pendingHead == len(vm.Pending) {
			vm.Pending = vm.Pending[:0]
			vm.pendingHead = 0
		} else if vm.pendingHead >= 1024 && vm.pendingHead*2 >= len(vm.Pending) {
			n := copy(vm.Pending, vm.Pending[vm.pendingHead:])
			clear(vm.Pending[n:])
			vm.Pending = vm.Pending[:n]
			vm.pendingHead = 0
		}

		if vm.Current == nil {
			if reportUnhandledPromises(vm) {
				os.Exit(ExitFailure)
			}
			if vm.HTTPActive == 0 && vm.HTTPFetchActive == 0 && vm.SocketActive == 0 && vm.SocketPending == 0 {
				return
			}
			select {
			case incoming := <-vm.HTTPRequests:
				if incoming != nil {
					serveHTTPIncoming(vm, incoming)
				}
			case <-vm.HTTPDone:
				vm.HTTPActive--
			case result := <-vm.HTTPFetches:
				vm.HTTPFetchActive--
				settleHTTPFetch(vm, result)
			case result := <-vm.SocketResults:
				if result.accept != nil {
					settleSocketAccept(vm, *result.accept)
				} else if result.io != nil {
					settleSocketIO(vm, *result.io)
				}
			}
		}
	}
}

func enqueueMicrotask(vm *VM, coroutine *Coroutine) {
	if coroutine != nil {
		vm.Pending = append(vm.Pending, coroutine)
	}
}

// runCoroutine is written WITHOUT closures on purpose: closures that capture
// ip, sp, stack, co and frame force those variables into memory (accessed by
// reference on every instruction). Everything here is plain local state, so
// the compiler can keep the hot variables in registers.
//
// Hot arithmetic/comparison opcodes test for the number/number case first and
// work in place on the operand stack; the BigInt and error paths follow
// unchanged.
//
// Errors are raised with `errMsg = ...; goto throwError`; the shared handler
// at the bottom of the dispatch loop calls raise().
func runCoroutine(state *State, vm *VM, co *Coroutine) {
	if co == nil || co.Frame.Function == nil {
		panic("invalid coroutine or call frame")
	}

	frame := &co.Frame
	bcode := frame.Function.Bytecode

	ip := frame.Ip
	sp := co.Sp
	stack := co.OperandStack
	bsize := len(bcode)
	currentInstruction := ip

	// Local environment of the current frame, cached next to bcode/stack and
	// refreshed at every frame switch (call / return).
	env := frame.Env

	// Literal table for the current bytecode; looked up lazily on the first
	// string literal executed in a frame, and reset whenever frames switch.
	var strTab []string

	// Message of the runtime error being raised (see throwError below).
	var errMsg string

	if co.ResumeReady {
		value := co.ResumeValue
		rejected := co.ResumeReject
		co.ResumeValue = OSValue{}
		co.ResumeReady = false
		co.ResumeReject = false
		if rejected {
			message := value.StringValue()
			if errorText, isError := errorMessage(value); isError {
				message = errorText
			}
			nip, nsp, dead := raise(vm, co, currentInstruction, stack, sp, message)
			if dead {
				return
			}
			ip, sp = nip, nsp
		} else {
			if sp >= len(stack) {
				stack = growStack(co)
			}
			stack[sp] = value
			sp++
		}
	}

	for ip < bsize {
		currentInstruction = ip
		op := bcode[ip]
		ip++

		switch op {
		case OpLoadNum:
			{
				a := bcode[ip]
				b := bcode[ip+1]
				ip += 2

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = OSValue{
					Type:   OSVNum,
					Number: CombineFloat(a, b),
				}
				sp++
			}
		case OpLoadBigInt:
			{
				var value string
				value, strTab, ip = readString(vm, bcode, ip, bsize, strTab)
				integer, ok := new(big.Int).SetString(value, 10)
				if !ok {
					errMsg = "invalid BigInt literal"
					goto throwError
				}
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = bigIntValue(integer)
				sp++
			}
		case OpLoadConst:
			{
				index := operand(bcode, ip, bsize)
				ip++
				if index < 0 || index >= len(frame.Function.Constants) {
					panic("invalid constant-pool index")
				}
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = materializeConstant(frame.Function.Constants[index])
				sp++
			}
		case OpLoadStr:
			{
				var value string
				value, strTab, ip = readString(vm, bcode, ip, bsize, strTab)

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = OSValue{
					Type:   OSVString,
					Number: float64(len(value)),
					Opaque: unsafe.Pointer(unsafe.StringData(value)),
				}
				sp++
			}
		case OpLoadTrue:
			{
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = vm.True
				sp++
			}
		case OpLoadFalse:
			{
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = vm.False
				sp++
			}
		case OpLoadNull:
			{
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = vm.Null
				sp++
			}
		case OpExtendArray:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				src := stack[sp-1]
				dst := stack[sp-2]

				if dst.Type != OSVArray || dst.Opaque == nil {
					panic("invalid destination array for OpExtendArray")
				}

				if src.Type != OSVArray || src.Opaque == nil {
					panic("invalid source array for OpExtendArray")
				}

				dstArray := (*Array)(dst.Opaque)
				srcArray := (*Array)(src.Opaque)
				dstArray.Elements = append(dstArray.Elements, srcArray.Elements...)

				// dst stays where it is; only the source is consumed.
				stack[sp-1] = OSValue{}
				sp--
			}
		case OpMakeArray:
			{
				size := operand(bcode, ip, bsize)
				ip++

				if size < 0 || sp < size {
					panic("operand stack underflow for array creation")
				}

				// Take the elements straight off the operand stack: one
				// copy instead of `size` pops.
				array := &Array{Elements: make([]OSValue, size)}
				copy(array.Elements, stack[sp-size:sp])
				clear(stack[sp-size : sp])
				sp -= size

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = OSValue{
					Type:   OSVArray,
					Opaque: unsafe.Pointer(array),
				}
				sp++
			}
		case OpExtendObject:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				src := stack[sp-1]
				dst := stack[sp-2]

				if dst.Type != OSVObject || dst.Opaque == nil {
					panic("invalid destination object for OpExtendObject")
				}

				if src.Type != OSVObject || src.Opaque == nil {
					panic("invalid source object for OpExtendObject")
				}

				dstObj := (*Object)(dst.Opaque)
				srcObj := (*Object)(src.Opaque)

				// Reserve key capacity once instead of growing per Set.
				dstObj.Keys = slices.Grow(dstObj.Keys, len(srcObj.Keys))
				for _, key := range srcObj.Keys {
					dstObj.Set(key, srcObj.Values[key])
				}

				// dst stays where it is; only the source is consumed.
				stack[sp-1] = OSValue{}
				sp--
			}
		case OpMakeObject:
			{
				size := operand(bcode, ip, bsize)
				ip++

				if size < 0 || sp < 2*size {
					panic("operand stack underflow for object creation")
				}

				base := sp - 2*size
				obj := &Object{Values: make(map[string]OSValue, size)}
				// Reserve key capacity once instead of growing per Set.
				obj.Keys = slices.Grow(obj.Keys, int(size))
				for i := 0; i < size; i++ {
					key := stack[base+2*i]
					if key.Type != OSVString {
						panic("object keys must be strings")
					}
					obj.Set(key.StringValue(), stack[base+2*i+1])
				}
				clear(stack[base:sp])
				sp = base

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = OSValue{
					Type:   OSVObject,
					Opaque: unsafe.Pointer(obj),
				}
				sp++
			}
		case OpMakeClass:
			{
				if sp < 4 {
					panic(msgUnderflow)
				}
				superValue := stack[sp-1]
				templateValue := stack[sp-2]
				constructor := stack[sp-3]
				name := stack[sp-4]
				clear(stack[sp-4 : sp])
				sp -= 4

				if name.Type != OSVString || (constructor.Type != OSVFunction && constructor.Type != OSVNativeFunction) || constructor.Opaque == nil ||
					templateValue.Type != OSVObject || templateValue.Opaque == nil ||
					(superValue.Type != OSVNull && (superValue.Type != OSVClass || superValue.Opaque == nil)) {
					panic("invalid class definition")
				}
				class := &Class{
					Name:        name.StringValue(),
					Super:       (*Class)(superValue.Opaque),
					Template:    (*Object)(templateValue.Opaque).Values,
					Constructor: constructor,
				}
				stack[sp] = OSValue{Type: OSVClass, Opaque: unsafe.Pointer(class)}
				sp++
			}
		case OpGetAttr:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				var attr string
				attr, strTab, ip = readString(vm, bcode, ip, bsize, strTab)

				// Plain objects are by far the most common receiver. The
				// value types are mutually exclusive, so checking them
				// first changes nothing. The result overwrites the receiver
				// in place: no pop, clear and push.
				top := stack[sp-1]
				if top.Type == OSVObject && top.Opaque != nil {
					object := (*Object)(top.Opaque)
					if value, exists := object.Values[attr]; exists {
						stack[sp-1] = value
					} else if object.Class != nil {
						if value, exists := object.Class.Template[attr]; exists {
							stack[sp-1] = value
						} else {
							stack[sp-1] = objectMethod(top, attr, vm)
						}
					} else {
						stack[sp-1] = objectMethod(top, attr, vm)
					}
					continue
				}

				sp--
				stack[sp] = OSValue{}

				if top.Type == OSVPromise {
					if attr == "then" {
						stack[sp] = BindPromiseThenMethod(top)
						sp++
						continue
					}
					if attr == "catch" {
						stack[sp] = BindPromiseCatchMethod(top)
						sp++
						continue
					}
				}

				if top.Type == OSVArray && top.Opaque != nil {
					switch attr {
					case "map":
						stack[sp] = BindArrayMapMethod(top)
					case "filter":
						stack[sp] = BindArrayFilterMethod(top)
					case "find":
						stack[sp] = BindArrayFindMethod(top)
					default:
						stack[sp] = vm.Null
					}
					sp++
					continue
				}

				if top.Type == OSVClass && top.Opaque != nil {
					class := (*Class)(top.Opaque)
					found := false
					for current := class; current != nil; current = current.Super {
						if value, exists := current.Template[attr]; exists {
							stack[sp] = value
							found = true
							break
						}
					}
					if !found {
						stack[sp] = vm.Null
					}
					sp++
					continue
				}

				errMsg = "attempted to get attribute of a non-object value " + valueString(top)
				goto throwError
			}
		case OpSetAttr:
			{
				var attr string
				attr, strTab, ip = readString(vm, bcode, ip, bsize, strTab)

				if sp < 2 {
					panic(msgUnderflow)
				}
				val := stack[sp-1]
				obj := stack[sp-2]
				stack[sp-1] = OSValue{}
				stack[sp-2] = OSValue{}
				sp -= 2

				if obj.Type != OSVObject || obj.Opaque == nil {
					errMsg = "attempted to set attribute of a non-object value " + valueString(obj)
					goto throwError
				}

				object := (*Object)(obj.Opaque)
				object.Set(attr, val)
			}
		case OpGetIndex:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				idx := stack[sp-1]
				obj := stack[sp-2]

				// Fast path: array[number].
				if obj.Type == OSVArray && obj.Opaque != nil && idx.Type == OSVNum {
					arr := (*Array)(obj.Opaque)
					i := int(idx.Number)
					if i >= 0 && i < len(arr.Elements) {
						stack[sp-1] = OSValue{}
						sp--
						stack[sp-1] = arr.Elements[i]
						continue
					}
					errMsg = msgIndexBounds
					goto throwError
				}

				stack[sp-1] = OSValue{}
				stack[sp-2] = OSValue{}
				sp -= 2

				if obj.Type == OSVObject && obj.Opaque != nil {
					if idx.Type != OSVString {
						errMsg = "object index must be a string"
						goto throwError
					}

					object := (*Object)(obj.Opaque)
					if value, exists := object.Values[idx.StringValue()]; exists {
						stack[sp] = value
					} else {
						stack[sp] = vm.Null
					}
					sp++
					continue
				}

				if obj.Type != OSVArray || obj.Opaque == nil {
					errMsg = "attempted to index a non-array value " + valueString(obj)
					goto throwError
				}

				// An array indexed by a non-number.
				errMsg = msgIndexNum
				goto throwError
			}
		case OpSetIndex:
			{
				if sp < 3 {
					panic(msgUnderflow)
				}
				val := stack[sp-1]
				idx := stack[sp-2]
				obj := stack[sp-3]

				if obj.Type != OSVArray || obj.Opaque == nil {
					errMsg = "attempted to index a non-array value " + valueString(obj)
					goto throwError
				}

				arr := (*Array)(obj.Opaque)

				if idx.Type != OSVNum {
					errMsg = msgIndexNum
					goto throwError
				}

				i := int(idx.Number)
				if i < 0 || i >= len(arr.Elements) {
					errMsg = msgIndexBounds
					goto throwError
				}

				arr.Elements[i] = val
				clear(stack[sp-3 : sp])
				sp -= 3
			}
		case OpRot2:
			{
				// A, B -> B, A
				stack[sp-2], stack[sp-1] = stack[sp-1], stack[sp-2]
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
				funcIndex := operand(bcode, ip, bsize)
				ip++

				if funcIndex < 0 || funcIndex >= len(state.Functions) {
					panic(fmt.Sprintf("invalid function index: %d", funcIndex))
				}

				fn := state.Functions[funcIndex]
				if fn == nil {
					panic(fmt.Sprintf("function at index %d is nil", funcIndex))
				}

				// Only functions that capture variables need a per-closure
				// copy. Everything else can share the single prototype.
				if len(fn.CaptureMeta) > 0 {
					fn = fn.Clone()
					if fn == nil {
						panic(fmt.Sprintf("function at index %d is nil", funcIndex))
					}

					// Save captured variables from the current environment
					// into the function's capture metadata.
					for i := range fn.CaptureMeta {
						capMeta := &fn.CaptureMeta[i]
						capMeta.Cell = env[capMeta.Src]
					}
				}

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = OSValue{
					Type:   OSVFunction,
					Opaque: unsafe.Pointer(fn),
				}
				sp++
			}
		case OpLoadBuiltin:
			{
				if sp < 2 || stack[sp-2].Type != OSVString || stack[sp-1].Type != OSVString {
					panic("invalid builtin module operands")
				}
				moduleName := stack[sp-2].StringValue()
				exportName := stack[sp-1].StringValue()
				stack[sp-2] = OSValue{}
				stack[sp-1] = OSValue{}
				sp -= 2
				module, exists := getBuiltinModule(moduleName)
				if !exists {
					panic("unknown builtin module: " + moduleName)
				}
				value, exists := module[exportName]
				if !exists {
					panic("unknown builtin export: " + exportName)
				}
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = value
				sp++
			}
		case OpLoadVar:
			{
				varIndex := int(bcode[ip])
				ip++

				if uint(varIndex) >= uint(len(env)) || env[varIndex] == nil {
					panic(fmt.Sprintf("invalid local variable index: %d of %d", varIndex, len(env)))
				}

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = env[varIndex].Value
				sp++
			}
		case OpUnaryPos:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				value := stack[sp-1]
				if value.Type != OSVBigInt && !IsNumber(value) {
					errMsg = "invalid operand for unary plus"
					goto throwError
				}
			}
		case OpUnaryNeg:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				value := stack[sp-1]
				if value.Type == OSVNum {
					stack[sp-1].Number = -value.Number
				} else if value.Type == OSVBigInt && value.Opaque != nil {
					stack[sp-1] = bigIntValue(new(big.Int).Neg((*big.Int)(value.Opaque)))
				} else if IsNumber(value) {
					stack[sp-1].Number = -value.Number
				} else {
					errMsg = "invalid operand for negation"
					goto throwError
				}
			}
		case OpUnaryNot:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				if stack[sp-1].Type != OSVBool {
					errMsg = "invalid operand for logical not"
					goto throwError
				}
				if stack[sp-1].Number == 0 {
					stack[sp-1] = vm.True
				} else {
					stack[sp-1] = vm.False
				}
			}
		case OpUnaryBitNot:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				value := stack[sp-1]
				if value.Type == OSVBigInt && value.Opaque != nil {
					stack[sp-1] = bigIntValue(new(big.Int).Not((*big.Int)(value.Opaque)))
				} else if IsNumber(value) {
					stack[sp-1] = OSValue{Type: OSVNum, Number: float64(^int64(value.Number))}
				} else {
					errMsg = "invalid operand for bitwise not"
					goto throwError
				}
			}
		case OpInc:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				top := &stack[sp-1]
				if top.Type == OSVNum {
					top.Number++
					continue
				}
				if top.Type == OSVBigInt && top.Opaque != nil {
					updated := new(big.Int).Add((*big.Int)(top.Opaque), big.NewInt(1))
					*top = bigIntValue(updated)
					continue
				}
				if !IsNumber(*top) {
					errMsg = msgIncDec
					goto throwError
				}
				top.Number++
			}
		case OpDec:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				top := &stack[sp-1]
				if top.Type == OSVNum {
					top.Number--
					continue
				}
				if top.Type == OSVBigInt && top.Opaque != nil {
					updated := new(big.Int).Sub((*big.Int)(top.Opaque), big.NewInt(1))
					*top = bigIntValue(updated)
					continue
				}
				if !IsNumber(*top) {
					errMsg = msgIncDec
					goto throwError
				}
				top.Number--
			}
		case OpPostInc:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				old := stack[sp-1]
				if old.Type == OSVBigInt && old.Opaque != nil {
					updated := new(big.Int).Add((*big.Int)(old.Opaque), big.NewInt(1))
					if sp >= len(stack) {
						stack = growStack(co)
					}
					stack[sp-1] = bigIntValue(updated)
					stack[sp] = old
					sp++
					continue
				}
				if old.Type != OSVNum && !IsNumber(old) {
					errMsg = msgIncDec
					goto throwError
				}

				// [..., v] -> [..., v+1, v]
				updated := old
				updated.Number++
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp-1] = updated
				stack[sp] = old
				sp++
			}
		case OpPostDec:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				old := stack[sp-1]
				if old.Type == OSVBigInt && old.Opaque != nil {
					updated := new(big.Int).Sub((*big.Int)(old.Opaque), big.NewInt(1))
					if sp >= len(stack) {
						stack = growStack(co)
					}
					stack[sp-1] = bigIntValue(updated)
					stack[sp] = old
					sp++
					continue
				}
				if old.Type != OSVNum && !IsNumber(old) {
					errMsg = msgIncDec
					goto throwError
				}

				// [..., v] -> [..., v-1, v]
				updated := old
				updated.Number--
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp-1] = updated
				stack[sp] = old
				sp++
			}
		case OpCallSuper:
			{
				argCount := operand(bcode, ip, bsize)
				ip++

				if !frame.Function.ClassConstructor || frame.Function.ClassSuperOffset >= len(env) ||
					frame.Function.ClassThisOffset >= len(env) {
					errMsg = "super constructor call outside a class constructor"
					goto throwError
				}
				if argCount < 0 || argCount > sp {
					errMsg = "invalid superclass constructor argument count"
					goto throwError
				}

				superValue := env[frame.Function.ClassSuperOffset].Value
				if superValue.Type != OSVClass || superValue.Opaque == nil {
					errMsg = "class has no superclass constructor"
					goto throwError
				}
				superClass := (*Class)(superValue.Opaque)
				constructor := superClass.Constructor
				if constructor.Type != OSVFunction || constructor.Opaque == nil {
					errMsg = "superclass has an invalid constructor"
					goto throwError
				}

				for sp+3 > len(stack) {
					stack = growStack(co)
				}
				argStart := sp - argCount
				copy(stack[argStart+2:sp+2], stack[argStart:sp])
				stack[argStart] = env[frame.Function.ClassThisOffset].Value
				stack[argStart+1] = OSValue{Type: OSVClass, Opaque: unsafe.Pointer(superClass.Super)}
				sp += 2
				stack[sp] = constructor
				sp++
			}
		case OpCall:
			{
				argCount := operand(bcode, ip, bsize)
				ip++

				// Expected stack layout:
				// [..., arg1, arg2, ..., argN, function]
				if sp <= 0 {
					panic(msgUnderflow)
				}
				sp--
				fnValue := stack[sp]
				stack[sp] = OSValue{}

				if fnValue.Type == OSVClass {
					if fnValue.Opaque == nil {
						errMsg = "attempted to call an invalid class"
						goto throwError
					}
					class := (*Class)(fnValue.Opaque)
					if (class.Constructor.Type != OSVFunction && class.Constructor.Type != OSVNativeFunction) || class.Constructor.Opaque == nil {
						errMsg = "class has an invalid constructor"
						goto throwError
					}
					constructorIsScript := class.Constructor.Type == OSVFunction
					if constructorIsScript && (*Function)(class.Constructor.Opaque).ClassConstructor {
						instance := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{
							ClassName: class.Name,
							Class:     class,
							Values:    make(map[string]OSValue),
						})}
						for sp+2 > len(stack) {
							stack = growStack(co)
						}
						argStart := sp - argCount
						copy(stack[argStart+2:sp+2], stack[argStart:sp])
						stack[argStart] = instance
						stack[argStart+1] = OSValue{Type: OSVClass, Opaque: unsafe.Pointer(class.Super)}
						sp += 2
						argCount += 2
					}
					fnValue = class.Constructor
				}

				switch fnValue.Type {
				case OSVFunction:
					{
						// Checked first: script-to-script calls are by far
						// the most common.
						if fnValue.Opaque == nil {
							errMsg = "attempted to call a nil function"
							goto throwError
						}
						fn := (*Function)(fnValue.Opaque)
						if argCount < 0 || argCount > sp {
							errMsg = "invalid function argument count"
							goto throwError
						}
						if fn == nil {
							errMsg = "attempted to call a nil function"
							goto throwError
						}

						if module := fn.Module; module != nil && module.Initialized {
							if argCount != 0 {
								errMsg = "invalid module initializer argument count"
								goto throwError
							}
							if sp >= len(stack) {
								stack = growStack(co)
							}
							stack[sp] = module.Value
							sp++
							continue
						}

						vaIndex := fn.VarArgIndex
						if (vaIndex == -1 && argCount != fn.ParamCount) || (vaIndex != -1 && argCount < vaIndex) {
							errMsg = argcMessage(fn.ParamCount, argCount)
							goto throwError
						}

						argStart := sp - argCount
						args := stack[argStart:sp]
						sp = argStart
						frame.Ip = ip
						co.Sp = sp

						caller := co
						callee := NewCoroutine(caller, fn, InitialStack(argCount))
						if callee == nil {
							panic("failed to create coroutine")
						}
						callee.SyncBoundary = caller.SyncBoundary
						if len(callee.OperandStack) < argCount {
							panic("callee operand stack is too small for arguments")
						}

						for i := range fn.CaptureMeta {
							capMeta := &fn.CaptureMeta[i]
							callee.Frame.Env[capMeta.Dst] = capMeta.Cell
						}

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
							if caller.SyncBoundary != nil {
								callee.SyncBoundary = promise
							}
							callee.Parent = SnapshotLexicalChain(caller)
							callee.Caller = nil
							callee.AsyncCaller = nil
							callee.AsyncResult = promise
							if caller.SyncBoundary != nil {
								if sp >= len(stack) {
									stack = growStack(co)
								}
								stack[sp] = OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
								sp++
								enqueueMicrotask(vm, callee)
								continue
							}
							callee.AsyncCaller = caller
							if sp >= len(stack) {
								stack = growStack(co)
							}
							stack[sp] = OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
							sp++
							frame.Ip = ip
							co.Sp = sp
							vm.Current = callee
							return
						}

						vm.Current = callee
						co = callee
						frame = &callee.Frame
						bcode = frame.Function.Bytecode
						bsize = len(bcode)
						ip = frame.Ip
						sp = callee.Sp
						stack = callee.OperandStack
						env = frame.Env
						strTab = nil
					}
				case OSVNativeFunction:
					{
						if fnValue.Opaque == nil {
							errMsg = "attempted to call a nil native function"
							goto throwError
						}
						native := (*NativeFunction)(fnValue.Opaque)
						if native.Callable == nil {
							errMsg = "attempted to call a native function without an implementation"
							goto throwError
						}
						if native.Paramc >= 0 && native.Paramc != argCount {
							errMsg = argcMessage(native.Paramc, argCount)
							goto throwError
						}
						if argCount < 0 || argCount > sp {
							errMsg = "invalid native function argument count"
							goto throwError
						}

						argStart := sp - argCount
						vm.NativeError = ""
						result := native.Callable(vm, stack[argStart:sp])
						if vm.NativeError != "" {
							errMsg = vm.NativeError
							vm.NativeError = ""
							clear(stack[argStart:sp])
							sp = argStart
							goto throwError
						}
						clear(stack[argStart:sp])
						sp = argStart
						if sp >= len(stack) {
							stack = growStack(co)
						}
						stack[sp] = result
						sp++
						continue
					}
				case OSVNativeMethod:
					{
						nativeMethod := (*NativeMethod)(fnValue.Opaque)
						if nativeMethod == nil {
							errMsg = "attempted to call a nil native method"
							goto throwError
						}
						if nativeMethod.Paramc != argCount {
							errMsg = "invalid argument count for native method"
							goto throwError
						}
						if argCount < 0 || argCount > sp {
							errMsg = "invalid native method argument count"
							goto throwError
						}

						argStart := sp - argCount
						vm.NativeError = ""
						result := nativeMethod.Callable(nativeMethod.This, vm, stack[argStart:sp])
						if vm.NativeError != "" {
							errMsg = vm.NativeError
							vm.NativeError = ""
							clear(stack[argStart:sp])
							sp = argStart
							goto throwError
						}
						clear(stack[argStart:sp])
						sp = argStart
						if sp >= len(stack) {
							stack = growStack(co)
						}
						stack[sp] = result
						sp++
						continue
					}
				default:
					{
						errMsg = "attempted to call a non-function value " + valueString(fnValue)
						goto throwError
					}
				}
			}
		case OpBinMul:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number * r.Number}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					stack[sp-2] = bigIntValue(new(big.Int).Mul((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgArith
					goto throwError
				}
				stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number * r.Number}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinDiv:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					if r.Number == 0 {
						errMsg = "division by zero"
						goto throwError
					}
					stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number / r.Number}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(r.Opaque).Sign() == 0 {
						errMsg = "division by zero"
						goto throwError
					}
					stack[sp-2] = bigIntValue(new(big.Int).Quo((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgArith
					goto throwError
				}
				if r.Number == 0 {
					errMsg = "division by zero"
					goto throwError
				}
				stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number / r.Number}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinMod:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					if r.Number == 0 {
						errMsg = "division by zero"
						goto throwError
					}
					stack[sp-2] = OSValue{
						Type:   OSVNum,
						Number: l.Number - float64(int64(l.Number/r.Number))*r.Number,
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(r.Opaque).Sign() == 0 {
						errMsg = "division by zero"
						goto throwError
					}
					stack[sp-2] = bigIntValue(new(big.Int).Rem((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgArith
					goto throwError
				}
				if r.Number == 0 {
					errMsg = "division by zero"
					goto throwError
				}
				stack[sp-2] = OSValue{
					Type:   OSVNum,
					Number: l.Number - float64(int64(l.Number/r.Number))*r.Number,
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinAdd:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number + r.Number}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					stack[sp-2] = bigIntValue(new(big.Int).Add((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if IsNumber(l) && IsNumber(r) {
					stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number + r.Number}
					sp--
					stack[sp] = OSValue{}
					continue
				}

				if IsString(l) && IsString(r) {
					value := l.StringValue() + r.StringValue()
					stack[sp-2] = OSValue{
						Type:   OSVString,
						Number: float64(len(value)),
						Opaque: unsafe.Pointer(unsafe.StringData(value)),
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if message, isError := errorMessage(r); IsString(l) && isError {
					value := l.StringValue() + message
					stack[sp-2] = OSValue{Type: OSVString, Number: float64(len(value)), Opaque: unsafe.Pointer(unsafe.StringData(value))}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if message, isError := errorMessage(l); isError && IsString(r) {
					value := message + r.StringValue()
					stack[sp-2] = OSValue{Type: OSVString, Number: float64(len(value)), Opaque: unsafe.Pointer(unsafe.StringData(value))}
					sp--
					stack[sp] = OSValue{}
					continue
				}

				errMsg = msgArith
				goto throwError
			}
		case OpBinSub:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number - r.Number}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					stack[sp-2] = bigIntValue(new(big.Int).Sub((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgArith
					goto throwError
				}
				stack[sp-2] = OSValue{Type: OSVNum, Number: l.Number - r.Number}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinShiftLeft:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{
						Type:   OSVNum,
						Number: float64(int64(l.Number) << uint(int64(r.Number))),
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(r.Opaque).Sign() < 0 || !(*big.Int)(r.Opaque).IsUint64() {
						errMsg = msgBitwise
						goto throwError
					}
					stack[sp-2] = bigIntValue(new(big.Int).Lsh((*big.Int)(l.Opaque), uint((*big.Int)(r.Opaque).Uint64())))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgBitwise
					goto throwError
				}
				stack[sp-2] = OSValue{
					Type:   OSVNum,
					Number: float64(int64(l.Number) << uint(int64(r.Number))),
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinShiftRight:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{
						Type:   OSVNum,
						Number: float64(int64(l.Number) >> uint(int64(r.Number))),
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(r.Opaque).Sign() < 0 || !(*big.Int)(r.Opaque).IsUint64() {
						errMsg = msgBitwise
						goto throwError
					}
					stack[sp-2] = bigIntValue(new(big.Int).Rsh((*big.Int)(l.Opaque), uint((*big.Int)(r.Opaque).Uint64())))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgBitwise
					goto throwError
				}
				stack[sp-2] = OSValue{
					Type:   OSVNum,
					Number: float64(int64(l.Number) >> uint(int64(r.Number))),
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinLess:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					if l.Number < r.Number {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(l.Opaque).Cmp((*big.Int)(r.Opaque)) < 0 {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgCompare
					goto throwError
				}
				if l.Number < r.Number {
					stack[sp-2] = vm.True
				} else {
					stack[sp-2] = vm.False
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinLessEqual:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					if l.Number <= r.Number {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(l.Opaque).Cmp((*big.Int)(r.Opaque)) <= 0 {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgCompare
					goto throwError
				}
				if l.Number <= r.Number {
					stack[sp-2] = vm.True
				} else {
					stack[sp-2] = vm.False
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinGreater:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					if l.Number > r.Number {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(l.Opaque).Cmp((*big.Int)(r.Opaque)) > 0 {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgCompare
					goto throwError
				}
				if l.Number > r.Number {
					stack[sp-2] = vm.True
				} else {
					stack[sp-2] = vm.False
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinGreaterEqual:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					if l.Number >= r.Number {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(l.Opaque).Cmp((*big.Int)(r.Opaque)) >= 0 {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgCompare
					goto throwError
				}
				if l.Number >= r.Number {
					stack[sp-2] = vm.True
				} else {
					stack[sp-2] = vm.False
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinEqual:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}

				l, r := &stack[sp-2], &stack[sp-1]
				var eq bool
				if l.Type == OSVNum && r.Type == OSVNum {
					eq = l.Number == r.Number
				} else {
					eq = IsEqual(*l, *r)
				}
				if eq {
					*l = vm.True
				} else {
					*l = vm.False
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinNotEqual:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}

				l, r := &stack[sp-2], &stack[sp-1]
				var eq bool
				if l.Type == OSVNum && r.Type == OSVNum {
					eq = l.Number == r.Number
				} else {
					eq = IsEqual(*l, *r)
				}
				if !eq {
					*l = vm.True
				} else {
					*l = vm.False
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpCaseEqual:
			{
				// [..., lhs, rhs] -> [..., lhs, bool]
				if sp < 2 {
					panic(msgUnderflow)
				}

				l, r := &stack[sp-2], &stack[sp-1]
				var eq bool
				if l.Type == OSVNum && r.Type == OSVNum {
					eq = l.Number == r.Number
				} else {
					eq = IsEqual(*l, *r)
				}
				if eq {
					*r = vm.True
				} else {
					*r = vm.False
				}
			}
		case OpBinBitAnd:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{
						Type:   OSVNum,
						Number: float64(int64(l.Number) & int64(r.Number)),
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					stack[sp-2] = bigIntValue(new(big.Int).And((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgBitwise
					goto throwError
				}
				stack[sp-2] = OSValue{
					Type:   OSVNum,
					Number: float64(int64(l.Number) & int64(r.Number)),
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinBitXor:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{
						Type:   OSVNum,
						Number: float64(int64(l.Number) ^ int64(r.Number)),
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					stack[sp-2] = bigIntValue(new(big.Int).Xor((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgBitwise
					goto throwError
				}
				stack[sp-2] = OSValue{
					Type:   OSVNum,
					Number: float64(int64(l.Number) ^ int64(r.Number)),
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpBinBitOr:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if l.Type == OSVNum && r.Type == OSVNum {
					stack[sp-2] = OSValue{
						Type:   OSVNum,
						Number: float64(int64(l.Number) | int64(r.Number)),
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if isBigIntPair(l, r) {
					stack[sp-2] = bigIntValue(new(big.Int).Or((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}
				if !(IsNumber(l) && IsNumber(r)) {
					errMsg = msgBitwise
					goto throwError
				}
				stack[sp-2] = OSValue{
					Type:   OSVNum,
					Number: float64(int64(l.Number) | int64(r.Number)),
				}
				sp--
				stack[sp] = OSValue{}
			}
		case OpDestructArray:
			{
				size := operand(bcode, ip, bsize)
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}
				sp--
				aobj := stack[sp]
				stack[sp] = OSValue{}

				if aobj.Type != OSVArray || aobj.Opaque == nil {
					errMsg = msgDestructArr
					goto throwError
				}

				elements := (*Array)(aobj.Opaque).Elements
				if len(elements) < size {
					errMsg = "array size mismatch for destructuring"
					goto throwError
				}

				// Make room once instead of checking per element.
				for sp+size > len(stack) {
					stack = growStack(co)
				}
				for i := size - 1; i >= 0; i-- {
					stack[sp] = elements[i]
					sp++
				}
			}
		case OpStoreLocal:
			{
				localIndex := int(bcode[ip])
				ip++

				if uint(localIndex) >= uint(len(env)) || env[localIndex] == nil {
					panic(fmt.Sprintf("invalid local variable index: %d (frame.Env length: %d)", localIndex, len(env)))
				}

				if sp <= 0 {
					panic(msgUnderflow)
				}

				sp--
				env[localIndex].Value = stack[sp]
				stack[sp] = OSValue{}
			}
		case OpPopTop:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				sp--
				stack[sp] = OSValue{}
			}

		case OpStdWrite:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				sp--
				value := stack[sp]
				stack[sp] = OSValue{}
				writeLog(valueString(value))
			}
		case OpJmp, OpAbsJmp:
			{
				ip = int(bcode[ip])
			}
		case OpPopJmpIfFalse:
			{
				offset := int(bcode[ip])
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}

				sp--
				expval := stack[sp]
				stack[sp] = OSValue{}
				var cond bool
				if expval.Type == OSVBool {
					cond = expval.Number != 0
				} else {
					cond = CoerceToBool(expval)
				}
				if !cond {
					ip = offset
				}
			}
		case OpPopJmpIfTrue:
			{
				offset := int(bcode[ip])
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}

				sp--
				expval := stack[sp]
				stack[sp] = OSValue{}
				var cond bool
				if expval.Type == OSVBool {
					cond = expval.Number != 0
				} else {
					cond = CoerceToBool(expval)
				}
				if cond {
					ip = offset
				}
			}
		case OpJmpIfFalseOrPop:
			{
				offset := int(bcode[ip])
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}

				top := &stack[sp-1]
				var cond bool
				if top.Type == OSVBool {
					cond = top.Number != 0
				} else {
					cond = CoerceToBool(*top)
				}
				if !cond {
					ip = offset
				} else {
					sp--
					stack[sp] = OSValue{}
				}
			}
		case OpJmpIfTrueOrPop:
			{
				offset := int(bcode[ip])
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}

				top := &stack[sp-1]
				var cond bool
				if top.Type == OSVBool {
					cond = top.Number != 0
				} else {
					cond = CoerceToBool(*top)
				}
				if cond {
					ip = offset
				} else {
					sp--
					stack[sp] = OSValue{}
				}
			}
		case OpDupTop:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = stack[sp-1]
				sp++
			}
		case OpDup2:
			{
				// [..., a, b] -> [..., a, b, a, b]
				for sp+2 > len(stack) {
					stack = growStack(co)
				}
				stack[sp] = stack[sp-2]
				stack[sp+1] = stack[sp-1]
				sp += 2
			}

		case OpAwait:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				sp--
				value := stack[sp]
				stack[sp] = OSValue{}

				if value.Type == OSVPromise {
					if value.Opaque == nil {
						errMsg = "attempted to await an invalid promise"
						goto throwError
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
					frame.Ip = ip
					co.Sp = sp
					return
				}

				stack[sp] = value
				sp++
			}
		case OpReturn:
			{
				var returnValue OSValue

				if sp > 0 {
					sp--
					returnValue = stack[sp]
					stack[sp] = OSValue{}
				} else {
					returnValue = vm.Null
				}
				if sp != 0 {
					panic(fmt.Sprintf("stack not empty on return (stack pointer: %d)", sp))
				}

				if module := frame.Function.Module; module != nil {
					module.Value = returnValue
					module.Initialized = true
				}

				// Save the completed coroutine's final state.
				frame.Ip = ip
				co.Sp = sp

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
				if caller != nil && caller.SyncCall && co.SyncRoot {
					caller.SyncResult = returnValue
					caller.SyncDone = true
					co.Release()
					vm.Current = caller
					return
				}

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
				if caller.Sp >= len(caller.OperandStack) {
					growStack(caller)
				}

				caller.OperandStack[caller.Sp] = returnValue
				caller.Sp++

				// Nothing references the finished coroutine any more; drop its
				// stack, cells and links so they can be collected.
				co.Release()

				// A caller that is waiting to be resumed with a value must go
				// through the normal entry path.
				if caller.ResumeReady {
					return
				}

				// Otherwise continue directly in the caller.
				co = caller
				frame = &caller.Frame
				bcode = frame.Function.Bytecode
				bsize = len(bcode)
				ip = frame.Ip
				sp = caller.Sp
				stack = caller.OperandStack
				env = frame.Env
				strTab = nil
			}
		default:
			{
				panic(fmt.Sprintf("unknown opcode %d at offset %d", op, ip-1))
			}
		}
		continue

		// Shared runtime-error handler: every `goto throwError` lands here
		// with errMsg set. raise() either redirects execution to a catch
		// block, kills the coroutine by rejecting its async promise, or
		// panics through ThrowError.
	throwError:
		{
			nip, nsp, dead := raise(vm, co, currentInstruction, stack, sp, errMsg)
			ip, sp = nip, nsp
			if dead {
				// The coroutine is dead: stop the dispatch loop.
				ip = bsize
			}
		}
	}

	// The coroutine was killed inside the loop (e.g. an unhandled error in an
	// async function rejected its promise); everything is already cleaned up.
	if co.State == CoroutineStateDead {
		return
	}

	// Persist state when bytecode reaches its end without OpReturn.
	frame.Ip = ip
	co.Sp = sp

	// Treat reaching the end of bytecode as coroutine completion.
	co.State = CoroutineStateDead
	returnValue := vm.Null
	if promise := co.AsyncResult; promise != nil {
		resolvePromise(vm, promise, returnValue)
	}

	parent := co.Caller
	if parent != nil && parent.SyncCall && co.SyncRoot {
		parent.SyncResult = returnValue
		parent.SyncDone = true
		co.Release()
		vm.Current = parent
		return
	}
	if parent == nil {
		co.Release()
		vm.Current = nil
		return
	}
	if parent.Sp >= len(parent.OperandStack) {
		growStack(parent)
	}
	parent.OperandStack[parent.Sp] = returnValue
	parent.Sp++
	co.Release()
	vm.Current = parent
}

func objectMethod(object OSValue, name string, vm *VM) OSValue {
	switch name {
	case "map":
		return BindObjectMapMethod(object)
	case "filter":
		return BindObjectFilterMethod(object)
	case "find":
		return BindObjectFindMethod(object)
	default:
		return vm.Null
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
		State:         state,
		Null:          OSValue{Type: OSVNull},
		True:          OSValue{Type: OSVBool, Number: 1},
		False:         OSValue{Type: OSVBool, Number: 0},
		strs:          make(map[unsafe.Pointer][]string, 16),
		HTTPRequests:  make(chan *httpIncoming, 64),
		HTTPDone:      make(chan *httpAppState, 64),
		HTTPFetches:   make(chan httpFetchResult, 64),
		SocketResults: make(chan socketResult, 64),
	}

	main := NewCoroutine(nil, program, InitialStack(0))

	if main == nil {
		panic("failed to create main coroutine")
	}

	vm.Current = main

	interpreterLoop(state, &vm)
}
