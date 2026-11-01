package main

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	kNull
	kArray
	kObject
)

var noConst constVal

const (
	initialBytecodeCap = 256
	initialLinesCap    = 64
	initialTryCap      = 4
	maxPooledScopeVars = 64
	maxPooledBreaks    = 64
	// Upper bounds for scratch buffers kept in the pool, so one huge
	// function does not pin a large buffer forever.
	maxPooledBytecode = 1 << 16
	maxPooledLines    = 1 << 12
	maxPooledTry      = 1 << 8
)

// Scopes are recycled through a pool: steady-state cost is zero allocations
// per block / loop / function.
var scopePool sync.Pool

// codeBuf is scratch space used while a function is being compiled. Functions
// are compiled into pooled, generously sized buffers and then copied ONCE into
// an exactly sized slice.
// Nested functions are compiled while the parent is still in progress, so each
// in-flight function owns its own codeBuf.
type codeBuf struct {
	// bc is pooled instruction capacity used during compilation before exact-size finalization.
	bc []int32
	// lines accumulates source mappings alongside bytecode so runtime diagnostics match emitted offsets.
	lines []Line
	// try accumulates exception-handler ranges whose offsets are patched during code generation.
	try []TryCatchTable
}

var codeBufPool sync.Pool

type constKind uint8

// constVal is returned by value, so folding never allocates
// (except for string concatenation, which has to).
// Fields are ordered so kind+b share one word: 32 bytes instead of 48.
type constVal struct {
	// num stores a folded numeric literal without allocating an OSValue.
	num float64
	// str stores a folded string result; concatenation is the only fold that may allocate.
	str string
	// kind tells constant-folding helpers which payload is valid and whether folding succeeded.
	kind constKind
	// b stores a folded boolean while keeping the compact return-by-value representation.
	b bool
	// values stores immutable compile-time array elements for constant-pool entries.
	values []constVal
	// keys stores immutable object keys aligned with values for constant-pool entries.
	keys []string
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

/******************* Pooling ***/

// acquireScope returns a clean scope. The caller sets Type (and any other
// fields) afterwards. VarCount starts at 0.
// Pooled scopes are always cleared by releaseScope, so Variables is empty here.
func acquireScope(parent *Scope) *Scope {
	s, _ := scopePool.Get().(*Scope)
	if s == nil {
		s = &Scope{Variables: make(map[string]Symbol, 8)}
	}
	vars, breaks, continues := s.Variables, s.BreakStart[:0], s.ContinueStart[:0]
	*s = Scope{Parent: parent, Variables: vars, BreakStart: breaks, ContinueStart: continues}
	if parent == nil {
		s.Frame = s
	} else {
		s.Frame = parent.Frame
	}
	return s
}

// releaseScope drops every reference the scope holds (symbols, parent) so a
// pooled scope never keeps compile-time data alive. The size check must run
// BEFORE clear(), otherwise len is always 0 and oversized scopes get pooled.
func releaseScope(s *Scope) {
	oversized := len(s.Variables) > maxPooledScopeVars || cap(s.BreakStart) > maxPooledBreaks || cap(s.ContinueStart) > maxPooledBreaks
	clear(s.Variables)
	s.Parent = nil
	s.Frame = nil
	if oversized {
		return
	}
	scopePool.Put(s)
}

func acquireCodeBuf() *codeBuf {
	if b, _ := codeBufPool.Get().(*codeBuf); b != nil {
		return b
	}
	return &codeBuf{
		bc:    make([]int32, 0, initialBytecodeCap),
		lines: make([]Line, 0, initialLinesCap),
		try:   make([]TryCatchTable, 0, initialTryCap),
	}
}

func visitClassExpression(s *State, scope *Scope, fn *Function, ast *Ast, name string) {
	emitString(fn, name)
	if ast.A == nil {
		emit(fn, OpLoadNull)
	} else {
		visitExpression(s, scope, fn, ast.A)
	}

	methodCount := 0
	constructorFound := false
	for method := ast.B; method != nil; method = method.Next {
		methodName := objectKey(method.A)
		if methodName == "constructor" {
			if method.B.Type == AstAsyncFunctionExpression {
				ThrowError(fn.File, "class constructors cannot be async", method.Pos)
			}
			if constructorFound {
				ThrowError(fn.File, "duplicate class constructor", method.Pos)
			}
			constructorFound = true
		}
		emitString(fn, methodName)
		if method.B.Type == AstFunctionExpression || method.B.Type == AstAsyncFunctionExpression {
			funcOffset := compileFunction(s, scope, fn, name+"."+methodName,
				method.B.Type == AstAsyncFunctionExpression, true, false, methodName == "constructor", method.B.A, method.B.B)
			emitInt(fn, OpLoadFunction, funcOffset)
		} else {
			if methodName == "constructor" {
				ThrowError(fn.File, "class constructor must be a function", method.Pos)
			}
			visitExpression(s, scope, fn, method.B)
		}
		methodCount++
	}
	if !constructorFound {
		funcOffset := compileFunction(s, scope, fn, name+".constructor", false, true, false, true, nil, nil)
		emitString(fn, "constructor")
		emitInt(fn, OpLoadFunction, funcOffset)
		methodCount++
	}

	emitInt(fn, OpMakeClass, methodCount)
}

/******************* Emit helpers ***/

// newFunction creates a function whose code slices are backed by buf.
func newFunction(file *File, name string, buf *codeBuf) *Function {
	fn := NewFunction(file, name, false)
	// append(buf[:0], existing...) keeps anything NewFunction may have preset.
	fn.Bytecode = append(buf.bc[:0], fn.Bytecode...)
	fn.Lines = append(buf.lines[:0], fn.Lines...)
	fn.TryCatchTable = append(buf.try[:0], fn.TryCatchTable...)
	return fn
}

// detach copies s into an exactly sized slice (one allocation, no slack) and
// returns the original backing array, emptied, for reuse (nil if too large).
func detach[T any](s []T, maxKeep int) (exact, scratch []T) {
	if len(s) > 0 {
		exact = make([]T, len(s))
		copy(exact, s)
	}
	if cap(s) <= maxKeep {
		scratch = s[:0]
	}
	return
}

// finalizeFunction moves the compiled code into exactly sized slices and
// returns the scratch buffers to the pool. fn must not be appended to
// afterwards.
func finalizeFunction(fn *Function, buf *codeBuf) {
	fn.Bytecode, buf.bc = detach(fn.Bytecode, maxPooledBytecode)
	fn.Lines, buf.lines = detach(fn.Lines, maxPooledLines)
	fn.TryCatchTable, buf.try = detach(fn.TryCatchTable, maxPooledTry)

	// A dropped (oversized) scratch slice must be replaced to keep the buffer usable.
	if buf.bc == nil {
		buf.bc = make([]int32, 0, initialBytecodeCap)
	}
	if buf.lines == nil {
		buf.lines = make([]Line, 0, initialLinesCap)
	}
	if buf.try == nil {
		buf.try = make([]TryCatchTable, 0, initialTryCap)
	}
	codeBufPool.Put(buf)
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
	idx := len(fn.Bytecode)
	ln := line.Line
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

func emitJump(fn *Function, op int32) int {
	fn.Bytecode = append(fn.Bytecode, op, 0) // opcode + reserved offset
	return len(fn.Bytecode) - 1
}

func labelJump(fn *Function, pos int) {
	fn.Bytecode[pos] = int32(len(fn.Bytecode))
}

func emit(fn *Function, op int32) {
	fn.Bytecode = append(fn.Bytecode, op)
}

func emitInt(fn *Function, op int32, value int) {
	fn.Bytecode = append(fn.Bytecode, op, int32(value))
}

func emitFloat(fn *Function, value float64) {
	whole, decimal := SliceFloat(value)
	fn.Bytecode = append(fn.Bytecode, OpLoadNum, whole, decimal)
}

func emitBigInt(fn *Function, value string) {
	emit(fn, OpLoadBigInt)
	emitRunes(fn, value)
}

// emitRunes writes the length-prefixed rune sequence of s directly into the
// bytecode, without a temporary []rune.
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
	case kNull:
		emit(fn, OpLoadNull)
	default:
		panic("unsupported type")
	}
}

