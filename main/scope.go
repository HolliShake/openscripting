package main

func (s *Scope) ExistsLocal(name string) bool {
	if _, exists := s.Variables[name]; exists {
		return true
	}
	return false
}

func (s *Scope) Exists(name string) bool {
	if _, exists := s.Variables[name]; exists {
		return true
	}
	if s.Parent != nil {
		return s.Parent.Exists(name)
	}
	return false
}

func (s *Scope) AddLocal(name string, sym Symbol) (status bool, msg string) {
	if _, exists := s.Variables[name]; exists {

		return false, "variable already exists in scope"
	}
	s.Variables[name] = sym
	s.VarCount++
	return true, "variable added successfully"
}

func (s *Scope) GetVariable(name string) (sym Symbol, exists bool) {
	if sym, exists = s.Variables[name]; exists {
		return sym, true
	}
	if s.Parent != nil {
		return s.Parent.GetVariable(name)
	}
	return Symbol{}, false
}

func (s *Scope) GetVarCount() int32 {
	return s.VarCount
}
