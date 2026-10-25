package main

import (
	"fmt"
	"strconv"
	"unsafe"
)

type OSValueType byte

const (
	OSVNull OSValueType = iota
	OSVInt
	OSVNum
	OSVBool
	OSVString
	OSVArray
	OSVObject
	OSVFunction
)

type OSValue struct {
	Type   OSValueType
	Number float64
	Opaque unsafe.Pointer
}

func (v OSValue) String() string {
	switch v.Type {
	case OSVNull:
		return "null"
	case OSVInt:
		return fmt.Sprintf("%d", int64(v.Number))
	case OSVNum:
		return strconv.FormatFloat(v.Number, 'f', -1, 64)
	case OSVBool:
		if v.Number != 0 {
			return "true"
		}
		return "false"
	case OSVString:
		return *(*string)(v.Opaque)
	case OSVFunction:
		return "<function>"
	default:
		return "<unknown>"
	}
}

func IsNumber(v OSValue) bool {
	return v.Type == OSVInt || v.Type == OSVNum
}

func IsString(v OSValue) bool {
	return v.Type == OSVString
}

func CoerceToNumber(v OSValue) float64 {
	switch v.Type {
	case OSVNull:
		return 0
	case OSVInt, OSVNum, OSVBool:
		return v.Number
	case OSVString:
		value := *(*string)(v.Opaque)
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0
		}
		return number
	}
	return 0
}

func CoerceToBool(v OSValue) bool {
	switch v.Type {
	case OSVNull:
		return false
	case OSVInt, OSVNum:
		return v.Number != 0
	case OSVBool:
		return v.Number != 0
	case OSVString:
		return *(*string)(v.Opaque) != ""
	case OSVFunction:
		return v.Opaque != nil
	default:
		return false
	}
}
