package main

import (
	"flag"
	"fmt"
	"os"
	"unicode/utf8"
)

var programState State
var scriptFile string
var showHelp bool

type RGB struct {
	R, G, B int
}

func gradientColor(t float64) RGB {
	stops := []RGB{
		{40, 50, 255},  // Royal blue
		{75, 55, 240},  // Indigo
		{30, 110, 255}, // Electric blue
		{0, 210, 255},  // Cyan
	}

	if t <= 0 {
		return stops[0]
	}

	if t >= 1 {
		return stops[len(stops)-1]
	}

	segments := len(stops) - 1
	position := t * float64(segments)
	index := int(position)
	local := position - float64(index)

	a := stops[index]
	b := stops[index+1]

	return RGB{
		R: int(float64(a.R) + local*float64(b.R-a.R)),
		G: int(float64(a.G) + local*float64(b.G-a.G)),
		B: int(float64(a.B) + local*float64(b.B-a.B)),
	}
}

func printGradientLogo() {
	logo := []string{
		" ██████╗ ██████╗ ███████╗███╗   ██╗███████╗ ██████╗██████╗ ██╗██████╗ ████████╗",
		"██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔════╝██╔════╝██╔══██╗██║██╔══██╗╚══██╔══╝",
		"██║   ██║██████╔╝█████╗  ██╔██╗ ██║███████╗██║     ██████╔╝██║██████╔╝   ██║",
		"██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║╚════██║██║     ██╔══██╗██║██╔═══╝    ██║",
		"╚██████╔╝██║     ███████╗██║ ╚████║███████║╚██████╗██║  ██║██║██║        ██║",
		" ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚══════╝ ╚═════╝╚═╝  ╚═╝╚═╝╚═╝        ╚═╝",
	}

	fmt.Println()

	for _, line := range logo {
		runes := []rune(line)
		width := len(runes) - 1

		if width < 1 {
			width = 1
		}

		for i, ch := range runes {
			t := float64(i) / float64(width)
			c := gradientColor(t)

			fmt.Printf(
				"\033[38;2;%d;%d;%dm%c",
				c.R, c.G, c.B, ch,
			)
		}

		fmt.Print("\033[0m\n")
	}
}

func main() {
	flag.StringVar(&scriptFile, "f", "", "path to the script file")
	flag.StringVar(&scriptFile, "file", "", "path to the script file")
	flag.BoolVar(&showHelp, "help", false, "print this help")

	flag.Usage = func() {
		printGradientLogo()

		fmt.Printf(
			"\033[1;97mUsage:\033[0m %s -f path/to/file.os\n\n",
			os.Args[0],
		)

		fmt.Println("\033[1;97mOptions:\033[0m")
		flag.PrintDefaults()
	}

	flag.Parse()

	if showHelp {
		flag.Usage()
		return
	}

	if scriptFile == "" {
		fmt.Fprintln(
			os.Stderr,
			"a script file is required; use -f or -file",
		)
		flag.Usage()
		os.Exit(2)
	}

	data, err := os.ReadFile(scriptFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading script: %v\n", err)
		os.Exit(1)
	}

	content := make([]rune, utf8.RuneCount(data))

	for index := 0; len(data) > 0; index++ {
		value, size := utf8.DecodeRune(data)
		content[index] = value
		data = data[size:]
	}

	state := &programState

	parser := NewParser(state, &File{
		Name:    scriptFile,
		Content: content,
	})

	program := Compile(parser)
	Run(state, program)
}
