package main

import "unsafe"

type Promise struct {
	Value     OSValue
	Resolved  bool
	Coroutine *Coroutine
	Then      OSValue
	Catch     OSValue
}

func PromiseThen(vm *VM, args []OSValue) {
	vm.Current.OperandStack[vm.Current.Sp] = args[0]
	vm.Current.Sp++
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
