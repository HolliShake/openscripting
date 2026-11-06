package main

import (
	"bufio"
	"io"
	"os"
	"strings"
	"unsafe"
)

var coreIOInput = bufio.NewReader(os.Stdin)

func nativeScan(vm *VM, args []OSValue) OSValue {
	os.Stdout.WriteString(args[0].String())
	line, err := coreIOInput.ReadString('\n')
	if err != nil && err != io.EOF {
		vm.NativeError = "core:io.scan: " + err.Error()
		return vm.Null
	}
	if err == io.EOF && line == "" {
		return vm.Null
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return OSValue{
		Type:   OSVString,
		Number: float64(len(line)),
		Opaque: unsafe.Pointer(unsafe.StringData(line)),
	}
}

func nativePrint(vm *VM, args []OSValue) OSValue {
	for index, value := range args {
		if index > 0 {
			os.Stdout.WriteString(" ")
		}
		os.Stdout.WriteString(value.String())
	}
	return vm.Null
}

func nativePrintln(vm *VM, args []OSValue) OSValue {
	for index, value := range args {
		if index > 0 {
			os.Stdout.WriteString(" ")
		}
		os.Stdout.WriteString(value.String())
	}
	os.Stdout.WriteString("\n")
	return vm.Null
}

func getCoreIo() map[string]OSValue {
	return map[string]OSValue{
		"scan": {
			Type: OSVNativeFunction,
			Opaque: unsafe.Pointer(&NativeFunction{
				ParamCount: 1,
				Callable:   nativeScan,
			}),
		},
		"print": {
			Type: OSVNativeFunction,
			Opaque: unsafe.Pointer(&NativeFunction{
				ParamCount: -1,
				Callable:   nativePrint,
			}),
		},
		"println": {
			Type: OSVNativeFunction,
			Opaque: unsafe.Pointer(&NativeFunction{
				ParamCount: -1,
				Callable:   nativePrintln,
			}),
		},
	}
}
