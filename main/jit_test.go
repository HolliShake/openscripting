package main

import (
	"math"
	"runtime"
	"testing"
)

func TestNativeJITEmitsArithmeticMachineCode(t *testing.T) {
	requireNativeJITEmitter(t)
	tests := []struct {
		name     string
		opcode   int32
		left     float64
		right    float64
		expected float64
	}{
		{name: "add", opcode: OpBinAdd, left: 7, right: 3, expected: 10},
		{name: "subtract", opcode: OpBinSub, left: 7, right: 3, expected: 4},
		{name: "multiply", opcode: OpBinMul, left: 7, right: 3, expected: 21},
		{name: "divide", opcode: OpBinDiv, left: 7, right: 2, expected: 3.5},
		{name: "remainder", opcode: OpBinMod, left: 7, right: 3, expected: 1},
		{name: "bitwise and", opcode: OpBinBitAnd, left: 6, right: 3, expected: 2},
		{name: "bitwise xor", opcode: OpBinBitXor, left: 6, right: 3, expected: 5},
		{name: "bitwise or", opcode: OpBinBitOr, left: 6, right: 3, expected: 7},
		{name: "shift left", opcode: OpBinShiftLeft, left: 6, right: 3, expected: 48},
		{name: "shift right", opcode: OpBinShiftRight, left: 12, right: 2, expected: 3},
		{name: "signed shift right", opcode: OpBinShiftRight, left: -8, right: 1, expected: -4},
		{name: "oversized shift", opcode: OpBinShiftLeft, left: 6, right: 64, expected: 0},
		{name: "negative shift", opcode: OpBinShiftRight, left: 6, right: -1, expected: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fn := &Function{
				ParamCount:  2,
				LocalCount:  2,
				VarArgIndex: -1,
				Bytecode:    []int32{OpLoadVar, 0, OpLoadVar, 1, test.opcode, OpReturn},
			}
			instructions, eligible := nativeFunctionCode(fn)
			if !eligible || len(instructions) == 0 {
				if (runtime.GOARCH == "arm64" || runtime.GOARCH == "arm") && (test.opcode == OpBinMod || test.opcode == OpBinBitAnd || test.opcode == OpBinBitXor || test.opcode == OpBinBitOr || test.opcode == OpBinShiftLeft || test.opcode == OpBinShiftRight) {
					t.Skip("ARM emitter intentionally falls back for this operator")
				}
				if runtime.GOARCH == "386" && (test.opcode == OpBinBitAnd || test.opcode == OpBinBitXor || test.opcode == OpBinBitOr || test.opcode == OpBinShiftLeft || test.opcode == OpBinShiftRight) {
					t.Skip("386 emitter intentionally falls back for bitwise operators")
				}
				t.Fatal("expected eligible function to emit machine code")
			}
			code, err := allocateNativeCode(instructions)
			if err != nil {
				t.Fatalf("allocate native code: %v", err)
			}
			if code == nil {
				t.Skip("native execution is unavailable on this target")
			}
			defer code.release()
			got, ok := code.run(test.left, test.right)
			if !ok || got != test.expected {
				t.Fatalf("native result = %v, %v; want %v; code=% x", got, ok, test.expected, instructions)
			}
		})
	}
}

func TestNativeJITCompilesBitwiseNot(t *testing.T) {
	requireNativeJITEmitter(t)
	if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
		t.Skip("this emitter intentionally falls back for bitwise operators")
	}
	fn := &Function{
		ParamCount:  2,
		LocalCount:  2,
		VarArgIndex: -1,
		Bytecode:    []int32{OpStoreLocal, 1, OpStoreLocal, 0, OpLoadVar, 0, OpUnaryBitNot, OpReturn},
	}
	instructions, eligible := nativeFunctionCode(fn)
	if !eligible {
		t.Fatal("expected bitwise-not function to compile")
	}
	code, err := allocateNativeCode(instructions)
	if err != nil {
		t.Fatalf("allocate native code: %v", err)
	}
	if code == nil {
		t.Skip("native execution is unavailable on this target")
	}
	defer code.release()
	got, ok := code.run(6, 0)
	if !ok || got != -7 {
		t.Fatalf("native bitwise-not result = %v, %v; want -7", got, ok)
	}
}

