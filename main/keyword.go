package main

const (
	KeyImport = "import"
	KeyVar    = "var"
	KeyLet    = "let"
	KeyConst  = "const"
	KeyAsync  = "async"
	KeyClass  = "class"
	KeyFn     = "fn"
	KeyLoop   = "loop"
	KeyIf     = "if"
	KeyElse   = "else"
	KeyTrue   = "true"
	KeyFalse  = "false"
	KeyNull   = "null"
)

var keywords = map[string]struct{}{
	KeyImport: {},
	KeyVar:    {},
	KeyLet:    {},
	KeyConst:  {},
	KeyAsync:  {},
	KeyClass:  {},
	KeyFn:     {},
	KeyLoop:   {},
	KeyIf:     {},
	KeyElse:   {},
	KeyTrue:   {},
	KeyFalse:  {},
	KeyNull:   {},
}

func isKeyword(value string) bool {
	_, ok := keywords[value]
	return ok
}
