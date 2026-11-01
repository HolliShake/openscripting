package main

import (
	"os"
	"strconv"
	"unicode/utf8"
)

const ExitFailure = 1
const ExitSuccess = 0

// errScratch is static because passing w.buf to os.Stderr.Write forces a local errWriter onto the heap.
var errScratch errWriter

// errWriter is a fixed-size, stack-allocated buffered writer for stderr.
// Output is flushed whenever the buffer fills, so no heap growth is ever needed.
type errWriter struct {
	// buf is fixed-size so writing diagnostics does not allocate or retain a growing buffer.
	buf [2048]byte
	// n is the occupied prefix of buf and is reset whenever flush writes it to stderr.
	n int
}

func (w *errWriter) flush() {
	if w.n > 0 {
		_, _ = os.Stderr.Write(w.buf[:w.n])
		w.n = 0
	}
}

func (w *errWriter) str(s string) {
	for len(s) > 0 {
		if w.n == len(w.buf) {
			w.flush()
		}
		c := copy(w.buf[w.n:], s)
		w.n += c
		s = s[c:]
	}
}

func (w *errWriter) byte(b byte) {
	if w.n == len(w.buf) {
		w.flush()
	}
	w.buf[w.n] = b
	w.n++
}

func (w *errWriter) rune(r rune) {
	if len(w.buf)-w.n < utf8.UTFMax {
		w.flush()
	}
	// Capacity is guaranteed, so this appends in place without allocating.
	w.n = len(utf8.AppendRune(w.buf[:w.n], r))
}

func (w *errWriter) spaces(count int) {
	for ; count > 0; count-- {
		w.byte(' ')
	}
}

func (w *errWriter) int(value int) {
	if len(w.buf)-w.n < 20 {
		w.flush()
	}
	w.n = len(strconv.AppendInt(w.buf[:w.n], int64(value), 10))
}

func (w *errWriter) header(file *File, position Position) {
	name := "<unknown>"
	if file != nil && file.Name != "" {
		name = file.Name
	}
	w.byte('[')
	w.str(name)
	w.byte(':')
	w.int(position.Line + 1)
	w.byte(':')
	w.int(position.Column + 1)
	w.str("]: ")
}

func (w *errWriter) snippet(file *File, position Position) {
	var content []rune
	if file != nil {
		content = file.Content
	}

	lineCount := sourceLineCount(content)
	line := position.Line
	if line < 0 {
		line = 0
	}
	if line >= lineCount {
		line = lineCount - 1
	}
	column := position.Column
	if column < 0 {
		column = 0
	}
	start := line - 1
	if start < 0 {
		start = 0
	}
	end := line + 2
	if end >= lineCount {
		end = lineCount - 1
	}
	width := digitCount(end + 1)

	currentLine := 0
	lineStart := 0
	for index := 0; index <= len(content); index++ {
		if index < len(content) && !isSourceLineBreak(content[index]) {
			continue
		}
		if currentLine >= start && currentLine <= end {
			w.spaces(width - digitCount(currentLine+1))
			w.int(currentLine + 1)
			w.str(" | ")
			for i := lineStart; i < index; i++ {
				w.rune(content[i])
			}
			w.byte('\n')
			if currentLine == line {
				w.spaces(width)
				w.str(" | ")
				w.spaces(visualColumn(content[lineStart:index], column))
				w.str("^\n")
			}
		}
		if currentLine > end { // nothing more to print
			break
		}
		if index == len(content) {
			break
		}
		if content[index] == '\r' && index+1 < len(content) && content[index+1] == '\n' {
			index++
		}
		currentLine++
		lineStart = index + 1
	}
}
func digitCount(n int) int {
	d := 1
	for n >= 10 {
		n /= 10
		d++
	}
	return d
}

// formatError writes "[file:line:col]: <parts...>\n" followed by the source snippet.
// parts is not retained, so the variadic slice stays on the caller's stack.
func formatError(file *File, position Position, parts ...string) {
	w := &errScratch
	w.n = 0
	w.header(file, position)
	for _, part := range parts {
		w.str(part)
	}
	w.byte('\n')
	w.snippet(file, position)
	w.flush()
}

func FormatError(file *File, message string, position Position) {
	formatError(file, position, message)
}

func ThrowError(file *File, message string, position Position) {
	formatError(file, position, message)
	os.Exit(ExitFailure)
}

func ThrowExpectedTokenError(file *File, expected, actual string, position Position) {
	formatError(file, position, `expected token "`, expected, `", got "`, actual, `"`)
	os.Exit(ExitFailure)
}

func sourceLineCount(content []rune) int {
	lineCount := 1
	for index := 0; index < len(content); index++ {
		if !isSourceLineBreak(content[index]) {
			continue
		}
		lineCount++
		if content[index] == '\r' && index+1 < len(content) && content[index+1] == '\n' {
			index++
		}
	}
	return lineCount
}

func isSourceLineBreak(character rune) bool {
	return character == '\r' || character == '\n' || character == '\u0085' ||
		character == '\u2028' || character == '\u2029'
}

func visualColumn(line []rune, column int) int {
	if column > len(line) {
		column = len(line)
	}
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
