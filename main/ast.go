package main

import "sync"

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
	AstSwitchExpr
	AstMemberAccess
	AstIndexAccess
	AstCall
	AstPos
	AstInc
	AstPostInc
	AstNeg
	AstDec
	AstPostDec
	AstNot
	AstBitNot
	AstTypeOf
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
	AstExpressionStmt
	AstProgram
)

const astChunk = 1024
const maxPooledAstChunks = 4

var (
	astSlab     []Ast
	astSlabs    [][]Ast
	astUsed     int
	astSlabPool sync.Pool
)

type AstType int

type Ast struct {
	// Type selects the syntax operation so compiler visitors can interpret child links by node kind.
	Type AstType
	// Value stores identifier/literal text directly on the node to avoid separate token ownership.
	Value string
	// Pos preserves source location for compile-time diagnostics and emitted line mappings.
	Pos Position
	// A-E are compact positional child links; their meaning is defined by Type to keep the AST allocation small.
	A *Ast
	B *Ast
	C *Ast
	D *Ast
	E *Ast
	// Next links sibling/list nodes without allocating a slice for every argument or statement list.
	Next *Ast
}

// newAst hands out a zeroed *Ast from the current chunk. The chunk is a single
// make() so the allocator is touched once per astChunk nodes. Advancing with an
// index avoids reslicing the header on every allocation.
func newAst(t AstType, pos Position) *Ast {
	if astUsed >= len(astSlab) {
		if pooled, ok := astSlabPool.Get().([]Ast); ok {
			astSlab = pooled
		} else {
			astSlab = make([]Ast, astChunk)
		}
		astSlabs = append(astSlabs, astSlab)
		astUsed = 0
	}
	n := &astSlab[astUsed]
	astUsed++
	n.Type = t
	n.Pos = pos
	return n
}

func releaseAstSlab() {
	for index, slab := range astSlabs {
		clear(slab)
		if index < maxPooledAstChunks {
			astSlabPool.Put(slab)
		}
	}
	clear(astSlabs)
	astSlabs = astSlabs[:0]
	astSlab = nil
	astUsed = 0
}

func NewKeyValue(position Position, key, value *Ast) *Ast {
	node := newAst(AstKeyValue, position)
	node.A = key
	node.B = value
	return node
}

func NewTerminalAst(ast AstType, position Position, value string) *Ast {
	node := newAst(ast, position)
	node.Value = value
	return node
}

func NewArray(position Position, elements *Ast) *Ast {
	node := newAst(AstArray, position)
	node.A = elements
	return node
}

func NewObject(position Position, properties *Ast) *Ast {
	node := newAst(AstObject, position)
	node.A = properties
	return node
}

func NewSwitchExpression(position Position, discriminant *Ast, cases *Ast) *Ast {
	node := newAst(AstSwitchExpr, position)
	node.A = discriminant
	node.B = cases
	return node
}

func NewMemberAccess(position Position, object, property *Ast) *Ast {
	node := newAst(AstMemberAccess, position)
	node.A = object
	node.B = property
	return node
}

func NewIndexAccess(position Position, object, index *Ast) *Ast {
	node := newAst(AstIndexAccess, position)
	node.A = object
	node.B = index
	return node
}

func NewCall(position Position, obj *Ast, arguments *Ast) *Ast {
	node := newAst(AstCall, position)
	node.A = obj
	node.B = arguments
	return node
}

func NewPostfixOperation(ast AstType, position Position, operand *Ast) *Ast {
	node := newAst(ast, position)
	node.A = operand
	return node
}

func NewUnaryOperation(ast AstType, position Position, operand *Ast) *Ast {
	node := newAst(ast, position)
	node.A = operand
	return node
}

func NewSpread(position Position, expr *Ast) *Ast {
	node := newAst(AstSpread, position)
	node.A = expr
	return node
}

