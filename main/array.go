package main

import "unsafe"

type Array struct {
	// Elements stores values in script-visible index order and is the array's mutable payload.
	Elements []OSValue
	// Map caches the bound map method so repeated property access reuses its receiver wrapper.
	Map OSValue
	// Filter caches the bound filter method for stable, allocation-free repeated lookup.
	Filter OSValue
	// Find caches the bound find method for stable, allocation-free repeated lookup.
	Find OSValue
}

func bindArrayMethod(array OSValue, method *OSValue, paramCount int, callable func(this OSValue, vm *VM, args []OSValue) OSValue) OSValue {
	if *method == (OSValue{}) {
		*method = OSValue{
			Type: OSVNativeMethod,
			Opaque: unsafe.Pointer(&NativeMethod{
				This:     array,
				Paramc:   paramCount,
				Callable: callable,
			}),
		}
	}
	return *method
}

func BindArrayMapMethod(this OSValue) OSValue {
	return bindArrayMethod(this, &(*Array)(this.Opaque).Map, 1, ArrayMap)
}

func BindArrayFilterMethod(this OSValue) OSValue {
	return bindArrayMethod(this, &(*Array)(this.Opaque).Filter, 1, ArrayFilter)
}

func BindArrayFindMethod(this OSValue) OSValue {
	return bindArrayMethod(this, &(*Array)(this.Opaque).Find, 1, ArrayFind)
}

func ArrayMap(this OSValue, vm *VM, args []OSValue) OSValue {
	array := (*Array)(this.Opaque)
	if !validateArrayCallback(args[0], vm) {
		return vm.Null
	}
	length := len(array.Elements)
	result := &Array{Elements: make([]OSValue, length)}
	for index := 0; index < length; index++ {
		value, ok := invokeArrayCallback(this, arrayElement(array, index, vm), index, args[0], vm)
		if !ok {
			return vm.Null
		}
		result.Elements[index] = value
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(result)}
}

func ArrayFilter(this OSValue, vm *VM, args []OSValue) OSValue {
	array := (*Array)(this.Opaque)
	if !validateArrayCallback(args[0], vm) {
		return vm.Null
	}
	length := len(array.Elements)
	result := &Array{Elements: make([]OSValue, 0, length)}
	for index := 0; index < length; index++ {
		value := arrayElement(array, index, vm)
		keep, ok := invokeArrayCallback(this, value, index, args[0], vm)
		if !ok {
			return vm.Null
		}
		if CoerceToBool(keep) {
			result.Elements = append(result.Elements, value)
		}
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(result)}
}

func ArrayFind(this OSValue, vm *VM, args []OSValue) OSValue {
	array := (*Array)(this.Opaque)
	if !validateArrayCallback(args[0], vm) {
		return vm.Null
	}
	length := len(array.Elements)
	for index := 0; index < length; index++ {
		value := arrayElement(array, index, vm)
		found, ok := invokeArrayCallback(this, value, index, args[0], vm)
		if !ok {
			return vm.Null
		}
		if CoerceToBool(found) {
			return value
		}
	}
	return vm.Null
}

func validateArrayCallback(callback OSValue, vm *VM) bool {
	if callback.Type != OSVFunction && callback.Type != OSVNativeFunction && callback.Type != OSVNativeMethod {
		vm.NativeError = "collection method expects a function callback"
		return false
	}
	if _, ok := arrayCallbackArgCount(callback); !ok {
		vm.NativeError = "collection callback must accept at most three arguments"
		return false
	}
	return true
}

func arrayElement(array *Array, index int, vm *VM) OSValue {
	if index >= len(array.Elements) {
		return vm.Null
	}
	return array.Elements[index]
}

func arrayCallbackArgCount(callback OSValue) (int, bool) {
	switch callback.Type {
	case OSVFunction:
		function := (*Function)(callback.Opaque)
		if function == nil {
			return 0, false
		}
		if function.VarArgIndex >= 0 {
			return 3, function.VarArgIndex <= 3
		}
		return function.ParamCount, function.ParamCount <= 3
	case OSVNativeFunction:
		if callback.Opaque == nil {
			return 0, false
		}
		count := (*NativeFunction)(callback.Opaque).Paramc
		if count < 0 {
			return 3, true
		}
		return count, count <= 3
	case OSVNativeMethod:
		if callback.Opaque == nil {
			return 0, false
		}
		return (*NativeMethod)(callback.Opaque).Paramc, (*NativeMethod)(callback.Opaque).Paramc <= 3
	default:
		return 0, false
	}
}

