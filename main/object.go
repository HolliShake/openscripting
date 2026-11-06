package main

import "unsafe"

type Object struct {
	// ClassName labels native/instance objects in string output and diagnostics when no Class is present.
	ClassName string
	// Class links instances to their inheritance chain and method template.
	Class *Class
	// StringRoot retains bytes backing string properties such as error messages owned by this object.
	StringRoot string
	// Native stores host-language state for builtin objects without exposing it as script properties.
	Native any
	// Keys preserves insertion order for deterministic iteration and object stringification.
	Keys []string
	// Values maps property names to runtime values for lookup and mutation.
	Values map[string]OSValue
	// Methods caches class-template methods bound to this object.
	Methods map[string]OSValue
}

func (obj *Object) Set(key string, value OSValue) {
	if _, exists := obj.Values[key]; !exists {
		obj.Keys = append(obj.Keys, key)
	}
	obj.Values[key] = value
}

func ObjectKeys(this OSValue, _ *VM, _ []OSValue) OSValue {
	object := (*Object)(this.Opaque)
	keys := make([]OSValue, 0, len(object.Keys))
	for _, key := range object.Keys {
		if _, exists := object.Values[key]; exists {
			keys = append(keys, objectString(key))
		}
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: keys})}
}

func ObjectMap(this OSValue, vm *VM, args []OSValue) OSValue {
	object := (*Object)(this.Opaque)
	if !validateArrayCallback(args[0], vm) {
		return vm.Null
	}
	length := len(object.Keys)
	result := &Object{Keys: make([]string, 0, length), Values: make(map[string]OSValue, length)}
	callbackArgs := make([]OSValue, 3)
	for index := 0; index < length; index++ {
		key := object.Keys[index]
		value, exists := object.Values[key]
		if !exists {
			continue
		}
		callbackArgs[0], callbackArgs[1], callbackArgs[2] = value, objectString(key), this
		mapped, ok := invokeCollectionCallback(args[0], callbackArgs, vm)
		if !ok {
			return vm.Null
		}
		result.Set(key, mapped)
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(result)}
}

func ObjectFilter(this OSValue, vm *VM, args []OSValue) OSValue {
	object := (*Object)(this.Opaque)
	if !validateArrayCallback(args[0], vm) {
		return vm.Null
	}
	length := len(object.Keys)
	result := &Object{Keys: make([]string, 0, length), Values: make(map[string]OSValue, length)}
	callbackArgs := make([]OSValue, 3)
	for index := 0; index < length; index++ {
		key := object.Keys[index]
		value, exists := object.Values[key]
		if !exists {
			continue
		}
		callbackArgs[0], callbackArgs[1], callbackArgs[2] = value, objectString(key), this
		keep, ok := invokeCollectionCallback(args[0], callbackArgs, vm)
		if !ok {
			return vm.Null
		}
		if CoerceToBool(keep) {
			result.Set(key, value)
		}
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(result)}
}

func ObjectFind(this OSValue, vm *VM, args []OSValue) OSValue {
	object := (*Object)(this.Opaque)
	if !validateArrayCallback(args[0], vm) {
		return vm.Null
	}
	length := len(object.Keys)
	callbackArgs := make([]OSValue, 3)
	for index := 0; index < length; index++ {
		key := object.Keys[index]
		value, exists := object.Values[key]
		if !exists {
			continue
		}
		callbackArgs[0], callbackArgs[1], callbackArgs[2] = value, objectString(key), this
		found, ok := invokeCollectionCallback(args[0], callbackArgs, vm)
		if !ok {
			return vm.Null
		}
		if CoerceToBool(found) {
			return value
		}
	}
	return vm.Null
}

func objectString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}
