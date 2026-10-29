package main

type NativeFunction struct {
	This     OSValue
	Paramc   int32
	Callable func(vm VM, args []OSValue)
}
