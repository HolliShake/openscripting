package main

const (
	OpLoadNum = iota + 59
	OpLoadStr
	OpLoadTrue
	OpLoadFalse
	OpLoadNull
	OpLoadFunction
	OpUnaryPos
	OpUnaryNeg
	OpUnaryNot
	OpUnaryBitNot
	OpBinMul
	OpBinDiv
	OpBinMod
	OpBinAdd
	OpBinSub
	OpBinShiftLeft
	OpBinShiftRight
	OpBinLess
	OpBinLessEqual
	OpBinGreater
	OpBinGreaterEqual
	OpBinEqual
	OpBinNotEqual
	OpBinBitAnd
	OpBinBitXor
	OpBinBitOr
	OpBinAnd
	OpBinOr
	OpPopJmpIfFalse
	OpPopJmpIfTrue
	OpJmpIfFalseOrPop
	OpJmpIfTrueOrPop
	OpStoreLocal
	OpReturn
)
