package main

import (
	"fmt"
	"strconv"
	"unicode"
)

/******************* Character classes (package-level tables, no struct changes) ***/

const (
	clsIDStart uint8 = 1 << iota
	clsIDCont
	clsDigit
	clsSymbol
	clsStop // ends a plain run inside a string literal
)

var (
	class           [128]uint8
	hexVal          [128]uint8 // 0xFF = not a hex digit
	otherIDStart    = unicode.Properties["Other_ID_Start"]
	otherIDContinue = unicode.Properties["Other_ID_Continue"]
)

func init() {
	for c := 'a'; c <= 'z'; c++ {
		class[c] |= clsIDStart | clsIDCont
		class[c-'a'+'A'] |= clsIDStart | clsIDCont
	}
	class['_'] |= clsIDStart | clsIDCont
	for c := '0'; c <= '9'; c++ {
		class[c] |= clsIDCont | clsDigit
	}
	for _, c := range "+-*/%=!<>&|^~?:.,;(){}[]#@\\" {
		class[c] |= clsSymbol
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
}

func isDigit(r rune) bool {
	if r < 128 {
		return class[r]&clsDigit != 0
	}
	return unicode.IsDigit(r)
}

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
	return &Parser{state: state, file: file}
}

func (p *Parser) pushError(err string) {
	p.errors = append(p.errors, err)
}

func (p *Parser) current() rune {
	if p.offset >= len(p.file.Content) {
		return 0
	}
	return p.file.Content[p.offset]
}

func (p *Parser) peek(distance int) rune {
	index := p.offset + distance
	if index >= len(p.file.Content) {
		return 0
	}
	return p.file.Content[index]
}

func (p *Parser) advance() rune {
	src := p.file.Content
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
func (p *Parser) emit(t TokenType, start, end int, pos Position) Token {
	p.colm += end - start
	p.offset = end
	return Token{Type: t, Value: string(p.file.Content[start:end]), Pos: pos}
}

/******************* Trivia ***/

func (p *Parser) skipTrivia() (Token, bool) {
	src := p.file.Content
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
			return Token{}, false
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
				ThrowError(p.file, "unterminated block comment", pos)
			}
			p.offset += 2
			p.colm += 2
		default:
			return Token{}, false
		}
	}
}

/******************* Scanners ***/

func (p *Parser) scanEscape() {
	src := p.file.Content
	n := len(src)
	pos := Position{Line: p.line, Column: p.colm}
	p.offset++ // backslash
	p.colm++
	if p.offset >= n || isLineBreak(src[p.offset]) {
		ThrowError(p.file, "invalid escape sequence", pos)
	}

	escape := p.advance()
	switch escape {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', '\'', '"', '0':
		return
	case 'x', 'u', 'U':
		width := 2
		if escape == 'u' {
			width = 4
		} else if escape == 'U' {
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
				ThrowError(p.file, "invalid escape sequence", pos)
			}
			value = value<<4 | d
			digits++
			p.offset++
			p.colm++
		}

		if braced {
			if digits == 0 || digits > 6 || p.offset >= n || src[p.offset] != '}' {
				ThrowError(p.file, "invalid escape sequence", pos)
			}
			p.offset++
			p.colm++
		} else if digits != width {
			ThrowError(p.file, "invalid escape sequence", pos)
		}
		if escape != 'x' && (value > 0x10ffff || value >= 0xd800 && value <= 0xdfff) {
			ThrowError(p.file, "invalid escape sequence", pos)
		}
	default:
		ThrowError(p.file, "invalid escape sequence", pos)
	}
}

func (p *Parser) scanString(pos Position) Token {
	src := p.file.Content
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
			ThrowError(p.file, "unterminated string literal", pos)
		default: // the other quote character
			p.offset++
			p.colm++
		}
	}

	if !terminated {
		ThrowError(p.file, "unterminated string literal", pos)
	}
	return Token{Type: STR, Value: string(src[start:p.offset]), Pos: pos}
}

func (p *Parser) scanNumber(pos Position, start int) Token {
	src := p.file.Content
	n := len(src)
	i := start

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
				ThrowError(p.file, "invalid numeric literal", pos)
			}
			return p.emit(NUM, start, i, pos)
		}
	}

	for i < n && (isDigit(src[i]) || src[i] == '_') {
		i++
	}
	if i+1 < n && src[i] == '.' && isDigit(src[i+1]) {
		i++
		for i < n && (isDigit(src[i]) || src[i] == '_') {
			i++
		}
	}
	if i < n && (src[i] == 'e' || src[i] == 'E') {
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
	return p.emit(NUM, start, i, pos)
}

