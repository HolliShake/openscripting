package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Set to true to print every folded constant.
const debugFold = false

/******************* Constant values (no boxing) ***/

type constKind uint8

const (
	kNone constKind = iota
	kNum
	kStr
	kBool
)

// constVal is returned by value, so folding never allocates
// (except for string concatenation, which has to).
type constVal struct {
	kind constKind
	num  float64
	str  string
	b    bool
}

var noConst constVal

func numConst(f float64) constVal { return constVal{kind: kNum, num: f} }
func strConst(s string) constVal  { return constVal{kind: kStr, str: s} }
func boolConst(b bool) constVal   { return constVal{kind: kBool, b: b} }
func (c constVal) folded() bool   { return c.kind != kNone }
func (c constVal) isNumber() bool { return c.kind == kNum }
func (c constVal) isString() bool { return c.kind == kStr }
func (c constVal) isBool() bool   { return c.kind == kBool }

// constEqual replaces reflect.DeepEqual for the supported kinds.
func constEqual(a, b constVal) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case kNum:
		return a.num == b.num
	case kStr:
		return a.str == b.str
	case kBool:
		return a.b == b.b
	}
	return false
}

/******************* Emit helpers ***/

const initialBytecodeCap = 256

func newFunction(file *File, name string) *Function {
	fn := NewFunction(file, name, false)
	fn.Bytecode = make([]int32, 0, initialBytecodeCap)
	return fn
}

func assert(astType AstType, ast *Ast) {
	if ast.Type != astType {
		panic("assertion failed")
	}
}

func emitLine(fn *Function, line Position) {
	fn.Lines = append(fn.Lines, Lines{
		Index: int32(len(fn.Bytecode)),
		Line:  int32(line.Line),
	})
}

func emitJump(fn *Function, op int32) int32 {
	fn.Bytecode = append(fn.Bytecode, op, 0) // opcode + reserved offset
	return int32(len(fn.Bytecode)) - 1
}

func labelJump(fn *Function, pos int32) {
	fn.Bytecode[pos] = int32(len(fn.Bytecode)) - 1
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
	// One growth up front: rune count <= byte length.
	fn.Bytecode = slices.Grow(fn.Bytecode, len(value)+2)
	fn.Bytecode = append(fn.Bytecode, OpLoadStr, 0)
	lengthIndex := len(fn.Bytecode) - 1
	runeCount := int32(0)
	for _, char := range value {
		fn.Bytecode = append(fn.Bytecode, int32(char))
		runeCount++
	}
	fn.Bytecode[lengthIndex] = runeCount
}

func emitBool(fn *Function, value bool) {
	if value {
		emit(fn, OpLoadTrue)
	} else {
		emit(fn, OpLoadFalse)
	}
}

func emitConst(fn *Function, c constVal) {
	switch c.kind {
	case kNum:
		emitFloat(fn, c.num)
	case kStr:
		emitString(fn, c.str)
	case kBool:
		emitBool(fn, c.b)
	default:
		panic("unsupported type")
	}
}

func binaryOpcode(astType AstType) int32 {
	switch astType {
	case AstMul:
		return OpBinMul
	case AstDiv:
		return OpBinDiv
	case AstMod:
		return OpBinMod
	case AstAdd:
		return OpBinAdd
	case AstSub:
		return OpBinSub
	case AstShiftLeft:
		return OpBinShiftLeft
	case AstShiftRight:
		return OpBinShiftRight
	case AstLess:
		return OpBinLess
	case AstLessEqual:
		return OpBinLessEqual
	case AstGreater:
		return OpBinGreater
	case AstGreaterEqual:
		return OpBinGreaterEqual
	case AstEqual:
		return OpBinEqual
	case AstNotEqual:
		return OpBinNotEqual
	case AstBitAnd:
		return OpBinBitAnd
	case AstBitXor:
		return OpBinBitXor
	case AstBitOr:
		return OpBinBitOr
	default:
		panic("invalid binary operator")
	}
}

/******************* Constant folding ***/

