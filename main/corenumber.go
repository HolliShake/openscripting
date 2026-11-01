package main

import (
	"math"
	"strconv"
	"strings"
	"unsafe"
)

func coreNumberFunction(paramc int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			Paramc:   paramc,
			Callable: callable,
		}),
	}
}

func nativeNumberStringArg(vm *VM, args []OSValue, index int, name string) (string, bool) {
	if !IsString(args[index]) {
		vm.NativeError = "core:number." + name + " expects a string"
		return "", false
	}
	return args[index].StringValue(), true
}

func nativeNumberParseInt(vm *VM, args []OSValue) OSValue {
	if len(args) < 1 || len(args) > 2 {
		vm.NativeError = "core:number.parseInt expects one or two arguments"
		return vm.Null
	}
	text, ok := nativeNumberStringArg(vm, args, 0, "parseInt")
	if !ok {
		return vm.Null
	}
	radix := 0
	if len(args) == 2 {
		if !IsNumber(args[1]) || math.IsNaN(args[1].Number) || math.IsInf(args[1].Number, 0) || math.Trunc(args[1].Number) != args[1].Number {
			vm.NativeError = "core:number.parseInt radix must be an integer"
			return vm.Null
		}
		radix = int(args[1].Number)
		if radix != 0 && (radix < 2 || radix > 36) {
			vm.NativeError = "core:number.parseInt radix must be between 2 and 36"
			return vm.Null
		}
	}
	value, valid := parseIntegerPrefix(text, radix)
	if !valid {
		return vm.Null
	}
	return OSValue{Type: OSVNum, Number: value}
}

func parseIntegerPrefix(text string, radix int) (float64, bool) {
	text = strings.TrimLeftFunc(text, unicodeSpace)
	if text == "" {
		return 0, false
	}
	sign := 1.0
	if text[0] == '+' || text[0] == '-' {
		if text[0] == '-' {
			sign = -1
		}
		text = text[1:]
	}
	if radix == 0 {
		radix = 10
		if len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
			radix = 16
			text = text[2:]
		}
	} else if radix == 16 && len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		text = text[2:]
	}
	var value float64
	parsed := false
	for index := 0; index < len(text); index++ {
		digit := int(text[index] - '0')
		if text[index] >= 'a' && text[index] <= 'z' {
			digit = int(text[index]-'a') + 10
		} else if text[index] >= 'A' && text[index] <= 'Z' {
			digit = int(text[index]-'A') + 10
		}
		if digit < 0 || digit >= radix {
			break
		}
		value = value*float64(radix) + float64(digit)
		parsed = true
	}
	return sign * value, parsed
}

func unicodeSpace(r rune) bool {
	return strings.ContainsRune("\u0009\u000a\u000b\u000c\u000d\u0020\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", r)
}

func nativeNumberParseFloat(vm *VM, args []OSValue) OSValue {
	text, ok := nativeNumberStringArg(vm, args, 0, "parseFloat")
	if !ok {
		return vm.Null
	}
	value, valid := parseFloatPrefix(text)
	if !valid {
		return vm.Null
	}
	return OSValue{Type: OSVNum, Number: value}
}

func parseFloatPrefix(text string) (float64, bool) {
	text = strings.TrimLeftFunc(text, unicodeSpace)
	if text == "" {
		return 0, false
	}
	if strings.HasPrefix(text, "Infinity") || strings.HasPrefix(text, "+Infinity") {
		return math.Inf(1), true
	}
	if strings.HasPrefix(text, "-Infinity") {
		return math.Inf(-1), true
	}
	index := 0
	if text[index] == '+' || text[index] == '-' {
		index++
	}
	digits := 0
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		index++
		digits++
	}
	if index < len(text) && text[index] == '.' {
		index++
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
			digits++
		}
	}
	if digits == 0 {
		return 0, false
	}
	numberEnd := index
	if index < len(text) && (text[index] == 'e' || text[index] == 'E') {
		exponent := index + 1
		if exponent < len(text) && (text[exponent] == '+' || text[exponent] == '-') {
			exponent++
		}
		exponentStart := exponent
		for exponent < len(text) && text[exponent] >= '0' && text[exponent] <= '9' {
			exponent++
		}
		if exponent > exponentStart {
			numberEnd = exponent
		}
	}
	value, err := strconv.ParseFloat(text[:numberEnd], 64)
	if err != nil {
		if numberError, ok := err.(*strconv.NumError); !ok || numberError.Err != strconv.ErrRange {
			return 0, false
		}
	}
	return value, true
}

func nativeNumberPredicate(name string, predicate func(float64) bool) func(vm *VM, args []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		if !IsNumber(args[0]) {
			vm.NativeError = "core:number." + name + " expects a number"
			return vm.False
		}
		return OSValue{Type: OSVBool, Number: boolNumber(predicate(args[0].Number))}
	}
}

func nativeNumberSign(vm *VM, args []OSValue) OSValue {
	if !IsNumber(args[0]) {
		vm.NativeError = "core:number.sign expects a number"
		return vm.Null
	}
	value := args[0].Number
	if math.IsNaN(value) || value == 0 {
		return OSValue{Type: OSVNum, Number: value}
	}
	if value < 0 {
		return OSValue{Type: OSVNum, Number: -1}
	}
	return OSValue{Type: OSVNum, Number: 1}
}

func nativeNumberClamp(vm *VM, args []OSValue) OSValue {
	if !IsNumber(args[0]) || !IsNumber(args[1]) || !IsNumber(args[2]) {
		vm.NativeError = "core:number.clamp expects three numbers"
		return vm.Null
	}
	value, minimum, maximum := args[0].Number, args[1].Number, args[2].Number
	if minimum > maximum {
		vm.NativeError = "core:number.clamp minimum must not exceed maximum"
		return vm.Null
	}
	return OSValue{Type: OSVNum, Number: math.Max(minimum, math.Min(value, maximum))}
}

func getCoreNumber() map[string]OSValue {
	return map[string]OSValue{
		"parseInt":   coreNumberFunction(-1, nativeNumberParseInt),
		"parseFloat": coreNumberFunction(1, nativeNumberParseFloat),
		"isFinite":   coreNumberFunction(1, nativeNumberPredicate("isFinite", func(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) })),
		"isNaN":      coreNumberFunction(1, nativeNumberPredicate("isNaN", math.IsNaN)),
		"sign":       coreNumberFunction(1, nativeNumberSign),
		"clamp":      coreNumberFunction(3, nativeNumberClamp),
		"MAX_VALUE":  {Type: OSVNum, Number: math.MaxFloat64},
		"MIN_VALUE":  {Type: OSVNum, Number: math.SmallestNonzeroFloat64},
		"NaN":        {Type: OSVNum, Number: math.NaN()},
		"Infinity":   {Type: OSVNum, Number: math.Inf(1)},
	}
}
