package main

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func assert(astType AstType, ast *Ast) {
	if ast.Type != astType {
		panic("assertion failed")
	}
}

func emitLine(fn *Function, line Position) {
	fn.Lines = append(fn.Lines, Lines{
		Index: int32(len(fn.Bytecode)),
		Line:  int32(line.Line)},
	)
}

func emitJump(fn *Function, op int32) int32 {
	fn.Bytecode = append(fn.Bytecode, op)
	// Reserve space for jump offset
	pos := int32(len(fn.Bytecode))
	fn.Bytecode = append(fn.Bytecode, 0)
	return pos
}

func labelJump(fn *Function, pos int32) {
	offset := int32(len(fn.Bytecode)) - 1
	fn.Bytecode[pos] = offset
}

func emit(fn *Function, op int32) {
	fn.Bytecode = append(fn.Bytecode, op)
}

func emitInt(fn *Function, op int32, value int32) {
	fn.Bytecode = append(fn.Bytecode, op, value)
}

func emitFloat(fn *Function, value float64) {
	whole, decimal := SliceFloat(value)
	fn.Bytecode = append(fn.Bytecode, OpLoadNum, whole, decimal)
}

func emitString(fn *Function, value string) {
	emit(fn, OpLoadStr)
	strBytes := []rune(value)
	emit(fn, int32(len(strBytes))) // onlyl int32 size for string length
	fn.Bytecode = append(fn.Bytecode, strBytes...)
}

func emitAny(fn *Function, value any) {
	if isNumber(value) {
		emitFloat(fn, value.(float64))
		return
	}

	if isString(value) {
		emitString(fn, value.(string))
		return
	}

	if value, ok := value.(bool); ok {
		if value {
			emit(fn, OpLoadTrue)
		} else {
			emit(fn, OpLoadFalse)
		}
		return
	}

	panic("unsupported type")

}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func isNumber(value any) bool {
	_, ok := value.(float64)
	return ok
}

func isBool(value any) bool {
	_, ok := value.(bool)
	return ok
}

