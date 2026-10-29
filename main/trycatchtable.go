package main

type TryCatchTable struct {
	TryStart   int32 // Opcode index
	TryEnd     int32 // Opcode index
	CatchStart int32 // Opcode index
	CatchEnd   int32 // Opcode index
	CatchLocal int32 // Local variable index, or -1 when catch has no parameter
}
