package main

type Promise struct {
	Value     OSValue
	Resolved  bool
	Coroutine *Coroutine
}

func OSPromiseThen(vm *VM, args []OSValue) {

}