func tryFold(fn *Function, ast *Ast) (result any, folded bool) {
	switch ast.Type {
	case AstNumLit:
		if value, ok := ast.Value.(float64); ok {
			return value, true
		}
		value, err := strconv.ParseFloat(ast.Value.(string), 64)
		if err != nil {
			panic("invalid number literal")
		}
		return value, true
	case AstStrLit:
		return ast.Value.(string), true
	case AstBoolLit:
		return ast.Value.(bool), true
	case AstPos,
		AstNeg,
		AstNot,
		AstBitNot:
		value, valueFolded := tryFold(fn, ast.A)
		if !valueFolded {
			return nil, false
		}
		switch ast.Type {
		case AstPos:
			if isNumber(value) {
				return value, true
			}
			ThrowError(fn.File, "invalid operand for unary plus", ast.Pos)
		case AstNeg:
			if isNumber(value) {
				return -value.(float64), true
			}
			ThrowError(fn.File, "invalid operand for negation", ast.Pos)
		case AstNot:
			if isBool(value) {
				return !value.(bool), true
			}
			ThrowError(fn.File, "invalid operand for logical not", ast.Pos)
		case AstBitNot:
			if isNumber(value) {
				return float64(^int64(value.(float64))), true
			}
			ThrowError(fn.File, "invalid operand for bitwise not", ast.Pos)
		}
	case AstMul,
		AstDiv,
		AstMod,
		AstAdd,
		AstSub:
		lhs, lFolded := tryFold(fn, ast.A)
		rhs, rFolded := tryFold(fn, ast.B)
		if !lFolded || !rFolded {
			return nil, false
		}
		if ast.Type == AstAdd && isString(lhs) && isString(rhs) {
			return lhs.(string) + rhs.(string), true
		}
		if !isNumber(lhs) || !isNumber(rhs) {
			ThrowError(fn.File, "invalid operands for arithmetic operation", ast.Pos)
		}
		left, right := lhs.(float64), rhs.(float64)
		switch ast.Type {
		case AstMul:
			return left * right, true
		case AstDiv:
			if right == 0 {
				ThrowError(fn.File, "zero division", ast.Pos)
			}
			return left / right, true
		case AstMod:
			if right == 0 {
				ThrowError(fn.File, "zero division", ast.Pos)
			}
			return left - float64(int64(left/right))*right, true
		case AstAdd:
			return left + right, true
		case AstSub:
			return left - right, true
		}
	case AstLess,
		AstLessEqual,
		AstGreater,
		AstGreaterEqual,
		AstEqual,
		AstNotEqual:
		lhs, lFolded := tryFold(fn, ast.A)
		rhs, rFolded := tryFold(fn, ast.B)
		if !lFolded || !rFolded {
			return nil, false
		}
		var comparison int
		if isNumber(lhs) && isNumber(rhs) {
			left, right := lhs.(float64), rhs.(float64)
			if left < right {
				comparison = -1
			} else if left > right {
				comparison = 1
			}
		} else if isString(lhs) && isString(rhs) {
			comparison = strings.Compare(lhs.(string), rhs.(string))
		} else if ast.Type != AstEqual && ast.Type != AstNotEqual {
			ThrowError(fn.File, "invalid operands for comparison", ast.Pos)
		}
		switch ast.Type {
		case AstLess:
			return comparison < 0, true
		case AstLessEqual:
			return comparison <= 0, true
		case AstGreater:
			return comparison > 0, true
		case AstGreaterEqual:
			return comparison >= 0, true
		case AstEqual:
			return reflect.DeepEqual(lhs, rhs), true
		case AstNotEqual:
			return !reflect.DeepEqual(lhs, rhs), true
		}
	case AstBitAnd,
		AstBitXor,
		AstBitOr,
		AstShiftLeft,
		AstShiftRight:
		lhs, lFolded := tryFold(fn, ast.A)
		rhs, rFolded := tryFold(fn, ast.B)
		if !lFolded || !rFolded {
			return nil, false
		}
		if !isNumber(lhs) || !isNumber(rhs) {
			ThrowError(fn.File, "invalid operands for bitwise operation", ast.Pos)
		}
		left, right := int64(lhs.(float64)), int64(rhs.(float64))
		switch ast.Type {
		case AstBitAnd:
			return float64(left & right), true
		case AstBitXor:
			return float64(left ^ right), true
		case AstBitOr:
			return float64(left | right), true
		case AstShiftLeft:
			return float64(left << uint(right)), true
		case AstShiftRight:
			return float64(left >> uint(right)), true
		}
	case AstAnd, AstOr:
		lhs, lFolded := tryFold(fn, ast.A)
		if !lFolded {
			return nil, false
		}
		if !isBool(lhs) {
			ThrowError(fn.File, "invalid left operand for logical operation", ast.Pos)
		}
		if ast.Type == AstAnd {
			if !lhs.(bool) {
				return false, true
			}
		} else if lhs.(bool) {
			return true, true
		}
		rhs, rFolded := tryFold(fn, ast.B)
		if !rFolded {
			return nil, false
		}
		if !isBool(rhs) {
			ThrowError(fn.File, "invalid right operand for logical operation", ast.Pos)
		}
		return rhs.(bool), true
	}
	return nil, false
}

func visitExpression(p *Parser, fn *Function, ast *Ast) {
	if result, folded := tryFold(fn, ast); folded {
		fmt.Printf("result: %v\n", result)
		emitAny(fn, result)
		return
	}

	switch ast.Type {
	case AstIdentifier:
		{
			emitString(fn, ast.Value.(string))
		}
	case AstNumLit:
		emitFloat(fn, ast.Value.(float64))
	case AstStrLit:
		emitString(fn, ast.Value.(string))
	case AstBoolLit:
		if ast.Value.(bool) {
			emit(fn, OpLoadTrue)
		} else {
			emit(fn, OpLoadFalse)
		}
	case AstNullLit:
		emit(fn, OpLoadNull)
	case AstPos, AstNeg, AstNot, AstBitNot:
		visitExpression(p, fn, ast.A)
		switch ast.Type {
		case AstPos:
			emit(fn, OpUnaryPos)
		case AstNeg:
			emit(fn, OpUnaryNeg)
		case AstNot:
			emit(fn, OpUnaryNot)
		case AstBitNot:
			emit(fn, OpUnaryBitNot)
		}
	case AstMul,
		AstDiv,
		AstMod,
		AstAdd,
		AstSub,
		AstShiftLeft,
		AstShiftRight,
		AstLess,
		AstLessEqual,
		AstGreater,
		AstGreaterEqual,
		AstEqual,
		AstNotEqual,
		AstBitAnd,
		AstBitXor,
		AstBitOr:
		visitExpression(p, fn, ast.A)
		visitExpression(p, fn, ast.B)

		opcode := map[AstType]int32{
			AstMul:          OpBinMul,
			AstDiv:          OpBinDiv,
			AstMod:          OpBinMod,
			AstAdd:          OpBinAdd,
			AstSub:          OpBinSub,
			AstShiftLeft:    OpBinShiftLeft,
			AstShiftRight:   OpBinShiftRight,
			AstLess:         OpBinLess,
			AstLessEqual:    OpBinLessEqual,
			AstGreater:      OpBinGreater,
			AstGreaterEqual: OpBinGreaterEqual,
			AstEqual:        OpBinEqual,
			AstNotEqual:     OpBinNotEqual,
			AstBitAnd:       OpBinBitAnd,
			AstBitXor:       OpBinBitXor,
			AstBitOr:        OpBinBitOr,
			AstAnd:          OpBinAnd,
			AstOr:           OpBinOr,
		}[ast.Type]

		emit(fn, opcode)
	case AstAnd, AstOr:
		{
			/*

				var x = false && true; // false
				var y = false || true; // true
			*/
			visitExpression(p, fn, ast.A)
			var jumpPos int32 = 0
			switch ast.Type {
			case AstAnd:
				jumpPos = emitJump(fn, OpJmpIfFalseOrPop)
			case AstOr:
				jumpPos = emitJump(fn, OpJmpIfTrueOrPop)
			}
			visitExpression(p, fn, ast.B)
			labelJump(fn, jumpPos)
		}

	default:
		panic("visitExpression::unexpected AST type")
	}
}

