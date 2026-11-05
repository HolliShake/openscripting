package main

import (
	"math"
	"unsafe"
)

func coreMathFunction(paramCount int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			ParamCount: paramCount,
			Callable:   callable,
		}),
	}
}

func nativeMathUnary(name string, operation func(float64) float64) func(vm *VM, args []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		if !IsNumber(args[0]) {
			vm.NativeError = "core:math." + name + " expects a number"
			return vm.Null
		}
		result := operation(args[0].Number)
		if math.IsNaN(result) || math.IsInf(result, 0) {
			vm.NativeError = "core:math." + name + " received an out-of-domain value"
			return vm.Null
		}
		return OSValue{Type: OSVNum, Number: result}
	}
}

func nativeMathPow(vm *VM, args []OSValue) OSValue {
	if !IsNumber(args[0]) || !IsNumber(args[1]) {
		vm.NativeError = "core:math.pow expects two numbers"
		return vm.Null
	}
	result := math.Pow(args[0].Number, args[1].Number)
	if math.IsNaN(result) || math.IsInf(result, 0) {
		vm.NativeError = "core:math.pow received an out-of-domain value"
		return vm.Null
	}
	return OSValue{Type: OSVNum, Number: result}
}

func nativeMathMinMax(isMin bool) func(vm *VM, args []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		if len(args) == 0 {
			vm.NativeError = "core:math.min and core:math.max require at least one number"
			return vm.Null
		}
		if !IsNumber(args[0]) {
			vm.NativeError = "core:math.min and core:math.max expect numbers"
			return vm.Null
		}
		result := args[0].Number
		for _, arg := range args[1:] {
			if !IsNumber(arg) {
				vm.NativeError = "core:math.min and core:math.max expect numbers"
				return vm.Null
			}
			if isMin && arg.Number < result || !isMin && arg.Number > result {
				result = arg.Number
			}
		}
		return OSValue{Type: OSVNum, Number: result}
	}
}

func getCoreMath() map[string]OSValue {
	return map[string]OSValue{
		"PI":        {Type: OSVNum, Number: math.Pi},
		"E":         {Type: OSVNum, Number: math.E},
		"abs":       coreMathFunction(1, nativeMathUnary("abs", math.Abs)),
		"ceil":      coreMathFunction(1, nativeMathUnary("ceil", math.Ceil)),
		"floor":     coreMathFunction(1, nativeMathUnary("floor", math.Floor)),
		"round":     coreMathFunction(1, nativeMathUnary("round", math.Round)),
		"sqrt":      coreMathFunction(1, nativeMathUnary("sqrt", math.Sqrt)),
		"sin":       coreMathFunction(1, nativeMathUnary("sin", math.Sin)),
		"cos":       coreMathFunction(1, nativeMathUnary("cos", math.Cos)),
		"tan":       coreMathFunction(1, nativeMathUnary("tan", math.Tan)),
		"logarithm": coreMathFunction(1, nativeMathUnary("logarithm", math.Log)),
		"exp":       coreMathFunction(1, nativeMathUnary("exp", math.Exp)),
		"pow":       coreMathFunction(2, nativeMathPow),
		"min":       coreMathFunction(-1, nativeMathMinMax(true)),
		"max":       coreMathFunction(-1, nativeMathMinMax(false)),
	}
}
