package main

func OSPromiseThen(vm VM, args []OSValue) OSValue {
	this := (*OSPromise)(args[0].Opaque)
	return OSValue{Type: OSVBool, Number: boolToNumber(this.Resolved)}
}