func visitFunctionDeclaration(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	function := NewFunction(fn.File, ast.A.Value.(string), false)
	localScope := Scope{
		Parent:    scope,
		Variables: make(map[string]Symbol),
		VarCount:  0,
	}

	nameNode := ast.A
	parmNode := ast.B.A
	bodyNode := ast.B.B

	for parmNode != nil {
		paramName := parmNode.Value.(string)

		if localScope.ExistsLocal(paramName) {
			ThrowError(fn.File, "variable is already defined in scope", parmNode.Pos)
		}

		offset := localScope.GetVarCount()

		localScope.AddLocal(paramName, Symbol{
			Name:    paramName,
			Offset:  offset,
			Const:   false,
			Capture: false,
			Pos:     parmNode.Pos,
		})

		emitInt(function, OpStoreLocal, offset)

		// Process each parameter here
		parmNode = parmNode.Next
	}

	visibleReturn := false
	for bodyNode != nil {
		if !visibleReturn && bodyNode.Type == AstReturnStmt {
			visibleReturn = true
		}
		visitDeclarationOrStatement(p, &localScope, function, bodyNode)
		bodyNode = bodyNode.Next
	}

	if !visibleReturn {
		emit(function, OpLoadNull)
		emit(function, OpReturn)
	}

	funcOffset := p.state.SaveFunction(function)

	if status, exists := scope.GetVariable(nameNode.Value.(string)); exists {
		emitInt(fn, OpLoadFunction, funcOffset)
		emitInt(fn, OpStoreLocal, status.Offset)
	} else {
		ThrowError(fn.File, "function does not exists", ast.Pos)
	}
}

func visitReturn(p *Parser, fn *Function, ast *Ast) {
	if ast.A != nil {
		visitExpression(p, fn, ast.A)
	} else {
		emit(fn, OpLoadNull)
	}
	emit(fn, OpReturn)
}

func visitDeclarationOrStatement(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	emitLine(fn, ast.Pos)
	switch ast.Type {
	case AstFunction:
		visitFunctionDeclaration(p, scope, fn, ast)
	case AstExpressionStmnt:
		visitExpression(p, fn, ast.A)
	case AstReturnStmt:
		visitReturn(p, fn, ast.A)
	default:
		panic(
			fmt.Sprintf("visitDeclarationOrStatement:: unexpected statement type %v", ast.Type),
		)
	}
}

func compileProgram(p *Parser) {
	ast := p.Parse()
	assert(AstProgram, ast)

	program := NewFunction(p.File, "$main", false)
	scope := Scope{
		Parent:    nil,
		Variables: make(map[string]Symbol),
		VarCount:  0,
	}

	current := ast.A
	// forward function declarations
	for current != nil {
		if current.Type == AstFunction {
			nameNode := current.A
			funcName := nameNode.Value.(string)
			offset := scope.GetVarCount()
			scope.AddLocal(funcName, Symbol{
				Name:    funcName,
				Offset:  offset,
				Const:   false,
				Capture: false,
				Pos:     nameNode.Pos,
			})
		}
		current = current.Next
	}

	current = ast.A
	for current != nil {
		visitDeclarationOrStatement(p, &scope, program, current)
		current = current.Next
	}

	emit(program, OpLoadNull)
	emit(program, OpReturn)
}

func Compile(p *Parser) {
	compileProgram(p)
}
