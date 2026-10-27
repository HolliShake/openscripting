package main

const (
	OpLoadNum = iota + 59
	OpLoadStr
	OpLoadTrue
	OpLoadFalse
	OpLoadNull
	OpMakeArray
	OpLoadFunction
	OpLoadCap
	OpLoadVar
	OpCall
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
	OpDupTop
	OpJmp
	OpPopJmpIfFalse
	OpPopJmpIfTrue
	OpJmpIfFalseOrPop
	OpJmpIfTrueOrPop
	OpStoreCap
	OpStoreLocal
	OpStdWrite
	OpReturn
)
