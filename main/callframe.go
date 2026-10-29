package main

type CallFrame struct {
	Caller   *CallFrame
	Function *Function
	Ip       int32 // Instruction pointer
	Env      []*Cell
}