func invokeArrayCallback(array OSValue, value OSValue, index int, callback OSValue, vm *VM) (OSValue, bool) {
	var callbackArgs [3]OSValue
	callbackArgs[0] = value
	callbackArgs[1] = OSValue{Type: OSVNum, Number: float64(index)}
	callbackArgs[2] = array
	return invokeCollectionCallback(callback, callbackArgs[:], vm)
}

func invokeCollectionCallback(callback OSValue, callbackArgs []OSValue, vm *VM) (OSValue, bool) {
	argumentCount, ok := arrayCallbackArgCount(callback)
	if !ok || argumentCount < 0 || argumentCount > 3 {
		vm.NativeError = "collection callback must accept at most three arguments"
		return vm.Null, false
	}
	if argumentCount > len(callbackArgs) {
		vm.NativeError = "collection callback argument list is incomplete"
		return vm.Null, false
	}
	args := callbackArgs[:argumentCount]

	switch callback.Type {
	case OSVFunction:
		function := (*Function)(callback.Opaque)
		if function.Async {
			parent := SnapshotLexicalChain(vm.Current)
			coroutine := NewCoroutine(parent, function, InitialStack(argumentCount))
			if !restoreCaptures(coroutine, function) {
				vm.NativeError = "array callback has an invalid captured variable"
				return vm.Null, false
			}
			copy(coroutine.OperandStack, args)
			coroutine.Sp = argumentCount
			coroutine.Caller = nil
			coroutine.Frame.Caller = nil
			promise := &Promise{Coroutine: coroutine}
			coroutine.AsyncResult = promise
			coroutine.SyncBoundary = promise
			enqueueMicrotask(vm, coroutine)
			return OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}, true
		}
		return invokeSyncArrayFunction(vm, function, args)
	case OSVNativeFunction:
		native := (*NativeFunction)(callback.Opaque)
		vm.NativeError = ""
		result := native.Callable(vm, args)
		if vm.NativeError != "" {
			return vm.Null, false
		}
		return result, true
	case OSVNativeMethod:
		method := (*NativeMethod)(callback.Opaque)
		vm.NativeError = ""
		result := method.Callable(method.This, vm, args)
		if vm.NativeError != "" {
			return vm.Null, false
		}
		return result, true
	default:
		vm.NativeError = "collection method expects a function callback"
		return vm.Null, false
	}
}

func restoreCaptures(coroutine *Coroutine, function *Function) bool {
	for index := range function.CaptureMeta {
		capture := &function.CaptureMeta[index]
		if capture.Dst < 0 || capture.Dst >= len(coroutine.Frame.Env) || capture.Cell == nil {
			return false
		}
		coroutine.Frame.Env[capture.Dst] = capture.Cell
	}
	return true
}

func invokeSyncArrayFunction(vm *VM, function *Function, args []OSValue) (OSValue, bool) {
	caller := vm.Current
	if caller == nil || vm.State == nil {
		vm.NativeError = "array callback cannot run without an active interpreter"
		return vm.Null, false
	}

	lexicalParent := SnapshotLexicalChain(caller)
	coroutine := NewCoroutine(lexicalParent, function, InitialStack(len(args)))
	if !restoreCaptures(coroutine, function) {
		vm.NativeError = "array callback has an invalid captured variable"
		return vm.Null, false
	}
	copy(coroutine.OperandStack, args)
	coroutine.Sp = len(args)
	coroutine.Caller = caller
	coroutine.Frame.Caller = &caller.Frame
	coroutine.SyncRoot = true

	caller.SyncCall = true
	caller.SyncDone = false
	caller.SyncResult = OSValue{}
	completion := &Promise{}
	completion.Coroutine = coroutine
	completion.Handled = true
	coroutine.AsyncResult = completion
	coroutine.SyncBoundary = completion
	vm.Current = coroutine
	runCoroutine(vm.State, vm, coroutine)
	vm.Current = caller
	caller.SyncCall = false
	if completion.Rejected {
		if message, ok := errorMessage(completion.Value); ok {
			vm.NativeError = message
		} else {
			vm.NativeError = completion.Value.String()
		}
		caller.SyncDone = false
		caller.SyncResult = OSValue{}
		return vm.Null, false
	}
	if !caller.SyncDone {
		vm.NativeError = "array callback did not complete synchronously"
		caller.SyncDone = false
		caller.SyncResult = OSValue{}
		return vm.Null, false
	}
	result := caller.SyncResult
	caller.SyncDone = false
	caller.SyncResult = OSValue{}
	return result, true
}