func emitConstPool(fn *Function, value constVal) {
	index := len(fn.Constants)
	fn.Constants = append(fn.Constants, value)
	emitInt(fn, OpLoadConst, index)
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
	}
	panic("invalid binary operator")
}

func compoundAssignOpcode(astType AstType) int32 {
	switch astType {
	case AstMulAssign:
		return OpBinMul
	case AstDivAssign:
		return OpBinDiv
	case AstModAssign:
		return OpBinMod
	case AstAddAssign:
		return OpBinAdd
	case AstSubAssign:
		return OpBinSub
	case AstShiftLeftAssign:
		return OpBinShiftLeft
	case AstShiftRightAssign:
		return OpBinShiftRight
	case AstAndAssign:
		return OpBinBitAnd
	case AstXorAssign:
		return OpBinBitXor
	case AstOrAssign:
		return OpBinBitOr
	}
	unexpected("invalid assignment operator", astType)
	return 0
}

/******************* Constant folding ***/

// mayFold reports whether tryFold can ever produce a constant for this node
// type. Every other node type (identifiers, calls, member access, ...) would
// fall through tryFold's switch and return noConst, so visitExpression skips
// the call entirely for them. This is purely a fast path: results and
// diagnostics are identical.
func mayFold(t AstType) bool {
	switch t {
	case AstNumLit, AstStrLit, AstBoolLit,
		AstPos, AstNeg, AstNot, AstBitNot,
		AstMul, AstDiv, AstMod, AstAdd, AstSub,
		AstLess, AstLessEqual, AstGreater, AstGreaterEqual, AstEqual, AstNotEqual,
		AstBitAnd, AstBitXor, AstBitOr, AstShiftLeft, AstShiftRight,
		AstAnd, AstOr:
		return true
	}
	return false
}

