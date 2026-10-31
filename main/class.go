package main

type Class struct {
	Name        string
	Super       *Class
	Template    map[string]OSValue
	Constructor OSValue // Callable like python example: MyClass()
}
