package main

func NewFunction(file *File, name string, async bool) *Function {
	return &Function{
		Name:        name,
		Async:       async,
		VarArgIndex: -1,
		File:        file,
	}
}
