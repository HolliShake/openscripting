package main

import (
	"os"
	"unsafe"
)

func nativeReadText(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:file.readText expects a path string"
		return vm.Null
	}

	path := args[0].StringValue()
	content, err := os.ReadFile(path)
	if err != nil {
		vm.NativeError = "core:file.readText: " + err.Error()
		return vm.Null
	}
	value := unsafe.String(unsafe.SliceData(content), len(content))
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func nativeWriteText(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) || !IsString(args[1]) {
		vm.NativeError = "core:file.writeText expects a path and text string"
		return vm.False
	}
	text := args[1].StringValue()
	if err := os.WriteFile(args[0].StringValue(), unsafe.Slice(unsafe.StringData(text), len(text)), 0644); err != nil {
		vm.NativeError = "core:file.writeText: " + err.Error()
		return vm.False
	}
	return vm.True
}

func nativeAppendText(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) || !IsString(args[1]) {
		vm.NativeError = "core:file.appendText expects a path and text string"
		return vm.False
	}
	file, err := os.OpenFile(args[0].StringValue(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		vm.NativeError = "core:file.appendText: " + err.Error()
		return vm.False
	}
	_, writeErr := file.WriteString(args[1].StringValue())
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		if writeErr != nil {
			vm.NativeError = "core:file.appendText: " + writeErr.Error()
		} else {
			vm.NativeError = "core:file.appendText: " + closeErr.Error()
		}
		return vm.False
	}
	return vm.True
}

func nativeFileExists(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:file.exists expects a path string"
		return vm.False
	}
	_, err := os.Stat(args[0].StringValue())
	if err != nil && !os.IsNotExist(err) {
		vm.NativeError = "core:file.exists: " + err.Error()
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(err == nil)}
}

func nativeRemoveFile(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:file.remove expects a path string"
		return vm.False
	}
	if err := os.Remove(args[0].StringValue()); err != nil {
		vm.NativeError = "core:file.remove: " + err.Error()
		return vm.False
	}
	return vm.True
}

func boolNumber(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func coreFileFunction(paramCount int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			ParamCount: paramCount,
			Callable:   callable,
		}),
	}
}

func getCoreFile() map[string]OSValue {
	return map[string]OSValue{
		"readText":   coreFileFunction(1, nativeReadText),
		"writeText":  coreFileFunction(2, nativeWriteText),
		"appendText": coreFileFunction(2, nativeAppendText),
		"exists":     coreFileFunction(1, nativeFileExists),
		"remove":     coreFileFunction(1, nativeRemoveFile),
	}
}
