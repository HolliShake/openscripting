package main

type State struct {
	File      []*File
	Imports   map[string]*Import
	Functions []*Function
}

func (s *State) SaveFunction(f *Function) int32 {
	offset := int32(len(s.Functions))
	s.Functions = append(s.Functions, f)
	return offset
}
