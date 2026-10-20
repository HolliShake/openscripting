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

type TryCatchTable struct {
	TryStart   int32 // Opcode index
	TryEnd     int32 // Opcode index
	CatchStart int32 // Opcode index
	CatchEnd   int32 // Opcode index
}

type Function struct {
	Name          string
	Async         bool
	ParamCount    int32
	VarArgIndex   int32 // -1 for non vararg functions
	Bytecode      []int32
	TryCatchTable []TryCatchTable
	File          *File
}

func (s TokenType) String() string {
	switch s {
	case KEY:
		return "keyword"
	case IDN:
		return "ident"
	case NUM:
		return "number"
	case STR:
		return "string"
	case SYM:
		return "symbol"
	case ILL:
		return "illegal"
	case EOF:
		return "eof"
	default:
		return "unknown"
	}
}

func (p Position) String() string {
	return fmt.Sprintf("Position {Line: %d, Colm: %d}", p.Line, p.Column)
}

func (t Token) String() string {
	return fmt.Sprintf("Token {Type: %s, Value: %q, Pos: %s}", t.Type, t.Value, t.Pos)
}
