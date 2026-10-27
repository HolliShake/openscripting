package main

import (
	"fmt"
	"strconv"
	"strings"
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

type OSObject struct {
	Keys   []string
	Values map[string]OSValue
}

func (obj *OSObject) Set(key string, value OSValue) {
	if _, exists := obj.Values[key]; !exists {
		obj.Keys = append(obj.Keys, key)
	}
	obj.Values[key] = value
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

func (v OSValue) StringValue() string {
	if v.Number == 0 {
		return ""
	}
	return unsafe.String((*byte)(v.Opaque), int(v.Number))
}

func (v OSValue) String() string {
	var builder strings.Builder
	v.writeString(&builder, make(map[unsafe.Pointer]bool))
	return builder.String()
}

func (v OSValue) writeString(builder *strings.Builder, active map[unsafe.Pointer]bool) {
	switch v.Type {
	case OSVNull:
		fmt.Fprint(builder, "null")
	case OSVInt:
		fmt.Fprintf(builder, "%d", int64(v.Number))
	case OSVNum:
		fmt.Fprint(builder, strconv.FormatFloat(v.Number, 'f', -1, 64))
	case OSVBool:
		{
			if v.Number != 0 {
				builder.WriteString("true")
			} else {
				builder.WriteString("false")
			}
		}
	case OSVString:
		fmt.Fprint(builder, v.StringValue())
	case OSVArray:
		{
			if active[v.Opaque] {
				builder.WriteString("[self]")
				return
			}

			active[v.Opaque] = true
			defer delete(active, v.Opaque)

			elements := *(*[]OSValue)(v.Opaque)
			builder.WriteByte('[')
			for index, element := range elements {
				if index > 0 {
					builder.WriteString(", ")
				}
				element.writeString(builder, active)
			}
			builder.WriteByte(']')
		}
	case OSVObject:
		{
			if active[v.Opaque] {
				builder.WriteString("{self}")
				return
			}

			active[v.Opaque] = true
			defer delete(active, v.Opaque)

			obj := (*OSObject)(v.Opaque)
			builder.WriteByte('{')
			for index, key := range obj.Keys {
				if index > 0 {
					builder.WriteString(", ")
				}
				fmt.Fprint(builder, key)
				builder.WriteString(": ")
				obj.Values[key].writeString(builder, active)
			}
			builder.WriteByte('}')
		}
	case OSVFunction:
		fmt.Fprint(builder, "<function>")
	default:
		fmt.Fprint(builder, "<unknown>")
	}
}
