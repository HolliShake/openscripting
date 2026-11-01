package main

import (
	"strconv"
	"unicode"
)

const (
	clsIDStart uint8 = 1 << iota
	clsIDCont
	clsDigit
	clsSymbol
	clsStop // ends a plain run inside a string literal
)

const (
	symbolChars   = "+-*/%=!<>&|^~?:.,;(){}[]#@\\"
	maxKeywordLen = 16
)

var (
	class           [128]uint8
	hexVal          [128]uint8 // 0xFF = not a hex digit
	singleSym       [128]string
	keywords        map[string]string // keyword text -> canonical static string
	otherIDStart    = unicode.Properties["Other_ID_Start"]
	otherIDContinue = unicode.Properties["Other_ID_Continue"]
)

type Parser struct {
	// state owns compiled functions and imported modules produced while parsing this source.
	state *State
	// File supplies rune content and the filename used in parser diagnostics.
	File *File
	// line and colm track the current one-based source position as runes are consumed.
	line int
	colm int
	// offset is the index into File.Content for constant-time lexer advancement.
	offset int
	// lookahead caches the next token so parser routines can branch without rescanning source.
	lookahead Token
	// errors accumulates recoverable parse diagnostics until the parser reports failure.
	errors []string
}

/******************* Character classes (package-level tables, no struct changes) ***/

func (p *Parser) isIdentifierStart(ch rune) bool {
	if ch < 128 {
		return class[ch]&clsIDStart != 0
	}
	return unicode.IsLetter(ch) || unicode.Is(unicode.Nl, ch) || unicode.Is(otherIDStart, ch)
}

func (p *Parser) isIdentifierContinue(ch rune) bool {
	if ch < 128 {
		return class[ch]&clsIDCont != 0
	}
	return p.isIdentifierStart(ch) || unicode.IsDigit(ch) || unicode.IsMark(ch) ||
		unicode.Is(unicode.Pc, ch) || unicode.Is(otherIDContinue, ch)
}

func (p *Parser) pushError(err string) {
	p.errors = append(p.errors, err)
}

func (p *Parser) current() rune {
	if p.offset >= len(p.File.Content) {
		return 0
	}
	return p.File.Content[p.offset]
}

func (p *Parser) peek(distance int) rune {
	index := p.offset + distance
	if index >= len(p.File.Content) {
		return 0
	}
	return p.File.Content[index]
}

func (p *Parser) advance() rune {
	src := p.File.Content
	if p.offset >= len(src) {
		return 0
	}
	ch := src[p.offset]
	p.offset++
	switch ch {
	case '\r':
		if p.offset < len(src) && src[p.offset] == '\n' {
			p.offset++
		}
		p.line++
		p.colm = 0
	case '\n', '\u0085', '\u2028', '\u2029':
		p.line++
		p.colm = 0
	default:
		p.colm++
	}
	return ch
}

// emit finishes a token known to contain no line break.
// start is always p.offset at the call site, so it is derived here.
// This is the only place a token string is allocated.
func (p *Parser) emit(t TokenType, end int, pos Position) Token {
	start := p.offset
	p.colm += end - start
	p.offset = end
	return Token{Type: t, Value: string(p.File.Content[start:end]), Pos: pos}
}

// emitStatic finishes an ASCII token whose text is already a static string.
// No allocation.
func (p *Parser) emitStatic(t TokenType, s string, pos Position) Token {
	p.colm += len(s)
	p.offset += len(s)
	return Token{Type: t, Value: s, Pos: pos}
}

/******************* Trivia ***/

// skipTrivia skips whitespace and comments. It never produced a token,
// so it returns nothing; the previous (Token, bool) return only forced
// a useless struct copy and a dead branch on every token.
func (p *Parser) skipTrivia() {
	src := p.File.Content
	n := len(src)
	for {
		for p.offset < n {
			c := src[p.offset]
			if c == ' ' || c == '\t' {
				p.offset++
				p.colm++
				continue
			}
			if !unicode.IsSpace(c) {
				break
			}
			p.advance() // handles line breaks
		}

		if p.offset+1 >= n || src[p.offset] != '/' {
			return
		}
		switch src[p.offset+1] {
		case '/':
			i := p.offset + 2
			for i < n && !isLineBreak(src[i]) {
				i++
			}
			p.colm += i - p.offset
			p.offset = i
		case '*':
			pos := Position{Line: p.line, Column: p.colm}
			p.offset += 2
			p.colm += 2
			i := p.offset
			for i < n && !(src[i] == '*' && i+1 < n && src[i+1] == '/') {
				if isLineBreak(src[i]) {
					p.colm += i - p.offset
					p.offset = i
					p.advance()
					i = p.offset
				} else {
					i++
				}
			}
			p.colm += i - p.offset
			p.offset = i
			if i >= n {
				ThrowError(p.File, "unterminated block comment", pos)
			}
			p.offset += 2
			p.colm += 2
		default:
			return
		}
	}
}

/******************* Scanners ***/

