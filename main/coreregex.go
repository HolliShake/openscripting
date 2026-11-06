package main

import (
	"math"
	"regexp"
	"strings"
	"sync"
	"unsafe"
)

var (
	coreRegexClass *Class
	coreRegexOnce  sync.Once
)

type regexData struct {
	compiled *regexp.Regexp
	posix    bool
	longest  bool
}

func ensureCoreRegexClass() *Class {
	coreRegexOnce.Do(func() {
		coreRegexClass = &Class{
			Name: "Regex",
			Constructor: OSValue{
				Type: OSVNativeFunction,
				Opaque: unsafe.Pointer(&NativeFunction{
					ParamCount: 1,
					Callable:   nativeRegexConstructor,
				}),
			},
			Template: map[string]OSValue{
				"appendText":                 nativeMethodTemplate(1, regexAppendText),
				"compile":                    {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 1, Callable: nativeRegexCompile})},
				"compilePOSIX":               {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 1, Callable: nativeRegexCompilePOSIX})},
				"copy":                       nativeMethodTemplate(0, regexCopy),
				"expand":                     nativeMethodTemplate(3, regexExpandString),
				"expandString":               nativeMethodTemplate(3, regexExpandString),
				"find":                       nativeMethodTemplate(1, regexFind),
				"findAll":                    nativeMethodTemplate(-1, regexFindAllBytes),
				"findAllIndex":               nativeMethodTemplate(-1, regexFindAllIndex),
				"findAllString":              nativeMethodTemplate(-1, regexFindAllString),
				"findAllStringIndex":         nativeMethodTemplate(-1, regexFindAllStringIndex),
				"findAllStringSubmatch":      nativeMethodTemplate(-1, regexFindAllStringSubmatch),
				"findAllStringSubmatchIndex": nativeMethodTemplate(-1, regexFindAllStringSubmatchIndex),
				"findAllSubmatch":            nativeMethodTemplate(-1, regexFindAllSubmatch),
				"findAllSubmatchIndex":       nativeMethodTemplate(-1, regexFindAllSubmatchIndex),
				"findIndex":                  nativeMethodTemplate(1, regexFindIndex),
				"findReaderIndex":            nativeMethodTemplate(1, regexFindReaderIndex),
				"findReaderSubmatchIndex":    nativeMethodTemplate(1, regexFindReaderSubmatchIndex),
				"findString":                 nativeMethodTemplate(1, regexFindString),
				"findStringIndex":            nativeMethodTemplate(1, regexFindStringIndex),
				"findStringSubmatch":         nativeMethodTemplate(1, regexFindStringSubmatch),
				"findStringSubmatchIndex":    nativeMethodTemplate(1, regexFindStringSubmatchIndex),
				"findSubmatch":               nativeMethodTemplate(1, regexFindSubmatch),
				"findSubmatchIndex":          nativeMethodTemplate(1, regexFindSubmatchIndex),
				"longest":                    nativeMethodTemplate(0, regexLongest),
				"literalPrefix":              nativeMethodTemplate(0, regexLiteralPrefix),
				"match":                      nativeMethodTemplate(1, regexMatch),
				"matchReader":                nativeMethodTemplate(1, regexMatchReader),
				"matchString":                nativeMethodTemplate(1, regexMatchString),
				"marshalText":                nativeMethodTemplate(0, regexMarshalText),
				"numSubexp":                  nativeMethodTemplate(0, regexNumSubexp),
				"replaceAllFunc":             nativeMethodTemplate(2, regexReplaceAllFunc),
				"replaceAllLiteralString":    nativeMethodTemplate(2, regexReplaceAllLiteralString),
				"replaceAllLiteral":          nativeMethodTemplate(2, regexReplaceAllLiteral),
				"replaceAll":                 nativeMethodTemplate(2, regexReplaceAll),
				"replaceAllStringFunc":       nativeMethodTemplate(2, regexReplaceAllStringFunc),
				"replaceAllString":           nativeMethodTemplate(2, regexReplaceAllString),
				"split":                      nativeMethodTemplate(-1, regexSplit),
				"string":                     nativeMethodTemplate(0, regexStringMethod),
				"subexpIndex":                nativeMethodTemplate(1, regexSubexpIndex),
				"subexpNames":                nativeMethodTemplate(0, regexSubexpNames),
				"unmarshalText":              nativeMethodTemplate(1, regexUnmarshalText),
			},
		}
	})
	return coreRegexClass
}

