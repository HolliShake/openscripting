package main

type Function struct {
	Name          string
	Async         bool
	ParamCount    int32
	LocalCount    int32
	VarArgIndex   int32 // -1 for non vararg functions
	Bytecode      []int32
	TryCatchTable []TryCatchTable
	Lines         []Line
	File          *File
	Captures      []*Cell
	CaptureMeta   []CaptureMeta
}

func (fn *Function) Clone() *Function {
	return &Function{
		Name:          fn.Name,
		Async:         fn.Async,
		ParamCount:    fn.ParamCount,
		LocalCount:    fn.LocalCount,
		VarArgIndex:   fn.VarArgIndex,
		Bytecode:      append([]int32(nil), fn.Bytecode...),
		TryCatchTable: append([]TryCatchTable(nil), fn.TryCatchTable...),
		Lines:         append([]Line(nil), fn.Lines...),
		File:          fn.File,
		Captures:      append([]*Cell(nil), fn.Captures...),
		CaptureMeta:   append([]CaptureMeta(nil), fn.CaptureMeta...),
	}
}

func NewFunction(file *File, name string, async bool) *Function {
	return &Function{
		Name:        name,
		Async:       async,
		VarArgIndex: -1,
		File:        file,
	}
}
