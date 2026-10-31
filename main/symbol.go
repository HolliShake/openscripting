package main

type Symbol struct {
	Name    string
	Offset  int
	Const   bool
	Capture bool
	Pos     Position
}
