package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"unsafe"
)

var errJSONCycle = errors.New("cannot serialize a cyclic value")

func coreJSONFunction(paramCount int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			ParamCount: paramCount,
			Callable:   callable,
		}),
	}
}

func jsonString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func nativeJSONParse(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:json.parse expects a JSON string"
		return vm.Null
	}

	decoder := json.NewDecoder(bytes.NewBufferString(args[0].StringValue()))
	decoder.UseNumber()
	value, err := parseJSONValue(decoder)
	if err != nil {
		vm.NativeError = "core:json.parse: " + err.Error()
		return vm.Null
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			vm.NativeError = "core:json.parse: trailing data after JSON value"
		} else {
			vm.NativeError = "core:json.parse: " + err.Error()
		}
		return vm.Null
	}
	return value
}

func parseJSONValue(decoder *json.Decoder) (OSValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return OSValue{}, err
	}
	switch value := token.(type) {
	case nil:
		return OSValue{Type: OSVNull}, nil
	case bool:
		if value {
			return OSValue{Type: OSVBool, Number: 1}, nil
		}
		return OSValue{Type: OSVBool}, nil
	case string:
		return jsonString(value), nil
	case json.Number:
		number, err := strconv.ParseFloat(string(value), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return OSValue{}, fmt.Errorf("number %q is outside the supported range", value)
		}
		return OSValue{Type: OSVNum, Number: number}, nil
	case json.Delim:
		switch value {
		case '[':
			array := &Array{}
			for decoder.More() {
				element, err := parseJSONValue(decoder)
				if err != nil {
					return OSValue{}, err
				}
				array.Elements = append(array.Elements, element)
			}
			if _, err := decoder.Token(); err != nil {
				return OSValue{}, err
			}
			return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(array)}, nil
		case '{':
			object := &Object{Values: make(map[string]OSValue)}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return OSValue{}, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return OSValue{}, fmt.Errorf("object key is not a string")
				}
				property, err := parseJSONValue(decoder)
				if err != nil {
					return OSValue{}, err
				}
				object.Set(key, property)
			}
			if _, err := decoder.Token(); err != nil {
				return OSValue{}, err
			}
			return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}, nil
		}
	}
	return OSValue{}, fmt.Errorf("unexpected token %v", token)
}

func nativeJSONStringify(vm *VM, args []OSValue) OSValue {
	var buffer bytes.Buffer
	if err := writeJSONValue(&buffer, args[0], make(map[unsafe.Pointer]bool)); err != nil {
		vm.NativeError = "core:json.stringify: " + err.Error()
		return vm.Null
	}
	return jsonString(buffer.String())
}

func writeJSONValue(buffer *bytes.Buffer, value OSValue, active map[unsafe.Pointer]bool) error {
	switch value.Type {
	case OSVNull:
		buffer.WriteString(KeyNull)
	case OSVBool:
		if value.Number != 0 {
			buffer.WriteString(KeyTrue)
		} else {
			buffer.WriteString(KeyFalse)
		}
	case OSVInt:
		if math.IsNaN(value.Number) || math.IsInf(value.Number, 0) {
			return fmt.Errorf("cannot serialize a non-finite number")
		}
		buffer.Write(strconv.AppendInt(nil, int64(value.Number), 10))
	case OSVNum:
		if math.IsNaN(value.Number) || math.IsInf(value.Number, 0) {
			return fmt.Errorf("cannot serialize a non-finite number")
		}
		buffer.Write(strconv.AppendFloat(nil, value.Number, 'f', -1, 64))
	case OSVString:
		encoded, err := json.Marshal(value.StringValue())
		if err != nil {
			return err
		}
		buffer.Write(encoded)
	case OSVArray:
		if value.Opaque == nil {
			return fmt.Errorf("cannot serialize a nil array")
		}
		if active[value.Opaque] {
			return errJSONCycle
		}
		active[value.Opaque] = true
		defer delete(active, value.Opaque)
		buffer.WriteByte('[')
		for index, element := range (*Array)(value.Opaque).Elements {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeJSONValue(buffer, element, active); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case OSVObject:
		if value.Opaque == nil {
			return fmt.Errorf("cannot serialize a nil object")
		}
		if active[value.Opaque] {
			return errJSONCycle
		}
		active[value.Opaque] = true
		defer delete(active, value.Opaque)
		object := (*Object)(value.Opaque)
		buffer.WriteByte('{')
		written := 0
		for _, key := range object.Keys {
			property, exists := object.Values[key]
			if !exists {
				return fmt.Errorf("object key %q has no value", key)
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return err
			}
			// A cyclic property is dropped entirely, so remember where it would start.
			mark := buffer.Len()
			if written > 0 {
				buffer.WriteByte(',')
			}
			buffer.Write(encodedKey)
			buffer.WriteByte(':')
			if err := writeJSONValue(buffer, property, active); errors.Is(err, errJSONCycle) {
				buffer.Truncate(mark)
				continue
			} else if err != nil {
				return err
			}
			written++
		}
		buffer.WriteByte('}')
	default:
		return fmt.Errorf("cannot serialize value of type %d", value.Type)
	}
	return nil
}

func getCoreJSON() map[string]OSValue {
	return map[string]OSValue{
		"parse":     coreJSONFunction(1, nativeJSONParse),
		"stringify": coreJSONFunction(1, nativeJSONStringify),
	}
}
