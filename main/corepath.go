package main

import (
	"os"
	"path/filepath"
	"unsafe"
)

func corePathFunction(paramc int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			Paramc:   paramc,
			Callable: callable,
		}),
	}
}

func pathString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func nativePathArg(vm *VM, args []OSValue, index int, function string) (string, bool) {
	if index >= len(args) || !IsString(args[index]) {
		vm.NativeError = "core:path." + function + " expects path strings"
		return "", false
	}
	return args[index].StringValue(), true
}

func nativePathExists(vm *VM, args []OSValue) OSValue {
	path, ok := nativePathArg(vm, args, 0, "exists")
	if !ok {
		return vm.False
	}
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		vm.NativeError = "core:path.exists: " + err.Error()
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(err == nil)}
}

func nativePathIsFile(vm *VM, args []OSValue) OSValue {
	path, ok := nativePathArg(vm, args, 0, "isFile")
	if !ok {
		return vm.False
	}
	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			vm.NativeError = "core:path.isFile: " + err.Error()
		}
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(info.Mode().IsRegular())}
}

func nativePathIsDir(vm *VM, args []OSValue) OSValue {
	path, ok := nativePathArg(vm, args, 0, "isDir")
	if !ok {
		return vm.False
	}
	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			vm.NativeError = "core:path.isDir: " + err.Error()
		}
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(info.IsDir())}
}

func nativePathIsAbs(vm *VM, args []OSValue) OSValue {
	path, ok := nativePathArg(vm, args, 0, "isAbs")
	if !ok {
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(filepath.IsAbs(path))}
}

func nativePathAbsolute(vm *VM, args []OSValue) OSValue {
	path, ok := nativePathArg(vm, args, 0, "absolute")
	if !ok {
		return vm.Null
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		vm.NativeError = "core:path.absolute: " + err.Error()
		return vm.Null
	}
	return pathString(absolute)
}

func nativePathJoin(vm *VM, args []OSValue) OSValue {
	if len(args) == 0 {
		vm.NativeError = "core:path.join expects at least one path string"
		return vm.Null
	}
	paths := make([]string, len(args))
	for index := range args {
		path, ok := nativePathArg(vm, args, index, "join")
		if !ok {
			return vm.Null
		}
		paths[index] = path
	}
	return pathString(filepath.Join(paths...))
}

func nativePathClean(vm *VM, args []OSValue) OSValue {
	path, ok := nativePathArg(vm, args, 0, "clean")
	if !ok {
		return vm.Null
	}
	return pathString(filepath.Clean(path))
}

func nativePathPart(vm *VM, args []OSValue, function string, extract func(string) string) OSValue {
	path, ok := nativePathArg(vm, args, 0, function)
	if !ok {
		return vm.Null
	}
	return pathString(extract(path))
}

func nativePathRelative(vm *VM, args []OSValue) OSValue {
	target, ok := nativePathArg(vm, args, 0, "relative")
	if !ok {
		return vm.Null
	}
	base, ok := nativePathArg(vm, args, 1, "relative")
	if !ok {
		return vm.Null
	}
	relative, err := filepath.Rel(base, target)
	if err != nil {
		vm.NativeError = "core:path.relative: " + err.Error()
		return vm.Null
	}
	return pathString(relative)
}

func getCorePath() map[string]OSValue {
	return map[string]OSValue{
		"exists":   corePathFunction(1, nativePathExists),
		"isFile":   corePathFunction(1, nativePathIsFile),
		"isDir":    corePathFunction(1, nativePathIsDir),
		"isAbs":    corePathFunction(1, nativePathIsAbs),
		"absolute": corePathFunction(1, nativePathAbsolute),
		"join":     corePathFunction(-1, nativePathJoin),
		"clean":    corePathFunction(1, nativePathClean),
		"base": corePathFunction(1, func(vm *VM, args []OSValue) OSValue {
			return nativePathPart(vm, args, "base", filepath.Base)
		}),
		"dir": corePathFunction(1, func(vm *VM, args []OSValue) OSValue {
			return nativePathPart(vm, args, "dir", filepath.Dir)
		}),
		"ext": corePathFunction(1, func(vm *VM, args []OSValue) OSValue {
			return nativePathPart(vm, args, "ext", filepath.Ext)
		}),
		"relative": corePathFunction(2, nativePathRelative),
	}
}
