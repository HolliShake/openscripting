package main

type CallFrame struct {
	// Caller links the active frame chain for runtime stack inspection and lexical lookup.
	Caller *CallFrame
	// Function supplies bytecode, constants, source mapping, and local-frame sizing for this invocation.
	Function *Function
	// Ip is the next bytecode instruction; saving it allows callers to resume after a callee returns.
	Ip int
	// Env maps compiled local offsets to cells so captured locals can outlive their defining call.
	Env []*Cell
}
