package main

type Class struct {
	Name string // Identifies the class in diagnostics and string representations.
	// Super links the parent class so inherited methods and constructors can be resolved.
	Super *Class
	// Template stores methods and class-level properties; instances consult it when a field is absent.
	Template map[string]OSValue
	// Constructor is invoked when the class value is called; it is stored here to share class lookup rules.
	Constructor OSValue
}
