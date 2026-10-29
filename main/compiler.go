package main

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
	"unicode/utf8"
)

// Set to true to print every folded constant.
const debugFold = false

/******************* Constant values (no boxing) ***/

const (
	kNone constKind = iota
	kNum
	kStr
	kBool
)

var noConst constVal

// Scopes used to be stack literals whose address was passed down a recursive
// visitor (and stored in child.Parent), so every one of them was "moved to
// heap" together with a fresh map. They are now recycled through a pool, so
// the steady-state cost is zero allocations per block / loop / function.
var scopePool sync.Pool

const initialBytecodeCap = 256
const maxPooledScopeVars = 64
const maxPooledBreaks = 64

type constKind uint8

// constVal is returned by value, so folding never allocates
// (except for string concatenation, which has to).
// Fields are ordered so kind+b share one word: 32 bytes instead of 48.
type constVal struct {
	num  float64
	str  string
	kind constKind
	b    bool
}

func (c constVal) folded() bool   { return c.kind != kNone }
func (c constVal) isNumber() bool { return c.kind == kNum }
func (c constVal) isString() bool { return c.kind == kStr }
func (c constVal) isBool() bool   { return c.kind == kBool }
func numConst(f float64) constVal { return constVal{kind: kNum, num: f} }
func strConst(s string) constVal  { return constVal{kind: kStr, str: s} }
func boolConst(b bool) constVal   { return constVal{kind: kBool, b: b} }

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

/******************* Scope pooling ***/

// acquireScope returns a clean scope. The caller sets Type (and any other
// fields) afterwards. VarCount starts at 0.
func acquireScope(parent *Scope) *Scope {
	s, _ := scopePool.Get().(*Scope)
	if s == nil {
		s = &Scope{Variables: make(map[string]Symbol)}
	}
	vars, breaks := s.Variables, s.BreakStart[:0]
	clear(vars)
	*s = Scope{Parent: parent, Variables: vars, BreakStart: breaks}
	return s
}

// releaseScope drops every reference the scope holds (symbols, parent) so a
// pooled scope never keeps compile-time data alive.
func releaseScope(s *Scope) {
	if len(s.Variables) > maxPooledScopeVars || cap(s.BreakStart) > maxPooledBreaks {
		clear(s.Variables)
		s.Parent = nil
		return
	}
	clear(s.Variables)
	s.Parent = nil
	scopePool.Put(s)
}

/******************* Emit helpers ***/

func newFunction(file *File, name string) *Function {
	fn := NewFunction(file, name, false)
	fn.Bytecode = make([]int32, 0, initialBytecodeCap)
	return fn
}

