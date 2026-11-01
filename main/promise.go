package main

import "unsafe"

type Promise struct {
	Value      OSValue
	Resolved   bool
	Rejected   bool
	StringRoot string
	Coroutine  *Coroutine
	Then       OSValue
	Catch      OSValue
	Reactions  []PromiseReaction
}

type PromiseReaction struct {
	Callback OSValue
	Catch    bool
	Scope    *Coroutine
	Next     *Promise
	Awaiter  *Coroutine
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
	reaction := PromiseReaction{
		Callback: callback,
		Catch:    catch,
		Scope:    SnapshotLexicalChain(vm.Current),
		Next:     next,
	}
	if promise.Resolved {
		schedulePromiseReaction(vm, promise, reaction)
	} else {
		promise.Reactions = append(promise.Reactions, reaction)
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
	for _, reaction := range promise.Reactions {
		schedulePromiseReaction(vm, promise, reaction)
	}
	promise.Reactions = nil
}

func schedulePromiseReaction(vm *VM, promise *Promise, reaction PromiseReaction) {
	if reaction.Awaiter != nil {
		waiter := reaction.Awaiter
		waiter.ResumeValue = promise.Value
		waiter.ResumeReady = true
		waiter.ResumeReject = promise.Rejected
		waiter.StringRoot = promise.StringRoot
		enqueueMicrotask(vm, waiter)
		return
	}
	if reaction.Next == nil {
		return
	}
	if promise.Rejected != reaction.Catch {
		settlePromise(vm, reaction.Next, promise.Value, promise.Rejected)
		return
	}

	switch reaction.Callback.Type {
	case OSVFunction:
		if reaction.Callback.Opaque == nil {
			resolvePromise(vm, reaction.Next, promise.Value)
			return
		}

		callback := NewCoroutine(reaction.Scope, (*Function)(reaction.Callback.Opaque), InitialStack(1))
		function := (*Function)(reaction.Callback.Opaque)
		for index := range function.CaptureMeta {
			capture := &function.CaptureMeta[index]
			callback.Frame.Env[capture.Dst] = capture.Cell
		}
		callback.Parent = reaction.Scope
		callback.Caller = nil
		callback.Frame.Caller = nil
		callback.AsyncResult = reaction.Next
		callback.StringRoot = promise.StringRoot
		callback.OperandStack[0] = promise.Value
		callback.Sp = 1
		enqueueMicrotask(vm, callback)
	case OSVNativeFunction:
		if reaction.Callback.Opaque == nil {
			resolvePromise(vm, reaction.Next, promise.Value)
			return
		}

		native := (*NativeFunction)(reaction.Callback.Opaque)
		if native.Callable == nil {
			resolvePromise(vm, reaction.Next, promise.Value)
			return
		}
		result := native.Callable(vm, []OSValue{promise.Value})
		resolvePromise(vm, reaction.Next, result)
	default:
		resolvePromise(vm, reaction.Next, promise.Value)
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
