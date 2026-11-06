package main

import "strconv"

type Position struct {
	// Line is the one-based source line for parser/compiler diagnostics.
	Line int
	// Column is the one-based rune column, keeping diagnostics consistent with Unicode-aware lexing.
	Column int
}

func (p Position) String() string {
	return "Position {Line: " + strconv.Itoa(p.Line) + ", Colm: " + strconv.Itoa(p.Column) + "}"
}
