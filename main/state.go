package main

type State struct {
	// File retains source files compiled in this state so functions and imports keep diagnostic data alive.
	File []*File
	// Imports caches modules by resolved path and tracks initialization/cycle state.
	Imports map[string]*Import
	// ImportStack records the active import chain to produce useful circular-import diagnostics.
	ImportStack []string
	// Functions is the stable index space addressed by OpLoadFunction operands.
	Functions []*Function
}

func (s *State) SaveFunction(f *Function) int {
	offset := len(s.Functions)
	s.Functions = append(s.Functions, f)
	return offset
}
