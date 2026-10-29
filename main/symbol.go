package main

type Symbol struct {
	Name    string
	Offset  int32
	Const   bool
	Capture bool
	Pos     Position
}