func (p *Parser) scanEscape() {
	src := p.File.Content
	n := len(src)
	pos := Position{Line: p.line, Column: p.colm}
	p.offset++ // backslash
	p.colm++
	if p.offset >= n || isLineBreak(src[p.offset]) {
		ThrowError(p.File, "invalid escape sequence", pos)
	}

	escape := p.advance()
	switch escape {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', '\'', '"', '0':
		return
	case 'x', 'u', 'U':
		width := 2
		switch escape {
		case 'u':
			width = 4
		case 'U':
			width = 8
		}

		braced := escape == 'u' && p.offset < n && src[p.offset] == '{'
		if braced {
			p.offset++
			p.colm++
			width = 1 << 30 // greedy, validated below
		}

		var value uint32
		digits := 0
		for digits < width && p.offset < n {
			d, ok := hexValue(src[p.offset])
			if !ok {
				break
			}
			if value > 0x10ffff>>4 {
				ThrowError(p.File, "invalid escape sequence", pos)
			}
			value = value<<4 | d
			digits++
			p.offset++
			p.colm++
		}

		if braced {
			if digits == 0 || digits > 6 || p.offset >= n || src[p.offset] != '}' {
				ThrowError(p.File, "invalid escape sequence", pos)
			}
			p.offset++
			p.colm++
		} else if digits != width {
			ThrowError(p.File, "invalid escape sequence", pos)
		}
		if escape != 'x' && (value > 0x10ffff || value >= 0xd800 && value <= 0xdfff) {
			ThrowError(p.File, "invalid escape sequence", pos)
		}
	default:
		ThrowError(p.File, "invalid escape sequence", pos)
	}
}

func (p *Parser) scanString(pos Position) Token {
	src := p.File.Content
	n := len(src)
	start := p.offset
	quote := src[start]
	p.offset++
	p.colm++
	terminated := false

loop:
	for {
		i := p.offset
		for i < n {
			c := src[i]
			if c < 128 {
				if class[c]&clsStop != 0 {
					break
				}
			} else if c == '\u0085' || c == '\u2028' || c == '\u2029' {
				break
			}
			i++
		}
		p.colm += i - p.offset
		p.offset = i
		if i >= n {
			break
		}

		switch c := src[i]; {
		case c == quote:
			p.offset++
			p.colm++
			terminated = true
			break loop
		case c == '\\':
			p.scanEscape()
		case isLineBreak(c):
			ThrowError(p.File, "unterminated string literal", pos)
		default: // the other quote character
			p.offset++
			p.colm++
		}
	}

	if !terminated {
		ThrowError(p.File, "unterminated string literal", pos)
	}
	return Token{Type: STR, Value: string(src[start:p.offset]), Pos: pos}
}

func (p *Parser) scanNumber(pos Position) Token {
	src := p.File.Content
	n := len(src)
	i := p.offset

	if src[i] == '0' && i+1 < n {
		var radix uint8
		switch src[i+1] {
		case 'x', 'X':
			radix = 16
		case 'o', 'O':
			radix = 8
		case 'b', 'B':
			radix = 2
		}
		if radix != 0 {
			i += 2
			hasDigit := false
			for i < n {
				c := src[i]
				if c == '_' {
					i++
					continue
				}
				ok := false
				switch radix {
				case 16:
					_, ok = hexValue(c)
				case 8:
					ok = c >= '0' && c <= '7'
				default:
					ok = c == '0' || c == '1'
				}
				if !ok {
					break
				}
				hasDigit = true
				i++
			}
			if !hasDigit {
				ThrowError(p.File, "invalid numeric literal", pos)
			}
			if i < n && (src[i] == 'n' || src[i] == 'N') {
				i++
			}
			return p.emit(NUM, i, pos)
		}
	}

	for i < n && (isDigit(src[i]) || src[i] == '_') {
		i++
	}
	isInteger := true
	if i+1 < n && src[i] == '.' && isDigit(src[i+1]) {
		isInteger = false
		i++
		for i < n && (isDigit(src[i]) || src[i] == '_') {
			i++
		}
	}
	if i < n && (src[i] == 'e' || src[i] == 'E') {
		isInteger = false
		k := i + 1
		if k < n && (src[k] == '+' || src[k] == '-') {
			k++
		}
		d := k
		for k < n && (isDigit(src[k]) || src[k] == '_') {
			k++
		}
		if k > d {
			i = k
		}
	}
	if isInteger && i < n && (src[i] == 'n' || src[i] == 'N') {
		i++
	}
	return p.emit(NUM, i, pos)
}

func (p *Parser) scanIdent(pos Position, start int) Token {
	src := p.File.Content
	n := len(src)
	i := start + 1
	for i < n {
		c := src[i]
		if c < 128 {
			if class[c]&clsIDCont == 0 {
				break
			}
		} else if !p.isIdentifierContinue(c) {
			break
		}
		i++
	}

	// Keyword fast path: look up through a stack buffer, so keywords cost zero
	// allocations. The map lookup with string(bytes) does not allocate.
	if length := i - start; length <= maxKeywordLen && src[start] >= 'a' && src[start] <= 'z' {
		var buf [maxKeywordLen]byte
		ascii := true
		for j := 0; j < length; j++ {
			c := src[start+j]
			if c >= 128 {
				ascii = false
				break
			}
			buf[j] = byte(c)
		}
		if ascii {
			if kw, ok := keywords[string(buf[:length])]; ok {
				p.colm += length
				p.offset = i
				return Token{Type: KEY, Value: kw, Pos: pos}
			}
		}
	}

	tok := p.emit(IDN, i, pos)
	if isKeyword(tok.Value) { // fallback, in case isKeyword knows more words
		tok.Type = KEY
	}
	return tok
}

