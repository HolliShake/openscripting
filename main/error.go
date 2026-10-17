package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const ExitFailure = 1
const ExitSuccess = 0

func FormatError(file *File, message string, position Position) {
	fmt.Fprint(os.Stderr, formatError(file, message, position))
}

func formatError(file *File, message string, position Position) string {
	name := "<unknown>"
	var lines [][]rune
	if file != nil {
		if file.Name != "" {
			name = file.Name
		}
		lines = splitSourceLines(file.Content)
	} else {
		lines = [][]rune{{}}
	}

	line := position.Line
	if line < 0 {
		line = 0
	}
	if line >= len(lines) {
		line = len(lines) - 1
	}
	column := position.Column
	if column < 0 {
		column = 0
	}
	if column > len(lines[line]) {
		column = len(lines[line])
	}

	start := line - 1
	if start < 0 {
		start = 0
	}
	end := line + 2
	if end >= len(lines) {
		end = len(lines) - 1
	}
	width := len(strconv.Itoa(end + 1))

	var output strings.Builder
	fmt.Fprintf(&output, "[%s:%d:%d]: %s\n", name, position.Line+1, position.Column+1, message)
	for index := start; index <= end; index++ {
		fmt.Fprintf(&output, "%*d | %s\n", width, index+1, string(lines[index]))
		if index == line {
			fmt.Fprintf(&output, "%s | %s^\n", strings.Repeat(" ", width), strings.Repeat(" ", visualColumn(lines[index], column)))
		}
	}
	return output.String()
}

func splitSourceLines(content []rune) [][]rune {
	lines := make([][]rune, 0, 1)
	start := 0
	for index, character := range content {
		if character != '\r' && character != '\n' && character != '\u0085' && character != '\u2028' && character != '\u2029' {
			continue
		}
		lines = append(lines, append([]rune(nil), content[start:index]...))
		if character == '\r' && index+1 < len(content) && content[index+1] == '\n' {
			start = index + 2
		} else {
			start = index + 1
		}
	}
	lines = append(lines, append([]rune(nil), content[start:]...))
	return lines
}

func visualColumn(line []rune, column int) int {
	visual := 0
	for _, character := range line[:column] {
		if character == '\t' {
			visual += 4 - visual%4
		} else {
			visual++
		}
	}
	return visual
}

func ThrowError(file *File, message string, position Position) {
	FormatError(file, message, position)
	os.Exit(ExitFailure)
}