func nativeRegexConstructor(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "Regex expects a pattern string"
		return vm.Null
	}
	compiled, err := regexp.Compile(args[0].StringValue())
	if err != nil {
		vm.NativeError = "Regex: " + err.Error()
		return vm.Null
	}
	return newRegexValue(compiled, false)
}

func nativeRegexCompile(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "Regex.compile expects a pattern string"
		return vm.Null
	}
	compiled, err := regexp.Compile(args[0].StringValue())
	if err != nil {
		vm.NativeError = "Regex.compile: " + err.Error()
		return vm.Null
	}
	return newRegexValue(compiled, false)
}

func nativeRegexCompilePOSIX(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "Regex.compilePOSIX expects a pattern string"
		return vm.Null
	}
	compiled, err := regexp.CompilePOSIX(args[0].StringValue())
	if err != nil {
		vm.NativeError = "Regex.compilePOSIX: " + err.Error()
		return vm.Null
	}
	return newRegexValue(compiled, true)
}

func nativeRegexMustCompile(vm *VM, args []OSValue) OSValue {
	return nativeRegexCompile(vm, args)
}

func nativeRegexMustCompilePOSIX(vm *VM, args []OSValue) OSValue {
	return nativeRegexCompilePOSIX(vm, args)
}

func newRegexValue(compiled *regexp.Regexp, posix bool) OSValue {
	instance := &Object{
		ClassName: "Regex",
		Class:     ensureCoreRegexClass(),
		Native:    &regexData{compiled: compiled, posix: posix, longest: posix},
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
}

func regexDataFrom(this OSValue, vm *VM) *regexData {
	if this.Type != OSVObject || this.Opaque == nil {
		vm.NativeError = "invalid Regex receiver"
		return nil
	}
	data, ok := (*Object)(this.Opaque).Native.(*regexData)
	if !ok || data == nil || data.compiled == nil {
		vm.NativeError = "invalid Regex receiver"
		return nil
	}
	return data
}

func regexReceiver(this OSValue, vm *VM) *regexp.Regexp {
	data := regexDataFrom(this, vm)
	if data == nil {
		return nil
	}
	return data.compiled
}

func regexStringValue(value OSValue, name string, vm *VM) (string, bool) {
	if !IsString(value) {
		vm.NativeError = "Regex." + name + " expects string arguments"
		return "", false
	}
	return value.StringValue(), true
}

func regexOptionalCount(args []OSValue, name string, vm *VM) (int, bool) {
	if len(args) == 1 {
		return -1, true
	}
	if !IsNumber(args[1]) || math.IsNaN(args[1].Number) || math.IsInf(args[1].Number, 0) {
		vm.NativeError = "Regex." + name + " expects an integer count"
		return 0, false
	}
	count := args[1].Number
	maxInt := float64(int(^uint(0) >> 1))
	if math.Trunc(count) != count || count > maxInt || count < -maxInt-1 {
		vm.NativeError = "Regex." + name + " expects an integer count"
		return 0, false
	}
	return int(count), true
}

func regexString(value string) OSValue {
	return OSValue{Type: OSVString, Number: float64(len(value)), Opaque: unsafe.Pointer(unsafe.StringData(value))}
}

func regexNumber(value int) OSValue {
	return OSValue{Type: OSVNum, Number: float64(value)}
}

func regexStringArray(values []string) OSValue {
	elements := make([]OSValue, len(values))
	for index, value := range values {
		elements[index] = regexString(value)
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
}

func regexIndexArray(values []int) OSValue {
	elements := make([]OSValue, len(values))
	for index, value := range values {
		elements[index] = regexNumber(value)
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
}

func regexStringArrayArray(values [][]string) OSValue {
	elements := make([]OSValue, len(values))
	for index, value := range values {
		elements[index] = regexStringArray(value)
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
}

func regexIndexArrayArray(values [][]int) OSValue {
	elements := make([]OSValue, len(values))
	for index, value := range values {
		elements[index] = regexIndexArray(value)
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
}

func regexCopy(this OSValue, vm *VM, _ []OSValue) OSValue {
	data := regexDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	var compiled *regexp.Regexp
	var err error
	if data.posix {
		compiled, err = regexp.CompilePOSIX(data.compiled.String())
	} else {
		compiled, err = regexp.Compile(data.compiled.String())
	}
	if err != nil {
		vm.NativeError = "Regex.copy: " + err.Error()
		return vm.Null
	}
	if data.longest && !data.posix {
		compiled.Longest()
	}
	return newRegexValue(compiled, data.posix)
}

func regexExpandString(this OSValue, vm *VM, args []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	template, ok := regexStringValue(args[0], "expandString", vm)
	if !ok {
		return vm.Null
	}
	source, ok := regexStringValue(args[1], "expandString", vm)
	if !ok {
		return vm.Null
	}
	if args[2].Type != OSVArray || args[2].Opaque == nil {
		vm.NativeError = "Regex.expandString expects an array of match indices"
		return vm.Null
	}
	matchValues := (*Array)(args[2].Opaque).Elements
	if len(matchValues) != 2*(compiled.NumSubexp()+1) {
		vm.NativeError = "Regex.expandString received invalid match indices"
		return vm.Null
	}
	match := make([]int, len(matchValues))
	for index, value := range matchValues {
		if !IsNumber(value) || math.IsNaN(value.Number) || math.IsInf(value.Number, 0) || math.Trunc(value.Number) != value.Number || value.Number < -1 || value.Number > float64(len(source)) {
			vm.NativeError = "Regex.expandString received invalid match indices"
			return vm.Null
		}
		match[index] = int(value.Number)
	}
	for index := 0; index < len(match); index += 2 {
		if match[index] == -1 && match[index+1] == -1 {
			continue
		}
		if match[index] < 0 || match[index+1] < match[index] {
			vm.NativeError = "Regex.expandString received invalid match indices"
			return vm.Null
		}
	}
	return regexString(string(compiled.ExpandString(nil, template, source, match)))
}

func regexFindAllString(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAllString", func(re *regexp.Regexp, source string, count int) OSValue {
		return regexStringArray(re.FindAllString(source, count))
	})
}

func regexFindAllStringIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAllStringIndex", func(re *regexp.Regexp, source string, count int) OSValue {
		return regexIndexArrayArray(re.FindAllStringIndex(source, count))
	})
}

