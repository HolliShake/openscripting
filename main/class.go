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

func classTemplateValue(class *Class, name string) (OSValue, bool) {
	for current := class; current != nil; current = current.Super {
		if value, exists := current.Template[name]; exists {
			return value, true
		}
	}
	return OSValue{}, false
}
