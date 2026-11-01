package main

type TryCatchTable struct {
	// TryStart is the first bytecode offset protected by this handler.
	TryStart int
	// TryEnd is the exclusive end offset so nested handlers can test instruction ranges efficiently.
	TryEnd int
	// CatchStart is the bytecode destination when an error is raised inside the protected range.
	CatchStart int
	// CatchEnd bounds catch execution for compiler control-flow analysis and jump patching.
	CatchEnd int
	// CatchLocal is the frame slot receiving the Error value, or -1 when the catch has no binding.
	CatchLocal int
}
