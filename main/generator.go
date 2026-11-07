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
			"hasNext": nativeMethodTemplate(0, generatorHasNext),
			"next":    nativeMethodTemplate(0, generatorNext),
		}}
	})
	return coreGeneratorClass
}

func generatorNext(this OSValue, vm *VM, _ []OSValue) OSValue {
	generator := (*Generator)(this.Opaque)
	if generator == nil {
		return generatorResult(vm.Null, true)
	}
	if generator.Done {
		return generatorFinalResult(generator)
	}

	if generator.HasValue {
		value := generator.Value
		generator.Value = OSValue{}
		generator.HasValue = false
		return generatorResult(value, false)
	}
	if !advanceGenerator(generator, vm) {
		return generatorFinalResult(generator)
	}
	value := generator.Value
	generator.Value = OSValue{}
	generator.HasValue = false
	return generatorResult(value, false)
}

func generatorFinalResult(generator *Generator) OSValue {
	value := OSValue{}
	if !generator.FinalRead {
		value = generator.Final
		generator.FinalRead = true
	}
	return generatorResult(value, true)
}

func generatorHasNext(this OSValue, vm *VM, _ []OSValue) OSValue {
	generator := (*Generator)(this.Opaque)
	if generator != nil && !generator.HasValue {
		advanceGenerator(generator, vm)
	}
	if generator != nil && generator.HasValue {
		return vm.True
	}
	return vm.False
}

func advanceGenerator(generator *Generator, vm *VM) bool {
	if generator == nil || generator.Done {
		return false
	}
	coroutine := generator.Coroutine
	if coroutine == nil || coroutine.State == CoroutineStateDead {
		generator.Done = true
		return false
	}
	if coroutine.State == CoroutineStateRunning {
		vm.NativeError = "generator is already running"
		return false
	}

	caller := vm.Current
	runCoroutine(vm.State, vm, coroutine)
	vm.Current = caller
	if generator.Done {
		return false
	}
	generator.Value = generator.Yielded
	generator.Yielded = OSValue{}
	generator.HasValue = true
	return true
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
