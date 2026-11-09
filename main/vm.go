package main

import (
	"fmt"
	"log"
	"math/big"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"
)

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
	// HTTPTimeouts delivers expired request dispatches to the VM thread.
	HTTPTimeouts chan *httpIncoming
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
	// nativeJIT stores executable machine-code tiers for eligible hot functions.
	nativeJIT map[*Function]nativeJITFunction

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
func (vm *VM) strTable(bytecode []int32) []string {
	key := unsafe.Pointer(unsafe.SliceData(bytecode))
	if key == vm.lastKey && vm.lastTab != nil {
		return vm.lastTab
	}
	t := vm.strs[key]
	if t == nil {
		t = make([]string, len(bytecode))
		vm.strs[key] = t
	}
	vm.lastKey, vm.lastTab = key, t
	return t
}

func (vm *VM) tryNative(fn *Function, args []OSValue) (OSValue, bool) {
	if !nativeJITPlatformSupported() {
		return OSValue{}, false
	}
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

func (vm *VM) closeNative() {
	for function, tier := range vm.nativeJIT {
		if tier.code != nil {
			tier.code.release()
		}
		delete(vm.nativeJIT, function)
	}
}

var StackSize = 1024

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

//go:noinline
func valueString(v OSValue) string { return v.String() }

func bigIntValue(value *big.Int) OSValue {
	return OSValue{Type: OSVBigInt, Opaque: unsafe.Pointer(value)}
}

// mkStringValue boxes s without copying it. Small enough to be inlined.
func mkStringValue(s string) OSValue {
	return OSValue{Type: OSVString, Number: float64(len(s)), Opaque: unsafe.Pointer(unsafe.StringData(s))}
}

func materializeConstant(value constVal) OSValue {
	switch value.kind {
	case kNum:
		{
			return OSValue{Type: OSVNum, Number: value.num}
		}
	case kStr:
		{
			return mkStringValue(value.str)
		}
	case kBool:
		{
			if value.b {
				return OSValue{Type: OSVBool, Number: 1}
			}
			return OSValue{Type: OSVBool}
		}
	case kNull:
		{
			return OSValue{}
		}
	case kArray:
		{
			elements := make([]OSValue, len(value.values))
			for index, element := range value.values {
				elements[index] = materializeConstant(element)
			}
			return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
		}
	case kObject:
		{
			object := &Object{Keys: make([]string, 0, len(value.keys)), Values: make(map[string]OSValue, len(value.keys))}
			for index, key := range value.keys {
				object.Set(key, materializeConstant(value.values[index]))
			}
			return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
		}
	default:
		{
			panic("invalid constant-pool value")
		}
	}
}

func isBigIntPair(left, right OSValue) bool {
	return left.Type == OSVBigInt && left.Opaque != nil && right.Type == OSVBigInt && right.Opaque != nil
}

func valuesEqual(left, right OSValue) bool {
	if left.Type == OSVNum && right.Type == OSVNum {
		return left.Number == right.Number
	}
	return IsEqual(left, right)
}

func finishBigIntBinary(stack []OSValue, sp int, op int32, left, right *big.Int) int {
	var result *big.Int
	switch op {
	case OpBinMul:
		result = new(big.Int).Mul(left, right)
	case OpBinSub:
		result = new(big.Int).Sub(left, right)
	case OpBinMod:
		result = new(big.Int).Rem(left, right)
	default:
		result = new(big.Int).Quo(left, right)
	}
	stack[sp-2] = bigIntValue(result)
	sp--
	stack[sp] = OSValue{}
	return sp
}

func extendOperands(stack []OSValue, sp int, valueType OSValueType, typeName, opcode string) (src, dst OSValue) {
	if sp < 2 {
		panic(msgUnderflow)
	}
	src, dst = stack[sp-1], stack[sp-2]
	if dst.Type != valueType || dst.Opaque == nil {
		panic("invalid destination " + typeName + " for " + opcode)
	}
	if src.Type != valueType || src.Opaque == nil {
		panic("invalid source " + typeName + " for " + opcode)
	}
	return src, dst
}

func compareNumbers(op int32, left, right float64) bool {
	switch op {
	case OpBinLess:
		return left < right
	case OpBinLessEqual:
		return left <= right
	case OpBinGreater:
		return left > right
	default:
		return left >= right
	}
}

func compareBigIntResult(op int32, comparison int) bool {
	switch op {
	case OpBinLess:
		return comparison < 0
	case OpBinLessEqual:
		return comparison <= 0
	case OpBinGreater:
		return comparison > 0
	default:
		return comparison >= 0
	}
}

func applyNumericBitwise(op int32, left, right int64) int64 {
	switch op {
	case OpBinBitAnd:
		return left & right
	case OpBinBitXor:
		return left ^ right
	default:
		return left | right
	}
}

//go:noinline
func argcMessage(expected, got int) string {
	return "function expects " + strconv.Itoa(expected) + " arguments, got " + strconv.Itoa(got)
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
func operand(bytecode []int32, ip, bsize int) int {
	if ip >= bsize {
		panic("unexpected end of bytecode")
	}
	return int(bytecode[ip])
}

// lineForOffset maps a bytecode offset to its source line (0 if before the
// first entry). Lines is sorted by Index, so a binary search replaces the
// linear scan that used to be duplicated in three cold paths.
func lineForOffset(fn *Function, offset int) int {
	lines := fn.Lines
	lo, hi := 0, len(lines)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if lines[mid].Index > offset {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == 0 {
		return 0
	}
	return lines[lo-1].Line
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
		} else if n := utf8.RuneLen(value); n > 0 {
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
		byteOffset += utf8.EncodeRune(buf[byteOffset:], value)
	}

	return unsafe.String(unsafe.SliceData(buf), byteSize)
}

// readString reads the string literal whose length word is at ip. It returns
// the string, the (possibly newly looked up) literal table and the ip after
// the literal.
func readString(vm *VM, bytecode []int32, ip, bsize int, strTab []string) (string, []string, int) {
	if ip >= bsize {
		panic("unexpected end of bytecode")
	}

	at := ip
	size := int(bytecode[ip])
	ip++

	if size < 0 || ip+size > bsize {
		panic("invalid string literal: exceeds bytecode")
	}

	if strTab == nil {
		strTab = vm.strTable(bytecode)
	}

	if s := strTab[at]; s != "" || size == 0 {
		return s, strTab, ip + size
	}

	s := decodeRunes(bytecode[ip : ip+size])
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
	return raiseValue(vm, co, instruction, stack, sp, OSValue{}, false, message)
}

func raiseValue(vm *VM, co *Coroutine, instruction int, stack []OSValue, sp int, value OSValue, isThrown bool, message string) (newIp, newSp int, dead bool) {
	frame := &co.Frame
	if !isThrown {
		value = newErrorValue(message, int64(lineForOffset(frame.Function, instruction)+1))
	}
	handlers := frame.Function.TryCatchTable
	for index := len(handlers) - 1; index >= 0; index-- {
		handler := handlers[index]
		if instruction < handler.TryStart || instruction >= handler.TryEnd {
			continue
		}

		if handler.CatchLocal >= 0 && handler.CatchLocal < len(frame.Env) && frame.Env[handler.CatchLocal] != nil {
			frame.Env[handler.CatchLocal].Value = value
		}
		clear(stack[:sp])
		return handler.CatchStart, 0, false
	}

	if promise := co.AsyncResult; promise != nil {
		promise.StringRoot = message
		promise.StackTrace = captureStackTrace(co, instruction)
		rejectPromise(vm, promise, value)
		co.State = CoroutineStateDead
		asyncCaller := co.AsyncCaller
		co.Release()
		vm.Current = asyncCaller
		return 0, 0, true
	}
	if boundary := co.SyncBoundary; boundary != nil {
		boundary.StringRoot = message
		boundary.StackTrace = captureStackTrace(co, instruction)
		rejectPromise(vm, boundary, value)
		co.State = CoroutineStateDead
		if root := boundary.Coroutine; root != nil && root != co {
			root.State = CoroutineStateDead
			root.Release()
		}
		co.Release()
		return 0, 0, true
	}

	fatalRuntimeError(co, instruction, message)
	return 0, 0, true
}

// fatalRuntimeError reports through the static errScratch writer (error.go)
// instead of fmt.Fprintln, so nothing is boxed into interfaces.
func fatalRuntimeError(co *Coroutine, instruction int, message string) {
	if co != nil && co.Frame.Function != nil {
		frame := &co.Frame
		line := lineForOffset(frame.Function, instruction)
		formatError(frame.Function.File, Position{Line: line, Column: 1}, message)
		printStackTrace(captureStackTrace(co, instruction))
	} else {
		w := &errScratch
		w.n = 0
		w.str(message)
		w.byte('\n')
		w.flush()
	}
	os.Exit(ExitFailure)
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
				frames = append(frames, StackTraceFrame{
					File:     frame.Function.File,
					Function: frame.Function.Name,
					Line:     lineForOffset(frame.Function, offset) + 1,
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

// printStackTrace writes through errScratch: no fmt.Fprintf, so name, file
// and frame.Line are no longer boxed (removes the escapes at old
// interpreter.go:371 and :381).
func printStackTrace(frames []StackTraceFrame) {
	if len(frames) == 0 {
		return
	}
	w := &errScratch
	w.n = 0
	w.str("Stack trace:\n")
	for _, frame := range frames {
		name := frame.Function
		if name == "" {
			name = "<anonymous>"
		}
		file := "<unknown>"
		if frame.File != nil && frame.File.Name != "" {
			file = frame.File.Name
		}
		w.str("  at ")
		w.str(name)
		w.str(" (")
		w.str(file)
		w.byte(':')
		w.int(frame.Line)
		w.str(")\n")
	}
	w.flush()
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
			w := &errScratch
			w.n = 0
			w.str("unhandled promise rejection: ")
			w.str(message)
			w.byte('\n')
			w.flush()
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
				{
					if incoming != nil {
						serveHTTPIncoming(vm, incoming)
					}
				}
			case <-vm.HTTPDone:
				{
					vm.HTTPActive--
				}
			case incoming := <-vm.HTTPTimeouts:
				{
					if incoming != nil && incoming.dispatch != nil {
						incoming.dispatch.timeout()
					}
				}
			case result := <-vm.HTTPFetches:
				{
					vm.HTTPFetchActive--
					settleHTTPFetch(vm, result)
				}
			case result := <-vm.SocketResults:
				{
					if result.accept != nil {
						settleSocketAccept(vm, *result.accept)
					} else if result.io != nil {
						settleSocketIO(vm, *result.io)
					}
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
// unchanged. In the number/number case the vacated stack slot is NOT zeroed:
// it only ever holds a pointer-free number, so there is nothing for the GC to
// retain and the store is pure overhead.
//
// Errors are raised with `errMsg = ...; goto throwError`; the shared handler
// at the bottom of the dispatch loop calls raise().
func runCoroutine(state *State, vm *VM, co *Coroutine) {
	defer func() {
		if recovered := recover(); recovered != nil {
			active := vm.Current
			instruction := 0
			if active != nil {
				instruction = active.Frame.Ip
			}
			fatalRuntimeError(active, instruction, "runtime failure: "+fmt.Sprint(recovered))
		}
	}()
	runCoroutineLoop(state, vm, co)
}

func finishNativeCall(co *Coroutine, stack []OSValue, sp, argStart int, result OSValue) ([]OSValue, int) {
	clear(stack[argStart:sp])
	sp = argStart
	if sp >= len(stack) {
		stack = growStack(co)
	}
	stack[sp] = result
	return stack, sp + 1
}

func consumeNativeError(vm *VM, stack []OSValue, sp, argStart int) string {
	message := vm.NativeError
	vm.NativeError = ""
	clear(stack[argStart:sp])
	return message
}

func finishNumericBinary(stack []OSValue, sp int, left, right, result OSValue) int {
	stack[sp-2] = result
	sp--
	if left.Type != OSVNum || right.Type != OSVNum {
		stack[sp] = OSValue{}
	}
	return sp
}

func runCoroutineLoop(state *State, vm *VM, co *Coroutine) {
	if co == nil || co.Frame.Function == nil {
		panic("invalid coroutine or call frame")
	}
	co.State = CoroutineStateRunning

	frame := &co.Frame
	bytecode := frame.Function.Bytecode

	ip := frame.Ip
	sp := co.Sp
	stack := co.OperandStack
	bsize := len(bytecode)
	currentInstruction := ip

	// Local environment of the current frame, cached next to bytecode/stack and
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
		frame.Ip = currentInstruction
		op := bytecode[ip]
		ip++

		switch op {
		case OpLoadNum:
			{
				a := bytecode[ip]
				b := bytecode[ip+1]
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
				value, strTab, ip = readString(vm, bytecode, ip, bsize, strTab)
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
				index := operand(bytecode, ip, bsize)
				ip++
				constants := frame.Function.Constants
				if uint(index) >= uint(len(constants)) {
					panic("invalid constant-pool index")
				}
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = materializeConstant(constants[index])
				sp++
			}
		case OpMakeClass:
			{
				methodCount := operand(bytecode, ip, bsize)
				ip++
				if methodCount < 0 || sp < 2+2*methodCount {
					panic("invalid class construction operands")
				}
				base := sp - 2 - 2*methodCount
				nameValue := stack[base]
				if nameValue.Type != OSVString {
					panic("invalid class name operand")
				}
				var super *Class
				baseValue := stack[base+1]
				if baseValue.Type != OSVNull {
					if baseValue.Type != OSVClass || baseValue.Opaque == nil {
						errMsg = "class superclass must be a class"
						goto throwError
					}
					super = (*Class)(baseValue.Opaque)
				}
				class := &Class{Name: nameValue.StringValue(), Super: super, Template: make(map[string]OSValue, methodCount)}
				for index := 0; index < methodCount; index++ {
					key := stack[base+2+2*index]
					val := stack[base+3+2*index]
					if key.Type != OSVString || (val.Type == OSVFunction && val.Opaque == nil) {
						panic("invalid class method entry")
					}
					methodName := key.StringValue()
					class.Template[methodName] = val
					if methodName == "constructor" {
						class.Constructor = val
					}
				}
				clear(stack[base:sp])
				sp = base
				stack[sp] = OSValue{Type: OSVClass, Opaque: unsafe.Pointer(class)}
				sp++
			}
		case OpLoadStr:
			{
				var value string
				value, strTab, ip = readString(vm, bytecode, ip, bsize, strTab)

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = mkStringValue(value)
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
				src, dst := extendOperands(stack, sp, OSVArray, "array", "OpExtendArray")

				dstArray := (*Array)(dst.Opaque)
				srcArray := (*Array)(src.Opaque)
				dstArray.Elements = append(dstArray.Elements, srcArray.Elements...)

				// dst stays where it is; only the source is consumed.
				stack[sp-1] = OSValue{}
				sp--
			}
		case OpMakeArray:
			{
				size := operand(bytecode, ip, bsize)
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
				src, dst := extendOperands(stack, sp, OSVObject, "object", "OpExtendObject")

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
				size := operand(bytecode, ip, bsize)
				ip++

				if size < 0 || sp < 2*size {
					panic("operand stack underflow for object creation")
				}

				base := sp - 2*size
				obj := &Object{Values: make(map[string]OSValue, size)}
				// Reserve key capacity once instead of growing per Set.
				obj.Keys = slices.Grow(obj.Keys, size)
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
		case OpGetAttr:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				var attr string
				attr, strTab, ip = readString(vm, bytecode, ip, bsize, strTab)

				// Plain objects are by far the most common receiver. The
				// value types are mutually exclusive, so checking them
				// first changes nothing. The result overwrites the receiver
				// in place: no pop, clear and push.
				top := stack[sp-1]
				if top.Type == OSVObject && top.Opaque != nil {
					object := (*Object)(top.Opaque)
					if value, exists := object.Values[attr]; exists {
						stack[sp-1] = value
					} else {
						class := runtimeClass(top)
						if value, exists := classTemplateValue(class, attr); exists {
							stack[sp-1] = bindClassMethod(top, attr, value)
						} else if class != getCoreObjectClass() {
							if value, exists := classTemplateValue(getCoreObjectClass(), attr); exists {
								stack[sp-1] = bindClassMethod(top, attr, value)
							} else {
								stack[sp-1] = vm.Null
							}
						} else {
							stack[sp-1] = vm.Null
						}
					}
					continue
				}

				if top.Type == OSVArray && attr == "length" && top.Opaque != nil {
					stack[sp-1] = OSValue{Type: OSVNum, Number: float64(len((*Array)(top.Opaque).Elements))}
					continue
				}

				if class := runtimeClass(top); class != nil {
					if value, exists := classTemplateValue(class, attr); exists {
						stack[sp-1] = bindClassMethod(top, attr, value)
						continue
					}
					stack[sp-1] = vm.Null
					continue
				}

				if top.Type == OSVClass && top.Opaque != nil {
					class := (*Class)(top.Opaque)
					if value, exists := classTemplateValue(class, attr); exists {
						stack[sp-1] = value
					} else {
						stack[sp-1] = vm.Null
					}
					continue
				}

				errMsg = "attempted to get attribute of a non-object value " + valueString(top)
				goto throwError
			}
		case OpSetAttr:
			{
				var attr string
				attr, strTab, ip = readString(vm, bytecode, ip, bsize, strTab)

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
					if uint(i) < uint(len(arr.Elements)) {
						// idx is a plain number: nothing to release.
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
					errMsg = "attempted to index a non-array value " + valueString(obj) + " with index " + valueString(idx)
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
				if obj.Type == OSVObject && obj.Opaque != nil {
					if idx.Type != OSVString {
						errMsg = "object index must be a string"
						goto throwError
					}
					(*Object)(obj.Opaque).Set(idx.StringValue(), val)
					clear(stack[sp-3 : sp])
					sp -= 3
					continue
				}

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
				if uint(i) >= uint(len(arr.Elements)) {
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
				funcIndex := operand(bytecode, ip, bsize)
				ip++

				if funcIndex < 0 || funcIndex >= len(state.Functions) {
					panic("invalid function index: " + strconv.Itoa(funcIndex))
				}

				fn := state.Functions[funcIndex]
				if fn == nil {
					panic("function at index " + strconv.Itoa(funcIndex) + " is nil")
				}

				// Only functions that capture variables need a per-closure
				// copy. Everything else can share the single prototype.
				if len(fn.CaptureMeta) > 0 {
					fn = fn.Clone()
					if fn == nil {
						panic("function at index " + strconv.Itoa(funcIndex) + " is nil")
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
		case OpTypeOf:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				stack[sp-1] = mkStringValue(stack[sp-1].TypeName())
			}
		case OpLoadVar:
			{
				// A bad index or a nil cell panics on its own (bounds check /
				// nil dereference) and is reported by the recover in
				// runCoroutine, so no explicit validation is needed here.
				cell := env[bytecode[ip]]
				ip++

				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp] = cell.Value
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
		case OpInc, OpDec:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				delta := 1.0
				if op == OpDec {
					delta = -1
				}
				top := &stack[sp-1]
				if top.Type == OSVNum {
					top.Number += delta
					continue
				}
				if top.Type == OSVBigInt && top.Opaque != nil {
					updated := new(big.Int).Add((*big.Int)(top.Opaque), big.NewInt(int64(delta)))
					*top = bigIntValue(updated)
					continue
				}
				if !IsNumber(*top) {
					errMsg = msgIncDec
					goto throwError
				}
			}
		case OpPostInc, OpPostDec:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}

				delta := 1.0
				if op == OpPostDec {
					delta = -1
				}
				old := stack[sp-1]
				if old.Type == OSVBigInt && old.Opaque != nil {
					updated := new(big.Int).Add((*big.Int)(old.Opaque), big.NewInt(int64(delta)))
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

				updated := old
				updated.Number += delta
				if sp >= len(stack) {
					stack = growStack(co)
				}
				stack[sp-1] = updated
				stack[sp] = old
				sp++
			}
		case OpCallSuper:
			{
				argCount := operand(bytecode, ip, bsize)
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
				argCount := operand(bytecode, ip, bsize)
				ip++

				// Expected stack layout:
				// [..., arg1, arg2, ..., argN, function]
				if sp <= 0 {
					panic(msgUnderflow)
				}
				sp--
				fnValue := stack[sp]
				stack[sp] = OSValue{}
				if fnValue.Type == OSVMethod {
					if fnValue.Opaque == nil {
						errMsg = "attempted to call an invalid method"
						goto throwError
					}
					method := (*Method)(fnValue.Opaque)
					if method.Callable.Type != OSVFunction || method.Callable.Opaque == nil || argCount < 0 || argCount > sp {
						errMsg = "invalid script method"
						goto throwError
					}
					for sp+1 > len(stack) {
						stack = growStack(co)
					}
					argStart := sp - argCount
					copy(stack[argStart+1:sp+1], stack[argStart:sp])
					stack[argStart] = method.This
					sp++
					argCount++
					fnValue = method.Callable
				}

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
						if argCount < 2 {
							panic("invalid class constructor arguments")
						}
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
						if fn.Generator {
							vaIndex := fn.VarArgIndex
							if (vaIndex == -1 && argCount != fn.ParamCount) || (vaIndex != -1 && argCount < vaIndex) {
								errMsg = argcMessage(fn.ParamCount, argCount)
								goto throwError
							}
							argStart := sp - argCount
							coroutine := NewCoroutine(co, fn, InitialStack(argCount))
							for i := range fn.CaptureMeta {
								capMeta := &fn.CaptureMeta[i]
								coroutine.Frame.Env[capMeta.Dst] = capMeta.Cell
							}
							if vaIndex >= 0 {
								variadicArgs := append([]OSValue(nil), stack[argStart+vaIndex:sp]...)
								copy(coroutine.OperandStack[:vaIndex], stack[argStart:argStart+vaIndex])
								coroutine.OperandStack[vaIndex] = OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: variadicArgs})}
								coroutine.Sp = vaIndex + 1
							} else {
								copy(coroutine.OperandStack[:argCount], stack[argStart:sp])
								coroutine.Sp = argCount
							}
							clear(stack[argStart:sp])
							sp = argStart
							generator := &Generator{Coroutine: coroutine}
							coroutine.Generator = generator
							stack[sp] = OSValue{Type: OSVGenerator, Opaque: unsafe.Pointer(generator)}
							sp++
							continue
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

						if result, executed := vm.tryNative(fn, stack[sp-argCount:sp]); executed {
							clear(stack[sp-argCount : sp])
							sp -= argCount
							if sp >= len(stack) {
								stack = growStack(co)
							}
							stack[sp] = result
							sp++
							continue
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
						bytecode = frame.Function.Bytecode
						bsize = len(bytecode)
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
						if native.ParamCount >= 0 && native.ParamCount != argCount {
							errMsg = argcMessage(native.ParamCount, argCount)
							goto throwError
						}
						if argCount < 0 || argCount > sp {
							errMsg = "invalid native function argument count"
							goto throwError
						}

						argStart := sp - argCount
						result := invokeNativeFunction(vm, native.Callable, stack[argStart:sp])
						if vm.NativeError != "" {
							errMsg = consumeNativeError(vm, stack, sp, argStart)
							sp = argStart
							goto throwError
						}
						stack, sp = finishNativeCall(co, stack, sp, argStart, result)
						continue
					}
				case OSVNativeMethod:
					{
						nativeMethod := (*NativeMethod)(fnValue.Opaque)
						if nativeMethod == nil {
							errMsg = "attempted to call a nil native method"
							goto throwError
						}
						if nativeMethod.ParamCount >= 0 && nativeMethod.ParamCount != argCount {
							errMsg = "invalid argument count for native method"
							goto throwError
						}
						if argCount < 0 || argCount > sp {
							errMsg = "invalid native method argument count"
							goto throwError
						}

						argStart := sp - argCount
						result := invokeNativeMethod(vm, nativeMethod.Callable, nativeMethod.This, stack[argStart:sp])
						if vm.NativeError != "" {
							errMsg = consumeNativeError(vm, stack, sp, argStart)
							sp = argStart
							goto throwError
						}
						stack, sp = finishNativeCall(co, stack, sp, argStart, result)
						continue
					}
				default:
					{
						errMsg = "attempted to call a non-function value " + valueString(fnValue)
						goto throwError
					}
				}
			}
		case OpBinMul, OpBinSub:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				multiply := op == OpBinMul
				if IsNumber(l) && IsNumber(r) {
					result := l.Number - r.Number
					if multiply {
						result = l.Number * r.Number
					}
					sp = finishNumericBinary(stack, sp, l, r, OSValue{Type: OSVNum, Number: result})
					continue
				}
				if isBigIntPair(l, r) {
					sp = finishBigIntBinary(stack, sp, op, (*big.Int)(l.Opaque), (*big.Int)(r.Opaque))
					continue
				}
				errMsg = msgArith
				goto throwError
			}
		case OpBinDiv, OpBinMod:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				modulo := op == OpBinMod
				if IsNumber(l) && IsNumber(r) {
					if r.Number == 0 {
						errMsg = "division by zero"
						goto throwError
					}
					result := l.Number / r.Number
					if modulo {
						result = l.Number - float64(int64(l.Number/r.Number))*r.Number
					}
					sp = finishNumericBinary(stack, sp, l, r, OSValue{Type: OSVNum, Number: result})
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(r.Opaque).Sign() == 0 {
						errMsg = "division by zero"
						goto throwError
					}
					sp = finishBigIntBinary(stack, sp, op, (*big.Int)(l.Opaque), (*big.Int)(r.Opaque))
					continue
				}
				errMsg = msgArith
				goto throwError
			}
		case OpBinAdd:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if IsNumber(l) && IsNumber(r) {
					sp = finishNumericBinary(stack, sp, l, r, OSValue{Type: OSVNum, Number: l.Number + r.Number})
					continue
				}
				if isBigIntPair(l, r) {
					stack[sp-2] = bigIntValue(new(big.Int).Add((*big.Int)(l.Opaque), (*big.Int)(r.Opaque)))
					sp--
					stack[sp] = OSValue{}
					continue
				}

				if IsString(l) {
					if IsString(r) {
						stack[sp-2] = mkStringValue(l.StringValue() + r.StringValue())
						sp--
						stack[sp] = OSValue{}
						continue
					}
					if message, isError := errorMessage(r); isError {
						stack[sp-2] = mkStringValue(l.StringValue() + message)
						sp--
						stack[sp] = OSValue{}
						continue
					}
				} else if IsString(r) {
					if message, isError := errorMessage(l); isError {
						stack[sp-2] = mkStringValue(message + r.StringValue())
						sp--
						stack[sp] = OSValue{}
						continue
					}
				}

				errMsg = msgArith
				goto throwError
			}
		case OpBinShiftLeft, OpBinShiftRight:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				shiftLeft := op == OpBinShiftLeft
				if IsNumber(l) && IsNumber(r) {
					shift := int64(r.Number)
					var result int64
					if shiftLeft {
						result = int64(l.Number) << uint(shift)
					} else {
						result = int64(l.Number) >> uint(shift)
					}
					sp = finishNumericBinary(stack, sp, l, r, OSValue{Type: OSVNum, Number: float64(result)})
					continue
				}
				if isBigIntPair(l, r) {
					if (*big.Int)(r.Opaque).Sign() < 0 || !(*big.Int)(r.Opaque).IsUint64() {
						errMsg = msgBitwise
						goto throwError
					}
					if shiftLeft {
						stack[sp-2] = bigIntValue(new(big.Int).Lsh((*big.Int)(l.Opaque), uint((*big.Int)(r.Opaque).Uint64())))
					} else {
						stack[sp-2] = bigIntValue(new(big.Int).Rsh((*big.Int)(l.Opaque), uint((*big.Int)(r.Opaque).Uint64())))
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				errMsg = msgBitwise
				goto throwError
			}
		case OpBinLess, OpBinLessEqual, OpBinGreater, OpBinGreaterEqual:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if IsNumber(l) && IsNumber(r) {
					result := vm.False
					if compareNumbers(op, l.Number, r.Number) {
						result = vm.True
					}
					sp = finishNumericBinary(stack, sp, l, r, result)
					continue
				}
				if isBigIntPair(l, r) {
					comparison := (*big.Int)(l.Opaque).Cmp((*big.Int)(r.Opaque))
					if compareBigIntResult(op, comparison) {
						stack[sp-2] = vm.True
					} else {
						stack[sp-2] = vm.False
					}
					sp--
					stack[sp] = OSValue{}
					continue
				}
				errMsg = msgCompare
				goto throwError
			}
		case OpBinEqual, OpBinNotEqual:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}

				l, r := &stack[sp-2], &stack[sp-1]
				matches := valuesEqual(*l, *r)
				if matches == (op == OpBinEqual) {
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
				if valuesEqual(*l, *r) {
					*r = vm.True
				} else {
					*r = vm.False
				}
			}
		case OpBinBitAnd, OpBinBitXor, OpBinBitOr:
			{
				if sp < 2 {
					panic(msgUnderflow)
				}
				l, r := stack[sp-2], stack[sp-1]
				if IsNumber(l) && IsNumber(r) {
					result := OSValue{
						Type:   OSVNum,
						Number: float64(applyNumericBitwise(op, int64(l.Number), int64(r.Number))),
					}
					sp = finishNumericBinary(stack, sp, l, r, result)
					continue
				}
				if isBigIntPair(l, r) {
					left, right := (*big.Int)(l.Opaque), (*big.Int)(r.Opaque)
					var result *big.Int
					switch op {
					case OpBinBitAnd:
						result = new(big.Int).And(left, right)
					case OpBinBitXor:
						result = new(big.Int).Xor(left, right)
					default:
						result = new(big.Int).Or(left, right)
					}
					stack[sp-2] = bigIntValue(result)
					sp--
					stack[sp] = OSValue{}
					continue
				}
				errMsg = msgBitwise
				goto throwError
			}
		case OpDestructArray:
			{
				size := operand(bytecode, ip, bsize)
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}
				sp--
				arrayValue := stack[sp]
				stack[sp] = OSValue{}

				if arrayValue.Type != OSVArray || arrayValue.Opaque == nil {
					errMsg = msgDestructArr
					goto throwError
				}

				elements := (*Array)(arrayValue.Opaque).Elements
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
				// See OpLoadVar: invalid indexes panic on their own.
				cell := env[bytecode[ip]]
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}

				sp--
				cell.Value = stack[sp]
				stack[sp] = OSValue{}
			}
		case OpCloneLocal:
			{
				offset := bytecode[ip]
				ip++
				cell := env[offset]
				env[offset] = &Cell{Value: cell.Value}
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
				ip = int(bytecode[ip])
			}
		case OpPopJmpIfFalse, OpPopJmpIfTrue:
			{
				offset := int(bytecode[ip])
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}

				sp--
				expressionValue := stack[sp]
				stack[sp] = OSValue{}
				cond := false
				if expressionValue.Type == OSVBool {
					cond = expressionValue.Number != 0
				} else {
					cond = CoerceToBool(expressionValue)
				}
				if cond == (op == OpPopJmpIfTrue) {
					ip = offset
				}
			}
		case OpJmpIfFalseOrPop, OpJmpIfTrueOrPop:
			{
				offset := int(bytecode[ip])
				ip++

				if sp <= 0 {
					panic(msgUnderflow)
				}

				top := &stack[sp-1]
				cond := false
				if top.Type == OSVBool {
					cond = top.Number != 0
				} else {
					cond = CoerceToBool(*top)
				}
				if cond == (op == OpJmpIfTrueOrPop) {
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
					co.State = CoroutineStateSuspended
					frame.Ip = ip
					co.Sp = sp
					return
				}

				stack[sp] = value
				sp++
			}
		case OpYield:
			{
				if co.Generator == nil || sp <= 0 {
					panic("yield outside generator coroutine")
				}
				sp--
				co.Generator.Yielded = stack[sp]
				stack[sp] = OSValue{}
				frame.Ip = ip
				co.Sp = sp
				co.State = CoroutineStateSuspended
				vm.Current = nil
				return
			}
		case OpThrow:
			{
				if sp <= 0 {
					panic(msgUnderflow)
				}
				sp--
				value := stack[sp]
				stack[sp] = OSValue{}
				message, isError := errorMessage(value)
				if !isError {
					message = value.String()
				}
				nip, nsp, dead := raiseValue(vm, co, currentInstruction, stack, sp, value, true, message)
				if dead {
					return
				}
				ip, sp = nip, nsp
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
				if frame.Function.ClassConstructor && frame.Function.ClassThisOffset >= 0 && frame.Function.ClassThisOffset < len(env) {
					returnValue = env[frame.Function.ClassThisOffset].Value
				}
				if sp != 0 {
					panic("stack not empty on return (stack pointer: " + strconv.Itoa(sp) + ")")
				}

				if module := frame.Function.Module; module != nil {
					module.Value = returnValue
					module.Initialized = true
				}

				// Save the completed coroutine's final state.
				frame.Ip = ip
				co.Sp = sp

				co.State = CoroutineStateDead
				if generator := co.Generator; generator != nil {
					generator.Done = true
					generator.Final = returnValue
					generator.Coroutine = nil
					co.Generator = nil
					vm.Current = nil
					co.Release()
					return
				}
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

				// Nothing references the finished coroutine anymore; drop its
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
				bytecode = frame.Function.Bytecode
				bsize = len(bytecode)
				ip = frame.Ip
				sp = caller.Sp
				stack = caller.OperandStack
				env = frame.Env
				strTab = nil
			}
		default:
			{
				panic("unknown opcode " + strconv.Itoa(int(op)) + " at offset " + strconv.Itoa(ip-1))
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

func Run(state *State, program *Function) {
	if state == nil {
		fatalRuntimeError(nil, 0, "state cannot be nil")
		return
	}

	if program == nil {
		fatalRuntimeError(nil, 0, "program function cannot be nil")
		return
	}

	vm := VM{
		State:         state,
		Null:          OSValue{Type: OSVNull},
		True:          OSValue{Type: OSVBool, Number: 1},
		False:         OSValue{Type: OSVBool, Number: 0},
		strs:          make(map[unsafe.Pointer][]string, 16),
		HTTPRequests:  make(chan *httpIncoming, 64),
		HTTPDone:      make(chan *httpAppState, 64),
		HTTPTimeouts:  make(chan *httpIncoming, 64),
		HTTPFetches:   make(chan httpFetchResult, 64),
		SocketResults: make(chan socketResult, 64),
	}
	defer vm.closeNative()

	main := NewCoroutine(nil, program, InitialStack(0))

	vm.Current = main

	interpreterLoop(state, &vm)
}
