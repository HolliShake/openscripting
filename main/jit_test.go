package main

import (
	"runtime"
	"testing"
)

func TestNativeJITEmitsArithmeticMachineCode(t *testing.T) {
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
				if runtime.GOARCH == "arm64" && (test.opcode == OpBinMod || test.opcode == OpBinShiftLeft || test.opcode == OpBinShiftRight) {
					t.Skip("ARM64 emitter intentionally falls back for this operator")
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
	if runtime.GOARCH == "386" {
		t.Skip("386 emitter intentionally falls back for bitwise operators")
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

func TestNativeJITCompilesNestedNumericExpression(t *testing.T) {
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