func regexFindAllStringSubmatch(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAllStringSubmatch", func(re *regexp.Regexp, source string, count int) OSValue {
		return regexStringArrayArray(re.FindAllStringSubmatch(source, count))
	})
}

func regexFindAllStringSubmatchIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAllStringSubmatchIndex", func(re *regexp.Regexp, source string, count int) OSValue {
		return regexIndexArrayArray(re.FindAllStringSubmatchIndex(source, count))
	})
}

func regexFind(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "find", func(re *regexp.Regexp, source string) OSValue {
		found := re.Find([]byte(source))
		if found == nil {
			return vm.Null
		}
		return regexString(string(found))
	})
}

func regexFindIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findIndex", func(re *regexp.Regexp, source string) OSValue {
		if indices := re.FindIndex([]byte(source)); indices != nil {
			return regexIndexArray(indices)
		}
		return vm.Null
	})
}

func regexFindSubmatch(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findSubmatch", func(re *regexp.Regexp, source string) OSValue {
		matches := re.FindSubmatch([]byte(source))
		values := make([]string, len(matches))
		for index, match := range matches {
			values[index] = string(match)
		}
		return regexStringArray(values)
	})
}

func regexFindSubmatchIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findSubmatchIndex", func(re *regexp.Regexp, source string) OSValue {
		if indices := re.FindSubmatchIndex([]byte(source)); indices != nil {
			return regexIndexArray(indices)
		}
		return vm.Null
	})
}

func regexFindReaderIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findReaderIndex", func(re *regexp.Regexp, source string) OSValue {
		if indices := re.FindReaderIndex(strings.NewReader(source)); indices != nil {
			return regexIndexArray(indices)
		}
		return vm.Null
	})
}

func regexFindReaderSubmatchIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findReaderSubmatchIndex", func(re *regexp.Regexp, source string) OSValue {
		if indices := re.FindReaderSubmatchIndex(strings.NewReader(source)); indices != nil {
			return regexIndexArray(indices)
		}
		return vm.Null
	})
}

func regexFindAllBytes(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAll", func(re *regexp.Regexp, source string, count int) OSValue {
		matches := re.FindAll([]byte(source), count)
		values := make([]string, len(matches))
		for index, match := range matches {
			values[index] = string(match)
		}
		return regexStringArray(values)
	})
}

func regexFindAllIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAllIndex", func(re *regexp.Regexp, source string, count int) OSValue {
		return regexIndexArrayArray(re.FindAllIndex([]byte(source), count))
	})
}

func regexFindAllSubmatch(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAllSubmatch", func(re *regexp.Regexp, source string, count int) OSValue {
		matches := re.FindAllSubmatch([]byte(source), count)
		values := make([][]string, len(matches))
		for index, match := range matches {
			values[index] = make([]string, len(match))
			for submatchIndex, submatch := range match {
				values[index][submatchIndex] = string(submatch)
			}
		}
		return regexStringArrayArray(values)
	})
}

func regexFindAllSubmatchIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindAll(vm, this, args, "findAllSubmatchIndex", func(re *regexp.Regexp, source string, count int) OSValue {
		return regexIndexArrayArray(re.FindAllSubmatchIndex([]byte(source), count))
	})
}

func regexFindAll(vm *VM, this OSValue, args []OSValue, name string, find func(*regexp.Regexp, string, int) OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	if len(args) < 1 || len(args) > 2 {
		vm.NativeError = "Regex." + name + " expects a string and an optional count"
		return vm.Null
	}
	source, ok := regexStringValue(args[0], name, vm)
	if !ok {
		return vm.Null
	}
	count, ok := regexOptionalCount(args, name, vm)
	if !ok {
		return vm.Null
	}
	return find(compiled, source, count)
}

func regexFindString(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findString", func(re *regexp.Regexp, source string) OSValue { return regexString(re.FindString(source)) })
}

func regexFindStringIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findStringIndex", func(re *regexp.Regexp, source string) OSValue {
		if indices := re.FindStringIndex(source); indices != nil {
			return regexIndexArray(indices)
		}
		return vm.Null
	})
}

func regexFindStringSubmatch(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findStringSubmatch", func(re *regexp.Regexp, source string) OSValue { return regexStringArray(re.FindStringSubmatch(source)) })
}

func regexFindStringSubmatchIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexFindOne(vm, this, args, "findStringSubmatchIndex", func(re *regexp.Regexp, source string) OSValue {
		if indices := re.FindStringSubmatchIndex(source); indices != nil {
			return regexIndexArray(indices)
		}
		return vm.Null
	})
}

func regexFindOne(vm *VM, this OSValue, args []OSValue, name string, find func(*regexp.Regexp, string) OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	source, ok := regexStringValue(args[0], name, vm)
	if !ok {
		return vm.Null
	}
	return find(compiled, source)
}

func regexLongest(this OSValue, vm *VM, _ []OSValue) OSValue {
	data := regexDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	data.compiled.Longest()
	data.longest = true
	return this
}

func regexMatchString(this OSValue, vm *VM, args []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	source, ok := regexStringValue(args[0], "matchString", vm)
	if !ok {
		return vm.Null
	}
	if compiled.MatchString(source) {
		return vm.True
	}
	return vm.False
}

func regexMatch(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexMatchSource(vm, this, args, "match", func(re *regexp.Regexp, source string) bool {
		return re.Match([]byte(source))
	})
}

func regexMatchReader(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexMatchSource(vm, this, args, "matchReader", func(re *regexp.Regexp, source string) bool {
		return re.MatchReader(strings.NewReader(source))
	})
}

func regexMatchSource(vm *VM, this OSValue, args []OSValue, name string, match func(*regexp.Regexp, string) bool) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	source, ok := regexStringValue(args[0], name, vm)
	if !ok {
		return vm.Null
	}
	if match(compiled, source) {
		return vm.True
	}
	return vm.False
}

