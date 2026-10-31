package main

type CallFrame struct {
	Caller   *CallFrame
	Function *Function
	Ip       int // Instruction pointer
	Env      []*Cell
}
