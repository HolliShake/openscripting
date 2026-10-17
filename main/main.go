package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")

	state := State{}
	parser := NewParser(&state, &File{
		Name:    "example.os",
		Content: []rune("2 + 3"),
	})
	parser.Parse()
}