func regexNumSubexp(this OSValue, vm *VM, _ []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	return regexNumber(compiled.NumSubexp())
}

func regexLiteralPrefix(this OSValue, vm *VM, _ []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	prefix, complete := compiled.LiteralPrefix()
	completeValue := vm.False
	if complete {
		completeValue = vm.True
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: []OSValue{regexString(prefix), completeValue}})}
}

func regexSubexpIndex(this OSValue, vm *VM, args []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	name, ok := regexStringValue(args[0], "subexpIndex", vm)
	if !ok {
		return vm.Null
	}
	return regexNumber(compiled.SubexpIndex(name))
}

func regexReplaceAllLiteralString(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexReplace(vm, this, args, "replaceAllLiteralString", func(re *regexp.Regexp, source, replacement string) string {
		return re.ReplaceAllLiteralString(source, replacement)
	})
}

func regexReplaceAllLiteral(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexReplace(vm, this, args, "replaceAllLiteral", func(re *regexp.Regexp, source, replacement string) string {
		return string(re.ReplaceAllLiteral([]byte(source), []byte(replacement)))
	})
}

func regexReplaceAll(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexReplace(vm, this, args, "replaceAll", func(re *regexp.Regexp, source, replacement string) string {
		return string(re.ReplaceAll([]byte(source), []byte(replacement)))
	})
}

func regexReplaceAllFunc(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexReplaceCallback(vm, this, args, "replaceAllFunc", func(re *regexp.Regexp, source string, replace func(string) string) string {
		return string(re.ReplaceAllFunc([]byte(source), func(match []byte) []byte {
			return []byte(replace(string(match)))
		}))
	})
}

func regexReplaceAllStringFunc(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexReplaceCallback(vm, this, args, "replaceAllStringFunc", func(re *regexp.Regexp, source string, replace func(string) string) string {
		return re.ReplaceAllStringFunc(source, replace)
	})
}

func regexReplaceCallback(vm *VM, this OSValue, args []OSValue, name string, replace func(*regexp.Regexp, string, func(string) string) string) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	source, ok := regexStringValue(args[0], name, vm)
	if !ok {
		return vm.Null
	}
	var callbackErr string
	callbackArgs := make([]OSValue, 1)
	result := replace(compiled, source, func(match string) string {
		if callbackErr != "" {
			return ""
		}
		callbackArgs[0] = regexString(match)
		value, succeeded := invokeCollectionCallback(args[1], callbackArgs, vm)
		if !succeeded {
			callbackErr = vm.NativeError
			vm.NativeError = ""
			return ""
		}
		if !IsString(value) {
			callbackErr = "Regex." + name + " callback must return a string"
			return ""
		}
		return value.StringValue()
	})
	if callbackErr != "" {
		vm.NativeError = callbackErr
		return vm.Null
	}
	return regexString(result)
}

func regexReplaceAllString(this OSValue, vm *VM, args []OSValue) OSValue {
	return regexReplace(vm, this, args, "replaceAllString", func(re *regexp.Regexp, source, replacement string) string {
		return re.ReplaceAllString(source, replacement)
	})
}

func regexReplace(vm *VM, this OSValue, args []OSValue, name string, replace func(*regexp.Regexp, string, string) string) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	source, ok := regexStringValue(args[0], name, vm)
	if !ok {
		return vm.Null
	}
	replacement, ok := regexStringValue(args[1], name, vm)
	if !ok {
		return vm.Null
	}
	return regexString(replace(compiled, source, replacement))
}

func regexSplit(this OSValue, vm *VM, args []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	if len(args) < 1 || len(args) > 2 {
		vm.NativeError = "Regex.split expects a string and an optional count"
		return vm.Null
	}
	source, ok := regexStringValue(args[0], "split", vm)
	if !ok {
		return vm.Null
	}
	count, ok := regexOptionalCount(args, "split", vm)
	if !ok {
		return vm.Null
	}
	return regexStringArray(compiled.Split(source, count))
}

func regexStringMethod(this OSValue, vm *VM, _ []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	return regexString(compiled.String())
}

func regexSubexpNames(this OSValue, vm *VM, _ []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	return regexStringArray(compiled.SubexpNames())
}

