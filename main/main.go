package main

import (
	"fmt"
	"os"
	"unicode/utf8"
)

func main() {
	fmt.Println("Hello, World!")

	file := "C:\\Users\\dtoDev\\Desktop\\openscripting\\tests\\file.os"
	data, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	content := make([]rune, utf8.RuneCount(data))
	for index := 0; len(data) > 0; index++ {
		value, size := utf8.DecodeRune(data)
		content[index] = value
		data = data[size:]
	}

	state := State{}
	parser := NewParser(&state, &File{
		Name:    file,
		Content: content,
	})
	program := Compile(parser)
	Run(&state, program)
}