func (p *Parser) getNextToken() Token {
	p.skipTrivia()
	src := p.File.Content
	pos := Position{Line: p.line, Column: p.colm}
	i := p.offset
	if i >= len(src) {
		return Token{Type: EOF, Pos: pos}
	}

	ch := src[i]
	if ch < 128 {
		cls := class[ch]
		switch {
		case cls&clsIDStart != 0:
			return p.scanIdent(pos, i)
		case cls&clsDigit != 0:
			return p.scanNumber(pos)
		case ch == '"' || ch == '\'':
			return p.scanString(pos)
		case cls&clsSymbol != 0:
			return p.emitStatic(SYM, symbolAt(src, i), pos)
		}
		ThrowError(p.File, "unexpected character "+strconv.QuoteRune(ch), pos)
	}

	if p.isIdentifierStart(ch) {
		return p.scanIdent(pos, i)
	}
	if unicode.IsDigit(ch) {
		return p.scanNumber(pos)
	}
	p.advance()
	ThrowError(p.File, "unexpected character "+strconv.QuoteRune(ch), pos)
	return Token{}
}

/******************* Parser helpers ***/

func (p *Parser) match(token string) bool {
	return (p.lookahead.Type == IDN ||
		p.lookahead.Type == KEY ||
		p.lookahead.Type == SYM) && token == p.lookahead.Value
}

func (p *Parser) matchType(token TokenType) bool {
	return p.lookahead.Type == token
}

func (p *Parser) expect(token string) {
	if p.match(token) {
		p.lookahead = p.getNextToken()
		return
	}
	ThrowExpectedTokenError(p.File, token, p.lookahead.Value, p.lookahead.Pos)
}

func (p *Parser) expectType(token TokenType) {
	if p.matchType(token) {
		p.lookahead = p.getNextToken()
		return
	}
	ThrowExpectedTokenError(p.File, token.String(), p.lookahead.Value, p.lookahead.Pos)
}

func (p *Parser) identifier(required bool) *Ast {
	if !p.matchType(IDN) {
		if required {
			ThrowError(p.File, "expected identifier", p.lookahead.Pos)
		}
		return nil
	}
	ast := NewTerminalAst(AstIdentifier, p.lookahead.Pos, p.lookahead.Value)
	p.lookahead = p.getNextToken()
	return ast
}

func (p *Parser) identifierOrKeyword(required bool) *Ast {
	if !p.matchType(IDN) && !p.matchType(KEY) {
		if required {
			ThrowError(p.File, "expected identifier or keyword", p.lookahead.Pos)
		}
		return nil
	}
	ast := NewTerminalAst(AstIdentifier, p.lookahead.Pos, p.lookahead.Value)
	p.lookahead = p.getNextToken()
	return ast
}

func (p *Parser) stringLit() *Ast {
	ast := NewTerminalAst(AstStrLit, p.lookahead.Pos, p.lookahead.Value)
	p.expectType(STR)
	return ast
}

func (p *Parser) listOfStatement() *Ast {
	head := p.statement()
	tail := head

	for tail != nil {
		stmt := p.statement()
		tail.Next = stmt
		tail = tail.Next
	}

	return head
}

func (p *Parser) listOfBlockStatement() *Ast {
	head := p.blockLevelStatement()
	tail := head

	for tail != nil {
		stmt := p.blockLevelStatement()
		tail.Next = stmt
		tail = tail.Next
	}

	return head
}

/******************* Expressions ***/

func (p *Parser) terminal() *Ast {
	switch p.lookahead.Type {
	case IDN:
		return p.identifier(true)
	case NUM:
		ast := NewTerminalAst(AstNumLit, p.lookahead.Pos, p.lookahead.Value)
		p.expectType(NUM)
		return ast
	case STR:
		return p.stringLit()
	case KEY:
		if p.lookahead.Value == KeyTrue || p.lookahead.Value == KeyFalse {
			ast := NewTerminalAst(AstBoolLit, p.lookahead.Pos, p.lookahead.Value)
			p.expectType(KEY)
			return ast
		}
		if p.lookahead.Value == KeyNull {
			ast := NewTerminalAst(AstNullLit, p.lookahead.Pos, "")
			p.expectType(KEY)
			return ast
		}
		if p.lookahead.Value == KeyThis || p.lookahead.Value == KeySuper {
			return p.identifierOrKeyword(true)
		}
	}
	return nil
}

func (p *Parser) group() *Ast {
	if p.match("(") {
		p.expect("(")
		expr := p.expression(true)
		p.expect(")")
		return expr
	} else if p.match("[") {
		p.expect("[")
		expr := p.listOfExpression(false, true)
		p.expect("]")
		return NewArray(p.lookahead.Pos, expr)
	} else if p.match("{") {
		p.expect("{")
		expr := p.listOfKeyValues(false, true) // parse object properties
		p.expect("}")
		return NewObject(p.lookahead.Pos, expr)
	} else if p.match(KeyClass) {
		return p.classExpression()
	} else if p.match(KeyFn) {
		return p.functionExpression()
	} else if p.match(KeySwitch) {
		return p.switchExpression()
	}
	return p.terminal()
}

func (p *Parser) switchExpression() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeySwitch)
	p.expect("(")
	condition := p.expression(true)
	p.expect(")")
	p.expect("{")

	caseExpr := p.listOfExpression(true, false)
	p.expect("=>")
	caseBlock := p.expression(true)

	caseHead := NewKeyValue(caseExpr.Pos, caseExpr, caseBlock)
	caseTail := caseHead

	hasDefault := false

	for caseTail != nil && p.match(",") {
		p.expect(",")

		if p.match(KeyDefault) {
			if hasDefault {
				ThrowError(p.File, "multiple default cases are not allowed", p.lookahead.Pos)
			}

			hasDefault = true
			p.expect(KeyDefault)
			p.expect("=>")
			defaultBlock := p.expression(true)
			defaultNode := defaultBlock
			caseTail.Next = defaultNode
			caseTail = caseTail.Next
			continue
		}

		caseExpr := p.listOfExpression(false, false)
		if caseExpr == nil {
			ThrowError(p.File, "invalid case expression", p.lookahead.Pos)
		}
		p.expect("=>")
		caseBlock := p.expression(true)

		caseTail.Next = NewKeyValue(caseExpr.Pos, caseExpr, caseBlock)
		caseTail = caseTail.Next
	}

	if !hasDefault {
		ThrowError(p.File, "missing default case in switch expression", p.lookahead.Pos)
	}

	p.expect("}")

	return NewSwitchExpression(pos, condition, caseHead)
}

