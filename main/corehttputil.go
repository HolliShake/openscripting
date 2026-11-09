package main

import (
	"errors"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"
)

// ---------------------------------------------------------------------------
// json replacer
// ---------------------------------------------------------------------------

func httpIsUndefined(value OSValue) bool {
	zero := OSValue{}
	return value.Type == zero.Type && value.Opaque == nil && value.Number == 0
}

// httpApplyReplacer implements the "json replacer" setting: a function (key, value) or an array of allowed keys.
// A replacer that returns undefined (the zero value) omits the property.
func httpApplyReplacer(vm *VM, replacer OSValue, value OSValue) OSValue {
	var function OSValue
	var allowed []string
	if replacer.Type == OSVFunction {
		function = replacer
	} else {
		allowed = httpCollectStrings([]OSValue{replacer})
	}
	result, keep := httpWalkReplace(vm, function, allowed, replacer.Type == OSVArray, "", value, 0)
	if !keep {
		return vm.Null
	}
	return result
}

func httpWalkReplace(vm *VM, function OSValue, allowed []string, filter bool, key string, value OSValue, depth int) (OSValue, bool) {
	if depth > 64 {
		return value, true
	}
	if function.Type == OSVFunction {
		completion := runHTTPHandler(vm, function, coreHTTPString(key), value)
		if completion.Resolved && !completion.Rejected {
			value = completion.Value
		}
		if httpIsUndefined(value) {
			return value, false
		}
	}
	switch {
	case value.Type == OSVObject && value.Opaque != nil:
		source := (*Object)(value.Opaque)
		copyObject := &Object{Values: make(map[string]OSValue, len(source.Values))}
		keys := source.Keys
		if filter {
			keys = allowed
		}
		for _, name := range keys {
			child, exists := source.Values[name]
			if !exists {
				continue
			}
			if replaced, keep := httpWalkReplace(vm, function, allowed, filter, name, child, depth+1); keep {
				copyObject.Set(name, replaced)
			}
		}
		return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(copyObject)}, true
	case value.Type == OSVArray && value.Opaque != nil:
		elements := (*Array)(value.Opaque).Elements
		replacedElements := make([]OSValue, len(elements))
		for index, element := range elements {
			replaced, keep := httpWalkReplace(vm, function, allowed, filter, strconv.Itoa(index), element, depth+1)
			if !keep {
				replaced = vm.Null
			}
			replacedElements[index] = replaced
		}
		return httpArray(replacedElements), true
	}
	return value, true
}

// ---------------------------------------------------------------------------
// res.format
// ---------------------------------------------------------------------------

