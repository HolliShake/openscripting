package main

type NativeFunction struct {
	Paramc   int32
	Callable func(vm *VM, args []OSValue)
}

type NativeMethod struct {
	This     OSValue
	Paramc   int32
	Callable func(vm *VM, args []OSValue)
}
