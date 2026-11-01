package main

type Line struct {
	// Index is the bytecode offset where this source line mapping begins.
	Index int
	// Line is the source line used to report runtime errors for instructions at or after Index.
	Line int
}
