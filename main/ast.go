package main

type AstType int

const (
	// Expressions
	AstIdentifier AstType = iota
	AstNumLit
	AstStrLit
	AstBoolLit
	AstNullLit
	// Complex Expressions
	AstClassExpression
	AstFunctionExpression
	AstArray
	AstObject
	AstKeyValue
	// Unary Operations
	AstSpread
	// Binary Operations
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
	// Declarations
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
	// Statements
	AstIfStatement
	AstForLoop
	AstWhileLoop
	AstBlock
	AstExpressionStmnt
	AstProgram
)

func newAst(ast AstType, position Position) *Ast {
	return &Ast{
		Type:  ast,
		Pos:   position,
		Value: nil,
		A:     nil,
		B:     nil,
		C:     nil,
		D:     nil,
		E:     nil,
		Next:  nil,
	}
}

func NewKeyValue(position Position, key, value *Ast) *Ast {
	node := newAst(AstKeyValue, position)
	node.A = key
	node.B = value
	return node
}

func NewTerminalAst(ast AstType, position Position, value interface{}) *Ast {
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

func NewClassExpression(position Position, attributeHead *Ast) *Ast {
	node := newAst(AstClassExpression, position)
	node.A = attributeHead
	return node
}

func NewFunctionExpression(position Position, parameterHead *Ast, body *Ast) *Ast {
	node := newAst(AstFunctionExpression, position)
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

func NewIfStatement(position Position, condition *Ast, thenBlock *Ast, elseBlock *Ast) *Ast {
	node := newAst(AstIfStatement, position)
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

func NewBlockStatement(position Position, statements *Ast) *Ast {
	node := newAst(AstBlock, position)
	node.A = statements
	return node
}

func NewExpressionStatement(position Position, expr *Ast) *Ast {
	node := newAst(AstExpressionStmnt, position)
	node.A = expr
	return node
}

func NewProgram(statements *Ast) *Ast {
	node := newAst(AstProgram, Position{})
	node.A = statements
	return node
}
