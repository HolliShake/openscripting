package main

import (
	"math/big"
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
	OSVPromise
	OSVClass
	OSVBigInt
)

type OSValueType byte

type OSValue struct {
	// Type selects the interpretation of the compact payload and keeps values boxed uniformly.
	Type OSValueType
	// Number stores numeric/bool payloads and byte lengths for strings in this tagged representation.
	Number float64
	// Opaque points to heap-backed strings, arrays, objects, functions, classes, promises, or BigInts.
	Opaque unsafe.Pointer
}

func (v OSValue) StringValue() string {
	if v.Number == 0 {
		return ""
	}
	return unsafe.String((*byte)(v.Opaque), int(v.Number))
}

func promiseState(value OSValue) string {
	if value.Opaque == nil {
		return "Pending"
	}
	promise := (*Promise)(value.Opaque)
	if !promise.Resolved {
		return "Pending"
	}
	if promise.Rejected {
		return "Rejected"
	}
	return "Fulfilled"
}

func (v OSValue) String() string {
	// Scalars never need a builder or cycle detection.
	switch v.Type {
	case OSVNull:
		{
			return KeyNull
		}
	case OSVInt:
		{
			return strconv.FormatInt(int64(v.Number), 10)
		}
	case OSVNum:
		{
			return strconv.FormatFloat(v.Number, 'f', -1, 64)
		}
	case OSVBigInt:
		{
			if v.Opaque == nil {
				return "0"
			}
			return (*big.Int)(v.Opaque).String()
		}
	case OSVBool:
		{
			if v.Number != 0 {
				return KeyTrue
			}
			return KeyFalse
		}
	case OSVString:
		{
			return v.StringValue()
		}
	case OSVFunction:
		{
			return "<function>"
		}
	case OSVClass:
		{
			if v.Opaque == nil || (*Class)(v.Opaque).Name == "" {
				return "<class>"
			}
			return "<class " + (*Class)(v.Opaque).Name + ">"
		}
	case OSVPromise:
		{
			return "<promise " + promiseState(v) + ">"
		}
	case OSVArray, OSVObject:
		{
			// Containers fall through to the builder below.
		}
	default:
		{
			return "<unknown>"
		}
	}

	// The set of containers currently being printed is the recursion path,
	// which is tiny: a slice on the stack replaces the per-call map.
	var pathBuf [8]unsafe.Pointer
	path := pathBuf[:0]

	var builder strings.Builder
	builder.Grow(64)
	v.writeString(&builder, &path)
	return builder.String()
}

// onPath reports whether p is a container that is currently being printed.
func onPath(path []unsafe.Pointer, p unsafe.Pointer) bool {
	for _, q := range path {
		if q == p {
			return true
		}
	}
	return false
}