// tryFold returns a constant if the expression can be evaluated at compile time.
// Check result.folded() to know whether it succeeded.
func tryFold(fn *Function, ast *Ast) constVal {
	switch ast.Type {
	case AstNumLit:
		if v := ast.Value; v != "" && (v[len(v)-1] == 'n' || v[len(v)-1] == 'N') {
			return noConst
		}
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
	case AstArray:
		values := make([]constVal, 0)
		for element := ast.A; element != nil; element = element.Next {
			if element.Type == AstSpread {
				return noConst
			}
			value := tryFoldArrayElement(fn, element)
			if !value.folded() {
				return noConst
			}
			values = append(values, value)
		}
		return constVal{kind: kArray, values: values}
	case AstObject:
		keys := make([]string, 0)
		values := make([]constVal, 0)
		for property := ast.A; property != nil; property = property.Next {
			if property.Type != AstKeyValue || property.A.Type != AstIdentifier && property.A.Type != AstStrLit {
				return noConst
			}
			value := tryFoldArrayElement(fn, property.B)
			if !value.folded() {
				return noConst
			}
			keys = append(keys, objectKey(property.A))
			values = append(values, value)
		}
		return constVal{kind: kObject, keys: keys, values: values}
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

func tryFoldArrayElement(fn *Function, ast *Ast) constVal {
	if ast.Type == AstNullLit {
		return constVal{kind: kNull}
	}
	return tryFold(fn, ast)
}

/******************* Variable resolution ***/

// resolveVar finds the variable named by an identifier node, registers a
// capture if it lives in an enclosing function, and returns the local offset
// to use in this function together with the variable's constness. It emits no
// code. Returning the constness lets callers skip a second scope-chain walk.
func resolveVar(scope *Scope, fn *Function, ast *Ast) (offset int, isConst bool) {
	name := ast.Value
	if !scope.Exists(name) {
		ThrowError(fn.File, "undefined variable", ast.Pos)
	}

	isLocalSymb := scope.ExistsFunction(name)
	sym, exists := scope.GetVariable(name)

	if exists && !isLocalSymb {
		// Capture
		offset = scope.GetVarCount()

		scope.AddLocal(name, Symbol{
			Name:    name,
			Offset:  offset,
			Const:   sym.Const,
			Capture: true,
			Pos:     ast.Pos,
		})

		fn.CaptureMeta = append(fn.CaptureMeta, CaptureMeta{
			Name: name,
			Cell: nil,
			Src:  sym.Offset,
			Dst:  offset,
		})
		return offset, sym.Const
	}
	return sym.Offset, sym.Const
}

/******************* Visitors ***/

// compileFunction compiles a function (declaration or expression) into a new
// Function saved in the parser state and returns its offset. Shared by both
// visitors, which used to carry identical copies of this code.
func compileFunction(s *State, scope *Scope, fn *Function, name string, async bool, implicitThis, staticThis, classConstructor bool, parmNode, bodyNode *Ast) int {
	buf := acquireCodeBuf()
	function := newFunction(fn.File, name, buf)
	funcOffset := s.SaveFunction(function)

	localScope := acquireScope(scope)
	localScope.Type = ScopeTypeFunction
	localScope.Frame = localScope
	staticThisOffset := -1
	if staticThis {
		if localScope.ExistsLocal(KeyThis) {
			ThrowError(fn.File, "this is reserved in class methods", parmNode.Pos)
		}
		staticThisOffset = localScope.GetVarCount()
		localScope.AddLocal(KeyThis, Symbol{Name: KeyThis, Offset: staticThisOffset})
	}

	// Stays on the stack for up to 8 parameters; only larger lists spill.
	var paramBuf [8]int
	reverse := paramBuf[:0]
	if implicitThis {
		if localScope.ExistsLocal(KeyThis) {
			ThrowError(fn.File, "this is reserved in class methods", parmNode.Pos)
		}
		localScope.AddLocal(KeyThis, Symbol{Name: KeyThis, Offset: 0})
		reverse = append(reverse, 0)
	}
	if classConstructor {
		superOffset := localScope.GetVarCount()
		localScope.AddLocal(KeySuper, Symbol{Name: KeySuper, Offset: superOffset})
		reverse = append(reverse, superOffset)
		function.ClassConstructor = true
		function.ClassThisOffset = 0
		function.ClassSuperOffset = superOffset
	}

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

	function.Async = async
	function.VarArgIndex = vaIndex
	function.ParamCount = len(reverse)

	for i := len(reverse) - 1; i >= 0; i-- {
		emitInt(function, OpStoreLocal, reverse[i])
	}
	if staticThis {
		emit(function, OpLoadNull)
		emitInt(function, OpStoreLocal, staticThisOffset)
	}

	visibleReturn := false
	for ; bodyNode != nil; bodyNode = bodyNode.Next {
		if bodyNode.Type == AstReturnStmt {
			visibleReturn = true
		}
		visitDeclarationOrStatement(s, localScope, function, bodyNode)
	}

	if !visibleReturn {
		emit(function, OpLoadNull)
		emit(function, OpReturn)
	}

	function.LocalCount = localScope.VarCount
	finalizeFunction(function, buf)
	releaseScope(localScope)
	return funcOffset
}

// Array / object / switch / bigint live in their own functions so their locals
// do not inflate the stack frame of the deeply recursive visitExpression.

func visitArrayLiteral(s *State, scope *Scope, fn *Function, ast *Ast) {
	count, hasSpread := 0, false
	for head := ast.A; head != nil; head = head.Next {
		if head.Type == AstSpread {
			hasSpread = true
		} else {
			count++
		}
	}

	if count > StackSize {
		if !hasSpread {
			if value := tryFold(fn, ast); value.kind == kArray {
				emitConstPool(fn, value)
				return
			}
		}
		ThrowError(fn.File, "array literal too large; elements must be compile-time constants", ast.Pos)
	}

	// Fast path: no spread, so push the items and build the array in one op
	// instead of MakeArray 0 + MakeArray n + ExtendArray.
	if !hasSpread {
		for head := ast.A; head != nil; head = head.Next {
			visitExpression(s, scope, fn, head)
		}
		emitInt(fn, OpMakeArray, count)
		return
	}

	emitInt(fn, OpMakeArray, 0)
	size := 0

	for head := ast.A; head != nil; head = head.Next {
		if head.Type == AstSpread {
			if size > 0 {
				emitInt(fn, OpMakeArray, size)
				emit(fn, OpExtendArray)
				size = 0
			}

			visitExpression(s, scope, fn, head.A)
			emit(fn, OpExtendArray)
			continue
		}

		visitExpression(s, scope, fn, head)
		size++
	}

	if size > 0 {
		emitInt(fn, OpMakeArray, size)
		emit(fn, OpExtendArray)
	}
}

func emitObjectKey(s *State, scope *Scope, fn *Function, key *Ast) {
	if key.Type == AstIdentifier || key.Type == AstStrLit {
		emitString(fn, objectKey(key))
	} else {
		visitExpression(s, scope, fn, key)
	}
}

func visitObjectLiteral(s *State, scope *Scope, fn *Function, ast *Ast) {
	count, hasSpread := 0, false
	for head := ast.A; head != nil; head = head.Next {
		if head.Type == AstSpread {
			hasSpread = true
		} else {
			count++
		}
	}

	if count > StackSize/2 {
		if !hasSpread {
			if value := tryFold(fn, ast); value.kind == kObject {
				emitConstPool(fn, value)
				return
			}
		}
		ThrowError(fn.File, "object literal too large; keys and values must be compile-time constants", ast.Pos)
	}

	// Fast path: no spread, build the object in one op.
	if !hasSpread {
		for head := ast.A; head != nil; head = head.Next {
			emitObjectKey(s, scope, fn, head.A)
			visitExpression(s, scope, fn, head.B)
		}
		emitInt(fn, OpMakeObject, count)
		return
	}

	emitInt(fn, OpMakeObject, 0)
	size := 0

	for head := ast.A; head != nil; head = head.Next {
		if head.Type == AstSpread {
			if size > 0 {
				emitInt(fn, OpMakeObject, size)
				emit(fn, OpExtendObject)
				size = 0
			}

			visitExpression(s, scope, fn, head.A)
			emit(fn, OpExtendObject)
			continue
		}

		emitObjectKey(s, scope, fn, head.A)
		visitExpression(s, scope, fn, head.B)
		size++
	}

	if size > 0 {
		emitInt(fn, OpMakeObject, size)
		emit(fn, OpExtendObject)
	}
}

// visitBigIntLiteral handles numeric literals with an n/N suffix. Kept out of
// visitExpression because big.Int and the string temporaries are rarely used.
func visitBigIntLiteral(fn *Function, ast *Ast) {
	literal := ast.Value[:len(ast.Value)-1]
	literal = strings.ReplaceAll(literal, "_", "")
	base := 10
	if strings.HasPrefix(literal, "0x") || strings.HasPrefix(literal, "0X") ||
		strings.HasPrefix(literal, "0o") || strings.HasPrefix(literal, "0O") ||
		strings.HasPrefix(literal, "0b") || strings.HasPrefix(literal, "0B") {
		base = 0
	}
	value, ok := new(big.Int).SetString(literal, base)
	if !ok {
		ThrowError(fn.File, "invalid BigInt literal", ast.Pos)
	}
	emitBigInt(fn, value.String())
}

func visitSwitchExpr(s *State, scope *Scope, fn *Function, ast *Ast) {
	visitExpression(s, scope, fn, ast.A)

	// Stack buffers: typical switches fit without touching the heap.
	var endBuf [8]int
	var defBuf [2]*Ast
	var exprBuf [8]int
	jumpEnds := endBuf[:0]
	defaults := defBuf[:0]

	for head := ast.B; head != nil; head = head.Next {
		if head.Type != AstKeyValue {
			// Default
			defaults = append(defaults, head)
			continue
		}

		// Consumed (labelled) before the next case, so one buffer is reused.
		jumpExpr := exprBuf[:0]
		for condition := head.A; condition != nil; condition = condition.Next {
			visitExpression(s, scope, fn, condition)
			emit(fn, OpCaseEqual)
			jumpExpr = append(jumpExpr, emitJump(fn, OpPopJmpIfTrue))
		}

		// If none of the conditions matched, go to the next case
		toNextCase := emitJump(fn, OpJmp)

		// Label the jumps to the value
		for _, jump := range jumpExpr {
			labelJump(fn, jump)
		}

		// Jump to value immediately without evaluating other conditions
		emit(fn, OpPopTop)
		visitExpression(s, scope, fn, head.B)

		jumpEnds = append(jumpEnds, emitJump(fn, OpJmp))

		labelJump(fn, toNextCase)
	}

	// Handle default cases
	for _, def := range defaults {
		emit(fn, OpPopTop)
		visitExpression(s, scope, fn, def)
	}

	for _, jump := range jumpEnds {
		labelJump(fn, jump)
	}
}

func visitSwitchStatement(s *State, scope *Scope, fn *Function, ast *Ast) {
	type switchCase struct {
		// node is the source AST case whose expressions and body are compiled in this pass.
		node *Ast
		// matchJumps are conditional branch operands patched to the case body after comparisons are emitted.
		matchJumps []int
		// bodyJump skips this case body when control falls through from an earlier matched body.
		bodyJump int
		// scope owns declarations in this case body so names do not leak into neighboring cases.
		scope *Scope
	}

	switchScope := acquireScope(scope)
	switchScope.Type = ScopeTypeSwitch
	visitExpression(s, switchScope, fn, ast.A)

	// Typical switches fit in the stack-backed array; only larger ones spill.
	var caseBuf [8]switchCase
	var bodyBuf [8]int
	cases := caseBuf[:0]
	defaultIndex := -1
	for node := ast.B; node != nil; node = node.Next {
		current := switchCase{node: node}
		if node.A == nil {
			defaultIndex = len(cases)
			cases = append(cases, current)
			continue
		}

		for condition := node.A; condition != nil; condition = condition.Next {
			visitExpression(s, switchScope, fn, condition)
			emit(fn, OpCaseEqual)
			current.matchJumps = append(current.matchJumps, emitJump(fn, OpPopJmpIfTrue))
		}
		cases = append(cases, current)
	}

	noMatchJump := emitJump(fn, OpJmp)
	for index := range cases {
		current := &cases[index]
		if index == defaultIndex {
			continue
		}
		for _, jump := range current.matchJumps {
			labelJump(fn, jump)
		}
		emit(fn, OpPopTop)
		current.bodyJump = emitJump(fn, OpJmp)
	}

	labelJump(fn, noMatchJump)
	emit(fn, OpPopTop)
	var endJump int
	if defaultIndex >= 0 {
		cases[defaultIndex].bodyJump = emitJump(fn, OpJmp)
	} else {
		endJump = emitJump(fn, OpJmp)
	}

	var caseBodies []int
	if len(cases) <= len(bodyBuf) {
		caseBodies = bodyBuf[:len(cases)]
	} else {
		caseBodies = make([]int, len(cases))
	}
	for index := range cases {
		current := &cases[index]
		labelJump(fn, current.bodyJump)
		caseBodies[index] = len(fn.Bytecode)
		current.scope = acquireScope(switchScope)
		current.scope.Type = ScopeTypeCase
		for statement := current.node.B; statement != nil; statement = statement.Next {
			visitDeclarationOrStatement(s, current.scope, fn, statement)
		}
	}

	if endJump != 0 {
		labelJump(fn, endJump)
	}
	for _, jump := range switchScope.BreakStart {
		labelJump(fn, jump)
	}
	for index := range cases {
		for _, jump := range cases[index].scope.BreakStart {
			labelJump(fn, jump)
		}
	}
	switchEnd := len(fn.Bytecode)
	for index := range cases {
		nextCase := switchEnd
		if index+1 < len(cases) {
			nextCase = caseBodies[index+1]
		}
		for _, jump := range cases[index].scope.ContinueStart {
			fn.Bytecode[jump] = int32(nextCase)
		}
		releaseScope(cases[index].scope)
	}
	releaseScope(switchScope)
}

func visitExpression(s *State, scope *Scope, fn *Function, ast *Ast) {
	// Literals and operators on literals are folded here. Literal node types
	// (number / string / bool) therefore never reach the switch below, except
	// BigInt literals, which tryFold declines. Node types that can never fold
	// skip the call entirely.
	if mayFold(ast.Type) {
		if c := tryFold(fn, ast); c.folded() {
			if debugFold {
				fmt.Printf("result: %+v\n", c)
			}
			emitConst(fn, c)
			return
		}
	}

	switch ast.Type {
	case AstNumLit:
		visitBigIntLiteral(fn, ast)
	case AstIdentifier:
		offset, _ := resolveVar(scope, fn, ast)
		emitInt(fn, OpLoadVar, offset)
	case AstNullLit:
		emit(fn, OpLoadNull)
	case AstArray:
		visitArrayLiteral(s, scope, fn, ast)
	case AstObject:
		visitObjectLiteral(s, scope, fn, ast)
	case AstClassExpression:
		visitClassExpression(s, scope, fn, ast, "")
	case AstFunctionExpression, AstAsyncFunctionExpression:
		funcOffset := compileFunction(s, scope, fn, "<anon/>",
			ast.Type == AstAsyncFunctionExpression, false, false, false, ast.A, ast.B)
		emitInt(fn, OpLoadFunction, funcOffset)
	case AstSwitchExpr:
		visitSwitchExpr(s, scope, fn, ast)
	case AstMemberAccess:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpGetAttr)
		emitRunes(fn, ast.B.Value)
	case AstIndexAccess:
		visitExpression(s, scope, fn, ast.A)
		visitExpression(s, scope, fn, ast.B)
		emit(fn, OpGetIndex)
	case AstCall:
		argCount := 0

		for arg := ast.B; arg != nil; arg = arg.Next {
			visitExpression(s, scope, fn, arg)
			argCount++
		}

		if ast.A.Type == AstIdentifier && ast.A.Value == KeySuper {
			emitInt(fn, OpCallSuper, argCount)
			emitInt(fn, OpCall, argCount+2)
		} else {
			visitExpression(s, scope, fn, ast.A)
			emitInt(fn, OpCall, argCount)
		}
	case AstAwait:
		if !fn.Async {
			ThrowError(fn.File, "await is only allowed inside async functions", ast.Pos)
		}
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpAwait)
	case AstPos:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpUnaryPos)
	case AstNeg:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpUnaryNeg)
	case AstNot:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpUnaryNot)
	case AstBitNot:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpUnaryBitNot)
	case AstInc:
		assign1(s, scope, fn, ast.A)
		emit(fn, OpInc)
		emit(fn, OpDupTop)
		assign2(s, scope, fn, ast.A, false)
	case AstDec:
		assign1(s, scope, fn, ast.A)
		emit(fn, OpDec)
		emit(fn, OpDupTop)
		assign2(s, scope, fn, ast.A, false)
	case AstPostInc:
		assign1(s, scope, fn, ast.A)
		emit(fn, OpPostInc)
		assign2(s, scope, fn, ast.A, false)
	case AstPostDec:
		assign1(s, scope, fn, ast.A)
		emit(fn, OpPostDec)
		assign2(s, scope, fn, ast.A, false)
	case AstMul, AstDiv, AstMod, AstAdd, AstSub,
		AstShiftLeft, AstShiftRight,
		AstLess, AstLessEqual, AstGreater, AstGreaterEqual,
		AstEqual, AstNotEqual,
		AstBitAnd, AstBitXor, AstBitOr:
		visitExpression(s, scope, fn, ast.A)
		visitExpression(s, scope, fn, ast.B)
		emit(fn, binaryOpcode(ast.Type))
	case AstAnd, AstOr:
		visitExpression(s, scope, fn, ast.A)
		var jumpPos int
		if ast.Type == AstAnd {
			jumpPos = emitJump(fn, OpJmpIfFalseOrPop)
		} else {
			jumpPos = emitJump(fn, OpJmpIfTrueOrPop)
		}
		visitExpression(s, scope, fn, ast.B)
		labelJump(fn, jumpPos)
	case AstAssign:
		assignVar(s, scope, fn, ast.A, ast.B)
	case AstMulAssign,
		AstDivAssign,
		AstModAssign,
		AstAddAssign,
		AstSubAssign,
		AstShiftLeftAssign,
		AstShiftRightAssign,
		AstAndAssign,
		AstXorAssign,
		AstOrAssign:
		assign1(s, scope, fn, ast.A)
		visitExpression(s, scope, fn, ast.B)
		emit(fn, compoundAssignOpcode(ast.Type))
		emit(fn, OpDupTop)
		assign2(s, scope, fn, ast.A, false)
	case AstTernaryOperation:
		visitExpression(s, scope, fn, ast.A)
		jumpPos := emitJump(fn, OpPopJmpIfFalse)
		visitExpression(s, scope, fn, ast.B)
		endJump := emitJump(fn, OpJmp)
		labelJump(fn, jumpPos)
		visitExpression(s, scope, fn, ast.C)
		labelJump(fn, endJump)
	default:
		unexpected("visitExpression", ast.Type)
	}
}

