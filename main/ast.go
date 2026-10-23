package main

const astChunk = 256

var astSlab []Ast

func newAst(t AstType, pos Position) *Ast {
	if len(astSlab) == 0 {
		astSlab = make([]Ast, astChunk)
	}
	n := &astSlab[0]
	astSlab = astSlab[1:]
	n.Type = t
	n.Pos = pos
	return n
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
	node := newAst(AstExpressionStmnt, position)
	node.A = expr
	return node
}

func NewProgram(statements *Ast) *Ast {
	node := newAst(AstProgram, Position{})
	node.A = statements
	return node
}
