package main

import (
	"unsafe"
)

type Class struct {
	Name string // Identifies the class in diagnostics and string representations.
	// Super links the parent class so inherited methods and constructors can be resolved.
	Super *Class
	// Template stores methods and class-level properties; instances consult it when a field is absent.
	Template map[string]OSValue
	// Constructor is invoked when the class value is called; it is stored here to share class lookup rules.
	Constructor OSValue
}

func classTemplateValue(class *Class, name string) (OSValue, bool) {
	for current := class; current != nil; current = current.Super {
		if value, exists := current.Template[name]; exists {
			return value, true
		}
	}
	return OSValue{}, false
}

func runtimeClass(value OSValue) *Class {
	if value.Opaque == nil {
		return nil
	}
	switch value.Type {
	case OSVArray:
		return getCoreArrayClass()
	case OSVPromise:
		return getCorePromiseClass()
	case OSVObject:
		if class := (*Object)(value.Opaque).Class; class != nil {
			return class
		}
		return getCoreObjectClass()
	default:
		return nil
	}
}

func nativeMethodTemplate(paramCount int, callable func(OSValue, *VM, []OSValue) OSValue) OSValue {
	return OSValue{Type: OSVNativeMethod, Opaque: unsafe.Pointer(&NativeMethod{
		ParamCount: paramCount,
		Callable:   callable,
	})}
}

func bindClassMethod(receiver OSValue, name string, value OSValue) OSValue {
	if value.Type == OSVFunction {
		return OSValue{Type: OSVMethod, Opaque: unsafe.Pointer(&Method{This: receiver, Callable: value})}
	}
	if value.Type != OSVNativeMethod || value.Opaque == nil {
		return value
	}

	cache := boundMethodCache(receiver)
	if cached, exists := cache[name]; exists {
		return cached
	}
	template := (*NativeMethod)(value.Opaque)
	bound := OSValue{Type: OSVNativeMethod, Opaque: unsafe.Pointer(&NativeMethod{
		This:       receiver,
		ParamCount: template.ParamCount,
		Callable:   template.Callable,
	})}
	cache[name] = bound
	return bound
}

func boundMethodCache(receiver OSValue) map[string]OSValue {
	switch receiver.Type {
	case OSVArray:
		array := (*Array)(receiver.Opaque)
		if array.Methods == nil {
			array.Methods = make(map[string]OSValue)
		}
		return array.Methods
	case OSVObject:
		object := (*Object)(receiver.Opaque)
		if object.Methods == nil {
			object.Methods = make(map[string]OSValue)
		}
		return object.Methods
	case OSVPromise:
		promise := (*Promise)(receiver.Opaque)
		if promise.Methods == nil {
			promise.Methods = make(map[string]OSValue)
		}
		return promise.Methods
	default:
		return nil
	}
}