// shrink returns a slice whose capacity equals its length, so the over-sized
// backing array can be garbage collected. (`s[:len(s)]` does not do that.)
func shrink[T any](s []T) []T {
	if len(s) == cap(s) {
		return s
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

// finalizeFunction releases the slack left over from append growth.
func finalizeFunction(fn *Function) {
	fn.Bytecode = shrink(fn.Bytecode)
	fn.Lines = shrink(fn.Lines)
	fn.TryCatchTable = shrink(fn.TryCatchTable)
}

func assert(astType AstType, ast *Ast) {
	if ast.Type != astType {
		panic("assertion failed")
	}
}

// unexpected is kept out of line so the fmt boxing of its arguments does not
// make variables in the hot visitor functions escape.
//
//go:noinline
func unexpected(where string, t AstType) {
	panic(fmt.Sprintf("%s: unexpected type %v", where, t))
}

func emitLine(fn *Function, line Position) {
	idx := int32(len(fn.Bytecode))
	ln := int32(line.Line)
	if n := len(fn.Lines); n > 0 {
		last := &fn.Lines[n-1]
		if last.Index == idx { // previous node emitted no code
			last.Line = ln
			return
		}
		if last.Line == ln { // same line, entry is redundant
			return
		}
	}
	fn.Lines = append(fn.Lines, Line{Index: idx, Line: ln})
}

func emitJump(fn *Function, op int32) int32 {
	fn.Bytecode = append(fn.Bytecode, op, 0) // opcode + reserved offset
	return int32(len(fn.Bytecode)) - 1
}

func labelJump(fn *Function, pos int32) {
	fn.Bytecode[pos] = int32(len(fn.Bytecode))
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

// emitRunes writes the length-prefixed rune sequence of s directly into the
// bytecode, without the temporary []rune that []rune(s) used to allocate.
func emitRunes(fn *Function, s string) {
	n := utf8.RuneCountInString(s)
	fn.Bytecode = slices.Grow(fn.Bytecode, n+1)
	fn.Bytecode = append(fn.Bytecode, int32(n))
	for _, r := range s {
		fn.Bytecode = append(fn.Bytecode, int32(r))
	}
}

func emitString(fn *Function, value string) {
	emit(fn, OpLoadStr)
	emitRunes(fn, value)
}

func emitBytes(fn *Function, value []int32) {
	fn.Bytecode = slices.Grow(fn.Bytecode, len(value)+1)
	fn.Bytecode = append(fn.Bytecode, int32(len(value)))
	fn.Bytecode = append(fn.Bytecode, value...)
}

func objectKey(ast *Ast) string {
	value := ast.Value
	if ast.Type == AstStrLit && len(value) >= 2 {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
	}
	return value
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
		value, err := strconv.ParseFloat(ast.Value, 64)
		if err != nil {
			ThrowError(fn.File, "invalid number format", ast.Pos)
		}
		return numConst(value)
	case AstStrLit:
		value, err := strconv.Unquote(ast.Value)
		if err != nil {
			ThrowError(fn.File, "invalid string literal", ast.Pos)
		}
		return strConst(value)
	case AstBoolLit:
		return boolConst(ast.Value == KeyTrue)

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
		if !lhs.folded() {
			return noConst
		}
		rhs := tryFold(fn, ast.B)
		if !rhs.folded() {
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
		if !lhs.folded() {
			return noConst
		}
		rhs := tryFold(fn, ast.B)
		if !rhs.folded() {
			return noConst
		}
		comparison := 0
		if lhs.isNumber() && rhs.isNumber() {
			if lhs.num < rhs.num {
				comparison = -1
			} else if lhs.num > rhs.num {
				comparison = 1
			}
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
		if !lhs.folded() {
			return noConst
		}
		rhs := tryFold(fn, ast.B)
		if !rhs.folded() {
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

func visitExpression(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	// Literals and operators on literals are folded here. Literal node types
	// (number / string / bool) therefore never reach the switch below.
	if c := tryFold(fn, ast); c.folded() {
		if debugFold {
			fmt.Printf("result: %+v\n", c)
		}
		emitConst(fn, c)
		return
	}

	switch ast.Type {
	case AstIdentifier:
		{
			name := ast.Value
			if !scope.Exists(name) {
				ThrowError(fn.File, "undefined variable", ast.Pos)
			}

			isLocal := scope.ExistsFunction(name)
			sym, exists := scope.GetVariable(name)

			if exists && !isLocal {
				// Capture
				offset := scope.GetVarCount()

				scope.AddLocal(name, Symbol{
					Name:    name,
					Offset:  offset,
					Const:   sym.Const,
					Capture: true,
					Pos:     ast.Pos,
				})

				emitInt(fn, OpLoadCap, sym.Offset)
				emitInt(fn, OpStoreCap, offset)
				emit(fn, sym.Offset)
			} else {
				emitInt(fn, OpLoadVar, sym.Offset)
			}
		}
	case AstNullLit:
		{
			emit(fn, OpLoadNull)
		}
	case AstArray:
		{
			emitInt(fn, OpMakeArray, 0)
			size := int32(0)

			for head := ast.A; head != nil; head = head.Next {
				if head.Type == AstSpread {
					if size > 0 {
						emitInt(fn, OpMakeArray, size)
						emit(fn, OpExtendArray)
						size = 0
					}

					visitExpression(p, scope, fn, head.A)
					emit(fn, OpExtendArray)
					continue
				}

				visitExpression(p, scope, fn, head)
				size++
			}

			if size > 0 {
				emitInt(fn, OpMakeArray, size)
				emit(fn, OpExtendArray)
			}
		}
	case AstObject:
		{
			emitInt(fn, OpMakeObject, 0)
			size := int32(0)

			for head := ast.A; head != nil; head = head.Next {
				if head.Type == AstSpread {
					if size > 0 {
						emitInt(fn, OpMakeObject, size)
						emit(fn, OpExtendObject)
						size = 0
					}

					visitExpression(p, scope, fn, head.A)
					emit(fn, OpExtendObject)
					continue
				}

				if head.A.Type == AstIdentifier || head.A.Type == AstStrLit {
					emitString(fn, objectKey(head.A))
				} else {
					visitExpression(p, scope, fn, head.A)
				}
				visitExpression(p, scope, fn, head.B)
				size++
			}

			if size > 0 {
				emitInt(fn, OpMakeObject, size)
				emit(fn, OpExtendObject)
			}
		}
	case AstFunctionExpression, AstAsyncFunctionExpression:
		{
			parmNode := ast.A
			bodyNode := ast.B

			function := newFunction(fn.File, "<anon/>")
			funcOffset := p.state.SaveFunction(function)

			localScope := acquireScope(scope)
			localScope.Type = ScopeTypeFunction

			var paramBuf [8]int32
			reverse := paramBuf[:0]

			vaIndex := -1
			for ; parmNode != nil; parmNode = parmNode.Next {

				if parmNode.Type == AstSpread {
					parmNode = parmNode.A
					vaIndex = len(reverse)
				}

				paramName := parmNode.Value

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

				reverse = append(reverse, offset)
			}

			function.Async = ast.Type == AstAsyncFunctionExpression
			function.VarArgIndex = int32(vaIndex)
			function.ParamCount = int32(len(reverse))

			for i := len(reverse) - 1; i >= 0; i-- {
				emitInt(function, OpStoreLocal, reverse[i])
			}

			visibleReturn := false
			for ; bodyNode != nil; bodyNode = bodyNode.Next {
				if bodyNode.Type == AstReturnStmt {
					visibleReturn = true
				}
				visitDeclarationOrStatement(p, localScope, function, bodyNode)
			}

			if !visibleReturn {
				emit(function, OpLoadNull)
				emit(function, OpReturn)
			}

			function.LocalCount = localScope.VarCount
			finalizeFunction(function) // actually drop the unused capacity
			releaseScope(localScope)

			emitInt(fn, OpLoadFunction, funcOffset)
		}
	case AstMemberAccess:
		{
			visitExpression(p, scope, fn, ast.A)
			emit(fn, OpGetAttr)
			emitRunes(fn, ast.B.Value)
		}
	case AstIndexAccess:
		{
			visitExpression(p, scope, fn, ast.A)
			visitExpression(p, scope, fn, ast.B)
			emit(fn, OpGetIndex)
		}
	case AstCall:
		{
			argCount := int32(0)

			for arg := ast.B; arg != nil; arg = arg.Next {
				visitExpression(p, scope, fn, arg)
				argCount++
			}

			switch ast.A.Type {
			case AstMemberAccess:
				{
					obj := ast.A.A
					mem := ast.A.B
					visitExpression(p, scope, fn, obj)
					emitInt(fn, OpCall, argCount)
					emitRunes(fn, mem.Value)
				}
			default:
				{
					visitExpression(p, scope, fn, ast.A)
					emitInt(fn, OpCall, argCount)
				}
			}
		}
	case AstAwait:
		{
			if !fn.Async {
				ThrowError(fn.File, "await is only allowed inside async functions", ast.Pos)
			}
			visitExpression(p, scope, fn, ast.A)
			emit(fn, OpAwait)
		}
	case AstPos, AstNeg, AstNot, AstBitNot:
		{
			visitExpression(p, scope, fn, ast.A)
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
		}
	case AstMul, AstDiv, AstMod, AstAdd, AstSub,
		AstShiftLeft, AstShiftRight,
		AstLess, AstLessEqual, AstGreater, AstGreaterEqual,
		AstEqual, AstNotEqual,
		AstBitAnd, AstBitXor, AstBitOr:
		{
			visitExpression(p, scope, fn, ast.A)
			visitExpression(p, scope, fn, ast.B)
			emit(fn, binaryOpcode(ast.Type))
		}
	case AstAnd, AstOr:
		{
			visitExpression(p, scope, fn, ast.A)
			var jumpPos int32
			if ast.Type == AstAnd {
				jumpPos = emitJump(fn, OpJmpIfFalseOrPop)
			} else {
				jumpPos = emitJump(fn, OpJmpIfTrueOrPop)
			}
			visitExpression(p, scope, fn, ast.B)
			labelJump(fn, jumpPos)
		}
	default:
		{
			unexpected("visitExpression", ast.Type)
		}
	}
}

// checkDeclKind validates var/let placement. Shared by all declaration visitors.
func checkDeclKind(fn *Function, scope *Scope, ast *Ast) {
	isGlobal := scope.Type == ScopeTypeGlobal
	switch {
	case ast.Type == AstVar && !isGlobal:
		ThrowError(fn.File, "var declarations are not allowed in non-global scope", ast.Pos)
	case ast.Type == AstLet && isGlobal:
		ThrowError(fn.File, "let declarations are not allowed in global scope", ast.Pos)
	}
}

// declareLocal registers a new local and emits the store for it.
func declareLocal(fn *Function, scope *Scope, name string, constant bool, pos Position) {
	if scope.ExistsLocal(name) {
		ThrowError(fn.File, "variable is already defined in scope", pos)
	}

	offset := scope.GetVarCount()
	scope.AddLocal(name, Symbol{
		Name:    name,
		Offset:  offset,
		Const:   constant,
		Capture: false,
		Pos:     pos,
	})

	emitInt(fn, OpStoreLocal, offset)
}

func visitVarDeclaration(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	checkDeclKind(fn, scope, ast)
	constant := ast.Type == AstConst

	for head := ast.A; head != nil; head = head.Next {
		varNode := head.A
		valNode := head.B

		if valNode != nil {
			visitExpression(p, scope, fn, valNode)
		} else {
			emit(fn, OpLoadNull)
		}

		declareLocal(fn, scope, varNode.Value, constant, varNode.Pos)
	}
}

func visitVarArrayDestructDeclaration(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	checkDeclKind(fn, scope, ast)
	constant := ast.Type == AstConst

	visitExpression(p, scope, fn, ast.B)

	size := int32(0)
	for head := ast.A; head != nil; head = head.Next {
		size++
	}

	emitInt(fn, OpDestructArray, size)

	for head := ast.A; head != nil; head = head.Next {
		declareLocal(fn, scope, head.Value, constant, head.Pos)
	}
}

func visitVarObjectDestructDeclaration(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	checkDeclKind(fn, scope, ast)
	constant := ast.Type == AstConst

	visitExpression(p, scope, fn, ast.B)

	for head := ast.A; head != nil; head = head.Next {
		name := "[INVALID]"

		emit(fn, OpDupTop)

		switch head.Type {
		case AstIdentifier:
			name = head.Value
			emit(fn, OpGetAttr)
			emitRunes(fn, name)
		case AstKeyValue:
			name = head.B.Value
			emit(fn, OpGetAttr)
			emitRunes(fn, objectKey(head.A))
		}

		declareLocal(fn, scope, name, constant, head.Pos)
	}

	emit(fn, OpPopTop)
}

func visitFunctionDeclaration(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	nameNode := ast.A
	parmNode := ast.B.A
	bodyNode := ast.B.B

	function := newFunction(fn.File, nameNode.Value)
	funcOffset := p.state.SaveFunction(function)

	localScope := acquireScope(scope)
	localScope.Type = ScopeTypeFunction

	var paramBuf [8]int32
	reverse := paramBuf[:0]

	vaIndex := -1
	for ; parmNode != nil; parmNode = parmNode.Next {

		if parmNode.Type == AstSpread {
			parmNode = parmNode.A
			vaIndex = len(reverse)
		}

		paramName := parmNode.Value

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

		reverse = append(reverse, offset)
	}

	function.Async = ast.Type == AstAsyncFunctionExpression
	function.VarArgIndex = int32(vaIndex)
	function.ParamCount = int32(len(reverse))

	for i := len(reverse) - 1; i >= 0; i-- {
		emitInt(function, OpStoreLocal, reverse[i])
	}

	visibleReturn := false
	for ; bodyNode != nil; bodyNode = bodyNode.Next {
		if bodyNode.Type == AstReturnStmt {
			visibleReturn = true
		}
		visitDeclarationOrStatement(p, localScope, function, bodyNode)
	}

	if !visibleReturn {
		emit(function, OpLoadNull)
		emit(function, OpReturn)
	}

	sym, exists := scope.GetVariable(nameNode.Value)
	if !exists {
		ThrowError(fn.File, "function does not exists", ast.Pos)
	}

	function.LocalCount = localScope.VarCount
	finalizeFunction(function) // actually drop the unused capacity
	releaseScope(localScope)

	emitInt(fn, OpLoadFunction, funcOffset)
	emitInt(fn, OpStoreLocal, sym.Offset)
}

func visitIfStatement(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	condNode := ast.A
	thenNode := ast.B
	elseNode := ast.C

	visitExpression(p, scope, fn, condNode)

	jmpIfFalsePos := emitJump(fn, OpPopJmpIfFalse)
	visitDeclarationOrStatement(p, scope, fn, thenNode)
	endIf := emitJump(fn, OpJmp)

	labelJump(fn, jmpIfFalsePos)
	if elseNode != nil {
		visitDeclarationOrStatement(p, scope, fn, elseNode)
	}
	labelJump(fn, endIf)
}

func visitWhileStatement(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	condNode := ast.A
	bodyNode := ast.B

	loopStart := int32(len(fn.Bytecode))

	loopScope := acquireScope(scope)
	loopScope.Type = ScopeTypeLoop
	loopScope.LoopStart = loopStart

	visitExpression(p, scope, fn, condNode)
	jmpIfFalsePos := emitJump(fn, OpPopJmpIfFalse)

	visitDeclarationOrStatement(p, loopScope, fn, bodyNode)
	emitInt(fn, OpAbsJmp, loopStart)

	labelJump(fn, jmpIfFalsePos)

	// Break
	for _, jmpSlot := range loopScope.BreakStart {
		labelJump(fn, jmpSlot)
	}

	releaseScope(loopScope)
}

func visitBlock(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	blockScope := acquireScope(scope)
	blockScope.Type = ScopeTypeBlock

	for statement := ast.A; statement != nil; statement = statement.Next {
		visitDeclarationOrStatement(p, blockScope, fn, statement)
	}

	releaseScope(blockScope)
}

func visitBreak(_ *Parser, scope *Scope, fn *Function, ast *Ast) {
	loopScope := scope.GetNearest(ScopeTypeLoop)
	if loopScope == nil {
		ThrowError(fn.File, "break statement not within a loop", ast.Pos)
	}
	jmpSlot := emitJump(fn, OpJmp)
	loopScope.BreakStart = append(loopScope.BreakStart, jmpSlot)
}

func visitContinue(_ *Parser, scope *Scope, fn *Function, ast *Ast) {
	loopScope := scope.GetNearest(ScopeTypeLoop)
	if loopScope == nil {
		ThrowError(fn.File, "continue statement not within a loop", ast.Pos)
	}
	emitInt(fn, OpAbsJmp, loopScope.LoopStart)
}

func visitReturn(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	if ast.A != nil {
		visitExpression(p, scope, fn, ast.A)
	} else {
		emit(fn, OpLoadNull)
	}
	emit(fn, OpReturn)
}

func visitTryCatch(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	if ast.B == nil {
		for statement := ast.A; statement != nil; statement = statement.Next {
			visitDeclarationOrStatement(p, scope, fn, statement)
		}
		return
	}

	// Index (not pointer) into the table: nested try/catch may grow the slice.
	tableIndex := len(fn.TryCatchTable)
	fn.TryCatchTable = append(fn.TryCatchTable, TryCatchTable{CatchLocal: -1})
	fn.TryCatchTable[tableIndex].TryStart = int32(len(fn.Bytecode))

	tryScope := acquireScope(scope)
	tryScope.Type = ScopeTypeBlock
	for statement := ast.A; statement != nil; statement = statement.Next {
		visitDeclarationOrStatement(p, tryScope, fn, statement)
	}
	releaseScope(tryScope)

	fn.TryCatchTable[tableIndex].TryEnd = int32(len(fn.Bytecode))
	endJump := emitJump(fn, OpJmp)
	fn.TryCatchTable[tableIndex].CatchStart = int32(len(fn.Bytecode))

	catchScope := acquireScope(scope)
	catchScope.Type = ScopeTypeBlock
	catchScope.VarCount = 1

	catchVarName := ast.B.Value
	offset := catchScope.GetVarCount()

	catchScope.AddLocal(catchVarName, Symbol{
		Name:   catchVarName,
		Offset: offset,
		Pos:    ast.B.Pos,
	})

	fn.TryCatchTable[tableIndex].CatchLocal = offset

	for statement := ast.C; statement != nil; statement = statement.Next {
		visitDeclarationOrStatement(p, catchScope, fn, statement)
	}
	releaseScope(catchScope)

	fn.TryCatchTable[tableIndex].CatchEnd = int32(len(fn.Bytecode))
	labelJump(fn, endJump)
}

func visitDeclarationOrStatement(p *Parser, scope *Scope, fn *Function, ast *Ast) {
	emitLine(fn, ast.Pos)
	switch ast.Type {
	case AstVar, AstLet, AstConst:
		visitVarDeclaration(p, scope, fn, ast)
	case AstDestructuringArrayVar, AstDestructuringArrayLet, AstDestructuringArrayConst:
		visitVarArrayDestructDeclaration(p, scope, fn, ast)
	case AstDestructuringObjectVar, AstDestructuringObjectLet, AstDestructuringObjectConst:
		visitVarObjectDestructDeclaration(p, scope, fn, ast)
	case AstFunction:
		visitFunctionDeclaration(p, scope, fn, ast)
	case AstIfStmt:
		visitIfStatement(p, scope, fn, ast)
	case AstWhileLoop:
		visitWhileStatement(p, scope, fn, ast)
	case AstBlock:
		visitBlock(p, scope, fn, ast)
	case AstLogStmt:
		visitExpression(p, scope, fn, ast.A)
		emit(fn, OpStdWrite)
	case AstExpressionStmnt:
		visitExpression(p, scope, fn, ast.A)
		emit(fn, OpPopTop)
	case AstBreakStmt:
		visitBreak(p, scope, fn, ast)
	case AstContinueStmt:
		visitContinue(p, scope, fn, ast)
	case AstReturnStmt:
		visitReturn(p, scope, fn, ast)
	case AstTryCatch:
		visitTryCatch(p, scope, fn, ast)
	default:
		unexpected("visitDeclarationOrStatement", ast.Type)
	}
}

func compileProgram(p *Parser) *Function {
	defer releaseAstSlab()

	ast := p.Parse()
	assert(AstProgram, ast)

	program := newFunction(p.File, "$main")

	scope := acquireScope(nil)
	scope.Type = ScopeTypeGlobal

	// forward function declarations
	for current := ast.A; current != nil; current = current.Next {
		if current.Type == AstFunction {
			nameNode := current.A
			funcName := nameNode.Value

			if scope.ExistsLocal(funcName) {
				ThrowError(p.File, "variable is already defined in scope", nameNode.Pos)
			}

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
		visitDeclarationOrStatement(p, scope, program, current)
	}

	emit(program, OpLoadNull)
	emit(program, OpReturn)

	program.LocalCount = scope.VarCount
	finalizeFunction(program)
	releaseScope(scope)

	return program
}

func Compile(p *Parser) *Function {
	return compileProgram(p)
}
