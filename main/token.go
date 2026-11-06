package main

import "strconv"

const (
	KEY TokenType = iota
	IDN
	NUM
	STR
	SYM
	ILL
	EOF
)

type TokenType int

type Token struct {
	// Type distinguishes identifiers, literals, symbols, keywords, and end/error markers.
	Type TokenType
	// Value is the token's decoded text consumed by grammar and compiler routines.
	Value string
	// Pos is the token start location and anchors syntax errors to the source.
	Pos Position
}

func (s TokenType) String() string {
	switch s {
	case KEY:
		return "keyword"
	case IDN:
		return "ident"
	case NUM:
		return "number"
	case STR:
		return "string"
	case SYM:
		return "symbol"
	case ILL:
		return "illegal"
	case EOF:
		return "eof"
	default:
		return "unknown"
	}
}

func (t Token) String() string {
	typeStr := t.Type.String()
	valStr := strconv.Quote(t.Value)
	posStr := t.Pos.String()
	return "Token {Type: " + typeStr + ", Value: " + valStr + ", Pos: " + posStr + "}"
}