// res.format({ html() {...}, json() {...}, default() {...} }) negotiates on Accept; 406 when nothing matches.
// Callbacks must be synchronous.
func nativeHTTPResponseFormat(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if len(args) == 0 || args[0].Type != OSVObject || args[0].Opaque == nil {
		vm.NativeError = "core:http res.format expects an object of type handlers"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	object := (*Object)(args[0].Opaque)
	var keys []string
	var fallback OSValue
	for _, key := range object.Keys {
		handler := object.Values[key]
		if handler.Type != OSVFunction {
			continue
		}
		if key == "default" {
			fallback = handler
			continue
		}
		keys = append(keys, key)
	}
	httpVary(data, "Accept")
	var chosen OSValue
	if index := httpBestMediaType(data.requestData.headers.Get("Accept"), keys); index >= 0 {
		chosen = object.Values[keys[index]]
		data.headers.Set("Content-Type", httpWithCharset(httpMimeFor(keys[index])))
	} else if fallback.Type == OSVFunction {
		chosen = fallback
	} else {
		failure := httpErrorValue(406, "Not Acceptable")
		if failure.Type == OSVObject && failure.Opaque != nil {
			(*Object)(failure.Opaque).Set("types", httpStringArray(keys))
		}
		data.hasErr, data.errValue, data.nextCalled = true, failure, true
		return vm.Null
	}
	completion := runHTTPHandler(vm, chosen)
	if completion.Rejected {
		data.hasErr, data.errValue, data.nextCalled = true, completion.Value, true
	}
	return this
}

// ---------------------------------------------------------------------------
// View lookup and rendering
// ---------------------------------------------------------------------------

func httpViewRoots(app *httpAppState) []string {
	value, _ := app.setting("views")
	var roots []string
	if IsString(value) {
		roots = []string{value.StringValue()}
	} else if value.Type == OSVArray {
		roots = httpCollectStrings([]OSValue{value})
	}
	if len(roots) == 0 {
		roots = []string{"views"}
	}
	return roots
}

func httpContainedRegularFile(root, relative string) string {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return ""
	}
	target, err := filepath.EvalSymlinks(filepath.Join(rootReal, filepath.Clean(string(filepath.Separator)+relative)))
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(rootReal, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return target
}

func httpLookupView(app *httpAppState, view, ext string) (string, error) {
	cacheEnabled := app.settingBool("view cache", app.settingString("env", "development") == "production")
	if cacheEnabled {
		app.mu.RLock()
		cached, ok := app.viewCache[view]
		app.mu.RUnlock()
		if ok {
			return cached, nil
		}
	}
	for _, root := range httpViewRoots(app) {
		for _, candidate := range []string{view, filepath.Join(strings.TrimSuffix(view, ext), "index"+ext)} {
			if found := httpContainedRegularFile(root, candidate); found != "" {
				if cacheEnabled {
					app.mu.Lock()
					if app.viewCache == nil {
						app.viewCache = make(map[string]string)
					}
					app.viewCache[view] = found
					app.mu.Unlock()
				}
				return found, nil
			}
		}
	}
	return "", errors.New("failed to lookup view \"" + view + "\" in views directory")
}

func httpMergeInto(destination *Object, source OSValue) {
	if source.Type != OSVObject || source.Opaque == nil {
		return
	}
	object := (*Object)(source.Opaque)
	for _, key := range object.Keys {
		if value, exists := object.Values[key]; exists {
			destination.Set(key, value)
		}
	}
}

func httpRenderView(vm *VM, data *httpResponseData, app *httpAppState, view string, options OSValue) (string, error) {
	if app == nil {
		return "", errors.New("no app is available to render views")
	}
	ext := filepath.Ext(view)
	if ext == "" {
		engine := strings.TrimPrefix(app.settingString("view engine", ""), ".")
		if engine == "" {
			return "", errors.New("no default engine was specified and no extension was provided")
		}
		ext = "." + engine
		view += ext
	}
	file, err := httpLookupView(app, view, ext)
	if err != nil {
		return "", err
	}
	engine, hasEngine := app.engine(ext)
	builtin := ext == ".tpl" || ext == ".mustache" || ext == ".html"
	if !hasEngine && !builtin {
		return "", errors.New("no view engine registered for " + ext + " (use app.engine)")
	}
	merged := &Object{Values: make(map[string]OSValue, 16)}
	if appObject := (*Object)(app.appValue.Opaque); appObject != nil {
		httpMergeInto(merged, appObject.Values["locals"])
	}
	if data.response != nil {
		httpMergeInto(merged, data.response.Values["locals"])
	}
	httpMergeInto(merged, options)
	mergedValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(merged)}
	if !hasEngine {
		source, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return httpRenderTemplate(string(source), []OSValue{mergedValue})
	}
	var output string
	var failure OSValue
	called, failed := false, false
	callback := &NativeFunction{ParamCount: 2, Callable: func(vm *VM, callArgs []OSValue) OSValue {
		called = true
		if len(callArgs) > 0 && callArgs[0].Type != OSVNull && !httpIsUndefined(callArgs[0]) {
			failed, failure = true, callArgs[0]
		}
		if len(callArgs) > 1 {
			output = callArgs[1].String()
		}
		return vm.Null
	}}
	completion := runHTTPHandler(vm, engine, coreHTTPString(file), mergedValue,
		OSValue{Type: OSVNativeFunction, Opaque: unsafe.Pointer(callback)})
	switch {
	case completion.Rejected:
		return "", errors.New("view engine failed: " + completion.Value.String())
	case failed:
		return "", errors.New(failure.String())
	case called:
		return output, nil
	case completion.Resolved && IsString(completion.Value):
		return completion.Value.StringValue(), nil
	}
	return "", errors.New("view engine must call its callback synchronously or return the rendered string")
}

// res.render(view, [locals], [callback])
func nativeHTTPResponseRender(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http res.render expects a view name, optional locals and callback"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	var options, callback OSValue
	for _, arg := range args[1:] {
		if arg.Type == OSVFunction {
			callback = arg
		} else if arg.Type == OSVObject {
			options = arg
		}
	}
	rendered, err := httpRenderView(vm, data, data.app, args[0].StringValue(), options)
	if callback.Type == OSVFunction {
		if err != nil {
			runHTTPHandler(vm, callback, httpErrorValue(500, err.Error()))
		} else {
			runHTTPHandler(vm, callback, vm.Null, coreHTTPString(rendered))
		}
		return this
	}
	if err != nil {
		data.hasErr, data.errValue, data.nextCalled = true, httpErrorValue(500, err.Error()), true
		return vm.Null
	}
	data.body = rendered
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "text/html; charset=utf-8")
	}
	httpMarkResponseSent(data)
	return this
}

