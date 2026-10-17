package main

type AstType int

const (
	AstIdentifier AstType = iota
	AstNumLit
	AstStrLit
	AstBoolLit
	AstNullLit
	//
	AstFunction
	AstArray
	AstObject
	AstKeyValue
	//
	AstSpread
	//
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
	AstDestructuringArrayVar
	AstDestructuringArrayLet
	AstDestructuringArrayConst
	AstDestructuringObjectVar
	AstDestructuringObjectLet
	AstDestructuringObjectConst
	AstVar
	AstLet
	AstConst
	AstExpressionStmnt
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

func NewExpressionStatement(position Position, expr *Ast) *Ast {
	node := newAst(AstExpressionStmnt, position)
	node.A = expr
	return node
}

func NewDestructuringArrayDeclaration(position Position, elements, value *Ast) *Ast {
	node := newAst(AstArray, position)
	node.A = elements
	node.B = value
	return node
}

func NewDestructuringObjectDeclaration(position Position, properties, value *Ast) *Ast {
	node := newAst(AstObject, position)
	node.A = properties
	node.B = value
	return node
}

func NewFunctionDeclaration(position Position, name *Ast, function *Ast) *Ast {
	node := newAst(AstFunction, position)
	node.A = name
	node.B = function
	return node
}
