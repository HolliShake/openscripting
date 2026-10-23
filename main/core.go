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
	File      []*File
	Imports   map[string]*Import
	Functions []*Function
}

type Import struct {
	IsImporting bool
	Node        *Ast
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
	File      *File
	line      int
	colm      int
	offset    int
	lookahead Token
	errors    []string
}

type AstType int

const (
	AstIdentifier AstType = iota
	AstNumLit
	AstStrLit
	AstBoolLit
	AstNullLit
	AstClassExpression
	AstFunctionExpression
	AstArray
	AstObject
	AstKeyValue
	AstMemberAccess
	AstIndexAccess
	AstCall
	AstPos
	AstInc
	AstNeg
	AstDec
	AstNot
	AstBitNot
	AstAwait
	AstSpread
	AstMul
	AstDiv
	AstMod
	AstAdd
	AstSub
	AstShiftLeft
	AstShiftRight
	AstLess
	AstLessEqual
	AstGreater
	AstGreaterEqual
	AstEqual
	AstNotEqual
	AstBitAnd
	AstBitXor
	AstBitOr
	AstAnd
	AstOr
	AstAssign
	AstMulAssign
	AstDivAssign
	AstModAssign
	AstAddAssign
	AstSubAssign
	AstShiftLeftAssign
	AstShiftRightAssign
	AstAndAssign
	AstXorAssign
	AstOrAssign
	AstTernaryOperation
	AstImport
	AstClass
	AstFunction
	AstDestructuringArrayVar
	AstDestructuringArrayLet
	AstDestructuringArrayConst
	AstDestructuringObjectVar
	AstDestructuringObjectLet
	AstDestructuringObjectConst
	AstVar
	AstLet
	AstConst
	AstTryCatch
	AstSwitchStmt
	AstIfStmt
	AstForLoop
	AstWhileLoop
	AstContinueStmt
	AstBreakStmt
	AstReturnStmt
	AstBlock
	AstExpressionStmnt
	AstProgram
)

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

type Lines struct {
	Index int32 // Opcode index
	Line  int32
}

type Scope struct {
	Parent    *Scope
	Variables map[string]Symbol
	VarCount  int32
}

type Symbol struct {
	Name    string
	Offset  int32
	Const   bool
	Capture bool
	Pos     Position
}

type Function struct {
	Name          string
	Async         bool
	ParamCount    int32
	VarArgIndex   int32 // -1 for non vararg functions
	Bytecode      []int32
	TryCatchTable []TryCatchTable
	Lines         []Lines
	File          *File
}

type CallFrame struct {
	Caller   *CallFrame
	Function *Function
	Ip       int32 // Instruction pointer
}

type Coroutine struct {
	Frame *CallFrame
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
