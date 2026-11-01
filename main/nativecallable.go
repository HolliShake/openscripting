package main

type NativeFunction struct {
	// Paramc is the required argument count, or -1 when the native accepts a variable number.
	Paramc int
	// Callable implements the builtin operation with VM access for errors, scheduling, and canonical values.
	Callable func(vm *VM, args []OSValue) OSValue
}

type NativeMethod struct {
	// This is the receiver captured during attribute lookup so the method remains bound when stored or passed.
	This OSValue
	// Paramc is checked by the interpreter before invoking the method.
	Paramc int
	// Callable implements the method; the receiver is separate from explicit script arguments.
	Callable func(this OSValue, vm *VM, args []OSValue) OSValue
}
