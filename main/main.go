package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("Hello, World!")

	file := "C:\\Users\\dtoDev\\Desktop\\openscripting\\tests\\file.os"
	data, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	content := []rune(string(data))

	state := State{}
	parser := NewParser(&state, &File{
		Name:    file,
		Content: content,
	})
	program := Compile(parser)
	Run(&state, program)
}
