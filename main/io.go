package main

import (
	"fmt"
	"unsafe"
)

func nativePrintln(vm *VM, args []OSValue) OSValue {
	for index, value := range args {
		if index > 0 {
			fmt.Print(" ")
		}
		fmt.Print(value.String())
	}
	fmt.Println()
	return vm.Null
}

func getCoreIo() map[string]OSValue {
	return map[string]OSValue{
		"println": {
			Type: OSVNativeFunction,
			Opaque: unsafe.Pointer(&NativeFunction{
				Paramc:   -1,
				Callable: nativePrintln,
			}),
		},
	}
}