func TestNativeJITExecutesConditionalReturns(t *testing.T) {
	requireNativeJITEmitter(t)
	state := &State{}
	file := &File{
		Name:    "jit-if-test.os",
		Content: []rune("var choose::fn(flag, value) { if (flag) { return value; } else { return 7; } return value; };"),
	}
	if Compile(NewParser(state, file)) == nil {
		t.Fatal("compilation returned no program")
	}
	var function *Function
	for _, candidate := range state.Functions {
		if candidate.Name == "choose" {
			function = candidate
			break
		}
	}
	if function == nil {
		t.Fatal("compiled function choose was not found")
	}
	instructions, eligible := nativeFunctionCode(function)
	if !eligible {
		t.Fatalf("conditional function was not eligible: bytecode=%v", function.Bytecode)
	}
	code, err := allocateNativeCode(instructions)
	if err != nil {
		t.Fatalf("allocate native code: %v", err)
	}
	if code == nil {
		t.Skip("native execution is unavailable on this target")
	}
	defer code.release()
	for _, test := range []struct {
		name string
		flag float64
		want float64
	}{
		{name: KeyTrue, flag: 1, want: 12},
		{name: KeyFalse, flag: 0, want: 7},
		{name: "NaN is truthy", flag: math.NaN(), want: 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := code.run(test.flag, 12)
			if !ok || got != test.want {
				t.Fatalf("native conditional result = %v, %v; want %v", got, ok, test.want)
			}
		})
	}
}

func TestNativeJITRejectsLoopBackEdges(t *testing.T) {
	fn := &Function{
		ParamCount:  2,
		LocalCount:  2,
		VarArgIndex: -1,
		Bytecode:    []int32{OpLoadVar, 0, OpPopJmpIfTrue, 0, OpLoadNum, 0, 0, OpReturn},
	}
	if _, eligible := nativeFunctionCode(fn); eligible {
		t.Fatal("loop back-edge must use interpreter fallback")
	}
}

func TestNativeJITExecutesSwitchExpression(t *testing.T) {
	requireNativeJITEmitter(t)
	state := &State{}
	file := &File{
		Name:    "jit-switch-expression.os",
		Content: []rune("var choose::fn(selector, fallback) { return switch(selector) { 1 => 11, 2, 3 => 22, default => fallback }; };"),
	}
	if Compile(NewParser(state, file)) == nil {
		t.Fatal("compilation returned no program")
	}
	function := findNativeTestFunction(state, "choose")
	instructions, eligible := nativeFunctionCode(function)
	if !eligible {
		t.Fatalf("switch expression was not eligible: bytecode=%v", function.Bytecode)
	}
	code, err := allocateNativeCode(instructions)
	if err != nil {
		t.Fatalf("allocate native code: %v", err)
	}
	if code == nil {
		t.Skip("native execution is unavailable on this target")
	}
	defer code.release()
	for _, test := range []struct {
		selector float64
		fallback float64
		want     float64
	}{
		{selector: 1, fallback: 99, want: 11},
		{selector: 3, fallback: 99, want: 22},
		{selector: 9, fallback: 99, want: 99},
		{selector: math.NaN(), fallback: 99, want: 99},
	} {
		got, ok := code.run(test.selector, test.fallback)
		if !ok || got != test.want {
			t.Errorf("native switch expression (%v) = %v, %v; want %v", test.selector, got, ok, test.want)
		}
	}
}

func TestNativeJITExecutesSwitchStatement(t *testing.T) {
	requireNativeJITEmitter(t)
	state := &State{}
	file := &File{
		Name:    "jit-switch-statement.os",
		Content: []rune("var choose::fn(selector, fallback) { switch(selector) { case 1: return 11; case 2: return 22; default: return fallback; } return 0; };"),
	}
	if Compile(NewParser(state, file)) == nil {
		t.Fatal("compilation returned no program")
	}
	function := findNativeTestFunction(state, "choose")
	instructions, eligible := nativeFunctionCode(function)
	if !eligible {
		t.Fatalf("switch statement was not eligible: bytecode=%v", function.Bytecode)
	}
	code, err := allocateNativeCode(instructions)
	if err != nil {
		t.Fatalf("allocate native code: %v", err)
	}
	if code == nil {
		t.Skip("native execution is unavailable on this target")
	}
	defer code.release()
	for _, test := range []struct {
		selector float64
		fallback float64
		want     float64
	}{
		{selector: 1, fallback: 99, want: 11},
		{selector: 2, fallback: 99, want: 22},
		{selector: 9, fallback: 99, want: 99},
	} {
		got, ok := code.run(test.selector, test.fallback)
		if !ok || got != test.want {
			t.Errorf("native switch statement (%v) = %v, %v; want %v", test.selector, got, ok, test.want)
		}
	}
}

func findNativeTestFunction(state *State, name string) *Function {
	for _, function := range state.Functions {
		if function.Name == name {
			return function
		}
	}
	return nil
}