func regexAppendText(this OSValue, vm *VM, args []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	prefix, ok := regexStringValue(args[0], "appendText", vm)
	if !ok {
		return vm.Null
	}
	encoded, err := compiled.AppendText([]byte(prefix))
	if err != nil {
		vm.NativeError = "Regex.appendText: " + err.Error()
		return vm.Null
	}
	return regexString(string(encoded))
}

func regexMarshalText(this OSValue, vm *VM, _ []OSValue) OSValue {
	compiled := regexReceiver(this, vm)
	if compiled == nil {
		return vm.Null
	}
	encoded, err := compiled.MarshalText()
	if err != nil {
		vm.NativeError = "Regex.marshalText: " + err.Error()
		return vm.Null
	}
	return regexString(string(encoded))
}

func regexUnmarshalText(this OSValue, vm *VM, args []OSValue) OSValue {
	if this.Type != OSVObject || this.Opaque == nil {
		vm.NativeError = "invalid Regex receiver"
		return vm.Null
	}
	pattern, ok := regexStringValue(args[0], "unmarshalText", vm)
	if !ok {
		return vm.Null
	}
	compiled := new(regexp.Regexp)
	if err := compiled.UnmarshalText([]byte(pattern)); err != nil {
		vm.NativeError = "Regex.unmarshalText: " + err.Error()
		return vm.Null
	}
	(*Object)(this.Opaque).Native = &regexData{compiled: compiled}
	return this
}

func nativeRegexQuoteMeta(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:regex.quoteMeta expects a string"
		return vm.Null
	}
	return regexString(regexp.QuoteMeta(args[0].StringValue()))
}

func nativeRegexMatchString(vm *VM, args []OSValue) OSValue {
	pattern, ok := regexStringValue(args[0], "matchString", vm)
	if !ok {
		return vm.Null
	}
	source, ok := regexStringValue(args[1], "matchString", vm)
	if !ok {
		return vm.Null
	}
	matched, err := regexp.MatchString(pattern, source)
	if err != nil {
		vm.NativeError = "core:regex.matchString: " + err.Error()
		return vm.Null
	}
	if matched {
		return vm.True
	}
	return vm.False
}

func nativeRegexMatch(vm *VM, args []OSValue) OSValue {
	pattern, ok := regexStringValue(args[0], "match", vm)
	if !ok {
		return vm.Null
	}
	source, ok := regexStringValue(args[1], "match", vm)
	if !ok {
		return vm.Null
	}
	matched, err := regexp.Match(pattern, []byte(source))
	if err != nil {
		vm.NativeError = "core:regex.match: " + err.Error()
		return vm.Null
	}
	if matched {
		return vm.True
	}
	return vm.False
}

func nativeRegexMatchReader(vm *VM, args []OSValue) OSValue {
	pattern, ok := regexStringValue(args[0], "matchReader", vm)
	if !ok {
		return vm.Null
	}
	source, ok := regexStringValue(args[1], "matchReader", vm)
	if !ok {
		return vm.Null
	}
	matched, err := regexp.MatchReader(pattern, strings.NewReader(source))
	if err != nil {
		vm.NativeError = "core:regex.matchReader: " + err.Error()
		return vm.Null
	}
	if matched {
		return vm.True
	}
	return vm.False
}

func getCoreRegex() map[string]OSValue {
	class := ensureCoreRegexClass()
	return map[string]OSValue{
		"Regex":            {Type: OSVClass, Opaque: unsafe.Pointer(class)},
		"compile":          {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 1, Callable: nativeRegexCompile})},
		"compilePOSIX":     {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 1, Callable: nativeRegexCompilePOSIX})},
		"mustCompile":      {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 1, Callable: nativeRegexMustCompile})},
		"mustCompilePOSIX": {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 1, Callable: nativeRegexMustCompilePOSIX})},
		"match":            {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 2, Callable: nativeRegexMatch})},
		"matchReader":      {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 2, Callable: nativeRegexMatchReader})},
		"matchString":      {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 2, Callable: nativeRegexMatchString})},
		"quoteMeta":        {Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: 1, Callable: nativeRegexQuoteMeta})},
	}
}
