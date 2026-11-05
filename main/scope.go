package main

const (
	ScopeTypeGlobal ScopeType = iota
	ScopeTypeFunction
	ScopeTypeBlock
	ScopeTypeLoop
	ScopeTypeSwitch
	ScopeTypeCase
	ScopeTypeModule
)

type ScopeType byte

type Scope struct {
	// Type controls declaration rules and which control-flow constructs this lexical scope represents.
	Type ScopeType
	// Parent preserves lexical visibility across nested blocks and enclosing functions.
	Parent *Scope
	// Frame points to the shared slot allocator for this function/module; nested blocks must not reuse offsets.
	Frame *Scope
	// Variables contains only names declared in this lexical scope, enabling shadowing and scope checks.
	Variables map[string]Symbol
	// VarCount is the number of frame slots allocated when this scope owns the frame.
	VarCount int
	// LoopStart is the bytecode destination for continue in the active loop.
	LoopStart int
	// LoopContinue is the destination after the body for the active loop.
	LoopContinue int
	// BreakStart stores jump operands patched to the end of the enclosing loop/switch.
	BreakStart []int
	// ContinueStart stores deferred continue jumps that require target patching.
	ContinueStart []int
}

func (s *Scope) ExistsFunction(name string) bool {
	currentFunctionScope := s

	for currentFunctionScope != nil {
		if currentFunctionScope.Type == ScopeTypeFunction || currentFunctionScope.Type == ScopeTypeGlobal || currentFunctionScope.Type == ScopeTypeModule {
			return currentFunctionScope.ExistsLocal(name)
		}
		// Skip non-function and non-global scopes
		if currentFunctionScope.ExistsLocal(name) {
			return true
		}
		currentFunctionScope = currentFunctionScope.Parent
	}

	return false
}

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
	s.Frame.VarCount++
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

func (s *Scope) GetVarCount() int {
	return s.Frame.VarCount
}

func (s *Scope) GetNearest(scopeType ScopeType) *Scope {
	currentScope := s
	for currentScope != nil {
		if currentScope.Type == scopeType {
			return currentScope
		}
		currentScope = currentScope.Parent
	}
	return nil
}
