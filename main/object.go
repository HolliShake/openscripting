package main

type Object struct {
	ClassName  string
	Class      *Class
	StringRoot string
	Keys       []string
	Values     map[string]OSValue
}

func (obj *Object) Set(key string, value OSValue) {
	if _, exists := obj.Values[key]; !exists {
		obj.Keys = append(obj.Keys, key)
	}
	obj.Values[key] = value
}