// ---------------------------------------------------------------------------
// Built-in minimal template engine (mustache subset, used for .tpl/.mustache/.html without app.engine)
//
//	{{name}} escaped, {{{name}}} or {{&name}} raw, {{! comment }},
//	{{#section}}...{{/section}} (arrays iterate, objects push context, truthy renders once),
//	{{^section}}...{{/section}} (renders when missing/falsy/empty), dotted names and {{.}}.
// ---------------------------------------------------------------------------

type httpTplNode struct {
	kind     byte // 't' text, 'v' escaped, 'r' raw, '#' section, '^' inverted
	text     string
	children []*httpTplNode
}

func httpParseTemplate(source string) ([]*httpTplNode, error) {
	root := &httpTplNode{}
	stack := []*httpTplNode{root}
	add := func(node *httpTplNode) {
		top := stack[len(stack)-1]
		top.children = append(top.children, node)
	}
	for len(source) > 0 {
		open := strings.Index(source, "{{")
		if open < 0 {
			add(&httpTplNode{kind: 't', text: source})
			break
		}
		if open > 0 {
			add(&httpTplNode{kind: 't', text: source[:open]})
		}
		source = source[open:]
		if strings.HasPrefix(source, "{{{") {
			end := strings.Index(source, "}}}")
			if end < 0 {
				return nil, errors.New("template: unclosed {{{")
			}
			add(&httpTplNode{kind: 'r', text: strings.TrimSpace(source[3:end])})
			source = source[end+3:]
			continue
		}
		end := strings.Index(source, "}}")
		if end < 0 {
			return nil, errors.New("template: unclosed {{")
		}
		tag := strings.TrimSpace(source[2:end])
		source = source[end+2:]
		if tag == "" {
			continue
		}
		switch tag[0] {
		case '!':
		case '&':
			add(&httpTplNode{kind: 'r', text: strings.TrimSpace(tag[1:])})
		case '#', '^':
			node := &httpTplNode{kind: tag[0], text: strings.TrimSpace(tag[1:])}
			add(node)
			stack = append(stack, node)
		case '/':
			if len(stack) < 2 || stack[len(stack)-1].text != strings.TrimSpace(tag[1:]) {
				return nil, errors.New("template: unexpected closing tag " + tag)
			}
			stack = stack[:len(stack)-1]
		default:
			add(&httpTplNode{kind: 'v', text: tag})
		}
	}
	if len(stack) != 1 {
		return nil, errors.New("template: unclosed section " + stack[len(stack)-1].text)
	}
	return root.children, nil
}

func httpTplLookup(context []OSValue, name string) (OSValue, bool) {
	if name == "." {
		return context[len(context)-1], true
	}
	parts := strings.Split(name, ".")
	for index := len(context) - 1; index >= 0; index-- {
		current := context[index]
		found := true
		for _, part := range parts {
			if current.Type != OSVObject || current.Opaque == nil {
				found = false
				break
			}
			next, exists := (*Object)(current.Opaque).Values[part]
			if !exists {
				found = false
				break
			}
			current = next
		}
		if found {
			return current, true
		}
	}
	return OSValue{}, false
}

func httpTplTruthy(value OSValue, exists bool) bool {
	if !exists || httpIsUndefined(value) || value.Type == OSVNull {
		return false
	}
	if value.Type == OSVArray && value.Opaque != nil {
		return len((*Array)(value.Opaque).Elements) > 0
	}
	return CoerceToBool(value)
}

func httpTplRender(nodes []*httpTplNode, context []OSValue, out *strings.Builder) {
	for _, node := range nodes {
		switch node.kind {
		case 't':
			out.WriteString(node.text)
		case 'v', 'r':
			value, exists := httpTplLookup(context, node.text)
			if !exists || httpIsUndefined(value) || value.Type == OSVNull {
				continue
			}
			text := value.String()
			if node.kind == 'v' {
				text = html.EscapeString(text)
			}
			out.WriteString(text)
		case '#':
			value, exists := httpTplLookup(context, node.text)
			if !httpTplTruthy(value, exists) {
				continue
			}
			if value.Type == OSVArray && value.Opaque != nil {
				for _, element := range (*Array)(value.Opaque).Elements {
					httpTplRender(node.children, append(context, element), out)
				}
			} else if value.Type == OSVObject {
				httpTplRender(node.children, append(context, value), out)
			} else {
				httpTplRender(node.children, context, out)
			}
		case '^':
			value, exists := httpTplLookup(context, node.text)
			if !httpTplTruthy(value, exists) {
				httpTplRender(node.children, context, out)
			}
		}
	}
}

func httpRenderTemplate(source string, context []OSValue) (string, error) {
	nodes, err := httpParseTemplate(source)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	httpTplRender(nodes, context, &out)
	return out.String(), nil
}
