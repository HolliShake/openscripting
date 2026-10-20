package main

type OSValueType int

const (
	OSVInt OSValueType = iota
	OSVNum
	OSVBool
	OSVString
	OSVArray
	OSVObject
)

type OSValue struct {
	Type   OSValueType
	Number float64
	Opaque any
}
