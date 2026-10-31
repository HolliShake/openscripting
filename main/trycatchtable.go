package main

type TryCatchTable struct {
	TryStart   int // Opcode index
	TryEnd     int // Opcode index
	CatchStart int // Opcode index
	CatchEnd   int // Opcode index
	CatchLocal int // Local variable index, or -1 when catch has no parameter
}
