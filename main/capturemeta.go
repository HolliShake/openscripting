package main

type CaptureMeta struct {
	// Name preserves the captured identifier for compiler diagnostics and debugging.
	Name string
	// Cell is filled when a closure is created and points at the enclosing variable's shared storage.
	Cell *Cell
	// Src is the enclosing frame slot used while the compiler is resolving the closure.
	Src int
	// Dst is the corresponding local slot in the closure frame; it is required to load captures uniformly.
	Dst int
}