func (p *Parser) memberOrCall() *Ast {
	obj := p.group()

	if obj == nil {
		return nil
	}

	for {
		if p.match("->") {
			p.expect("->")
			prop := p.identifierOrKeyword(true)
			obj = NewMemberAccess(obj.Pos, obj, prop)
		} else if p.match("[") {
			p.expect("[")
			index := p.expression(true)
			p.expect("]")
			obj = NewIndexAccess(obj.Pos, obj, index)
		} else if p.match("(") {
			p.expect("(")
			args := p.listOfExpression(false, false)
			p.expect(")")
			obj = NewCall(obj.Pos, obj, args)
		} else {
			return obj
		}
	}
}

func (p *Parser) expression(required bool) *Ast {
	node := p.ternaryOperation()
	if node == nil && required {
		ThrowError(p.File, "missing expression", p.lookahead.Pos)
	}
	return node
}

// Precedence methods below are ordered from tightest to loosest.
// The operator is matched once, so it is consumed with next() instead of a second expect().

func (p *Parser) postfix() *Ast {
	lhs := p.memberOrCall()

	if lhs == nil {
		return nil
	}

	for p.match("++") || p.match("--") {
		opt := p.lookahead.Value
		switch opt {
		case "++":
			p.expect("++")
		case "--":
			p.expect("--")
		}

		lhs = NewPostfixOperation(postfixOperator(opt), lhs.Pos, lhs)
	}

	return lhs
}

func (p *Parser) unary() *Ast {
	if p.match("+") || p.match("++") || p.match("-") || p.match("--") || p.match("!") || p.match("~") || p.match(KeyAwait) {
		opt := p.lookahead.Value
		switch opt {
		case "+":
			p.expect("+")
		case "++":
			p.expect("++")
		case "-":
			p.expect("-")
		case "--":
			p.expect("--")
		case "!":
			p.expect("!")
		case "~":
			p.expect("~")
		case KeyAwait:
			p.expect(KeyAwait)
		}

		operand := p.unary()
		if operand == nil {
			ThrowError(p.File, "missing operand", p.lookahead.Pos)
		}
		return NewUnaryOperation(unaryOperator(opt), operand.Pos, operand)
	}

	return p.postfix()
}