func TestNativeJITCompilesNestedNumericExpression(t *testing.T) {
	requireNativeJITEmitter(t)
	fn := &Function{
		ParamCount:  2,
		LocalCount:  2,
		VarArgIndex: -1,
		Bytecode: []int32{
			OpStoreLocal, 1, OpStoreLocal, 0,
			OpLoadVar, 0, OpLoadNum, 2, 0, OpBinMul,
			OpLoadVar, 1, OpLoadNum, 4, 0, OpBinDiv,
			OpBinAdd, OpLoadNum, 1, 0, OpBinSub, OpReturn,
		},
	}
	instructions, eligible := nativeFunctionCode(fn)
	if !eligible {
		t.Fatal("expected nested numeric expression to compile")
	}
	code, err := allocateNativeCode(instructions)
	if err != nil {
		t.Fatalf("allocate native code: %v", err)
	}
	if code == nil {
		t.Skip("native execution is unavailable on this target")
	}
	defer code.release()
	got, ok := code.run(7, 8)
	if !ok || got != 15 {
		t.Fatalf("native expression result = %v, %v; want 15; code=% x", got, ok, instructions)
	}
}

func TestNativeJITCompilesUnaryNegation(t *testing.T) {
	requireNativeJITEmitter(t)
	fn := &Function{
		ParamCount:  2,
		LocalCount:  2,
		VarArgIndex: -1,
		Bytecode:    []int32{OpStoreLocal, 1, OpStoreLocal, 0, OpLoadVar, 0, OpUnaryNeg, OpReturn},
	}
	instructions, eligible := nativeFunctionCode(fn)
	if !eligible {
		t.Fatal("expected unary negation function to compile")
	}
	code, err := allocateNativeCode(instructions)
	if err != nil {
		t.Fatalf("allocate native code: %v", err)
	}
	if code == nil {
		t.Skip("native execution is unavailable on this target")
	}
	defer code.release()
	got, ok := code.run(7, 2)
	if !ok || got != -7 {
		t.Fatalf("native unary result = %v, %v; want -7", got, ok)
	}
}

func TestNativeJITFallsBackOnDivisionByZero(t *testing.T) {
	requireNativeJITEmitter(t)
	fn := &Function{
		ParamCount:  2,
		LocalCount:  2,
		VarArgIndex: -1,
		Bytecode:    []int32{OpStoreLocal, 1, OpStoreLocal, 0, OpLoadVar, 0, OpLoadVar, 1, OpBinDiv, OpReturn},
	}
	instructions, eligible := nativeFunctionCode(fn)
	if !eligible {
		t.Fatal("expected division function to compile")
	}
	code, err := allocateNativeCode(instructions)
	if err != nil {
		t.Fatalf("allocate native code: %v", err)
	}
	if code == nil {
		t.Skip("native execution is unavailable on this target")
	}
	defer code.release()
	if _, ok := code.run(3, 0); ok {
		t.Fatalf("division by zero must deopt to the interpreter's error path; code=% x", instructions)
	}
}

func TestNativeJITCompilesAfterHotCallsAndFallsBack(t *testing.T) {
	requireNativeJITEmitter(t)
	fn := &Function{
		ParamCount:  2,
		LocalCount:  2,
		VarArgIndex: -1,
		Bytecode:    []int32{OpLoadVar, 0, OpLoadVar, 1, OpBinAdd, OpReturn},
	}
	vm := VM{}
	numericArgs := []OSValue{{Type: OSVNum, Number: 2}, {Type: OSVNum, Number: 5}}
	for count := uint32(1); count < nativeJITCallThreshold; count++ {
		if _, executed := vm.tryNative(fn, numericArgs); executed {
			t.Fatalf("executed native code before call %d", count)
		}
	}
	result, executed := vm.tryNative(fn, numericArgs)
	if !executed {
		if runtime.GOOS != "windows" {
			t.Skip("native executable memory is unavailable on this OS")
		}
		t.Fatal("hot eligible function did not enter native code")
	}
	if result.Type != OSVNum || result.Number != 7 {
		t.Fatalf("native result = %+v, want numeric 7", result)
	}
	if code := vm.nativeJIT[fn].code; code != nil {
		stringArgs := []OSValue{{Type: OSVString}, {Type: OSVString}}
		if _, executed := vm.tryNative(fn, stringArgs); executed {
			t.Fatal("nonnumeric call should use the interpreter fallback")
		}
	}
	vm.closeNative()
}

func TestNativeJITRecognizesCompiledFunction(t *testing.T) {
	requireNativeJITEmitter(t)
	state := &State{}
	file := &File{
		Name:    "jit-test.os",
		Content: []rune("var add::fn(left, right) { return left + right; };"),
	}
	program := Compile(NewParser(state, file))
	if program == nil {
		t.Fatal("compilation returned no program")
	}
	for _, fn := range state.Functions {
		if _, eligible := nativeFunctionCode(fn); eligible {
			return
		}
	}
	t.Fatal("compiled two-argument addition function was not eligible for native JIT")
}

func requireNativeJITEmitter(t *testing.T) {
	t.Helper()
	if !nativeJITEmitterAvailable {
		t.Skip("native JIT emitter is unavailable on this target")
	}
}
