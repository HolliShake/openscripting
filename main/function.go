package main

import "slices"

type Function struct {
	File          *File
	Name          string
	Async         bool
	ParamCount    int32
	LocalCount    int32
	VarArgIndex   int32 // -1 for non vararg functions
	Bytecode      []int32
	TryCatchTable []TryCatchTable
	Lines         []Line
	CaptureMeta   []CaptureMeta
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