func (p *Parser) multiplicative() *Ast {
	lhs := p.unary()
	if lhs == nil {
		return nil
	}

	for p.match("*") || p.match("/") || p.match("%") {
		opt := p.lookahead.Value
		switch opt {
		case "*":
			p.expect("*")
		case "/":
			p.expect("/")
		case "%":
			p.expect("%")
		}

		rhs := p.unary()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) additive() *Ast {
	lhs := p.multiplicative()
	if lhs == nil {
		return nil
	}

	for p.match("+") || p.match("-") {
		opt := p.lookahead.Value
		switch opt {
		case "+":
			p.expect("+")
		case "-":
			p.expect("-")
		}

		rhs := p.multiplicative()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) shift() *Ast {
	lhs := p.additive()
	if lhs == nil {
		return nil
	}

	for p.match("<<") || p.match(">>") {
		opt := p.lookahead.Value
		switch opt {
		case "<<":
			p.expect("<<")
		case ">>":
			p.expect(">>")
		}

		rhs := p.additive()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) relational() *Ast {
	lhs := p.shift()
	if lhs == nil {
		return nil
	}

	for p.match("<") || p.match("<=") || p.match(">") || p.match(">=") {
		opt := p.lookahead.Value
		switch opt {
		case "<":
			p.expect("<")
		case "<=":
			p.expect("<=")
		case ">":
			p.expect(">")
		case ">=":
			p.expect(">=")
		}

		rhs := p.shift()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) equality() *Ast {
	lhs := p.relational()
	if lhs == nil {
		return nil
	}

	for p.match("==") || p.match("!=") {
		opt := p.lookahead.Value
		switch opt {
		case "==":
			p.expect("==")
		case "!=":
			p.expect("!=")
		}

		rhs := p.relational()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) bitwiseAnd() *Ast {
	lhs := p.equality()
	if lhs == nil {
		return nil
	}

	for p.match("&") {
		opt := p.lookahead.Value
		p.expect("&")

		rhs := p.equality()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) bitwiseXor() *Ast {
	lhs := p.bitwiseAnd()
	if lhs == nil {
		return nil
	}

	for p.match("^") {
		opt := p.lookahead.Value
		p.expect("^")

		rhs := p.bitwiseAnd()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) bitwiseOr() *Ast {
	lhs := p.bitwiseXor()
	if lhs == nil {
		return nil
	}

	for p.match("|") {
		opt := p.lookahead.Value
		p.expect("|")

		rhs := p.bitwiseXor()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) logicalAnd() *Ast {
	lhs := p.bitwiseOr()
	if lhs == nil {
		return nil
	}

	for p.match("&&") {
		opt := p.lookahead.Value
		p.expect("&&")

		rhs := p.bitwiseOr()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) logicalOr() *Ast {
	lhs := p.logicalAnd()
	if lhs == nil {
		return nil
	}

	for p.match("||") {
		opt := p.lookahead.Value
		p.expect("||")

		rhs := p.logicalAnd()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) assignment() *Ast {
	lhs := p.logicalOr()
	if lhs == nil {
		return nil
	}

	if p.match("=") {
		opt := p.lookahead.Value
		p.expect("=")

		rhs := p.assignment()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) mulAssign() *Ast {
	lhs := p.assignment()
	if lhs == nil {
		return nil
	}

	for p.match("*=") || p.match("/=") || p.match("%=") {
		opt := p.lookahead.Value
		switch opt {
		case "*=":
			p.expect("*=")
		case "/=":
			p.expect("/=")
		case "%=":
			p.expect("%=")
		}

		rhs := p.mulAssign()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) addAssign() *Ast {
	lhs := p.mulAssign()
	if lhs == nil {
		return nil
	}

	for p.match("+=") || p.match("-=") {
		opt := p.lookahead.Value
		switch opt {
		case "+=":
			p.expect("+=")
		case "-=":
			p.expect("-=")
		}

		rhs := p.addAssign()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) shiftAssign() *Ast {
	lhs := p.addAssign()
	if lhs == nil {
		return nil
	}

	for p.match("<<=") || p.match(">>=") {
		opt := p.lookahead.Value
		switch opt {
		case "<<=":
			p.expect("<<=")
		case ">>=":
			p.expect(">>=")
		}

		rhs := p.shiftAssign()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}
		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) andAssign() *Ast {
	lhs := p.shiftAssign()
	if lhs == nil {
		return nil
	}

	for p.match("&=") {
		opt := p.lookahead.Value
		p.expect("&=")

		rhs := p.andAssign()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}
		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) xorAssign() *Ast {
	lhs := p.andAssign()
	if lhs == nil {
		return nil
	}

	for p.match("^=") {
		opt := p.lookahead.Value
		p.expect("^=")

		rhs := p.xorAssign()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}
		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) orAssign() *Ast {
	lhs := p.xorAssign()
	if lhs == nil {
		return nil
	}

	for p.match("|=") {
		opt := p.lookahead.Value
		p.expect("|=")

		rhs := p.orAssign()
		if rhs == nil {
			ThrowError(p.File, "missing right-hand operand", p.lookahead.Pos)
		}
		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) ternaryOperation() *Ast {
	condition := p.orAssign()
	if condition == nil {
		return nil
	}

	if !p.match("?") {
		return condition
	}

	p.expect("?")
	thenExpr := p.expression(true)
	p.expect(":")
	elseExpr := p.expression(true)

	return NewTernaryOperation(condition.Pos, condition, thenExpr, elseExpr)
}

func (p *Parser) spreadOrExpression(required bool, spread bool) *Ast {
	if spread && p.match("...") {
		p.expect("...")
		return NewSpread(p.lookahead.Pos, p.expression(true))
	}

	return p.expression(required)
}

func (p *Parser) listOfExpression(required bool, spread bool) *Ast {
	head := p.spreadOrExpression(required, spread)
	tail := head

	for tail != nil && p.match(",") {
		p.expect(",")

		tail.Next = p.spreadOrExpression(false, spread) // expression or spread
		if tail.Next == nil {
			ThrowError(p.File, "missing expression after comma", p.lookahead.Pos)
		}

		tail = tail.Next
	}

	return head
}

func (p *Parser) key(required bool) *Ast {
	if p.matchType(IDN) || p.matchType(STR) {
		return p.terminal()
	}

	if required {
		ThrowError(p.File, "missing or invalid key", p.lookahead.Pos)
	}

	return nil
}

func (p *Parser) keyValue(required bool, spread bool) *Ast {
	if spread && p.match("...") {
		p.expect("...")
		return NewSpread(p.lookahead.Pos, p.expression(true))
	}

	k := p.key(required)
	if required && k == nil {
		ThrowError(p.File, "missing or invalid key", p.lookahead.Pos)
	}

	if !required && k == nil {
		return nil
	}

	p.expect(":")

	v := p.expression(false)
	if v == nil {
		ThrowError(p.File, "missing value for key", p.lookahead.Pos)
	}

	return NewKeyValue(k.Pos, k, v)
}

func (p *Parser) listOfKeyValues(required bool, spread bool) *Ast {
	head := p.keyValue(required, spread)
	tail := head

	for tail != nil && p.match(",") {
		p.expect(",")
		tail.Next = p.keyValue(true, spread)
		tail = tail.Next
	}

	return head
}

/******************* Declarations ***/

// objectPattern parses `a, b: expr, c` (shared by import lists and object destructuring).
func (p *Parser) objectPattern() *Ast {
	k := p.identifier(true)
	var v *Ast

	if p.match(":") {
		p.expect(":")
		v = p.identifier(true)
	}

	head := k
	tail := head

	if k != nil && v != nil {
		head = NewKeyValue(k.Pos, k, v)
		tail = head
	}

	for tail != nil && p.match(",") {
		p.expect(",")

		k = p.identifier(false)
		v = nil

		if p.match(":") {
			p.expect(":")
			v = p.identifier(true)
		}

		if k != nil && v != nil {
			tail.Next = NewKeyValue(k.Pos, k, v)
		} else {
			tail.Next = k
		}

		tail = tail.Next
	}

	return head
}

func (p *Parser) importStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyImport)
	if p.matchType(STR) {
		module := p.stringLit()
		p.expect(";")
		return NewImport(pos, module, nil)
	} else if p.match("{") {
		p.expect("{")
		head := p.objectPattern()
		p.expect("}")
		p.expect(KeyFrom)
		module := p.stringLit()
		p.expect(";")
		return NewImport(pos, module, head)
	}

	ThrowError(p.File, "missing module in import statement", pos)
	return nil
}

func (p *Parser) variableDeclaration(keyWord string, topLevel bool) *Ast {
	position := p.lookahead.Pos
	var declarationType, arrayDestructuringType, objectDestructuringType AstType
	switch keyWord {
	case KeyVar:
		declarationType = AstVar
		arrayDestructuringType = AstDestructuringArrayVar
		objectDestructuringType = AstDestructuringObjectVar
	case KeyLet:
		declarationType = AstLet
		arrayDestructuringType = AstDestructuringArrayLet
		objectDestructuringType = AstDestructuringObjectLet
	case KeyConst:
		declarationType = AstConst
		arrayDestructuringType = AstDestructuringArrayConst
		objectDestructuringType = AstDestructuringObjectConst
	default:
		ThrowError(p.File, "invalid variable declaration", position)
	}

	p.expect(keyWord) // var | let | const

	if p.match("[") { // array destructuring
		p.expect("[")
		head := p.identifier(true)
		tail := head
		for p.match(",") {
			p.expect(",")
			tail.Next = p.identifier(false)
			if tail.Next == nil {
				ThrowError(p.File, "missing identifier after comma", p.lookahead.Pos)
			}
			tail = tail.Next
		}
		p.expect("]")
		p.expect("=")
		v := p.expression(true)
		p.expect(";")
		return NewDestructuringArrayDeclaration(arrayDestructuringType, position, head, v)
	} else if p.match("{") { // object destructuring
		p.expect("{")
		head := p.objectPattern()
		p.expect("}")
		p.expect("=")
		value := p.expression(true)
		p.expect(";")
		return NewDestructuringObjectDeclaration(objectDestructuringType, position, head, value)
	}

	// regular variable dec
	key := p.identifier(true)
	var val *Ast

	// function or class
	if topLevel && p.match("::") {
		p.expect("::")
		if p.match(KeyFn) {
			exp := p.functionExpression()
			fun := NewFunctionDeclaration(key.Pos, key, exp)
			p.expect(";")
			return fun
		} else if p.match(KeyClass) {
			exp := p.classExpression()
			cls := NewClassDeclaration(key.Pos, key, exp)
			p.expect(";")
			return cls
		}

		ThrowError(p.File, "invalid syntax, expected fn or class", p.lookahead.Pos)
	}

	if p.match("=") {
		p.expect("=")
		val = p.expression(true)
	}

	head := NewKeyValue(key.Pos, key, val)
	tail := head

	for tail != nil && p.match(",") {
		p.expect(",")

		key = p.identifier(false)
		if key == nil {
			ThrowError(p.File, "missing identifier after comma", p.lookahead.Pos)
		}

		val = nil
		if p.match("=") {
			p.expect("=")
			val = p.expression(true)
		}

		tail.Next = NewKeyValue(key.Pos, key, val)
		tail = tail.Next
	}

	p.expect(";")

	return NewVariableDeclaration(declarationType, head.Pos, head)
}

func (p *Parser) classExpression() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyClass)

	var base *Ast
	if p.match("(") {
		p.expect("(")
		base = p.identifier(true)
		p.expect(")")
	}

	p.expect("{")
	k := p.identifier(true)
	p.expect(":")
	v := p.expression(true)

	head := NewKeyValue(k.Pos, k, v)
	tail := head

	for tail != nil && p.match(",") {
		p.expect(",")
		k = p.identifier(true)
		p.expect(":")
		v = p.expression(true)
		tail.Next = NewKeyValue(k.Pos, k, v)
		tail = tail.Next
	}

	p.expect("}")
	return NewClassExpression(pos, base, head)
}

func (p *Parser) functionExpression() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyFn)

	p.expect("(")
	parameterHead := p.identifier(false)

	if p.match("...") {
		p.expect("...")
		parameterHead = NewSpread(parameterHead.Pos, parameterHead)
	}

	parameterTail := parameterHead
	for parameterTail != nil && p.match(",") {
		p.expect(",")
		ident := p.identifier(false)
		if ident == nil {
			ThrowError(p.File, "missing identifier after comma", p.lookahead.Pos)
		}
		if p.match("...") {
			p.expect("...")
			ident = NewSpread(ident.Pos, ident)
		}
		parameterTail.Next = ident
		parameterTail = parameterTail.Next
	}
	p.expect(")")

	astType := AstFunctionExpression

	if p.match(KeyAsync) {
		p.expect(KeyAsync)
		astType = AstAsyncFunctionExpression
	}

	p.expect("{")
	block := p.listOfBlockStatement()
	p.expect("}")

	return NewFunctionExpression(astType, pos, parameterHead, block)
}

/******************* Statements ***/

func (p *Parser) loopInitializer() *Ast {
	if p.match(KeyVar) || p.match(KeyLet) || p.match(KeyConst) {
		return p.variableDeclaration(p.lookahead.Value, false)
	}
	return p.expression(false)
}

func (p *Parser) isInitializer(ast *Ast) bool {
	if ast == nil {
		return false
	}
	switch ast.Type {
	case AstVar, AstLet, AstConst:
		return true
	case AstDestructuringArrayVar, AstDestructuringArrayLet, AstDestructuringArrayConst:
		return true
	case AstDestructuringObjectVar, AstDestructuringObjectLet, AstDestructuringObjectConst:
		return true
	}
	return false
}

func (p *Parser) tryCatch() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyTry)
	p.expect("{")
	tryBlock := p.listOfBlockStatement()
	p.expect("}")

	var catchParam, catchBlock *Ast
	if p.match(KeyCatch) {
		p.expect(KeyCatch)
		p.expect("(")
		catchParam = p.identifier(true)
		p.expect(")")
		p.expect("{")
		catchBlock = p.listOfBlockStatement()
		p.expect("}")
	}

	return NewTryCatch(pos, tryBlock, catchParam, catchBlock)
}

func (p *Parser) switchStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeySwitch)
	p.expect("(")
	condition := p.expression(true)
	p.expect(")")
	p.expect("{")

	var caseHead, caseTail *Ast
	hasDefault := false

	for p.match(KeyCase) || p.match(KeyDefault) {
		var caseNode *Ast
		if p.match(KeyCase) {
			p.expect(KeyCase)
			caseExpr := p.listOfExpression(true, false)
			p.expect(":")
			caseBlock := p.listOfBlockStatement()
			caseNode = NewKeyValue(caseExpr.Pos, caseExpr, caseBlock)
		} else {
			if hasDefault {
				ThrowError(p.File, "multiple default cases are not allowed", p.lookahead.Pos)
			}
			hasDefault = true
			defaultPos := p.lookahead.Pos
			p.expect(KeyDefault)
			p.expect(":")
			defaultBlock := p.listOfBlockStatement()
			caseNode = NewKeyValue(defaultPos, nil, defaultBlock)
		}

		if caseHead == nil {
			caseHead = caseNode
			caseTail = caseNode
		} else {
			caseTail.Next = caseNode
			caseTail = caseTail.Next
		}
	}

	p.expect("}")

	return NewSwitchStatement(pos, condition, caseHead)
}

func (p *Parser) ifStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyIf)
	p.expect("(")
	condition := p.expression(true)
	p.expect(")")
	thenBlock := p.blockLevelStatement()
	var elseBlock *Ast
	if p.match(KeyElse) {
		p.expect(KeyElse)
		elseBlock = p.blockLevelStatement()
	}
	return NewIfStatement(pos, condition, thenBlock, elseBlock)
}

func (p *Parser) loopStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyLoop)
	p.expect("(")
	init := p.loopInitializer()
	var cond, mutt *Ast
	whileMode := false
	if init != nil && p.isInitializer(init) {
		cond = p.expression(false)
		p.expect(";")
		mutt = p.expression(false)
	} else if p.match(";") {
		p.expect(";")
		cond = p.expression(false)
		p.expect(";")
		mutt = p.expression(false)
	} else {
		whileMode = true
	}
	p.expect(")")
	block := p.blockLevelStatement()

	if block == nil {
		ThrowError(p.File, "missing block after loop", p.lookahead.Pos)
	}

	if !whileMode {
		return NewForLoop(pos, init, cond, mutt, block)
	}

	return NewWhileLoop(pos, init, block)
}

