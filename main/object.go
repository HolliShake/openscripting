package main

import "unsafe"

type Object struct {
	ClassName  string
	Class      *Class
	StringRoot string
	Native     any
	Keys       []string
	Values     map[string]OSValue
	Map        OSValue
	Filter     OSValue
	Find       OSValue
}

func (obj *Object) Set(key string, value OSValue) {
	if _, exists := obj.Values[key]; !exists {
		obj.Keys = append(obj.Keys, key)
	}
	obj.Values[key] = value
}

func bindObjectMethod(object OSValue, method *OSValue, callable func(this OSValue, vm *VM, args []OSValue) OSValue) OSValue {
	if *method == (OSValue{}) {
		*method = OSValue{
			Type: OSVNativeMethod,
			Opaque: unsafe.Pointer(&NativeMethod{
				This:     object,
				Paramc:   1,
				Callable: callable,
			}),
		}
	}
	return *method
}

func BindObjectMapMethod(this OSValue) OSValue {
	return bindObjectMethod(this, &(*Object)(this.Opaque).Map, ObjectMap)
}

func BindObjectFilterMethod(this OSValue) OSValue {
	return bindObjectMethod(this, &(*Object)(this.Opaque).Filter, ObjectFilter)
}

func BindObjectFindMethod(this OSValue) OSValue {
	return bindObjectMethod(this, &(*Object)(this.Opaque).Find, ObjectFind)
}

func ObjectMap(this OSValue, vm *VM, args []OSValue) OSValue {
	object := (*Object)(this.Opaque)
	if !validateArrayCallback(args[0], vm) {
		return vm.Null
	}
	length := len(object.Keys)
	result := &Object{Keys: make([]string, 0, length), Values: make(map[string]OSValue, length)}
	for index := 0; index < length; index++ {
		key := object.Keys[index]
		value, exists := object.Values[key]
		if !exists {
			continue
		}
		mapped, ok := invokeCollectionCallback(args[0], []OSValue{
			value,
			objectString(key),
			this,
		}, vm)
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
	for index := 0; index < length; index++ {
		key := object.Keys[index]
		value, exists := object.Values[key]
		if !exists {
			continue
		}
		keep, ok := invokeCollectionCallback(args[0], []OSValue{
			value,
			objectString(key),
			this,
		}, vm)
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
	for index := 0; index < length; index++ {
		key := object.Keys[index]
		value, exists := object.Values[key]
		if !exists {
			continue
		}
		found, ok := invokeCollectionCallback(args[0], []OSValue{
			value,
			objectString(key),
			this,
		}, vm)
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
