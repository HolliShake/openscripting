package main

import (
	"fmt"
	"strconv"
)

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
	Type  TokenType
	Value string
	Pos   Position
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
	return fmt.Sprintf("Token {Type: %s, Value: %s, Pos: %s}", typeStr, valStr, posStr)
}
