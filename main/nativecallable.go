package main

type NativeFunction struct {
	Paramc   int
	Callable func(vm *VM, args []OSValue) OSValue
}

type NativeMethod struct {
	This     OSValue
	Paramc   int
	Callable func(this OSValue, vm *VM, args []OSValue) OSValue
}
