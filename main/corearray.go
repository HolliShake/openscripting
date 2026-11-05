package main

import "sync"

var (
	coreArrayClass     *Class
	coreArrayClassOnce sync.Once
)

func getCoreArrayClass() *Class {
	coreArrayClassOnce.Do(func() {
		coreArrayClass = &Class{Name: "Array", Template: map[string]OSValue{
			"len":    nativeMethodTemplate(0, arrayLen),
			"map":    nativeMethodTemplate(1, ArrayMap),
			"filter": nativeMethodTemplate(1, ArrayFilter),
			"find":   nativeMethodTemplate(1, ArrayFind),
		}}
	})
	return coreArrayClass
}

func arrayLen(this OSValue, _ *VM, _ []OSValue) OSValue {
	return OSValue{Type: OSVNum, Number: float64(len((*Array)(this.Opaque).Elements))}
}
