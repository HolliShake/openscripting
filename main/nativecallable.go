package main

import "fmt"

type NativeFunction struct {
	// ParamCount is the required argument count, or -1 when the native accepts a variable number.
	ParamCount int
	// Callable implements the builtin operation with VM access for errors, scheduling, and canonical values.
	Callable func(vm *VM, args []OSValue) OSValue
}

type NativeMethod struct {
	// This is the receiver captured during attribute lookup so the method remains bound when stored or passed.
	This OSValue
	// ParamCount is checked by the interpreter before invoking the method.
	ParamCount int
	// Callable implements the method; the receiver is separate from explicit script arguments.
	Callable func(this OSValue, vm *VM, args []OSValue) OSValue
}

func invokeNativeFunction(vm *VM, callable func(vm *VM, args []OSValue) OSValue, args []OSValue) (result OSValue) {
	vm.NativeError = ""
	defer func() {
		if recovered := recover(); recovered != nil {
			vm.NativeError = "native function panic: " + fmt.Sprint(recovered)
			result = vm.Null
		}
	}()
	return callable(vm, args)
}

func invokeNativeMethod(vm *VM, callable func(this OSValue, vm *VM, args []OSValue) OSValue, this OSValue, args []OSValue) (result OSValue) {
	vm.NativeError = ""
	defer func() {
		if recovered := recover(); recovered != nil {
			vm.NativeError = "native method panic: " + fmt.Sprint(recovered)
			result = vm.Null
		}
	}()
	return callable(this, vm, args)
}
