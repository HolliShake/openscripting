package main

import "sync"

var (
	coreObjectClass     *Class
	coreObjectClassOnce sync.Once
)

func getCoreObjectClass() *Class {
	coreObjectClassOnce.Do(func() {
		coreObjectClass = &Class{Name: "Object", Template: map[string]OSValue{
			"keys":   nativeMethodTemplate(0, ObjectKeys),
			"map":    nativeMethodTemplate(1, ObjectMap),
			"filter": nativeMethodTemplate(1, ObjectFilter),
			"find":   nativeMethodTemplate(1, ObjectFind),
		}}
	})
	return coreObjectClass
}
