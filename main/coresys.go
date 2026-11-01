package main

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"unsafe"
)

func coreSysFunction(paramc int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			Paramc:   paramc,
			Callable: callable,
		}),
	}
}

func coreSysString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func nativeSysStringArg(vm *VM, args []OSValue, name string) (string, bool) {
	if !IsString(args[0]) {
		vm.NativeError = "core:sys." + name + " expects a string"
		return "", false
	}
	return args[0].StringValue(), true
}

func nativeSysDirectory(vm *VM, name string, get func() (string, error)) OSValue {
	value, err := get()
	if err != nil {
		vm.NativeError = "core:sys." + name + ": " + err.Error()
		return vm.Null
	}
	return coreSysString(value)
}

func nativeSysEnv(vm *VM, args []OSValue) OSValue {
	name, ok := nativeSysStringArg(vm, args, "env")
	if !ok {
		return vm.Null
	}
	value, exists := os.LookupEnv(name)
	if !exists {
		return vm.Null
	}
	return coreSysString(value)
}

func nativeSysExecutable(vm *VM, _ []OSValue) OSValue {
	return nativeSysDirectory(vm, "executable", os.Executable)
}

func nativeSysHostname(vm *VM, _ []OSValue) OSValue {
	return nativeSysDirectory(vm, "hostname", os.Hostname)
}

func nativeSysHomeDir(vm *VM, _ []OSValue) OSValue {
	return nativeSysDirectory(vm, "homeDir", os.UserHomeDir)
}

func nativeSysCwd(vm *VM, _ []OSValue) OSValue {
	return nativeSysDirectory(vm, "cwd", os.Getwd)
}

func nativeSysTempDir(_ *VM, _ []OSValue) OSValue {
	return coreSysString(os.TempDir())
}

func coreSysBool(value bool) OSValue {
	return OSValue{Type: OSVBool, Number: boolNumber(value)}
}

func coreSysArgv() OSValue {
	arguments := flag.Args()
	values := make([]OSValue, len(arguments))
	for index, argument := range arguments {
		values[index] = coreSysString(argument)
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: values})}
}

func getCoreSys() map[string]OSValue {
	return map[string]OSValue{
		"OS":            coreSysString(runtime.GOOS),
		"ARCH":          coreSysString(runtime.GOARCH),
		"BITNESS":       {Type: OSVNum, Number: strconv.IntSize},
		"CPU_COUNT":     {Type: OSVNum, Number: float64(runtime.NumCPU())},
		"GO_VERSION":    coreSysString(runtime.Version()),
		"PID":           {Type: OSVNum, Number: float64(os.Getpid())},
		"PATH_SEP":      coreSysString(string(filepath.Separator)),
		"PATH_LIST_SEP": coreSysString(string(filepath.ListSeparator)),
		"LINE_SEP":      coreSysString("\n"),
		"IS_WINDOWS":    coreSysBool(runtime.GOOS == "windows"),
		"IS_LINUX":      coreSysBool(runtime.GOOS == "linux"),
		"IS_MACOS":      coreSysBool(runtime.GOOS == "darwin"),
		"argv":          coreSysArgv(),
		"cwd":           coreSysFunction(0, nativeSysCwd),
		"executable":    coreSysFunction(0, nativeSysExecutable),
		"hostname":      coreSysFunction(0, nativeSysHostname),
		"homeDir":       coreSysFunction(0, nativeSysHomeDir),
		"tempDir":       coreSysFunction(0, nativeSysTempDir),
		"env":           coreSysFunction(1, nativeSysEnv),
	}
}
