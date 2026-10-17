package main

import "fmt"

type TokenType int

const (
	KEY TokenType = iota
	IDN
	NUM
	STR
	SYM
	ILL
	EOF
)

type File struct {
	Name    string
	Content []rune
}

type State struct {
	File []*File
}

type Position struct {
	Line   int
	Column int
}

type Token struct {
	Type  TokenType
	Value string
	Pos   Position
}

type Parser struct {
	state     *State
	file      *File
	line      int
	colm      int
	offset    int
	lookahead Token
	errors    []string
}

type Ast struct {
	Type  AstType
	Value interface{}
	Pos   Position
	A     *Ast
	B     *Ast
	C     *Ast
	D     *Ast
	E     *Ast
	Next  *Ast
}

type Function struct {
	Name        string
	Async       bool
	Argc        int32
	VarArgIndex int32 // -1 for non vararg functions
	Bytecode    []int32
}

func (s TokenType) String() string {
	switch s {
	case KEY:
		return "KEY"
	case IDN:
		return "IDN"
	case NUM:
		return "NUM"
	case STR:
		return "STR"
	case SYM:
		return "SYM"
	case ILL:
		return "ILLEGAL"
	case EOF:
		return "EOF"
	default:
		return "UNKNOWN"
	}
}

func (p Position) String() string {
	return fmt.Sprintf("Position {Line: %d, Colm: %d}", p.Line, p.Column)
}

func (t Token) String() string {
	return fmt.Sprintf("Token {Type: %s, Value: %q, Pos: %s}", t.Type, t.Value, t.Pos)
}
