package main

import (
	"strings"
	"sync"
	"unsafe"
)

var (
	coreStringClass     *Class
	coreStringClassOnce sync.Once
)

func getCoreStringClass() *Class {
	coreStringClassOnce.Do(func() {
		coreStringClass = &Class{Name: "String", Template: map[string]OSValue{
			"len":         nativeMethodTemplate(0, stringLen),
			"contains":    nativeMethodTemplate(1, stringContains),
			"startsWith":  nativeMethodTemplate(1, stringStartsWith),
			"endsWith":    nativeMethodTemplate(1, stringEndsWith),
			"indexOf":     nativeMethodTemplate(1, stringIndexOf),
			"toLower":     nativeMethodTemplate(0, stringToLower),
			"toLowerCase": nativeMethodTemplate(0, stringToLower),
			"toUpper":     nativeMethodTemplate(0, stringToUpper),
			"toUpperCase": nativeMethodTemplate(0, stringToUpper),
			"trim":        nativeMethodTemplate(0, stringTrim),
			"split":       nativeMethodTemplate(1, stringSplit),
			"replace":     nativeMethodTemplate(2, stringReplace),
			"replaceAll":  nativeMethodTemplate(2, stringReplaceAll),
		}}
	})
	return coreStringClass
}

func stringLen(this OSValue, _ *VM, _ []OSValue) OSValue {
	return OSValue{Type: OSVNum, Number: float64(len(this.StringValue()))}
}

func stringValue(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func stringArgument(vm *VM, args []OSValue, index int, method string) (string, bool) {
	if !IsString(args[index]) {
		vm.NativeError = "String." + method + " expects string arguments"
		return "", false
	}
	return args[index].StringValue(), true
}

func stringContains(this OSValue, vm *VM, args []OSValue) OSValue {
	search, ok := stringArgument(vm, args, 0, "contains")
	if !ok {
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(strings.Contains(this.StringValue(), search))}
}

func stringStartsWith(this OSValue, vm *VM, args []OSValue) OSValue {
	prefix, ok := stringArgument(vm, args, 0, "startsWith")
	if !ok {
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(strings.HasPrefix(this.StringValue(), prefix))}
}

func stringEndsWith(this OSValue, vm *VM, args []OSValue) OSValue {
	suffix, ok := stringArgument(vm, args, 0, "endsWith")
	if !ok {
		return vm.False
	}
	return OSValue{Type: OSVBool, Number: boolNumber(strings.HasSuffix(this.StringValue(), suffix))}
}

func stringIndexOf(this OSValue, vm *VM, args []OSValue) OSValue {
	search, ok := stringArgument(vm, args, 0, "indexOf")
	if !ok {
		return vm.Null
	}
	return OSValue{Type: OSVNum, Number: float64(strings.Index(this.StringValue(), search))}
}

func stringToLower(this OSValue, _ *VM, _ []OSValue) OSValue {
	return stringValue(strings.ToLower(this.StringValue()))
}

func stringToUpper(this OSValue, _ *VM, _ []OSValue) OSValue {
	return stringValue(strings.ToUpper(this.StringValue()))
}

func stringTrim(this OSValue, _ *VM, _ []OSValue) OSValue {
	return stringValue(strings.TrimSpace(this.StringValue()))
}

func stringSplit(this OSValue, vm *VM, args []OSValue) OSValue {
	separator, ok := stringArgument(vm, args, 0, "split")
	if !ok {
		return vm.Null
	}
	parts := strings.Split(this.StringValue(), separator)
	values := make([]OSValue, len(parts))
	for index, part := range parts {
		values[index] = stringValue(part)
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: values})}
}

func stringReplace(this OSValue, vm *VM, args []OSValue) OSValue {
	search, ok := stringArgument(vm, args, 0, "replace")
	if !ok {
		return vm.Null
	}
	replacement, ok := stringArgument(vm, args, 1, "replace")
	if !ok {
		return vm.Null
	}
	return stringValue(strings.Replace(this.StringValue(), search, replacement, 1))
}

func stringReplaceAll(this OSValue, vm *VM, args []OSValue) OSValue {
	search, ok := stringArgument(vm, args, 0, "replaceAll")
	if !ok {
		return vm.Null
	}
	replacement, ok := stringArgument(vm, args, 1, "replaceAll")
	if !ok {
		return vm.Null
	}
	return stringValue(strings.ReplaceAll(this.StringValue(), search, replacement))
}
