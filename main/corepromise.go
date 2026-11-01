package main

import "sync"

var (
	corePromiseClass     *Class
	corePromiseClassOnce sync.Once
)

func getCorePromiseClass() *Class {
	corePromiseClassOnce.Do(func() {
		corePromiseClass = &Class{Name: "Promise", Template: map[string]OSValue{
			"then":  nativeMethodTemplate(1, PromiseThen),
			"catch": nativeMethodTemplate(1, PromiseCatch),
		}}
	})
	return corePromiseClass
}
