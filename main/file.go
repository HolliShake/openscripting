package main

type File struct {
	// Name identifies the source in diagnostics and relative-import resolution.
	Name string
	// Content stores decoded Unicode code points so lexer positions and character classes are rune-based.
	Content []rune
}