func assignVar(s *State, scope *Scope, fn *Function, lhs *Ast, rhs *Ast) {
	switch lhs.Type {
	case AstMemberAccess:
		visitExpression(s, scope, fn, lhs.A)
	case AstIndexAccess:
		visitExpression(s, scope, fn, lhs.A)
		visitExpression(s, scope, fn, lhs.B)
	}
	visitExpression(s, scope, fn, rhs)
	emit(fn, OpDupTop)
	assign2(s, scope, fn, lhs, true)
}

func assign1(s *State, scope *Scope, fn *Function, ast *Ast) {
	switch ast.Type {
	case AstIdentifier:
		offset, _ := resolveVar(scope, fn, ast)
		emitInt(fn, OpLoadVar, offset)
	case AstMemberAccess:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpDupTop)
		emit(fn, OpGetAttr)
		emitRunes(fn, ast.B.Value)
	case AstIndexAccess:
		visitExpression(s, scope, fn, ast.A)
		visitExpression(s, scope, fn, ast.B)
		emit(fn, OpDup2)
		emit(fn, OpGetIndex)
	default:
		ThrowError(fn.File, "invalid left-hand operand", ast.Pos)
	}
}

func assign2(_ *State, scope *Scope, fn *Function, lhs *Ast, simpleAssign bool) {
	switch lhs.Type {
	case AstIdentifier:
		symb, exists := scope.GetVariable(lhs.Value)
		if !exists {
			ThrowError(fn.File, "variable not defined", lhs.Pos)
		}
		if symb.Const {
			ThrowError(fn.File, "cannot assign to constant variable", lhs.Pos)
		}
		if !simpleAssign {
			emit(fn, OpRot2)
		}
		emitInt(fn, OpStoreLocal, symb.Offset)
	case AstMemberAccess:
		emit(fn, OpRot3)
		emit(fn, OpSetAttr)
		emitRunes(fn, lhs.B.Value)
	case AstIndexAccess:
		emit(fn, OpRot4)
		emit(fn, OpSetIndex)
	default:
		ThrowError(fn.File, "invalid left-hand operand", lhs.Pos)
	}
}

