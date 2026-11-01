package main

type Symbol struct {
	// Name is the lexical identifier associated with this binding.
	Name string
	// Offset is its slot in the owning frame; bytecode uses this integer for local access.
	Offset int
	// Const prevents reassignment after declaration.
	Const bool
	// Capture marks a proxy local slot populated from a closure cell rather than a new declaration.
	Capture bool
	// Pos points to the declaration for duplicate-binding and assignment diagnostics.
	Pos Position
}