func (p *Parser) logStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyLog)
	expr := p.expression(true)
	p.expect(";")
	return NewLogStatement(pos, expr)
}

func (p *Parser) continueStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyContinue)
	p.expect(";")
	return NewContinueStatement(pos)
}

func (p *Parser) breakStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyBreak)
	p.expect(";")
	return NewBreakStatement(pos)
}

func (p *Parser) returnStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyReturn)
	value := p.expression(false)
	p.expect(";")
	return NewReturnStatement(pos, value)
}

func (p *Parser) blockStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect("{")
	block := p.listOfBlockStatement()
	p.expect("}")
	return NewBlockStatement(pos, block)
}

func (p *Parser) expressionStatement() *Ast {
	node := p.expression(false)
	if node != nil {
		p.expect(";")
		for p.match(";") {
			p.expect(";")
		}
		return NewExpressionStatement(p.lookahead.Pos, node)
	}
	return nil
}

func (p *Parser) statement() *Ast {
	if p.match(KeyImport) {
		return p.importStatement()
	} else if p.match(KeyVar) {
		return p.variableDeclaration(KeyVar, true)
	} else if p.match(KeyConst) {
		return p.variableDeclaration(KeyConst, true)
	}
	return p.blockLevelStatement()
}

func (p *Parser) blockLevelStatement() *Ast {
	if p.match(KeyLet) || p.match(KeyConst) {
		return p.variableDeclaration(p.lookahead.Value, false)
	} else if p.match(KeySwitch) {
		return p.switchStatement()
	} else if p.match(KeyIf) {
		return p.ifStatement()
	} else if p.match(KeyLoop) {
		return p.loopStatement()
	} else if p.match(KeyTry) {
		return p.tryCatch()
	} else if p.match(KeyLog) {
		return p.logStatement()
	} else if p.match(KeyContinue) {
		return p.continueStatement()
	} else if p.match(KeyBreak) {
		return p.breakStatement()
	} else if p.match(KeyReturn) {
		return p.returnStatement()
	} else if p.match("{") {
		return p.blockStatement()
	}
	return p.expressionStatement()
}