func (v OSValue) writeString(builder *strings.Builder, path *[]unsafe.Pointer) {
	switch v.Type {
	case OSVNull:
		{
			builder.WriteString(KeyNull)
		}
	case OSVInt:
		{
			var tmp [24]byte
			builder.Write(strconv.AppendInt(tmp[:0], int64(v.Number), 10))
		}
	case OSVNum:
		{
			var tmp [32]byte
			builder.Write(strconv.AppendFloat(tmp[:0], v.Number, 'f', -1, 64))
		}
	case OSVBigInt:
		{
			if v.Opaque == nil {
				builder.WriteByte('0')
			} else {
				builder.WriteString((*big.Int)(v.Opaque).String())
			}
		}
	case OSVBool:
		{
			if v.Number != 0 {
				builder.WriteString(KeyTrue)
			} else {
				builder.WriteString(KeyFalse)
			}
		}
	case OSVString:
		{
			// Quote straight into a stack buffer instead of allocating the
			// quoted string first.
			var tmp [64]byte
			builder.Write(strconv.AppendQuote(tmp[:0], v.StringValue()))
		}
	case OSVArray:
		{
			if onPath(*path, v.Opaque) {
				builder.WriteString("[self]")
				return
			}

			*path = append(*path, v.Opaque)

			elements := (*Array)(v.Opaque).Elements
			builder.WriteByte('[')
			for index, element := range elements {
				if index > 0 {
					builder.WriteString(", ")
				}
				element.writeString(builder, path)
			}
			builder.WriteByte(']')

			*path = (*path)[:len(*path)-1]
		}
	case OSVObject:
		{
			if onPath(*path, v.Opaque) {
				builder.WriteString("{self}")
				return
			}

			*path = append(*path, v.Opaque)

			obj := (*Object)(v.Opaque)
			if obj.ClassName != "" {
				builder.WriteString(obj.ClassName)
				builder.WriteByte(' ')
			}
			builder.WriteByte('{')
			for index, key := range obj.Keys {
				if index > 0 {
					builder.WriteString(", ")
				}
				builder.WriteString(key)
				builder.WriteString(": ")
				obj.Values[key].writeString(builder, path)
			}
			builder.WriteByte('}')

			*path = (*path)[:len(*path)-1]
		}
	case OSVFunction:
		{
			builder.WriteString("<function>")
		}
	case OSVClass:
		{
			if v.Opaque == nil || (*Class)(v.Opaque).Name == "" {
				builder.WriteString("<class>")
			} else {
				builder.WriteString("<class ")
				builder.WriteString((*Class)(v.Opaque).Name)
				builder.WriteByte('>')
			}
		}
	case OSVPromise:
		{
			builder.WriteString("<promise ")
			builder.WriteString(promiseState(v))
			builder.WriteByte('>')
		}
	default:
		{
			builder.WriteString("<unknown>")
		}
	}
}

// IsNumber: OSVInt and OSVNum are adjacent, and the byte subtraction wraps
// for every type below OSVInt, so one unsigned compare replaces two.
func IsNumber(v OSValue) bool {
	return v.Type-OSVInt <= OSVNum-OSVInt
}

func IsString(v OSValue) bool {
	return v.Type == OSVString
}

func CoerceToNumber(v OSValue) float64 {
	switch v.Type {
	case OSVNull:
		{
			return 0
		}
	case OSVInt, OSVNum, OSVBool:
		{
			return v.Number
		}
	case OSVString:
		{
			number, err := strconv.ParseFloat(v.StringValue(), 64)
			if err != nil {
				return 0
			}
			return number
		}
	}
	return 0
}

func IsEqual(a, b OSValue) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case OSVNull:
		{
			return true
		}
	case OSVInt, OSVNum, OSVBool:
		{
			return a.Number == b.Number
		}
	case OSVBigInt:
		{
			if a.Opaque == nil || b.Opaque == nil {
				return a.Opaque == b.Opaque
			}
			return (*big.Int)(a.Opaque).Cmp((*big.Int)(b.Opaque)) == 0
		}
	case OSVString:
		{
			// Number holds the byte length: different lengths can never be
			// equal, and identical data pointers need no comparison.
			if a.Number != b.Number {
				return false
			}
			if a.Opaque == b.Opaque {
				return true
			}
			return a.StringValue() == b.StringValue()
		}
	case OSVFunction, OSVClass, OSVPromise:
		{
			return a.Opaque == b.Opaque
		}
	default:
		{
			return false
		}
	}
}

func CoerceToBool(v OSValue) bool {
	switch v.Type {
	case OSVNull:
		{
			return false
		}
	case OSVInt, OSVNum, OSVBool, OSVString:
		{
			// For strings Number is the byte length.
			return v.Number != 0
		}
	case OSVBigInt:
		{
			return v.Opaque != nil && (*big.Int)(v.Opaque).Sign() != 0
		}
	case OSVFunction, OSVPromise, OSVClass:
		{
			return v.Opaque != nil
		}
	default:
		{
			return false
		}
	}
}
