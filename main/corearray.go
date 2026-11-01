package main

import "sync"

var (
	coreArrayClass     *Class
	coreArrayClassOnce sync.Once
)

func getCoreArrayClass() *Class {
	coreArrayClassOnce.Do(func() {
		coreArrayClass = &Class{Name: "Array", Template: map[string]OSValue{
			"map":    nativeMethodTemplate(1, ArrayMap),
			"filter": nativeMethodTemplate(1, ArrayFilter),
			"find":   nativeMethodTemplate(1, ArrayFind),
		}}
	})
	return coreArrayClass
}