func (p *Parser) program() *Ast {
	stmnts := p.listOfStatement()
	p.expectType(EOF)
	return NewProgram(stmnts)
}

func (p *Parser) Parse() *Ast {
	p.lookahead = p.getNextToken()
	return p.program()
}
func init() {
	for c := 'a'; c <= 'z'; c++ {
		class[c] |= clsIDStart | clsIDCont
		class[c-'a'+'A'] |= clsIDStart | clsIDCont
	}
	class['_'] |= clsIDStart | clsIDCont
	for c := '0'; c <= '9'; c++ {
		class[c] |= clsIDCont | clsDigit
	}
	for i := 0; i < len(symbolChars); i++ {
		c := symbolChars[i]
		class[c] |= clsSymbol
		singleSym[c] = symbolChars[i : i+1] // substring of a constant: no allocation
	}
	for _, c := range "'\"\\\n\r" {
		class[c] |= clsStop
	}
	for i := range hexVal {
		hexVal[i] = 0xFF
	}
	for c := '0'; c <= '9'; c++ {
		hexVal[c] = uint8(c - '0')
	}
	for c := 'a'; c <= 'f'; c++ {
		hexVal[c] = uint8(c-'a') + 10
		hexVal[c-'a'+'A'] = uint8(c-'a') + 10
	}

	// Only words that isKeyword accepts are added, so behavior can never diverge.
	keywords = make(map[string]string, 32)
	for _, k := range [...]string{
		KeyTrue, KeyFalse, KeyNull, KeyClass, KeyFn, KeySwitch, KeyAwait, KeyAsync,
		KeyImport, KeyFrom, KeyVar, KeyLet, KeyConst, KeyTry, KeyCatch, KeyCase,
		KeyDefault, KeyIf, KeyElse, KeyLoop, KeyContinue, KeyBreak, KeyReturn,
	} {
		if len(k) <= maxKeywordLen && isKeyword(k) {
			keywords[k] = k
		}
	}
}

