package main

import "unsafe"

type Promise struct {
	Value         OSValue
	Resolved      bool
	Rejected      bool
	StringRoot    string
	Coroutine     *Coroutine
	Then          OSValue
	Catch         OSValue
	Callback      OSValue
	CallbackCatch bool
	CallbackScope *Coroutine
	Next          *Promise
}

func PromiseThen(this OSValue, vm *VM, args []OSValue) OSValue {
	return registerPromiseCallback(this, vm, args[0], false)
}

func PromiseCatch(this OSValue, vm *VM, args []OSValue) OSValue {
	return registerPromiseCallback(this, vm, args[0], true)
}

func registerPromiseCallback(this OSValue, vm *VM, callback OSValue, catch bool) OSValue {
	promise := (*Promise)(this.Opaque)
	next := &Promise{}
	promise.Callback = callback
	promise.CallbackCatch = catch
	promise.CallbackScope = SnapshotLexicalChain(vm.Current)
	promise.Next = next
	if promise.Resolved {
		schedulePromiseCallback(vm, promise)
	}
	return OSValue{
		Type:   OSVPromise,
		Opaque: unsafe.Pointer(next),
	}
}

func resolvePromise(vm *VM, promise *Promise, value OSValue) {
	settlePromise(vm, promise, value, false)
}

func rejectPromise(vm *VM, promise *Promise, value OSValue) {
	settlePromise(vm, promise, value, true)
}

func settlePromise(vm *VM, promise *Promise, value OSValue, rejected bool) {
	if value.Type == OSVString {
		promise.StringRoot = value.StringValue()
		value = OSValue{
			Type:   OSVString,
			Number: float64(len(promise.StringRoot)),
			Opaque: unsafe.Pointer(unsafe.StringData(promise.StringRoot)),
		}
	}
	promise.Value = value
	promise.Resolved = true
	promise.Rejected = rejected
	promise.Coroutine = nil
	if promise.Next != nil {
		schedulePromiseCallback(vm, promise)
	}
}

func schedulePromiseCallback(vm *VM, promise *Promise) {
	if promise.Next == nil {
		return
	}
	if promise.Rejected != promise.CallbackCatch {
		settlePromise(vm, promise.Next, promise.Value, promise.Rejected)
		return
	}

	switch promise.Callback.Type {
	case OSVFunction:
		if promise.Callback.Opaque == nil {
			resolvePromise(vm, promise.Next, promise.Value)
			return
		}

		callback := NewCoroutine(promise.CallbackScope, (*Function)(promise.Callback.Opaque), InitialStack(1))
		callback.Parent = promise.CallbackScope
		callback.Caller = nil
		callback.Frame.Caller = nil
		callback.AsyncResult = promise.Next
		callback.StringRoot = promise.StringRoot
		callback.OperandStack[0] = promise.Value
		callback.Sp = 1
		vm.Pending = append(vm.Pending, callback)
	case OSVNativeFunction:
		if promise.Callback.Opaque == nil {
			resolvePromise(vm, promise.Next, promise.Value)
			return
		}

		native := (*NativeFunction)(promise.Callback.Opaque)
		if native.Callable == nil {
			resolvePromise(vm, promise.Next, promise.Value)
			return
		}
		native.Callable(vm, []OSValue{promise.Value})
		resolvePromise(vm, promise.Next, vm.Null)
	default:
		resolvePromise(vm, promise.Next, promise.Value)
	}
}

func BindPromiseThenMethod(this OSValue) OSValue {
	promise := (*Promise)(this.Opaque)

	if promise.Then == (OSValue{}) {
		promise.Then = OSValue{
			Type: OSVNativeMethod,
			Opaque: unsafe.Pointer(&NativeMethod{
				This:     this,
				Paramc:   1,
				Callable: PromiseThen,
			}),
		}
	}

	return promise.Then
}

func BindPromiseCatchMethod(this OSValue) OSValue {
	promise := (*Promise)(this.Opaque)

	if promise.Catch == (OSValue{}) {
		promise.Catch = OSValue{
			Type: OSVNativeMethod,
			Opaque: unsafe.Pointer(&NativeMethod{
				This:     this,
				Paramc:   1,
				Callable: PromiseCatch,
			}),
		}
	}

	return promise.Catch
}
