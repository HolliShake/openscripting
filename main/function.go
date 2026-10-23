package main

func NewFunction(file *File, name string, async bool) *Function {
	return &Function{
		Name:          name,
		Async:         async,
		ParamCount:    0,
		Bytecode:      make([]int32, 0),
		VarArgIndex:   -1,
		TryCatchTable: make([]TryCatchTable, 0),
		File:          file,
	}
}
