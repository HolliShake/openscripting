package main

import (
	"fmt"
	"strconv"
)

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
	AstAsyncFunctionExpression
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
	AstLogStmt
	AstContinueStmt
	AstBreakStmt
	AstReturnStmt
	AstBlock
	AstExpressionStmnt
	AstProgram
)

type Ast struct {
	Type  AstType
	Value string
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
	CatchLocal int32 // Local variable index, or -1 when catch has no parameter
}

type Line struct {
	Index int32 // Opcode index
	Line  int32
}

type ScopeType byte

const (
	ScopeTypeGlobal ScopeType = iota
	ScopeTypeFunction
	ScopeTypeBlock
	ScopeTypeLoop
)

type Scope struct {
	Type       ScopeType
	Parent     *Scope
	Variables  map[string]Symbol
	VarCount   int32
	LoopStart  int32   // Opcode index for the start of the loop, used in loop scopes
	BreakStart []int32 // Opcode index for the start of the break, used in loop scopes
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
	LocalCount    int32
	VarArgIndex   int32 // -1 for non vararg functions
	Bytecode      []int32
	TryCatchTable []TryCatchTable
	Lines         []Line
	File          *File
}

type Cell struct {
	Value OSValue
}

type CallFrame struct {
	Caller   *CallFrame
	Function *Function
	Ip       int32 // Instruction pointer
	Env      []*Cell
}

type CoroutineState int32

const (
	CoroutineStateSuspended CoroutineState = iota
	CoroutineStateRunning
	CoroutineStateDead
)

type Coroutine struct {
	Parent       *Coroutine
	Caller       *Coroutine
	State        CoroutineState
	Frame        CallFrame
	Sp           int32
	OperandStack []OSValue
	Transfer     OSValue
	AsyncResult  *OSPromise
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
	typeStr := t.Type.String()
	valStr := strconv.Quote(t.Value)
	posStr := t.Pos.String()
	return fmt.Sprintf("Token {Type: %s, Value: %s, Pos: %s}", typeStr, valStr, posStr)
}