// tryFold returns a constant if the expression can be evaluated at compile time.
// Check result.folded() to know whether it succeeded.
func tryFold(fn *Function, ast *Ast) constVal {
	switch ast.Type {
	case AstNumLit:
		if value, ok := ast.Value.(float64); ok {
			return numConst(value)
		}
		value, err := strconv.ParseFloat(ast.Value.(string), 64)
		if err != nil {
			panic("invalid number literal")
		}
		return numConst(value)
	case AstStrLit:
		return strConst(ast.Value.(string))
	case AstBoolLit:
		return boolConst(ast.Value.(bool))

	case AstPos, AstNeg, AstNot, AstBitNot:
		v := tryFold(fn, ast.A)
		if !v.folded() {
			return noConst
		}
		switch ast.Type {
		case AstPos:
			if v.isNumber() {
				return v
			}
			ThrowError(fn.File, "invalid operand for unary plus", ast.Pos)
		case AstNeg:
			if v.isNumber() {
				return numConst(-v.num)
			}
			ThrowError(fn.File, "invalid operand for negation", ast.Pos)
		case AstNot:
			if v.isBool() {
				return boolConst(!v.b)
			}
			ThrowError(fn.File, "invalid operand for logical not", ast.Pos)
		case AstBitNot:
			if v.isNumber() {
				return numConst(float64(^int64(v.num)))
			}
			ThrowError(fn.File, "invalid operand for bitwise not", ast.Pos)
		}

	case AstMul, AstDiv, AstMod, AstAdd, AstSub:
		lhs := tryFold(fn, ast.A)
		rhs := tryFold(fn, ast.B)
		if !lhs.folded() || !rhs.folded() {
			return noConst
		}
		if ast.Type == AstAdd && lhs.isString() && rhs.isString() {
			return strConst(lhs.str + rhs.str)
		}
		if !lhs.isNumber() || !rhs.isNumber() {
			ThrowError(fn.File, "invalid operands for arithmetic operation", ast.Pos)
		}
		left, right := lhs.num, rhs.num
		switch ast.Type {
		case AstMul:
			return numConst(left * right)
		case AstDiv:
			if right == 0 {
				ThrowError(fn.File, "zero division", ast.Pos)
			}
			return numConst(left / right)
		case AstMod:
			if right == 0 {
				ThrowError(fn.File, "zero division", ast.Pos)
			}
			return numConst(left - float64(int64(left/right))*right)
		case AstAdd:
			return numConst(left + right)
		case AstSub:
			return numConst(left - right)
		}

	case AstLess, AstLessEqual, AstGreater, AstGreaterEqual, AstEqual, AstNotEqual:
		lhs := tryFold(fn, ast.A)
		rhs := tryFold(fn, ast.B)
		if !lhs.folded() || !rhs.folded() {
			return noConst
		}
		comparison := 0
		if lhs.isNumber() && rhs.isNumber() {
			if lhs.num < rhs.num {
				comparison = -1
			} else if lhs.num > rhs.num {
				comparison = 1
			}
		} else if lhs.isString() && rhs.isString() {
			comparison = strings.Compare(lhs.str, rhs.str)
		} else if ast.Type != AstEqual && ast.Type != AstNotEqual {
			ThrowError(fn.File, "invalid operands for comparison", ast.Pos)
		}
		switch ast.Type {
		case AstLess:
			return boolConst(comparison < 0)
		case AstLessEqual:
			return boolConst(comparison <= 0)
		case AstGreater:
			return boolConst(comparison > 0)
		case AstGreaterEqual:
			return boolConst(comparison >= 0)
		case AstEqual:
			return boolConst(constEqual(lhs, rhs))
		case AstNotEqual:
			return boolConst(!constEqual(lhs, rhs))
		}

	case AstBitAnd, AstBitXor, AstBitOr, AstShiftLeft, AstShiftRight:
		lhs := tryFold(fn, ast.A)
		rhs := tryFold(fn, ast.B)
		if !lhs.folded() || !rhs.folded() {
			return noConst
		}
		if !lhs.isNumber() || !rhs.isNumber() {
			ThrowError(fn.File, "invalid operands for bitwise operation", ast.Pos)
		}
		left, right := int64(lhs.num), int64(rhs.num)
		switch ast.Type {
		case AstBitAnd:
			return numConst(float64(left & right))
		case AstBitXor:
			return numConst(float64(left ^ right))
		case AstBitOr:
			return numConst(float64(left | right))
		case AstShiftLeft:
			return numConst(float64(left << uint(right)))
		case AstShiftRight:
			return numConst(float64(left >> uint(right)))
		}

	case AstAnd, AstOr:
		lhs := tryFold(fn, ast.A)
		if !lhs.folded() {
			return noConst
		}
		if !lhs.isBool() {
			ThrowError(fn.File, "invalid left operand for logical operation", ast.Pos)
		}
		if ast.Type == AstAnd {
			if !lhs.b {
				return boolConst(false)
			}
		} else if lhs.b {
			return boolConst(true)
		}
		rhs := tryFold(fn, ast.B)
		if !rhs.folded() {
			return noConst
		}
		if !rhs.isBool() {
			ThrowError(fn.File, "invalid right operand for logical operation", ast.Pos)
		}
		return rhs
	}
	return noConst
}