func (p *Parser) scanIdent(pos Position, start int) Token {
	src := p.file.Content
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
	tok := p.emit(IDN, start, i, pos)
	if isKeyword(tok.Value) {
		tok.Type = KEY
	}
	return tok
}

// symbolLen returns 1-3: the length of the operator starting at src[i].
func symbolLen(src []rune, i int) int {
	if i+1 >= len(src) {
		return 1
	}
	c, d := src[i], src[i+1]
	switch c {
	case '=':
		if d == '=' || d == '>' {
			return 2
		}
	case '!', '*', '/', '%':
		if d == '=' {
			return 2
		}
	case '<', '>':
		if d == '=' || d == c {
			return 2
		}
	case '&', '|':
		if d == c {
			return 2
		}
	case '+':
		if d == '+' || d == '=' {
			return 2
		}
	case '-':
		if d == '-' || d == '=' || d == '>' {
			return 2
		}
	case ':':
		if d == ':' {
			return 2
		}
	case '.':
		if d == '.' && i+2 < len(src) && src[i+2] == '.' {
			return 3
		}
	}
	return 1
}

/******************* Lexer entry ***/

func (p *Parser) getNextToken() Token {
	if tok, ok := p.skipTrivia(); ok {
		return tok
	}
	src := p.file.Content
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
			return p.scanNumber(pos, i)
		case ch == '"' || ch == '\'':
			return p.scanString(pos)
		case cls&clsSymbol != 0:
			return p.emit(SYM, i, i+symbolLen(src, i), pos)
		}
		ThrowError(p.file, fmt.Sprintf("unexpected character %q", ch), pos)
	}

	if p.isIdentifierStart(ch) {
		return p.scanIdent(pos, i)
	}
	if unicode.IsDigit(ch) {
		return p.scanNumber(pos, i)
	}
	p.advance()
	ThrowError(p.file, fmt.Sprintf("unexpected character %q", ch), pos)
	return Token{}
}

/******************* Parser helpers ***/

func (p *Parser) match(token any) bool {
	if token, ok := token.(string); ok {
		return (p.lookahead.Type == IDN ||
			p.lookahead.Type == KEY ||
			p.lookahead.Type == SYM) && token == p.lookahead.Value
	}
	if token, ok := token.(TokenType); ok {
		return p.lookahead.Type == token
	}
	return false
}

func (p *Parser) expect(token any) {
	if p.match(token) {
		p.lookahead = p.getNextToken()
		return
	}
	ThrowError(p.file, fmt.Sprintf("expected token \"%s\", got \"%s\"", token, p.lookahead.Value), p.lookahead.Pos)
}

func (p *Parser) identifier(required bool) *Ast {
	if !p.match(IDN) && required {
		ThrowError(p.file, "expected identifier", p.lookahead.Pos)
	}
	if !p.match(IDN) {
		return nil
	}
	ast := NewTerminalAst(AstIdentifier, p.lookahead.Pos, p.lookahead.Value)
	p.expect(IDN)
	return ast
}

func (p *Parser) listOfStatement() *Ast {
	var head *Ast = p.statement()
	tail := head

	for tail != nil {
		stmt := p.statement()
		tail.Next = stmt
		tail = tail.Next
	}

	return head
}

func (p *Parser) listOfBlockStatement() *Ast {
	var head *Ast = p.blockLevelStatement()
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
		{
			return p.identifier(true)
		}
	case NUM:
		{
			v, err := strconv.ParseFloat(p.lookahead.Value, 64)
			if err != nil {
				ThrowError(p.file, "invalid number format", p.lookahead.Pos)
			}
			ast := NewTerminalAst(AstNumLit, p.lookahead.Pos, v)
			p.expect(NUM)
			return ast
		}
	case STR:
		{
			ast := NewTerminalAst(AstStrLit, p.lookahead.Pos, p.lookahead.Value)
			p.expect(STR)
			return ast
		}
	case KEY:
		{
			if p.lookahead.Value == KeyTrue || p.lookahead.Value == KeyFalse {
				ast := NewTerminalAst(AstBoolLit, p.lookahead.Pos, p.lookahead.Value == KeyTrue)
				p.expect(KEY)
				return ast
			}
			if p.lookahead.Value == KeyNull {
				ast := NewTerminalAst(AstNullLit, p.lookahead.Pos, nil)
				p.expect(KEY)
				return ast
			}
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
	}
	return p.terminal()
}

