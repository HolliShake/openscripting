package main

import (
	"strconv"
	"strings"
	"unsafe"
)

const (
	OSVNull OSValueType = iota
	OSVInt
	OSVNum
	OSVBool
	OSVString
	OSVArray
	OSVObject
	OSVFunction
	OSVNativeFunction
	OSVNativeMethod // bind
	OSVMethod       // bind
	OSVPromise
)

type OSValueType byte

type OSValue struct {
	Type   OSValueType
	Number float64
	Opaque unsafe.Pointer
}

func (v OSValue) StringValue() string {
	if v.Number == 0 {
		return ""
	}
	return unsafe.String((*byte)(v.Opaque), int(v.Number))
}

func (v OSValue) String() string {
	// Scalars never need a builder or the cycle-detection map.
	switch v.Type {
	case OSVNull:
		return "null"
	case OSVInt:
		return strconv.FormatInt(int64(v.Number), 10)
	case OSVNum:
		return strconv.FormatFloat(v.Number, 'f', -1, 64)
	case OSVBool:
		if v.Number != 0 {
			return "true"
		}
		return "false"
	case OSVString:
		return v.StringValue()
	case OSVFunction:
		return "<function>"
	case OSVPromise:
		return "<promise>"
	case OSVArray, OSVObject:
	default:
		return "<unknown>"
	}

	var builder strings.Builder
	v.writeString(&builder, make(map[unsafe.Pointer]bool))
	return builder.String()
}

func (v OSValue) writeString(builder *strings.Builder, active map[unsafe.Pointer]bool) {
	switch v.Type {
	case OSVNull:
		builder.WriteString("null")
	case OSVInt:
		var tmp [24]byte
		builder.Write(strconv.AppendInt(tmp[:0], int64(v.Number), 10))
	case OSVNum:
		var tmp [32]byte
		builder.Write(strconv.AppendFloat(tmp[:0], v.Number, 'f', -1, 64))
	case OSVBool:
		{
			if v.Number != 0 {
				builder.WriteString("true")
			} else {
				builder.WriteString("false")
			}
		}
	case OSVString:
		builder.WriteString(v.StringValue())
	case OSVArray:
		{
			if active[v.Opaque] {
				builder.WriteString("[self]")
				return
			}

			active[v.Opaque] = true

			elements := (*Array)(v.Opaque).Elements
			builder.WriteByte('[')
			for index, element := range elements {
				if index > 0 {
					builder.WriteString(", ")
				}
				element.writeString(builder, active)
			}
			builder.WriteByte(']')

			delete(active, v.Opaque)
		}
	case OSVObject:
		{
			if active[v.Opaque] {
				builder.WriteString("{self}")
				return
			}

			active[v.Opaque] = true

			obj := (*Object)(v.Opaque)
			builder.WriteByte('{')
			for index, key := range obj.Keys {
				if index > 0 {
					builder.WriteString(", ")
				}
				builder.WriteString(key)
				builder.WriteString(": ")
				obj.Values[key].writeString(builder, active)
			}
			builder.WriteByte('}')

			delete(active, v.Opaque)
		}
	case OSVFunction:
		builder.WriteString("<function>")
	case OSVPromise:
		builder.WriteString("<promise>")
	default:
		builder.WriteString("<unknown>")
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
		number, err := strconv.ParseFloat(v.StringValue(), 64)
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
		return a.StringValue() == b.StringValue()
	case OSVFunction:
		return a.Opaque == b.Opaque
	case OSVPromise:
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
	case OSVFunction, OSVPromise:
		return v.Opaque != nil
	default:
		return false
	}
}
