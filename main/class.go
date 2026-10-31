package main

type Class struct {
	Name        string
	Super       *Class
	Methods     map[string]OSValue
	Constructor OSValue // Callable like python example: MyClass()
}
