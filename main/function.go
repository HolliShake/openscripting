package main

import "slices"

type Function struct {
	// File retains source text and filename for diagnostics from this function's bytecode.
	File *File
	// Name identifies the callable in error reports and runtime diagnostics.
	Name string
	// Async selects promise-based scheduling and await/yield behavior for this function.
	Async bool
	// ClassConstructor marks the special constructor frame that receives instance and superclass values.
	ClassConstructor bool
	// ClassThisOffset is the constructor-frame slot containing the new instance.
	ClassThisOffset int
	// ClassSuperOffset is the constructor-frame slot containing the parent-class value.
	ClassSuperOffset int
	// ParamCount is the fixed parameter count used for call validation and argument setup.
	ParamCount int
	// LocalCount sizes Env; every compiled local and capture destination must fit this frame.
	LocalCount int
	// VarArgIndex is the first variadic parameter slot, or -1 when arguments are fixed.
	VarArgIndex int
	// Module links module initializer bytecode to its cached import result.
	Module *Import
	// Bytecode is the executable instruction stream for this function.
	Bytecode []int32
	// TryCatchTable maps protected instruction ranges to catch destinations and bindings.
	TryCatchTable []TryCatchTable
	// Lines maps instruction offsets to source lines for runtime error reporting.
	Lines []Line
	// CaptureMeta maps enclosing cells into closure-local environment slots.
	CaptureMeta []CaptureMeta
}

func (fn *Function) Clone() *Function {
	c := *fn
	c.Bytecode = slices.Clone(fn.Bytecode)
	c.TryCatchTable = slices.Clone(fn.TryCatchTable)
	c.Lines = slices.Clone(fn.Lines)
	c.CaptureMeta = slices.Clone(fn.CaptureMeta)
	return &c
}

func NewFunction(file *File, name string, async bool) *Function {
	return &Function{
		Name:        name,
		Async:       async,
		VarArgIndex: -1,
		File:        file,
	}
}
