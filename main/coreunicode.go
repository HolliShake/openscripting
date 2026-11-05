package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

func coreUnicodeFunction(paramCount int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			ParamCount: paramCount,
			Callable:   callable,
		}),
	}
}

func coreUnicodeString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func nativeUnicodeStringArg(vm *VM, args []OSValue, index int, name string) (string, bool) {
	if !IsString(args[index]) {
		vm.NativeError = "core:unicode." + name + " expects a string"
		return "", false
	}
	return args[index].StringValue(), true
}

func nativeUnicodePredicate(name string, predicate func(rune) bool) func(vm *VM, args []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		value, ok := nativeUnicodeStringArg(vm, args, 0, name)
		if !ok {
			return vm.False
		}
		if !utf8.ValidString(value) {
			vm.NativeError = "core:unicode." + name + " expects valid UTF-8"
			return vm.False
		}
		r, size := utf8.DecodeRuneInString(value)
		return OSValue{Type: OSVBool, Number: boolNumber(size > 0 && size == len(value) && predicate(r))}
	}
}

func nativeUnicodeCase(name string, convert func(string) string) func(vm *VM, args []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		value, ok := nativeUnicodeStringArg(vm, args, 0, name)
		if !ok {
			return vm.Null
		}
		if !utf8.ValidString(value) {
			vm.NativeError = "core:unicode." + name + " expects valid UTF-8"
			return vm.Null
		}
		return coreUnicodeString(convert(value))
	}
}

func nativeUnicodeCodePointAt(vm *VM, args []OSValue) OSValue {
	value, ok := nativeUnicodeStringArg(vm, args, 0, "codePointAt")
	if !ok {
		return vm.Null
	}
	if !IsNumber(args[1]) {
		vm.NativeError = "core:unicode.codePointAt expects a string and an integer index"
		return vm.Null
	}
	if !utf8.ValidString(value) {
		vm.NativeError = "core:unicode.codePointAt expects valid UTF-8"
		return vm.Null
	}
	index := int(args[1].Number)
	if float64(index) != args[1].Number {
		vm.NativeError = "core:unicode.codePointAt index must be an integer"
		return vm.Null
	}
	if index < 0 {
		index += utf8.RuneCountInString(value)
	}
	if index < 0 {
		return vm.Null
	}
	for _, r := range value {
		if index == 0 {
			return OSValue{Type: OSVNum, Number: float64(r)}
		}
		index--
	}
	return vm.Null
}

func nativeUnicodeFromCodePoint(vm *VM, args []OSValue) OSValue {
	if !IsNumber(args[0]) {
		vm.NativeError = "core:unicode.fromCodePoint expects an integer"
		return vm.Null
	}
	value := int64(args[0].Number)
	if float64(value) != args[0].Number || value < 0 || value > unicode.MaxRune || value >= 0xD800 && value <= 0xDFFF {
		vm.NativeError = "core:unicode.fromCodePoint expects a valid Unicode scalar value"
		return vm.Null
	}
	return coreUnicodeString(string(rune(value)))
}

func nativeUnicodePredicateExport(name string, predicate func(rune) bool) OSValue {
	return coreUnicodeFunction(1, nativeUnicodePredicate(name, predicate))
}

func getCoreUnicode() map[string]OSValue {
	return map[string]OSValue{
		"byteLength": coreUnicodeFunction(1, func(vm *VM, args []OSValue) OSValue {
			value, ok := nativeUnicodeStringArg(vm, args, 0, "byteLength")
			if !ok {
				return vm.Null
			}
			return OSValue{Type: OSVNum, Number: float64(len(value))}
		}),
		"codePointAt":   coreUnicodeFunction(2, nativeUnicodeCodePointAt),
		"fromCodePoint": coreUnicodeFunction(1, nativeUnicodeFromCodePoint),
		"isValid": coreUnicodeFunction(1, func(vm *VM, args []OSValue) OSValue {
			value, ok := nativeUnicodeStringArg(vm, args, 0, "isValid")
			return OSValue{Type: OSVBool, Number: boolNumber(ok && utf8.ValidString(value))}
		}),
		"length": coreUnicodeFunction(1, func(vm *VM, args []OSValue) OSValue {
			value, ok := nativeUnicodeStringArg(vm, args, 0, "length")
			if !ok {
				return vm.Null
			}
			if !utf8.ValidString(value) {
				vm.NativeError = "core:unicode.length expects valid UTF-8"
				return vm.Null
			}
			return OSValue{Type: OSVNum, Number: float64(utf8.RuneCountInString(value))}
		}),
		"isLetter":      nativeUnicodePredicateExport("isLetter", unicode.IsLetter),
		"isDigit":       nativeUnicodePredicateExport("isDigit", unicode.IsDigit),
		"isNumber":      nativeUnicodePredicateExport("isNumber", unicode.IsNumber),
		"isWhitespace":  nativeUnicodePredicateExport("isWhitespace", unicode.IsSpace),
		"isUpper":       nativeUnicodePredicateExport("isUpper", unicode.IsUpper),
		"isLower":       nativeUnicodePredicateExport("isLower", unicode.IsLower),
		"isMark":        nativeUnicodePredicateExport("isMark", unicode.IsMark),
		"isPunctuation": nativeUnicodePredicateExport("isPunctuation", unicode.IsPunct),
		"isSymbol":      nativeUnicodePredicateExport("isSymbol", unicode.IsSymbol),
		"isControl":     nativeUnicodePredicateExport("isControl", unicode.IsControl),
		"isGraphic":     nativeUnicodePredicateExport("isGraphic", unicode.IsGraphic),
		"isPrintable":   nativeUnicodePredicateExport("isPrintable", unicode.IsPrint),
		"toUpper":       coreUnicodeFunction(1, nativeUnicodeCase("toUpper", strings.ToUpper)),
		"toLower":       coreUnicodeFunction(1, nativeUnicodeCase("toLower", strings.ToLower)),
	}
}
