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

func NewStringValue(value string) OSValue {
	if value == "" {
		return OSValue{Type: OSVString}
	}
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func (v OSValue) stringValue() string {
	if v.Number == 0 {
		return ""
	}
	return unsafe.String((*byte)(v.Opaque), int(v.Number))
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
		return v.stringValue()
	case OSVArray:
		{
			elements := *(*[]OSValue)(v.Opaque)
			str := "["
			for i, elem := range elements {
				if i > 0 {
					str += ", "
				}
				str += elem.String()
			}
			str += "]"
			return str
		}
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
		number, err := strconv.ParseFloat(v.stringValue(), 64)
		if err != nil {
			return 0
		}
		return number
	}
	return 0
}

func IsEqual(a, b OSValue) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case OSVNull:
		return true
	case OSVInt, OSVNum, OSVBool:
		return a.Number == b.Number
	case OSVString:
		return a.stringValue() == b.stringValue()
	case OSVFunction:
		return a.Opaque == b.Opaque
	default:
		return false
	}
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
		return v.Number != 0
	case OSVFunction:
		return v.Opaque != nil
	default:
		return false
	}
}