func isDigit(r rune) bool {
	if r < 128 {
		return class[r]&clsDigit != 0
	}
	return unicode.IsDigit(r)
}

func isLineBreak(ch rune) bool {
	switch ch {
	case '\n', '\r', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}

func isSymbol(ch rune) bool {
	return ch < 128 && class[ch]&clsSymbol != 0
}

func hexValue(ch rune) (uint32, bool) {
	if ch < 128 {
		if v := hexVal[ch]; v != 0xFF {
			return uint32(v), true
		}
	}
	return 0, false
}

/******************* Cursor ***/

func NewParser(state *State, file *File) *Parser {
	return &Parser{state: state, File: file}
}

// symbolAt returns the operator starting at src[i] as a static string.
// The result is never allocated. Longest-match rules are identical to the old symbolLen.
func symbolAt(src []rune, i int) string {
	c := src[i]
	var d, e rune
	if i+1 < len(src) {
		d = src[i+1]
	}
	if i+2 < len(src) {
		e = src[i+2]
	}
	switch c {
	case '=':
		if d == '=' {
			return "=="
		}
		if d == '>' {
			return "=>"
		}
	case '!':
		if d == '=' {
			return "!="
		}
	case '*':
		if d == '=' {
			return "*="
		}
	case '/':
		if d == '=' {
			return "/="
		}
	case '%':
		if d == '=' {
			return "%="
		}
	case '<':
		if d == '=' {
			return "<="
		}
		if d == '<' {
			return "<<"
		}
	case '>':
		if d == '=' {
			return ">="
		}
		if d == '>' {
			return ">>"
		}
	case '&':
		if d == '&' {
			return "&&"
		}
	case '|':
		if d == '|' {
			return "||"
		}
	case '+':
		if d == '+' {
			return "++"
		}
		if d == '=' {
			return "+="
		}
	case '-':
		if d == '-' {
			return "--"
		}
		if d == '=' {
			return "-="
		}
		if d == '>' {
			return "->"
		}
	case ':':
		if d == ':' {
			return "::"
		}
	case '.':
		if d == '.' && e == '.' {
			return "..."
		}
	}
	return singleSym[c]
}

/******************* Lexer entry ***/

func postfixOperator(opt string) AstType {
	switch opt {
	case "++":
		return AstPostInc
	case "--":
		return AstPostDec
	}
	panic("invalid postfix operator")
}

func unaryOperator(opt string) AstType {
	switch opt {
	case "+":
		return AstPos
	case "++":
		return AstInc
	case "-":
		return AstNeg
	case "--":
		return AstDec
	case "!":
		return AstNot
	case "~":
		return AstBitNot
	case KeyAwait:
		return AstAwait
	}
	panic("invalid unary operator")
}

func binOperator(opt string) AstType {
	switch opt {
	case "*":
		return AstMul
	case "/":
		return AstDiv
	case "%":
		return AstMod
	case "+":
		return AstAdd
	case "-":
		return AstSub
	case "<<":
		return AstShiftLeft
	case ">>":
		return AstShiftRight
	case "<":
		return AstLess
	case "<=":
		return AstLessEqual
	case ">":
		return AstGreater
	case ">=":
		return AstGreaterEqual
	case "==":
		return AstEqual
	case "!=":
		return AstNotEqual
	case "&":
		return AstBitAnd
	case "^":
		return AstBitXor
	case "|":
		return AstBitOr
	case "&&":
		return AstAnd
	case "||":
		return AstOr
	case "=":
		return AstAssign
	case "*=":
		return AstMulAssign
	case "/=":
		return AstDivAssign
	case "%=":
		return AstModAssign
	case "+=":
		return AstAddAssign
	case "-=":
		return AstSubAssign
	case "<<=":
		return AstShiftLeftAssign
	case ">>=":
		return AstShiftRightAssign
	case "&=":
		return AstAndAssign
	case "^=":
		return AstXorAssign
	case "|=":
		return AstOrAssign
	}
	panic("invalid operator")
}
