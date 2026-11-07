package main

import (
	"sync"
	"unsafe"
)

var (
	coreGeneratorClass     *Class
	coreGeneratorClassOnce sync.Once
)

func getCoreGeneratorClass() *Class {
	coreGeneratorClassOnce.Do(func() {
		coreGeneratorClass = &Class{Name: "Generator", Template: map[string]OSValue{
			"next": nativeMethodTemplate(0, generatorNext),
		}}
	})
	return coreGeneratorClass
}

func generatorNext(this OSValue, vm *VM, _ []OSValue) OSValue {
	generator := (*Generator)(this.Opaque)
	if generator == nil || generator.Done {
		return generatorResult(vm.Null, true)
	}

	coroutine := generator.Coroutine
	if coroutine == nil || coroutine.State == CoroutineStateDead {
		generator.Done = true
		return generatorResult(generator.Final, true)
	}
	if coroutine.State == CoroutineStateRunning {
		vm.NativeError = "generator is already running"
		return vm.Null
	}

	caller := vm.Current
	runCoroutine(vm.State, vm, coroutine)
	vm.Current = caller
	if generator.Done {
		return generatorResult(generator.Final, true)
	}
	return generatorResult(generator.Yielded, false)
}

func generatorResult(value OSValue, done bool) OSValue {
	result := &Object{Values: make(map[string]OSValue, 2)}
	result.Set("value", value)
	if done {
		result.Set("done", OSValue{Type: OSVBool, Number: 1})
	} else {
		result.Set("done", OSValue{Type: OSVBool})
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(result)}
}
