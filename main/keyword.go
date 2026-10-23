package main

const (
	KeyImport   = "import"
	KeyFrom     = "from"
	KeyVar      = "var"
	KeyLet      = "let"
	KeyConst    = "const"
	KeyAsync    = "async"
	KeyClass    = "class"
	KeyFn       = "fn"
	KeyLoop     = "loop"
	KeyTry      = "try"
	KeyCatch    = "catch"
	KeySwitch   = "switch"
	KeyCase     = "case"
	KeyDefault  = "default"
	KeyIf       = "if"
	KeyElse     = "else"
	KeyContinue = "continue"
	KeyBreak    = "break"
	KeyReturn   = "return"
	KeyAwait    = "await"
	KeyTrue     = "true"
	KeyFalse    = "false"
	KeyNull     = "null"
)

var keywords = map[string]struct{}{
	KeyImport:   {},
	KeyFrom:     {},
	KeyVar:      {},
	KeyLet:      {},
	KeyConst:    {},
	KeyAsync:    {},
	KeyClass:    {},
	KeyFn:       {},
	KeyLoop:     {},
	KeyTry:      {},
	KeyCatch:    {},
	KeySwitch:   {},
	KeyCase:     {},
	KeyDefault:  {},
	KeyIf:       {},
	KeyElse:     {},
	KeyContinue: {},
	KeyBreak:    {},
	KeyReturn:   {},
	KeyAwait:    {},
	KeyTrue:     {},
	KeyFalse:    {},
	KeyNull:     {},
}

func isKeyword(value string) bool {
	_, ok := keywords[value]
	return ok
}