// checkDeclKind validates var/let placement. Shared by all declaration visitors.
func checkDeclKind(fn *Function, scope *Scope, ast *Ast) {
	isGlobal := scope.Type == ScopeTypeGlobal
	isModule := scope.Type == ScopeTypeModule
	switch {
	case ast.Type == AstVar && !isGlobal && !isModule:
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

func visitVarDeclaration(s *State, scope *Scope, fn *Function, ast *Ast) {
	checkDeclKind(fn, scope, ast)
	constant := ast.Type == AstConst

	for head := ast.A; head != nil; head = head.Next {
		varNode := head.A
		valNode := head.B

		if valNode != nil {
			visitExpression(s, scope, fn, valNode)
		} else {
			emit(fn, OpLoadNull)
		}

		declareLocal(fn, scope, varNode.Value, constant, varNode.Pos)
	}
}

func visitVarArrayDestructDeclaration(s *State, scope *Scope, fn *Function, ast *Ast) {
	checkDeclKind(fn, scope, ast)
	constant := ast.Type == AstConst

	visitExpression(s, scope, fn, ast.B)

	size := 0
	for head := ast.A; head != nil; head = head.Next {
		size++
	}

	emitInt(fn, OpDestructArray, size)

	for head := ast.A; head != nil; head = head.Next {
		declareLocal(fn, scope, head.Value, constant, head.Pos)
	}
}

func visitVarObjectDestructDeclaration(s *State, scope *Scope, fn *Function, ast *Ast) {
	checkDeclKind(fn, scope, ast)
	constant := ast.Type == AstConst

	visitExpression(s, scope, fn, ast.B)

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

func visitFunctionDeclaration(s *State, scope *Scope, fn *Function, ast *Ast) {
	nameNode := ast.A

	funcOffset := compileFunction(s, scope, fn, nameNode.Value,
		ast.B.Type == AstAsyncFunctionExpression, false, false, false, ast.B.A, ast.B.B)

	sym, exists := scope.GetVariable(nameNode.Value)
	if !exists {
		ThrowError(fn.File, "function does not exists", ast.Pos)
	}

	emitInt(fn, OpLoadFunction, funcOffset)
	emitInt(fn, OpStoreLocal, sym.Offset)
}

func visitIfStatement(s *State, scope *Scope, fn *Function, ast *Ast) {
	condNode := ast.A
	thenNode := ast.B
	elseNode := ast.C

	visitExpression(s, scope, fn, condNode)

	jmpIfFalsePos := emitJump(fn, OpPopJmpIfFalse)
	visitDeclarationOrStatement(s, scope, fn, thenNode)
	endIf := emitJump(fn, OpJmp)

	labelJump(fn, jmpIfFalsePos)
	if elseNode != nil {
		visitDeclarationOrStatement(s, scope, fn, elseNode)
	}
	labelJump(fn, endIf)
}

func visitForStatement(s *State, scope *Scope, fn *Function, ast *Ast) {
	initNode := ast.A
	condNode := ast.B
	muttNode := ast.C
	bodyNode := ast.D

	loopStart := len(fn.Bytecode)

	loopScope := acquireScope(scope)
	loopScope.Type = ScopeTypeLoop
	loopScope.LoopStart = loopStart

	if initNode != nil {
		visitDeclarationOrStatement(s, loopScope, fn, initNode)
	}

	condPos := len(fn.Bytecode)
	var jmpIfFalsePos int
	if condNode != nil {
		visitExpression(s, loopScope, fn, condNode)
		jmpIfFalsePos = emitJump(fn, OpPopJmpIfFalse)
	}

	visitDeclarationOrStatement(s, loopScope, fn, bodyNode)

	loopScope.LoopContinue = len(fn.Bytecode)
	for _, jump := range loopScope.ContinueStart {
		fn.Bytecode[jump] = int32(loopScope.LoopContinue)
	}
	loopScope.ContinueStart = loopScope.ContinueStart[:0]
	if muttNode != nil {
		visitExpression(s, loopScope, fn, muttNode)
		emit(fn, OpPopTop)
	}
	emitInt(fn, OpAbsJmp, condPos)

	if condNode != nil {
		labelJump(fn, jmpIfFalsePos)
	}

	// Break
	for _, jmpSlot := range loopScope.BreakStart {
		labelJump(fn, jmpSlot)
	}

	releaseScope(loopScope)
}

func visitWhileStatement(s *State, scope *Scope, fn *Function, ast *Ast) {
	condNode := ast.A
	bodyNode := ast.B

	loopStart := len(fn.Bytecode)

	loopScope := acquireScope(scope)
	loopScope.Type = ScopeTypeLoop
	loopScope.LoopStart = loopStart
	loopScope.LoopContinue = loopStart

	visitExpression(s, scope, fn, condNode)
	jmpIfFalsePos := emitJump(fn, OpPopJmpIfFalse)

	visitDeclarationOrStatement(s, loopScope, fn, bodyNode)
	emitInt(fn, OpAbsJmp, loopStart)

	labelJump(fn, jmpIfFalsePos)

	// Break
	for _, jmpSlot := range loopScope.BreakStart {
		labelJump(fn, jmpSlot)
	}

	releaseScope(loopScope)
}

func visitBlock(s *State, scope *Scope, fn *Function, ast *Ast) {
	blockScope := acquireScope(scope)
	blockScope.Type = ScopeTypeBlock

	for statement := ast.A; statement != nil; statement = statement.Next {
		visitDeclarationOrStatement(s, blockScope, fn, statement)
	}

	releaseScope(blockScope)
}

func visitBreak(_ *State, scope *Scope, fn *Function, ast *Ast) {
	breakScope := scope
	for breakScope != nil && breakScope.Type != ScopeTypeLoop && breakScope.Type != ScopeTypeSwitch && breakScope.Type != ScopeTypeCase {
		breakScope = breakScope.Parent
	}
	if breakScope == nil {
		ThrowError(fn.File, "break statement not within a loop or switch", ast.Pos)
	}
	jmpSlot := emitJump(fn, OpJmp)
	breakScope.BreakStart = append(breakScope.BreakStart, jmpSlot)
}

func visitContinue(_ *State, scope *Scope, fn *Function, ast *Ast) {
	continueScope := scope
	for continueScope != nil && continueScope.Type != ScopeTypeLoop && continueScope.Type != ScopeTypeCase {
		continueScope = continueScope.Parent
	}
	if continueScope == nil {
		ThrowError(fn.File, "continue statement not within a loop or switch case", ast.Pos)
	}
	if continueScope.Type == ScopeTypeCase {
		continueScope.ContinueStart = append(continueScope.ContinueStart, emitJump(fn, OpJmp))
		return
	}
	if continueScope.Type == ScopeTypeLoop && continueScope.LoopContinue != 0 {
		emitInt(fn, OpAbsJmp, continueScope.LoopContinue)
		return
	}
	continueScope.ContinueStart = append(continueScope.ContinueStart, emitJump(fn, OpJmp))
}

func visitReturn(s *State, scope *Scope, fn *Function, ast *Ast) {
	if ast.A != nil {
		visitExpression(s, scope, fn, ast.A)
	} else {
		emit(fn, OpLoadNull)
	}
	emit(fn, OpReturn)
}

func visitTryCatch(s *State, scope *Scope, fn *Function, ast *Ast) {
	if ast.B == nil {
		for statement := ast.A; statement != nil; statement = statement.Next {
			visitDeclarationOrStatement(s, scope, fn, statement)
		}
		return
	}

	// Index (not pointer) into the table: nested try/catch may grow the slice.
	tableIndex := len(fn.TryCatchTable)
	fn.TryCatchTable = append(fn.TryCatchTable, TryCatchTable{CatchLocal: -1})
	fn.TryCatchTable[tableIndex].TryStart = len(fn.Bytecode)

	tryScope := acquireScope(scope)
	tryScope.Type = ScopeTypeBlock
	for statement := ast.A; statement != nil; statement = statement.Next {
		visitDeclarationOrStatement(s, tryScope, fn, statement)
	}
	releaseScope(tryScope)

	fn.TryCatchTable[tableIndex].TryEnd = len(fn.Bytecode)
	endJump := emitJump(fn, OpJmp)
	fn.TryCatchTable[tableIndex].CatchStart = len(fn.Bytecode)

	catchScope := acquireScope(scope)
	catchScope.Type = ScopeTypeBlock

	catchVarName := ast.B.Value
	offset := catchScope.GetVarCount()

	catchScope.AddLocal(catchVarName, Symbol{
		Name:   catchVarName,
		Offset: offset,
		Pos:    ast.B.Pos,
	})

	fn.TryCatchTable[tableIndex].CatchLocal = offset

	for statement := ast.C; statement != nil; statement = statement.Next {
		visitDeclarationOrStatement(s, catchScope, fn, statement)
	}
	releaseScope(catchScope)

	fn.TryCatchTable[tableIndex].CatchEnd = len(fn.Bytecode)
	labelJump(fn, endJump)
}

// moduleDeclarationNames appends every top-level declared name to names, in
// source order. Callers pass a stack-backed slice, so typical modules need no
// heap allocation and the result order is deterministic (unlike a map).
func moduleDeclarationNames(ast *Ast, names []string) []string {
	for statement := ast.A; statement != nil; statement = statement.Next {
		switch statement.Type {
		case AstClass, AstFunction:
			names = append(names, statement.A.Value)
		case AstVar, AstLet, AstConst:
			for declaration := statement.A; declaration != nil; declaration = declaration.Next {
				names = append(names, declaration.A.Value)
			}
		case AstDestructuringArrayVar, AstDestructuringArrayLet, AstDestructuringArrayConst:
			for item := statement.A; item != nil; item = item.Next {
				names = append(names, item.Value)
			}
		case AstDestructuringObjectVar, AstDestructuringObjectLet, AstDestructuringObjectConst:
			for item := statement.A; item != nil; item = item.Next {
				if item.Type == AstKeyValue {
					names = append(names, item.B.Value)
				} else {
					names = append(names, item.Value)
				}
			}
		}
	}
	return names
}

// sourceRunes is []rune(string(data)) without the intermediate string copy.
// Invalid bytes decode to U+FFFD, exactly like the conversion it replaces.
func sourceRunes(data []byte) []rune {
	runes := make([]rune, 0, utf8.RuneCount(data))
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		runes = append(runes, r)
		data = data[n:]
	}
	return runes
}

func compileImportedModule(s *State, fn *Function, ast *Ast, path string) *Import {
	if s.Imports == nil {
		s.Imports = make(map[string]*Import)
	}

	// Fast path: this exact path was already seen. No Stat, no scan.
	if cached, ok := s.Imports[path]; ok {
		if cached.IsImporting {
			cycle := append(append([]string(nil), s.ImportStack...), cached.Path)
			ThrowError(fn.File, "circular module import: "+strings.Join(cycle, " -> "), ast.Pos)
		}
		return cached
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		ThrowError(fn.File, fmt.Sprintf("error reading imported module %q: %v", path, err), ast.Pos)
	}

	// Slow path: a different path may still name the same file (symlink or
	// hard link), so compare file identity against every known module.
	for _, cached := range s.Imports {
		if cached.FileInfo == nil || !os.SameFile(cached.FileInfo, fileInfo) {
			continue
		}
		if cached.IsImporting {
			cycle := append(append([]string(nil), s.ImportStack...), cached.Path)
			ThrowError(fn.File, "circular module import: "+strings.Join(cycle, " -> "), ast.Pos)
		}
		s.Imports[path] = cached
		return cached
	}

	module := &Import{IsImporting: true, Path: path, FileInfo: fileInfo}
	s.Imports[path] = module
	s.ImportStack = append(s.ImportStack, path)
	defer func() {
		s.ImportStack = s.ImportStack[:len(s.ImportStack)-1]
	}()

	data, err := os.ReadFile(path)
	if err != nil {
		ThrowError(fn.File, fmt.Sprintf("error reading imported module %q: %v", path, err), ast.Pos)
	}
	file := &File{Name: path, Content: sourceRunes(data)}
	moduleParser := NewParser(s, file)
	moduleAst := moduleParser.Parse()
	assert(AstProgram, moduleAst)

	buf := acquireCodeBuf()
	moduleFunction := newFunction(file, path, buf)
	moduleFunction.Module = module
	module.Function = s.SaveFunction(moduleFunction)
	moduleScope := acquireScope(nil)
	moduleScope.Type = ScopeTypeModule

	for statement := moduleAst.A; statement != nil; statement = statement.Next {
		if statement.Type != AstFunction {
			continue
		}
		name := statement.A
		if moduleScope.ExistsLocal(name.Value) {
			ThrowError(file, "variable is already defined in scope", name.Pos)
		}
		moduleScope.AddLocal(name.Value, Symbol{
			Name:   name.Value,
			Offset: moduleScope.GetVarCount(),
			Pos:    name.Pos,
		})
	}

	var nameBuf [16]string
	declaredNames := moduleDeclarationNames(moduleAst, nameBuf[:0])
	for statement := moduleAst.A; statement != nil; statement = statement.Next {
		visitDeclarationOrStatement(s, moduleScope, moduleFunction, statement)
	}

	module.Exports = make(map[string]struct{}, len(declaredNames))
	for _, name := range declaredNames {
		if _, dup := module.Exports[name]; dup {
			continue
		}
		if symbol, exists := moduleScope.Variables[name]; exists {
			emitString(moduleFunction, name)
			emitInt(moduleFunction, OpLoadVar, symbol.Offset)
			module.Exports[name] = struct{}{}
		}
	}
	emitInt(moduleFunction, OpMakeObject, len(module.Exports))
	emit(moduleFunction, OpReturn)
	moduleFunction.LocalCount = moduleScope.VarCount
	finalizeFunction(moduleFunction, buf)
	releaseScope(moduleScope)
	module.IsImporting = false
	return module
}

// libraryRoot is resolved once per process: os.Executable and filepath.Abs
// are syscall-backed and the answer cannot change while compiling.
var libraryRootCache struct {
	once   sync.Once
	root   string
	errMsg string
}

func libraryRoot() (root, errMsg string) {
	c := &libraryRootCache
	c.once.Do(func() {
		executable, err := os.Executable()
		if err != nil {
			c.errMsg = "could not locate executable for library import"
			return
		}
		abs, err := filepath.Abs(filepath.Join(filepath.Dir(executable), "lib"))
		if err != nil {
			c.errMsg = "invalid library module path"
			return
		}
		c.root = abs
	})
	return c.root, c.errMsg
}

// resolveLibraryPath maps "lib:<name>" to an absolute path inside the lib
// directory next to the executable, rejecting anything that escapes it.
func resolveLibraryPath(fn *Function, ast *Ast, libraryName string) string {
	if libraryName == "" {
		ThrowError(fn.File, "invalid library module path", ast.A.Pos)
	}
	root, errMsg := libraryRoot()
	if errMsg != "" {
		ThrowError(fn.File, errMsg, ast.A.Pos)
	}
	path := filepath.Join(root, filepath.FromSlash(libraryName))
	if filepath.Ext(path) == "" {
		path += ".os"
	}
	path, err := filepath.Abs(path)
	if err != nil {
		ThrowError(fn.File, "invalid library module path", ast.A.Pos)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		ThrowError(fn.File, "library module path escapes lib directory", ast.A.Pos)
	}
	return path
}

func visitImport(s *State, scope *Scope, fn *Function, ast *Ast) {
	moduleName, err := strconv.Unquote(ast.A.Value)
	if err != nil {
		ThrowError(fn.File, "invalid module path", ast.A.Pos)
	}
	if exports, isBuiltin := getBuiltinModule(moduleName); isBuiltin {
		if ast.B == nil {
			ThrowError(fn.File, "builtin module import requires named imports", ast.Pos)
		}
		for item := ast.B; item != nil; item = item.Next {
			exportName, localName := item.Value, item.Value
			if item.Type == AstKeyValue {
				exportName = objectKey(item.A)
				localName = item.B.Value
			}
			if _, exists := exports[exportName]; !exists {
				ThrowError(fn.File, "builtin module does not export requested name", item.Pos)
			}
			if scope.ExistsLocal(localName) {
				ThrowError(fn.File, "variable is already defined in scope", item.Pos)
			}
			offset := scope.GetVarCount()
			scope.AddLocal(localName, Symbol{
				Name:   localName,
				Offset: offset,
				Const:  true,
				Pos:    item.Pos,
			})
			emitString(fn, moduleName)
			emitString(fn, exportName)
			emit(fn, OpLoadBuiltin)
			emitInt(fn, OpStoreLocal, offset)
		}
		return
	}
	var path string
	if libraryName, isLib := strings.CutPrefix(moduleName, "lib:"); isLib {
		path = resolveLibraryPath(fn, ast, libraryName)
	} else {
		// filepath.Abs already returns a cleaned path, so no extra Clean is needed.
		path, err = filepath.Abs(filepath.Join(filepath.Dir(fn.File.Name), moduleName))
		if err != nil {
			ThrowError(fn.File, "invalid module path", ast.A.Pos)
		}
	}
	module := compileImportedModule(s, fn, ast, path)
	emitInt(fn, OpLoadFunction, module.Function)
	emitInt(fn, OpCall, 0)

	for item := ast.B; item != nil; item = item.Next {
		exportName, localName := item.Value, item.Value
		if item.Type == AstKeyValue {
			exportName = objectKey(item.A)
			localName = item.B.Value
		}
		if _, exists := module.Exports[exportName]; !exists {
			ThrowError(fn.File, "module does not export requested name", item.Pos)
		}
		if scope.ExistsLocal(localName) {
			ThrowError(fn.File, "variable is already defined in scope", item.Pos)
		}
		offset := scope.GetVarCount()
		scope.AddLocal(localName, Symbol{
			Name:   localName,
			Offset: offset,
			Const:  true,
			Pos:    item.Pos,
		})
		emit(fn, OpDupTop)
		emit(fn, OpGetAttr)
		emitRunes(fn, exportName)
		emitInt(fn, OpStoreLocal, offset)
	}
	emit(fn, OpPopTop)
}

func visitDeclarationOrStatement(s *State, scope *Scope, fn *Function, ast *Ast) {
	emitLine(fn, ast.Pos)
	switch ast.Type {
	case AstClass:
		name := ast.A.Value
		symbol, exists := scope.GetVariable(name)
		if !exists {
			symbol = Symbol{Name: name, Offset: scope.GetVarCount(), Const: true, Pos: ast.Pos}
			scope.AddLocal(name, symbol)
		}
		visitClassExpression(s, scope, fn, ast.B, name)
		emitInt(fn, OpStoreLocal, symbol.Offset)
	case AstFunction:
		visitFunctionDeclaration(s, scope, fn, ast)
	case AstVar, AstLet, AstConst:
		visitVarDeclaration(s, scope, fn, ast)
	case AstDestructuringArrayVar, AstDestructuringArrayLet, AstDestructuringArrayConst:
		visitVarArrayDestructDeclaration(s, scope, fn, ast)
	case AstDestructuringObjectVar, AstDestructuringObjectLet, AstDestructuringObjectConst:
		visitVarObjectDestructDeclaration(s, scope, fn, ast)
	case AstIfStmt:
		visitIfStatement(s, scope, fn, ast)
	case AstForLoop:
		visitForStatement(s, scope, fn, ast)
	case AstWhileLoop:
		visitWhileStatement(s, scope, fn, ast)
	case AstBlock:
		visitBlock(s, scope, fn, ast)
	case AstLogStmt:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpStdWrite)
	case AstExpressionStmnt:
		visitExpression(s, scope, fn, ast.A)
		emit(fn, OpPopTop)
	case AstBreakStmt:
		visitBreak(s, scope, fn, ast)
	case AstContinueStmt:
		visitContinue(s, scope, fn, ast)
	case AstReturnStmt:
		visitReturn(s, scope, fn, ast)
	case AstTryCatch:
		visitTryCatch(s, scope, fn, ast)
	case AstSwitchStmt:
		visitSwitchStatement(s, scope, fn, ast)
	case AstImport:
		visitImport(s, scope, fn, ast)
	default:
		unexpected("visitDeclarationOrStatement", ast.Type)
	}
}

// compileProgram is the only place that still needs the *Parser: it calls
// p.Parse() and reads p.File. Everything below it receives just p.state.
func compileProgram(p *Parser) *Function {
	defer releaseAstSlab()

	ast := p.Parse()
	assert(AstProgram, ast)

	buf := acquireCodeBuf()
	program := newFunction(p.File, "$main", buf)

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
		visitDeclarationOrStatement(p.state, scope, program, current)
	}

	emit(program, OpLoadNull)
	emit(program, OpReturn)

	program.LocalCount = scope.VarCount
	finalizeFunction(program, buf)
	releaseScope(scope)

	return program
}

func Compile(p *Parser) *Function {
	return compileProgram(p)
}
