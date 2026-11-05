package main

import "unsafe"

func coreTypingFunction(paramCount int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			ParamCount: paramCount,
			Callable:   callable,
		}),
	}
}

func nativeTypingInstance(vm *VM, args []OSValue) OSValue {
	classValue := args[1]
	if classValue.Type != OSVClass || classValue.Opaque == nil {
		vm.NativeError = "core:typing.instance expects a value and a class"
		return vm.False
	}

	target := (*Class)(classValue.Opaque)
	for current := runtimeClass(args[0]); current != nil; current = current.Super {
		if current == target {
			return vm.True
		}
	}
	return vm.False
}

func getCoreTyping() map[string]OSValue {
	return map[string]OSValue{
		"instance": coreTypingFunction(2, nativeTypingInstance),
	}
}
