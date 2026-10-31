package main

type State struct {
	File        []*File
	Imports     map[string]*Import
	ImportStack []string
	Functions   []*Function
}

func (s *State) SaveFunction(f *Function) int {
	offset := len(s.Functions)
	s.Functions = append(s.Functions, f)
	return offset
}