/******************* Visitors ***/

func visitExpression(p *Parser, fn *Function, ast *Ast) {
	if c := tryFold(fn, ast); c.folded() {
		if debugFold {
			fmt.Printf("result: %+v\n", c)
		}
		emitConst(fn, c)
		return
	}

	switch ast.Type {
	case AstIdentifier:
		emitString(fn, ast.Value.(string))
	case AstNumLit:
		emitFloat(fn, ast.Value.(float64))
	case AstStrLit:
		emitString(fn, ast.Value.(string))
	case AstBoolLit:
		emitBool(fn, ast.Value.(bool))
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
	case AstMul, AstDiv, AstMod, AstAdd, AstSub,
		AstShiftLeft, AstShiftRight,
		AstLess, AstLessEqual, AstGreater, AstGreaterEqual,
		AstEqual, AstNotEqual,
		AstBitAnd, AstBitXor, AstBitOr:
		visitExpression(p, fn, ast.A)
		visitExpression(p, fn, ast.B)
		emit(fn, binaryOpcode(ast.Type))
	case AstAnd, AstOr:
		visitExpression(p, fn, ast.A)
		var jumpPos int32
		if ast.Type == AstAnd {
			jumpPos = emitJump(fn, OpJmpIfFalseOrPop)
		} else {
			jumpPos = emitJump(fn, OpJmpIfTrueOrPop)
		}
		visitExpression(p, fn, ast.B)
		labelJump(fn, jumpPos)
	default:
		panic("visitExpression::unexpected AST type")
	}
}

func visitFunctionDeclaration(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	function := newFunction(fn.File, ast.A.Value.(string))
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
		parmNode = parmNode.Next
	}

	visibleReturn := false
	for bodyNode != nil {
		if bodyNode.Type == AstReturnStmt {
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

	status, exists := scope.GetVariable(nameNode.Value.(string))
	if !exists {
		ThrowError(fn.File, "function does not exists", ast.Pos)
	}
	emitInt(fn, OpLoadFunction, funcOffset)
	emitInt(fn, OpStoreLocal, status.Offset)
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
		panic(fmt.Sprintf("visitDeclarationOrStatement:: unexpected statement type %v", ast.Type))
	}
}

func compileProgram(p *Parser) {
	ast := p.Parse()
	assert(AstProgram, ast)

	program := newFunction(p.File, "$main")
	scope := Scope{
		Parent:    nil,
		Variables: make(map[string]Symbol),
		VarCount:  0,
	}

	// forward function declarations
	for current := ast.A; current != nil; current = current.Next {
		if current.Type == AstFunction {
			nameNode := current.A
			funcName := nameNode.Value.(string)
			scope.AddLocal(funcName, Symbol{
				Name:    funcName,
				Offset:  scope.GetVarCount(),
				Const:   false,
				Capture: false,
				Pos:     nameNode.Pos,
			})
		}
	}

	for current := ast.A; current != nil; current = current.Next {
		visitDeclarationOrStatement(p, &scope, program, current)
	}

	emit(program, OpLoadNull)
	emit(program, OpReturn)
}

func Compile(p *Parser) {
	compileProgram(p)
}
