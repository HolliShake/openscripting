package main

import (
	"sync"
	"unsafe"
)

var (
	coreErrorClass *Class
	coreErrorOnce  sync.Once
)

func ensureCoreErrorClass() *Class {
	coreErrorOnce.Do(func() {
		coreErrorClass = &Class{
			Name: "Error",
			Constructor: OSValue{
				Type: OSVNativeFunction,
				Opaque: unsafe.Pointer(&NativeFunction{
					ParamCount: -1,
					Callable:   nativeErrorConstructor,
				}),
			},
		}
	})
	return coreErrorClass
}

func nativeErrorConstructor(vm *VM, args []OSValue) OSValue {
	if len(args) < 1 || len(args) > 2 || !IsString(args[0]) {
		vm.NativeError = "Error expects a message string and an optional line number"
		return vm.Null
	}
	line := int64(0)
	if len(args) == 2 {
		if !IsNumber(args[1]) {
			vm.NativeError = "Error line number must be a number"
			return vm.Null
		}
		line = int64(args[1].Number)
	}
	return newErrorValue(args[0].StringValue(), line)
}

func newErrorValue(message string, line int64) OSValue {
	class := ensureCoreErrorClass()
	object := &Object{
		ClassName:  "Error",
		Class:      class,
		StringRoot: message,
		Values:     make(map[string]OSValue, 2),
	}
	object.Set("line", OSValue{Type: OSVNum, Number: float64(line)})
	object.Set("message", OSValue{
		Type:   OSVString,
		Number: float64(len(message)),
		Opaque: unsafe.Pointer(unsafe.StringData(message)),
	})
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func errorMessage(value OSValue) (string, bool) {
	if value.Type != OSVObject || value.Opaque == nil {
		return "", false
	}
	object := (*Object)(value.Opaque)
	if object.Class != ensureCoreErrorClass() {
		return "", false
	}
	return object.StringRoot, true
}

func getCoreError() map[string]OSValue {
	class := ensureCoreErrorClass()
	return map[string]OSValue{
		"Error": {Type: OSVClass, Opaque: unsafe.Pointer(class)},
	}
}