func (p *Parser) memberOrCall() *Ast {
	obj := p.group()

	if obj == nil {
		return nil
	}

	for p.match("->") || p.match("[") || p.match("(") {
		if p.match("->") {
			p.expect("->")
			prop := p.identifier(true)
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
		}
	}

	return obj
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

func (p *Parser) expression(required bool) *Ast {
	node := p.orAssign()
	if node == nil && required {
		ThrowError(p.file, "missing expression", p.lookahead.Pos)
	}
	return node
}

// Precedence methods below are ordered from tightest to loosest.
func (p *Parser) multiplicative() *Ast {
	lhs := p.memberOrCall()

	if lhs == nil {
		return nil
	}

	for p.match("*") || p.match("/") || p.match("%") {
		opt := p.lookahead.Value
		p.expect(opt)

		rhs := p.memberOrCall()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(
			binOperator(opt),
			lhs.Pos,
			lhs,
			rhs,
		)
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
		p.expect(opt)
		rhs := p.multiplicative()

		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(
			binOperator(opt),
			lhs.Pos,
			lhs,
			rhs,
		)
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
		p.expect(opt)

		rhs := p.additive()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.shift()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.relational()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.equality()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.bitwiseAnd()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.bitwiseXor()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.bitwiseOr()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.logicalAnd()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.assignment()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.mulAssign()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.addAssign()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.shiftAssign()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.andAssign()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.xorAssign()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
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
		p.expect(opt)

		rhs := p.orAssign()
		if rhs == nil {
			ThrowError(p.file, "missing right-hand operand", p.lookahead.Pos)
		}

		lhs = NewBinaryOperation(binOperator(opt), lhs.Pos, lhs, rhs)
	}

	return lhs
}

func (p *Parser) spreadOrExpression(required bool, spread bool) *Ast {
	if spread && p.match("...") {
		p.expect("...")
		return NewSpread(p.lookahead.Pos, p.expression(true))
	}
	return p.expression(required)
}

func (p *Parser) listOfExpression(required bool, spread bool) *Ast {
	var head *Ast = p.spreadOrExpression(required, spread)
	var tail *Ast = head

	for tail != nil && p.match(",") {
		p.expect(",")
		tail.Next = p.spreadOrExpression(false, spread) // expression or spread
		if tail.Next == nil {
			ThrowError(p.file, "missing expression after comma", p.lookahead.Pos)
		}
		tail = tail.Next
	}

	return head
}

func (p *Parser) key(required bool) *Ast {
	if p.match(IDN) || p.match(STR) {
		node := p.terminal()
		if node == nil && required {
			ThrowError(p.file, "missing key", p.lookahead.Pos)
		}
		return node
	}
	if required {
		ThrowError(p.file, "missing key", p.lookahead.Pos)
	}
	return nil
}

func (p *Parser) keyValue(required bool, spread bool) *Ast {
	if spread && p.match("...") {
		p.expect("...")
		return NewSpread(p.lookahead.Pos, p.expression(true))
	}

	k := p.key(required)
	p.expect(":")
	v := p.expression(false)
	if v == nil {
		ThrowError(p.file, "missing value for key", p.lookahead.Pos)
	}
	return NewKeyValue(k.Pos, k, v)
}

func (p *Parser) listOfKeyValues(required bool, spread bool) *Ast {
	var head *Ast = p.keyValue(required, spread)
	var tail *Ast = head

	for tail != nil && p.match(",") {
		p.expect(",")
		tail.Next = p.keyValue(false, spread)
		if tail.Next == nil {
			ThrowError(p.file, "missing key-value after comma", p.lookahead.Pos)
		}
		tail = tail.Next
	}

	return head
}

/******************* Declarations ***/

func (p *Parser) variableDeclaration(keyWord string) *Ast {
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
		ThrowError(p.file, "invalid variable declaration", position)
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
				ThrowError(p.file, "missing identifier after comma", p.lookahead.Pos)
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
		var k *Ast = p.key(true)
		var v *Ast = nil

		if p.match(":") {
			p.expect(":")
			v = p.expression(true)
		}

		var head *Ast = k
		var tail *Ast = head

		if k != nil && v != nil {
			head = NewKeyValue(k.Pos, k, v)
			tail = head
		}

		for tail != nil && p.match(",") {
			p.expect(",")

			k = p.key(false)
			v = nil

			if p.match(":") {
				p.expect(":")
				v = p.expression(true)
			}

			if k != nil && v != nil {
				tail.Next = NewKeyValue(k.Pos, k, v)
			} else {
				tail.Next = k
			}

			tail = tail.Next
		}
		p.expect("}")
		p.expect("=")
		value := p.expression(true)
		p.expect(";")
		return NewDestructuringObjectDeclaration(objectDestructuringType, position, head, value)
	}

	// regular variable dec
	var key *Ast = p.identifier(true)
	var val *Ast = nil

	// function or class
	if p.match("::") {
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

		ThrowError(p.file, "invalid syntax, expected fn or class", p.lookahead.Pos)
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
			ThrowError(p.file, "missing identifier after comma", p.lookahead.Pos)
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

	var base *Ast = nil
	if p.match("(") {
		p.expect("(")
		base = p.identifier(true)
		p.expect(")")
	}

	p.expect("{")
	var k *Ast = p.identifier(true)
	p.expect(":")
	var v *Ast = p.expression(true)

	var head *Ast = NewKeyValue(k.Pos, k, v)
	var tail *Ast = head

	for tail != nil && p.match(",") {
		p.expect(",")
		k = p.identifier(true)
		if k == nil {
			ThrowError(p.file, "missing identifier after comma", p.lookahead.Pos)
		}
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
	var parameterHead *Ast = p.identifier(false)
	var parameterTail *Ast = parameterHead
	for parameterTail != nil && p.match(",") {
		p.expect(",")
		var ident *Ast = p.identifier(false)
		if ident == nil {
			ThrowError(p.file, "missing identifier after comma", p.lookahead.Pos)
		}
		parameterTail.Next = ident
		parameterTail = parameterTail.Next
	}
	p.expect(")")

	async := p.match(KeyAsync)
	if async {
		p.expect(KeyAsync)
	}

	p.expect("{")
	block := p.listOfBlockStatement()
	p.expect("}")

	return NewFunctionExpression(pos, parameterHead, block)
}

/******************* Statements ***/

func (p *Parser) loopInitializer() *Ast {
	if p.match(KeyVar) || p.match(KeyLet) || p.match(KeyConst) {
		return p.variableDeclaration(p.lookahead.Value)
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

func (p *Parser) ifStatement() *Ast {
	pos := p.lookahead.Pos
	p.expect(KeyIf)
	p.expect("(")
	condition := p.expression(true)
	p.expect(")")
	thenBlock := p.blockLevelStatement()
	var elseBlock *Ast = nil
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
	var init *Ast = p.loopInitializer()
	var cond *Ast = nil
	var mutt *Ast = nil
	whileMode := false
	if init != nil && p.isInitializer(init) {
		cond = p.expression(false)
		p.expect(";")
		mutt = p.expression(false)
	} else {
		if p.match(";") {
			p.expect(";")
			cond = p.expression(false)
			p.expect(";")
			mutt = p.expression(false)
		} else {
			whileMode = true
		}
	}
	p.expect(")")
	block := p.blockLevelStatement()

	if block == nil {
		ThrowError(p.file, "missing block after loop", p.lookahead.Pos)
	}

	if !whileMode {
		return NewForLoop(pos, init, cond, mutt, block)
	}

	return NewWhileLoop(pos, init, block)
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
	if p.match(KeyVar) {
		return p.variableDeclaration(KeyVar)
	}
	return p.blockLevelStatement()
}

func (p *Parser) blockLevelStatement() *Ast {
	if p.match(KeyLet) || p.match(KeyConst) {
		return p.variableDeclaration(p.lookahead.Value)
	} else if p.match(KeyIf) {
		return p.ifStatement()
	} else if p.match(KeyLoop) {
		return p.loopStatement()
	} else if p.match("{") {
		return p.blockStatement()
	}
	return p.expressionStatement()
}

func (p *Parser) program() *Ast {
	stmnts := p.listOfStatement()
	p.expect(EOF)
	return NewProgram(stmnts)
}

func (p *Parser) Parse() *Ast {
	p.lookahead = p.getNextToken()
	return p.program()
}