func NewBinaryOperation(ast AstType, position Position, lhs, rhs *Ast) *Ast {
	node := newAst(ast, position)
	node.A = lhs
	node.B = rhs
	return node
}

func NewTernaryOperation(position Position, condition, thenExpr, elseExpr *Ast) *Ast {
	node := newAst(AstTernaryOperation, position)
	node.A = condition
	node.B = thenExpr
	node.C = elseExpr
	return node
}

func NewClassExpression(position Position, base *Ast, attributeHead *Ast) *Ast {
	node := newAst(AstClassExpression, position)
	node.A = base
	node.B = attributeHead
	return node
}

func NewFunctionExpression(astType AstType, position Position, parameterHead *Ast, body *Ast) *Ast {
	node := newAst(astType, position)
	node.A = parameterHead
	node.B = body
	return node
}

// Declarations

func NewDestructuringArrayDeclaration(ast AstType, position Position, elements, value *Ast) *Ast {
	node := newAst(ast, position)
	node.A = elements
	node.B = value
	return node
}

func NewDestructuringObjectDeclaration(ast AstType, position Position, properties, value *Ast) *Ast {
	node := newAst(ast, position)
	node.A = properties
	node.B = value
	return node
}

func NewVariableDeclaration(ast AstType, position Position, declarations *Ast) *Ast {
	node := newAst(ast, position)
	node.A = declarations
	return node
}

func NewImport(position Position, moduleName *Ast, attributes *Ast) *Ast {
	node := newAst(AstImport, position)
	node.A = moduleName
	node.B = attributes
	return node
}

func NewClassDeclaration(position Position, className *Ast, class *Ast) *Ast {
	node := newAst(AstClass, position)
	node.A = className
	node.B = class
	return node
}

func NewFunctionDeclaration(position Position, name *Ast, function *Ast) *Ast {
	node := newAst(AstFunction, position)
	node.A = name
	node.B = function
	return node
}

// Statements

func NewTryCatch(position Position, tryBlock, catchParam, catchBlock *Ast) *Ast {
	node := newAst(AstTryCatch, position)
	node.A = tryBlock
	node.B = catchParam
	node.C = catchBlock
	return node
}

func NewSwitchStatement(position Position, condition *Ast, caseHead *Ast) *Ast {
	node := newAst(AstSwitchStmt, position)
	node.A = condition
	node.B = caseHead
	return node
}

func NewIfStatement(position Position, condition *Ast, thenBlock *Ast, elseBlock *Ast) *Ast {
	node := newAst(AstIfStmt, position)
	node.A = condition
	node.B = thenBlock
	node.C = elseBlock
	return node
}

func NewWhileLoop(position Position, condition *Ast, block *Ast) *Ast {
	node := newAst(AstWhileLoop, position)
	node.A = condition
	node.B = block
	return node
}

func NewForLoop(position Position, init, cond, mutt, block *Ast) *Ast {
	node := newAst(AstForLoop, position)
	node.A = init
	node.B = cond
	node.C = mutt
	node.D = block
	return node
}

func NewLogStatement(position Position, expr *Ast) *Ast {
	node := newAst(AstLogStmt, position)
	node.A = expr
	return node
}

func NewContinueStatement(position Position) *Ast {
	node := newAst(AstContinueStmt, position)
	return node
}

func NewBreakStatement(position Position) *Ast {
	node := newAst(AstBreakStmt, position)
	return node
}

func NewReturnStatement(position Position, value *Ast) *Ast {
	node := newAst(AstReturnStmt, position)
	node.A = value
	return node
}

func NewBlockStatement(position Position, statements *Ast) *Ast {
	node := newAst(AstBlock, position)
	node.A = statements
	return node
}

func NewExpressionStatement(position Position, expr *Ast) *Ast {
	node := newAst(AstExpressionStmt, position)
	node.A = expr
	return node
}

func NewProgram(statements *Ast) *Ast {
	node := newAst(AstProgram, Position{})
	node.A = statements
	return node
}
