package main

const (
	KeyImport   = "import"
	KeyFrom     = "from"
	KeyVar      = "var"
	KeyLet      = "let"
	KeyConst    = "const"
	KeyLog      = "log"
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
	KeyThrow    = "throw"
	KeyTypeOf   = "typeof"
	KeyAwait    = "await"
	KeyYield    = "yield"
	KeyTrue     = "true"
	KeyFalse    = "false"
	KeyNull     = "null"
	KeyThis     = "this"
	KeySuper    = "super"
)

func isKeyword(value string) bool {
	switch value {
	case KeyImport, KeyFrom, KeyVar, KeyLet, KeyConst, KeyLog, KeyAsync, KeyClass,
		KeyFn, KeyLoop, KeyTry, KeyCatch, KeySwitch, KeyCase, KeyDefault,
		KeyIf, KeyElse, KeyContinue, KeyBreak, KeyReturn, KeyThrow, KeyTypeOf, KeyAwait, KeyYield,
		KeyTrue, KeyFalse, KeyNull, KeyThis, KeySuper:
		return true
	default:
		return false
	}
}
