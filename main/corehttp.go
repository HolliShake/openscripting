package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	maxFetchResponseBytes = 10 << 20
	fetchTimeout          = 30 * time.Second
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type httpRoute struct {
	// status is the response status returned by a route without a script handler.
	status int
	path   string
	method string
	// body is the static response payload used for routes that do not invoke handler.
	body string
	// handler is the script callback invoked for a matching request, or null for a static route.
	handler      OSValue
	routeID      int
	errorHandler bool
}

type httpRouteBuilder struct {
	app  OSValue
	path string
}

// httpLayer is one ordered entry in the app's stack (a route or a middleware).
type httpLayer struct {
	// method is the HTTP verb this layer answers; "" matches every method (use/all).
	method string
	// pattern is the path split into segments; segments may be ":name" params or "*".
	pattern []string
	// prefix is true for use(): the layer matches any path below the pattern.
	prefix  bool
	matcher *httpPathMatcher
	// route is the static response or script handler for this layer.
	route httpRoute
}

type httpPathMatcher struct {
	compiled       *regexp.Regexp
	prefixCompiled *regexp.Regexp
	params         []httpPathParam
	prefix         bool
}

type httpPathParam struct {
	name  string
	group string
}

// httpMatch is a layer that matched a request, with its extracted path params.
type httpMatch struct {
	route  httpRoute
	params map[string]string
}

type httpIncoming struct {
	// method, path, query, body are copied from the listener request for VM-thread dispatch.
	method, path, query, body string
	// host and remote are the Host header and peer address of the request.
	host, remote string
	// protocol and secure describe the transport selected by net/http.
	protocol    string
	secure      bool
	environment string
	// headers carries request metadata without sharing the mutable net/http request object.
	headers           http.Header
	requestMethod     string
	etagEnabled       bool
	etagMode          string
	poweredBy         bool
	queryParser       string
	jsonIndent        string
	jsonEscape        bool
	jsonpCallbackName string
	// chain is the ordered list of matching layers selected on the listener thread.
	chain         []httpMatch
	paramHandlers map[string][]OSValue
	appState      *httpAppState
	// onError is the app's error handler snapshot, or null.
	onError OSValue
	// write sends status, headers, and body back to the listener goroutine.
	write func(int, http.Header, string)
	// done signals that the response was written and the listener may release request resources.
	done     chan struct{}
	dispatch *httpDispatch
	timedOut bool
	timeout  time.Duration
}

type httpRequestData struct {
	// headers are the request headers, used by req.get.
	headers http.Header
	// body is the raw request body, used by req.json.
	body string
	// values retains repeated header values for req.getAll.
	values http.Header
}

type httpResponseData struct {
	// status is the response code selected by the script.
	status int
	// headers are the response headers selected by the script.
	headers http.Header
	// body is the response payload.
	body              string
	jsonIndent        string
	jsonEscape        bool
	jsonpCallbackName string
	jsonpCallback     string
	response          *Object
	// sent is set once a handler produced a final response.
	sent bool
	// next is set when a handler called next() with no error.
	next       bool
	nextCalled bool
	skipRoute  bool
	// hasErr is set when a handler called next(err).
	hasErr bool
	// errValue is the value passed to next(err).
	errValue OSValue
}

type httpDispatch struct {
	vm         *VM
	incoming   *httpIncoming
	request    OSValue
	response   OSValue
	next       OSValue
	data       *httpResponseData
	index      int
	errorMode  bool
	errValue   OSValue
	finished   bool
	paramCache map[string]bool
}

type httpFetchResult struct {
	// promise identifies the script Promise to settle when the worker result reaches the VM thread.
	promise *Promise
	// status is the HTTP status code received by the fetch worker.
	status int
	// statusText is the server's textual status description.
	statusText string
	// url is the final response URL after redirects.
	url string
	// headers are copied response metadata used to construct the script response value.
	headers http.Header
	// body is the response payload read up to the configured response-size bound.
	body string
	// err records transport/read failures for Promise rejection on the VM thread.
	err error
}

type httpFetchResponse struct {
	// status is the numeric HTTP response code exposed to script.
	status int
	// statusText is the human-readable response status exposed to script.
	statusText string
	// url is the final URL after redirects.
	url string
	// headers contains the response headers visible to script.
	headers http.Header
	// body is consumed by response text/JSON readers.
	body string
}

type httpAppState struct {
	// mu protects layer registration and lifecycle flags shared by VM and listener goroutines.
	mu sync.RWMutex
	// layers is the ordered stack of routes and middleware.
	layers        []httpLayer
	paramHandlers map[string][]OSValue
	// onError is the error handler invoked by next(err) or script errors.
	onError OSValue
	// server owns the listener and supports graceful shutdown.
	server *http.Server
	// done signals that the listener has stopped and allows the event loop to release this app.
	done chan struct{}
	// address is the bound address reported to script and used for lifecycle diagnostics.
	address string
	// started prevents duplicate listener startup.
	started bool
	// closed prevents new requests/registrations after shutdown begins.
	closed bool
	// bodyLimit and environment are copied under mu for listener-side behavior.
	bodyLimit      int64
	etagEnabled    bool
	etagMode       string
	poweredBy      bool
	handlerTimeout time.Duration
	environment    string
	flags          map[string]bool
	settings       map[string]OSValue
	// vm is the owning event loop that receives request and listener-completion messages.
	vm       *VM
	appValue OSValue
}

var (
	coreHTTPClass *Class
	coreHTTPOnce  sync.Once
	httpRouteID   atomic.Uint64
)

// ---------------------------------------------------------------------------
// Class / constructor
// ---------------------------------------------------------------------------

func ensureCoreHTTPClass() *Class {
	coreHTTPOnce.Do(func() {
		coreHTTPClass = &Class{
			Name: "Express",
			Constructor: OSValue{
				Type: OSVNativeFunction,
				Opaque: unsafe.Pointer(&NativeFunction{
					ParamCount: 0,
					Callable:   nativeHTTPConstructor,
				}),
			},
		}
	})
	return coreHTTPClass
}

func coreHTTPString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func nativeHTTPConstructor(_ *VM, _ []OSValue) OSValue {
	state := &httpAppState{etagEnabled: true, etagMode: "weak", poweredBy: true}
	instance := &Object{
		ClassName: "Express",
		Class:     ensureCoreHTTPClass(),
		Native:    state,
		Values:    make(map[string]OSValue, 16),
	}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
	state.appValue = value
	instance.Set("locals", httpValueObject(nil))
	for _, verb := range []string{"get", "post", "put", "delete", "patch", "head", "options"} {
		method := strings.ToUpper(verb)
		instance.Set(verb, httpBoundMethod(value, -1, func(this OSValue, vm *VM, args []OSValue) OSValue {
			return nativeHTTPVerb(this, vm, args, method)
		}))
	}
	instance.Set("all", httpBoundMethod(value, -1, func(this OSValue, vm *VM, args []OSValue) OSValue {
		return nativeHTTPVerb(this, vm, args, "")
	}))
	instance.Set("use", httpBoundMethod(value, -1, nativeHTTPUse))
	instance.Set("onError", httpBoundMethod(value, 1, nativeHTTPOnError))
	instance.Set("set", httpBoundMethod(value, 2, nativeHTTPSetSetting))
	instance.Set("enable", httpBoundMethod(value, 1, nativeHTTPEnable))
	instance.Set("disable", httpBoundMethod(value, 1, nativeHTTPDisable))
	instance.Set("enabled", httpBoundMethod(value, 1, nativeHTTPEnabled))
	instance.Set("disabled", httpBoundMethod(value, 1, nativeHTTPDisabled))
	instance.Set("route", httpBoundMethod(value, -1, nativeHTTPRoute))
	instance.Set("param", httpBoundMethod(value, 2, nativeHTTPParam))
	instance.Set("listen", httpBoundMethod(value, 1, nativeHTTPListen))
	instance.Set("address", httpBoundMethod(value, 0, nativeHTTPAddress))
	instance.Set("close", httpBoundMethod(value, 0, nativeHTTPClose))
	return value
}

func httpBoundMethod(this OSValue, paramCount int, callable func(OSValue, *VM, []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeMethod,
		Opaque: unsafe.Pointer(&NativeMethod{
			This:       this,
			ParamCount: paramCount,
			Callable:   callable,
		}),
	}
}

func httpApp(this OSValue, vm *VM) (*httpAppState, bool) {
	if state, ok := httpAppFromValue(this); ok {
		return state, true
	}
	vm.NativeError = "core:http method called with an invalid Express instance"
	return nil, false
}

func httpAppFromValue(value OSValue) (*httpAppState, bool) {
	if value.Type == OSVObject && value.Opaque != nil {
		state, ok := (*Object)(value.Opaque).Native.(*httpAppState)
		return state, ok
	}
	return nil, false
}

func nativeHTTPSetSetting(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || !IsString(args[0]) {
		vm.NativeError = "core:http app.set expects a setting name and value"
		return vm.Null
	}
	name := args[0].StringValue()
	state.mu.Lock()
	defer state.mu.Unlock()
	switch name {
	case "body limit":
		if !IsNumber(args[1]) || args[1].Number < 1 {
			vm.NativeError = "core:http body limit must be a positive number of bytes"
			return vm.Null
		}
		state.bodyLimit = int64(args[1].Number)
	case "env":
		if !IsString(args[1]) {
			vm.NativeError = "core:http env setting must be a string"
			return vm.Null
		}
		state.environment = args[1].StringValue()
	case "handler timeout":
		if !IsNumber(args[1]) || args[1].Number < 0 {
			vm.NativeError = "core:http handler timeout must be a non-negative number of milliseconds"
			return vm.Null
		}
		state.handlerTimeout = time.Duration(args[1].Number * float64(time.Millisecond))
	case "etag":
		if IsString(args[1]) {
			mode := args[1].StringValue()
			if mode != "weak" && mode != "strong" {
				vm.NativeError = "core:http etag setting must be false, true, weak, or strong"
				return vm.Null
			}
			state.etagEnabled = true
			state.etagMode = mode
		} else {
			state.etagEnabled = CoerceToBool(args[1])
			if state.etagEnabled {
				state.etagMode = "weak"
			}
		}
		if state.settings == nil {
			state.settings = make(map[string]OSValue)
		}
		state.settings[name] = args[1]
	case "x-powered-by":
		state.poweredBy = CoerceToBool(args[1])
	case "query parser":
		if !IsString(args[1]) || (args[1].StringValue() != "simple" && args[1].StringValue() != "extended") {
			vm.NativeError = "core:http query parser must be simple or extended"
			return vm.Null
		}
		if state.settings == nil {
			state.settings = make(map[string]OSValue)
		}
		state.settings[name] = args[1]
	case "json spaces":
		if !IsString(args[1]) && !IsNumber(args[1]) {
			vm.NativeError = "core:http json spaces must be a string or number"
			return vm.Null
		}
		if state.settings == nil {
			state.settings = make(map[string]OSValue)
		}
		state.settings[name] = args[1]
	case "json escape":
		if state.settings == nil {
			state.settings = make(map[string]OSValue)
		}
		state.settings[name] = OSValue{Type: OSVBool, Number: boolNumber(CoerceToBool(args[1]))}
	case "jsonp callback name":
		if !IsString(args[1]) || args[1].StringValue() == "" {
			vm.NativeError = "core:http jsonp callback name must be a non-empty string"
			return vm.Null
		}
		if state.settings == nil {
			state.settings = make(map[string]OSValue)
		}
		state.settings[name] = args[1]
	default:
		if state.settings == nil {
			state.settings = make(map[string]OSValue)
		}
		state.settings[name] = args[1]
	}
	return this
}

func nativeHTTPGetSetting(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || !IsString(args[0]) {
		vm.NativeError = "core:http app.get expects a setting name"
		return vm.Null
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	if value, exists := state.settings[args[0].StringValue()]; exists {
		return value
	}
	return vm.Null
}

func nativeHTTPSetFlag(this OSValue, vm *VM, args []OSValue, enabled bool) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || !IsString(args[0]) {
		vm.NativeError = "core:http app flag expects a setting name"
		return vm.Null
	}
	state.mu.Lock()
	if state.flags == nil {
		state.flags = make(map[string]bool)
	}
	state.flags[args[0].StringValue()] = enabled
	state.mu.Unlock()
	return this
}

func nativeHTTPEnable(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPSetFlag(this, vm, args, true)
}

func nativeHTTPDisable(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPSetFlag(this, vm, args, false)
}

func nativeHTTPFlag(this OSValue, vm *VM, args []OSValue, expected bool) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || !IsString(args[0]) {
		vm.NativeError = "core:http flag query expects a setting name"
		return vm.False
	}
	state.mu.RLock()
	value := state.flags[args[0].StringValue()]
	state.mu.RUnlock()
	return OSValue{Type: OSVBool, Number: boolNumber(value == expected)}
}

func nativeHTTPEnabled(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPFlag(this, vm, args, true)
}

func nativeHTTPDisabled(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPFlag(this, vm, args, false)
}

// ---------------------------------------------------------------------------
// Path matching
// ---------------------------------------------------------------------------

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func splitRequestPath(escaped string) ([]string, error) {
	parts := splitPath(escaped)
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, err
		}
		parts[i] = decoded
	}
	return parts, nil
}

func compileHTTPPath(pattern []string) (*httpPathMatcher, error) {
	return compileHTTPPathWithOptions(pattern, false, false)
}

func compileHTTPPathWithOptions(pattern []string, caseSensitive, _ bool) (*httpPathMatcher, error) {
	var expression strings.Builder
	expression.WriteByte('^')
	params := make([]httpPathParam, 0, len(pattern))
	group := 0
	for _, segment := range pattern {
		if segment == "*" {
			expression.WriteByte('/')
			capture := "httpParam" + strconv.Itoa(group)
			expression.WriteString("(?P<" + capture + ">.*)")
			params = append(params, httpPathParam{name: "0", group: capture})
			group++
			continue
		}
		if strings.HasPrefix(segment, ":") {
			name := strings.TrimPrefix(segment, ":")
			optional := strings.HasSuffix(name, "?")
			repeated := strings.HasSuffix(name, "+") || strings.HasSuffix(name, "*")
			repeatOptional := strings.HasSuffix(name, "*")
			if optional || repeated {
				name = name[:len(name)-1]
			}
			constraint := "[^/]+"
			if open := strings.IndexByte(name, '('); open >= 0 {
				if !strings.HasSuffix(name, ")") {
					return nil, errors.New("unterminated route parameter constraint")
				}
				constraint = name[open+1 : len(name)-1]
				name = name[:open]
			}
			if name == "" {
				return nil, errors.New("route parameter name cannot be empty")
			}
			if repeated {
				capture := "httpParam" + strconv.Itoa(group)
				expression.WriteString("(?:/(?P<" + capture + ">(?:" + constraint + ")(?:/(?:" + constraint + "))*))")
				if repeatOptional {
					expression.WriteByte('?')
				}
			} else if optional {
				expression.WriteString("(?:/(?P<httpParam" + strconv.Itoa(group) + ">")
				expression.WriteString(constraint)
				expression.WriteString("))?")
			} else {
				expression.WriteByte('/')
				expression.WriteString("(?P<httpParam" + strconv.Itoa(group) + ">")
				expression.WriteString(constraint)
				expression.WriteByte(')')
			}
			params = append(params, httpPathParam{name: name, group: "httpParam" + strconv.Itoa(group)})
			group++
			continue
		}
		expression.WriteByte('/')
		expression.WriteString(regexp.QuoteMeta(segment))
	}
	if len(pattern) == 0 {
		expression.WriteString("/")
	}
	prefixExpression := expression.String() + "(?:/.*)?$"
	if len(pattern) == 0 {
		prefixExpression = "^/.*$"
	}
	expression.WriteByte('$')
	if !caseSensitive {
		expressionStr := "(?i:" + expression.String() + ")"
		expression.Reset()
		expression.WriteString(expressionStr)
	}
	if !caseSensitive {
		prefixExpression = "(?i:" + prefixExpression + ")"
	}
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, err
	}
	prefixCompiled, err := regexp.Compile(prefixExpression)
	if err != nil {
		return nil, err
	}
	return &httpPathMatcher{compiled: compiled, prefixCompiled: prefixCompiled, params: params}, nil
}

func (matcher *httpPathMatcher) match(path string) (map[string]string, bool) {
	if matcher == nil || matcher.compiled == nil {
		return nil, false
	}
	values := matcher.compiled.FindStringSubmatch(path)
	if values == nil {
		return nil, false
	}
	params := make(map[string]string, len(matcher.params))
	for _, param := range matcher.params {
		group := matcher.compiled.SubexpIndex(param.group)
		if group >= 0 && group < len(values) && values[group] != "" {
			params[param.name] = values[group]
		}
	}
	return params, true
}

func (matcher *httpPathMatcher) matchPrefix(path string) (map[string]string, bool) {
	if matcher == nil || matcher.prefixCompiled == nil {
		return nil, false
	}
	values := matcher.prefixCompiled.FindStringSubmatch(path)
	if values == nil {
		return nil, false
	}
	params := make(map[string]string, len(matcher.params))
	for _, param := range matcher.params {
		group := matcher.prefixCompiled.SubexpIndex(param.group)
		if group >= 0 && group < len(values) && values[group] != "" {
			params[param.name] = values[group]
		}
	}
	return params, true
}

// matchSegments matches a registered pattern against request segments.
// ":name" captures a segment, "*" captures the rest as params["0"], and
// prefix layers (use) accept any path below the pattern.
func matchSegments(pattern, actual []string, prefix bool) (map[string]string, bool) {
	params := make(map[string]string)
	for i, segment := range pattern {
		if segment == "*" {
			if i < len(actual) {
				params["0"] = strings.Join(actual[i:], "/")
			} else {
				params["0"] = ""
			}
			return params, true
		}
		if i >= len(actual) {
			return nil, false
		}
		if strings.HasPrefix(segment, ":") && len(segment) > 1 {
			params[segment[1:]] = actual[i]
		} else if segment != actual[i] {
			return nil, false
		}
	}
	if !prefix && len(actual) != len(pattern) {
		return nil, false
	}
	return params, true
}

func httpAllowedMethods(layers []httpLayer, actual []string) []string {
	methods := make([]string, 0, len(layers)+1)
	seen := make(map[string]struct{}, len(layers)+1)
	appendMethod := func(method string) {
		if _, exists := seen[method]; exists {
			return
		}
		seen[method] = struct{}{}
		methods = append(methods, method)
	}
	for _, layer := range layers {
		if layer.prefix || layer.method == "" {
			continue
		}
		matched := false
		if layer.matcher != nil {
			_, matched = layer.matcher.match("/" + strings.Join(actual, "/"))
		} else {
			_, matched = matchSegments(layer.pattern, actual, false)
		}
		if !matched {
			continue
		}
		appendMethod(layer.method)
		if layer.method == http.MethodGet {
			appendMethod(http.MethodHead)
		}
	}
	if len(methods) != 0 {
		appendMethod(http.MethodOptions)
	}
	return methods
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func httpValidTarget(vm *VM, value OSValue, allowString bool) bool {
	switch {
	case value.Type == OSVString && allowString:
		return true
	case value.Type == OSVFunction:
		if value.Opaque == nil {
			vm.NativeError = "core:http route handler cannot be nil"
			return false
		}
		return true
	}
	vm.NativeError = "core:http expects a handler function (only the last route target may be a string)"
	return false
}

func nativeHTTPVerb(this OSValue, vm *VM, args []OSValue, method string) OSValue {
	if method == http.MethodGet && len(args) == 1 && IsString(args[0]) {
		return nativeHTTPGetSetting(this, vm, args)
	}
	if len(args) >= 2 && args[0].Type == OSVArray && args[0].Opaque != nil {
		for _, path := range (*Array)(args[0].Opaque).Elements {
			if !IsString(path) || httpRegister(this, vm, method, path.StringValue(), false, 200, args[1:]).Type == OSVNull {
				return vm.Null
			}
		}
		return this
	}
	if len(args) < 2 || !IsString(args[0]) {
		vm.NativeError = "core:http route expects a path followed by one or more handlers"
		return vm.Null
	}
	return httpRegister(this, vm, method, args[0].StringValue(), false, 200, args[1:])
}

func nativeHTTPRoute(this OSValue, vm *VM, args []OSValue) OSValue {
	if len(args) == 1 && IsString(args[0]) {
		return newHTTPRouteBuilder(this, args[0].StringValue())
	}
	if len(args) < 4 {
		vm.NativeError = "core:http.route expects a path or method, path, status, and response"
		return vm.Null
	}
	if !IsString(args[0]) || !IsString(args[1]) || !IsNumber(args[2]) || (args[3].Type != OSVString && args[3].Type != OSVFunction) {
		vm.NativeError = "core:http.route expects method, path, status, and a response string or handler"
		return vm.Null
	}
	status := int(args[2].Number)
	if status < 100 || status > 599 {
		vm.NativeError = "core:http.route status must be between 100 and 599"
		return vm.Null
	}
	return httpRegister(this, vm, strings.ToUpper(args[0].StringValue()), args[1].StringValue(), false, status, args[3:4])
}

func nativeHTTPParam(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || len(args) != 2 || !IsString(args[0]) || args[1].Type != OSVFunction {
		vm.NativeError = "core:http app.param expects a name and callback function"
		return vm.Null
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.paramHandlers == nil {
		state.paramHandlers = make(map[string][]OSValue)
	}
	name := args[0].StringValue()
	state.paramHandlers[name] = append(state.paramHandlers[name], args[1])
	return this
}

func newHTTPRouteBuilder(app OSValue, path string) OSValue {
	builder := &Object{Native: &httpRouteBuilder{app: app, path: path}, Values: make(map[string]OSValue, 8)}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(builder)}
	for _, verb := range []string{"get", "post", "put", "delete", "patch", "head", "options", "all"} {
		method := strings.ToUpper(verb)
		if verb == "all" {
			method = ""
		}
		builder.Set(verb, httpBoundMethod(value, -1, func(this OSValue, vm *VM, args []OSValue) OSValue {
			if len(args) == 0 {
				vm.NativeError = "core:http Route method expects one or more handlers"
				return vm.Null
			}
			data := (*Object)(this.Opaque).Native.(*httpRouteBuilder)
			registered := httpRegister(data.app, vm, method, data.path, false, 200, args)
			if registered.Type == OSVNull {
				return vm.Null
			}
			return this
		}))
	}
	return value
}

func httpRegister(this OSValue, vm *VM, method, path string, prefix bool, status int, targets []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	if path == "" || path[0] != '/' {
		vm.NativeError = "core:http route path must start with /"
		return vm.Null
	}
	pattern := splitPath(path)
	state.mu.RLock()
	caseSensitive := false
	strictRouting := false
	if value, exists := state.settings["case sensitive routing"]; exists {
		caseSensitive = CoerceToBool(value)
	}
	if value, exists := state.settings["strict routing"]; exists {
		strictRouting = CoerceToBool(value)
	}
	state.mu.RUnlock()
	if strictRouting && strings.HasSuffix(path, "/") && len(pattern) != 0 {
		pattern = append(pattern, "")
	}
	matcher, err := compileHTTPPathWithOptions(pattern, caseSensitive, strictRouting)
	if err != nil {
		vm.NativeError = "core:http invalid route pattern: " + err.Error()
		return vm.Null
	}
	layers := make([]httpLayer, 0, len(targets))
	routeID := int(httpRouteID.Add(1))
	for i, target := range targets {
		if !httpValidTarget(vm, target, i == len(targets)-1) {
			return vm.Null
		}
		route := httpRoute{status: status, path: path, method: method}
		if target.Type == OSVFunction {
			route.handler = target
			route.errorHandler = (*Function)(target.Opaque).ParamCount == 4
		} else {
			route.body = target.StringValue()
		}
		route.routeID = routeID
		layer := httpLayer{method: method, pattern: pattern, matcher: matcher, prefix: prefix, route: route}
		layers = append(layers, layer)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		vm.NativeError = "core:http cannot add routes after close"
		return vm.Null
	}
	state.layers = append(state.layers, layers...)
	return this
}

func nativeHTTPUse(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	prefix := "/"
	if len(args) > 0 && IsString(args[0]) {
		prefix = args[0].StringValue()
		args = args[1:]
	}
	if prefix == "" || prefix[0] != '/' {
		vm.NativeError = "core:http.use path must start with /"
		return vm.Null
	}
	if len(args) == 0 {
		vm.NativeError = "core:http.use expects a handler function or an Express/Router instance"
		return vm.Null
	}
	base := splitPath(prefix)
	for _, arg := range args {
		if sub, isApp := httpAppFromValue(arg); isApp {
			if sub == state {
				vm.NativeError = "core:http.use cannot mount an app on itself"
				return vm.Null
			}
			sub.mu.RLock()
			layers := make([]httpLayer, len(sub.layers))
			copy(layers, sub.layers)
			subError := sub.onError
			sub.mu.RUnlock()
			state.mu.Lock()
			if state.closed {
				state.mu.Unlock()
				vm.NativeError = "core:http cannot add routes after close"
				return vm.Null
			}
			for _, layer := range layers {
				layer.pattern = append(append([]string{}, base...), layer.pattern...)
				state.layers = append(state.layers, layer)
			}
			if subError.Type == OSVFunction && state.onError.Type != OSVFunction {
				state.onError = subError
			}
			state.mu.Unlock()
			continue
		}
		if arg.Type != OSVFunction {
			vm.NativeError = "core:http.use expects handler functions or Express/Router instances"
			return vm.Null
		}
		if result := httpRegister(this, vm, "", prefix, true, 200, []OSValue{arg}); result.Type == OSVNull {
			return vm.Null
		}
	}
	return this
}

func nativeHTTPOnError(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	if !httpValidTarget(vm, args[0], false) {
		return vm.Null
	}
	state.mu.Lock()
	state.onError = args[0]
	state.mu.Unlock()
	return this
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

func nativeHTTPListen(this OSValue, vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:http.listen expects an address string"
		return vm.Null
	}
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.started || state.closed {
		vm.NativeError = "core:http server can only be started once"
		return vm.Null
	}
	listener, err := net.Listen("tcp", args[0].StringValue())
	if err != nil {
		vm.NativeError = "core:http.listen: " + err.Error()
		return vm.Null
	}
	state.server = &http.Server{Handler: state}
	state.vm = vm
	state.done = make(chan struct{})
	state.address = listener.Addr().String()
	state.started = true
	go func(server *http.Server, listener net.Listener) {
		defer close(state.done)
		defer func() { vm.HTTPDone <- state }()
		_ = server.Serve(listener)
	}(state.server, listener)
	vm.HTTPApps = append(vm.HTTPApps, state)
	vm.HTTPActive++
	return coreHTTPString(state.address)
}

func nativeHTTPAddress(this OSValue, vm *VM, _ []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	if !state.started || state.closed {
		return vm.Null
	}
	return coreHTTPString(state.address)
}

func nativeHTTPClose(this OSValue, vm *VM, _ []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.False
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return vm.True
	}
	state.closed = true
	if state.server == nil {
		return vm.True
	}
	if err := state.server.Close(); err != nil {
		vm.NativeError = "core:http.close: " + err.Error()
		return vm.False
	}
	return vm.True
}

// ---------------------------------------------------------------------------
// Listener goroutine
// ---------------------------------------------------------------------------

func (state *httpAppState) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	state.mu.RLock()
	bodyLimit := state.bodyLimit
	etagEnabled := state.etagEnabled
	etagMode := state.etagMode
	poweredBy := state.poweredBy
	queryParser := "simple"
	if value, exists := state.settings["query parser"]; exists && IsString(value) {
		queryParser = value.StringValue()
	}
	jsonIndent := ""
	if value, exists := state.settings["json spaces"]; exists {
		if IsString(value) {
			jsonIndent = value.StringValue()
		} else if IsNumber(value) && value.Number > 0 {
			jsonIndent = strings.Repeat(" ", min(int(value.Number), 10))
		}
		if len(jsonIndent) > 10 {
			jsonIndent = jsonIndent[:10]
		}
	}
	jsonEscape := false
	if value, exists := state.settings["json escape"]; exists {
		jsonEscape = CoerceToBool(value)
	}
	jsonpCallbackName := "callback"
	if value, exists := state.settings["jsonp callback name"]; exists && IsString(value) {
		jsonpCallbackName = value.StringValue()
	}
	state.mu.RUnlock()
	if bodyLimit <= 0 {
		bodyLimit = 1 << 20
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, bodyLimit))
	if err != nil {
		http.Error(writer, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	segments, err := splitRequestPath(request.URL.EscapedPath())
	if err != nil {
		http.Error(writer, "malformed path encoding", http.StatusBadRequest)
		return
	}
	var chain []httpMatch
	state.mu.RLock()
	for _, layer := range state.layers {
		if layer.method != "" && layer.method != request.Method &&
			!(request.Method == http.MethodHead && layer.method == http.MethodGet) {
			continue
		}
		var params map[string]string
		matched := false
		if layer.matcher != nil && !layer.prefix {
			params, matched = layer.matcher.match(request.URL.Path)
		} else if layer.matcher != nil {
			params, matched = layer.matcher.matchPrefix(request.URL.Path)
		} else {
			params, matched = matchSegments(layer.pattern, segments, layer.prefix)
		}
		if matched {
			chain = append(chain, httpMatch{route: layer.route, params: params})
		}
	}
	hasNormalLayer := false
	for _, match := range chain {
		if !match.route.errorHandler {
			hasNormalLayer = true
			break
		}
	}
	if !hasNormalLayer {
		chain = nil
	}
	allowedMethods := httpAllowedMethods(state.layers, segments)
	methodNotAllowed := false
	if len(chain) == 0 {
		for _, layer := range state.layers {
			if layer.prefix {
				continue
			}
			matched := false
			if layer.matcher != nil {
				_, matched = layer.matcher.match(request.URL.Path)
			} else {
				_, matched = matchSegments(layer.pattern, segments, false)
			}
			if matched {
				methodNotAllowed = true
				break
			}
		}
	}
	respond405 := state.flags["405"]
	environment := state.environment
	handlerTimeout := state.handlerTimeout
	onError := state.onError
	paramHandlers := make(map[string][]OSValue, len(state.paramHandlers))
	for name, handlers := range state.paramHandlers {
		paramHandlers[name] = append([]OSValue(nil), handlers...)
	}
	state.mu.RUnlock()
	if len(chain) == 0 {
		if request.Method == http.MethodOptions && len(allowedMethods) != 0 {
			writer.Header().Set("Allow", strings.Join(allowedMethods, ", "))
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if methodNotAllowed && respond405 {
			writer.Header().Set("Allow", strings.Join(allowedMethods, ", "))
			http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		http.NotFound(writer, request)
		return
	}
	write := func(status int, headers http.Header, responseBody string) {
		for key, values := range headers {
			for _, value := range values {
				writer.Header().Add(key, value)
			}
		}
		writer.WriteHeader(status)
		if request.Method != http.MethodHead {
			_, _ = io.WriteString(writer, responseBody)
		}
	}
	// Fast path: a single static route needs no VM round trip.
	if len(chain) == 1 && chain[0].route.handler.Type != OSVFunction {
		status, responseBody := chain[0].route.status, chain[0].route.body
		headers := make(http.Header)
		headers.Set("Content-Type", "text/plain; charset=utf-8")
		if poweredBy {
			headers.Set("X-Powered-By", "OpenScript")
		}
		if etagEnabled && (request.Method == http.MethodGet || request.Method == http.MethodHead) && status >= 200 && status < 300 {
			if etagMode == "strong" {
				digest := sha256.Sum256([]byte(responseBody))
				headers.Set("ETag", `"`+base64.RawStdEncoding.EncodeToString(digest[:])+`"`)
			} else {
				digest := sha1.Sum([]byte(responseBody))
				headers.Set("ETag", `W/"`+base64.RawStdEncoding.EncodeToString(digest[:])+`"`)
			}
			if httpETagMatches(request.Header.Values("If-None-Match"), headers.Get("ETag")) {
				status, responseBody = http.StatusNotModified, ""
				headers.Del("Content-Type")
			}
		}
		if status >= 100 && (status < 200 || status == http.StatusNoContent || status == http.StatusNotModified) {
			responseBody = ""
			headers.Del("Content-Type")
		} else {
			headers.Set("Content-Length", strconv.Itoa(len(responseBody)))
		}
		write(status, headers, responseBody)
		return
	}
	incoming := &httpIncoming{
		method: request.Method, path: request.URL.Path, query: request.URL.RawQuery,
		body: unsafe.String(unsafe.SliceData(body), len(body)), host: request.Host,
		remote: request.RemoteAddr, headers: request.Header.Clone(),
		requestMethod:     request.Method,
		etagEnabled:       etagEnabled,
		etagMode:          etagMode,
		poweredBy:         poweredBy,
		queryParser:       queryParser,
		jsonIndent:        jsonIndent,
		jsonEscape:        jsonEscape,
		jsonpCallbackName: jsonpCallbackName,
		paramHandlers:     paramHandlers,
		protocol:          "http", secure: request.TLS != nil,
		environment: environment,
		timeout:     handlerTimeout,
		chain:       chain, appState: state, onError: onError, write: write, done: make(chan struct{}),
	}
	if incoming.secure {
		incoming.protocol = "https"
	}
	select {
	case state.vm.HTTPRequests <- incoming:
	case <-state.done:
		http.Error(writer, "server is closed", http.StatusServiceUnavailable)
		return
	}
	select {
	case <-incoming.done:
	case <-state.done:
		select {
		case <-incoming.done:
		default:
			http.Error(writer, "server is closed", http.StatusServiceUnavailable)
		}
	}
}

// ---------------------------------------------------------------------------
// VM thread: request dispatch
// ---------------------------------------------------------------------------

func httpStringObject(values map[string]string) OSValue {
	object := &Object{Values: make(map[string]OSValue, len(values))}
	for key, value := range values {
		object.Set(key, coreHTTPString(value))
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func httpRouteObject(vm *VM, route httpRoute) OSValue {
	methods := make(map[string]OSValue, 1)
	if route.method == "" {
		methods["_all"] = vm.True
	} else {
		methods[strings.ToLower(route.method)] = vm.True
	}
	return httpValueObject(map[string]OSValue{
		"path":    coreHTTPString(route.path),
		"methods": httpValueObject(methods),
	})
}

func httpValueObject(values map[string]OSValue) OSValue {
	object := &Object{Values: make(map[string]OSValue, len(values))}
	for key, value := range values {
		object.Set(key, value)
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func httpQueryPath(key string) []string {
	firstBracket := strings.IndexByte(key, '[')
	if firstBracket < 0 {
		return []string{key}
	}
	path := []string{key[:firstBracket]}
	for rest := key[firstBracket:]; rest != ""; {
		if rest[0] != '[' {
			return []string{key}
		}
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return []string{key}
		}
		path = append(path, rest[1:end])
		rest = rest[end+1:]
	}
	return path
}

func httpQueryInsert(target map[string]any, path []string, value string) {
	key := path[0]
	if len(path) == 1 {
		if existing, exists := target[key]; exists {
			if values, ok := existing.([]any); ok {
				target[key] = append(values, value)
			} else {
				target[key] = []any{existing, value}
			}
		} else {
			target[key] = value
		}
		return
	}
	if path[1] == "" {
		if len(path) == 2 {
			values, _ := target[key].([]any)
			target[key] = append(values, value)
			return
		}
		return
	}
	nested, ok := target[key].(map[string]any)
	if !ok {
		nested = make(map[string]any)
		target[key] = nested
	}
	httpQueryInsert(nested, path[1:], value)
}

func httpQueryValue(value any) OSValue {
	switch typed := value.(type) {
	case string:
		return coreHTTPString(typed)
	case []any:
		elements := make([]OSValue, len(typed))
		for index, element := range typed {
			elements[index] = httpQueryValue(element)
		}
		return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
	case map[string]any:
		properties := make(map[string]OSValue, len(typed))
		for key, child := range typed {
			properties[key] = httpQueryValue(child)
		}
		return httpValueObject(properties)
	default:
		return OSValue{}
	}
}

func httpExtendedQuery(raw string) map[string]OSValue {
	parsed, err := url.ParseQuery(raw)
	if err != nil {
		return nil
	}
	root := make(map[string]any)
	for key, values := range parsed {
		path := httpQueryPath(key)
		for _, value := range values {
			httpQueryInsert(root, path, value)
		}
	}
	result := make(map[string]OSValue, len(root))
	for key, value := range root {
		result[key] = httpQueryValue(value)
	}
	return result
}

func httpHeadersObject(headers http.Header) OSValue {
	object := &Object{Values: make(map[string]OSValue, len(headers))}
	for key, values := range headers {
		if len(values) == 1 {
			object.Set(strings.ToLower(key), coreHTTPString(values[0]))
		} else if len(values) > 1 {
			elements := make([]OSValue, len(values))
			for index, value := range values {
				elements[index] = coreHTTPString(value)
			}
			object.Set(strings.ToLower(key), OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})})
		}
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func runHTTPHandler(vm *VM, handler OSValue, args ...OSValue) *Promise {
	function := (*Function)(handler.Opaque)
	if function.VarArgIndex < 0 && len(args) > function.ParamCount {
		args = args[:function.ParamCount]
	}
	coroutine := NewCoroutine(nil, function, InitialStack(len(args)))
	completion := &Promise{Coroutine: coroutine, Handled: true}
	coroutine.AsyncResult = completion
	for index := range function.CaptureMeta {
		capture := &function.CaptureMeta[index]
		coroutine.Frame.Env[capture.Dst] = capture.Cell
	}
	copy(coroutine.OperandStack[:], args)
	coroutine.Sp = len(args)
	if function.Async {
		coroutine.Parent = SnapshotLexicalChain(vm.Current)
		enqueueMicrotask(vm, coroutine)
		return completion
	}
	coroutine.SyncBoundary = completion
	previous := vm.Current
	vm.Current = coroutine
	runCoroutine(nil, vm, coroutine)
	vm.Current = previous
	return completion
}

func (dispatch *httpDispatch) continueAt(index int) {
	for ; index < len(dispatch.incoming.chain) && !dispatch.data.sent; index++ {
		dispatch.index = index
		match := dispatch.incoming.chain[index]
		if dispatch.errorMode {
			if !match.route.errorHandler {
				continue
			}
			dispatch.data.hasErr = false
			dispatch.data.next = false
			dispatch.data.nextCalled = false
			completion := runHTTPHandler(dispatch.vm, match.route.handler, dispatch.errValue, dispatch.request, dispatch.response, dispatch.next)
			if !completion.Resolved {
				dispatch.await(completion, index+1, true)
				return
			}
			if completion.Rejected || dispatch.data.hasErr {
				dispatch.errValue = completion.Value
				if dispatch.data.hasErr {
					dispatch.errValue = dispatch.data.errValue
				}
				continue
			}
			if dispatch.data.sent {
				dispatch.finishResponse()
				return
			}
			if dispatch.data.next {
				dispatch.errorMode = false
				dispatch.continueAt(index + 1)
				return
			}
			continue
		}
		if match.route.errorHandler {
			continue
		}
		if match.route.handler.Type != OSVFunction {
			dispatch.data.status = match.route.status
			dispatch.data.body = match.route.body
			httpMarkResponseSent(dispatch.data)
			break
		}
		(*Object)(dispatch.request.Opaque).Set("route", httpRouteObject(dispatch.vm, match.route))
		(*Object)(dispatch.request.Opaque).Set("params", httpStringObject(match.params))
		if !dispatch.runParamHandlers(match, index) {
			return
		}
		dispatch.data.next = false
		dispatch.data.nextCalled = false
		dispatch.data.skipRoute = false
		completion := runHTTPHandler(dispatch.vm, match.route.handler, dispatch.request, dispatch.response, dispatch.next)
		if !completion.Resolved {
			dispatch.await(completion, index+1, false)
			return
		}
		if completion.Rejected {
			dispatch.startError(completion.Value, index+1)
			return
		}
		if dispatch.data.hasErr {
			dispatch.startError(dispatch.data.errValue, index+1)
			return
		}
		if dispatch.data.skipRoute {
			for index+1 < len(dispatch.incoming.chain) && dispatch.incoming.chain[index+1].route.routeID == match.route.routeID {
				index++
			}
			continue
		}
		if !dispatch.data.next {
			break
		}
	}
	dispatch.finish()
}

func (dispatch *httpDispatch) startError(value OSValue, index int) {
	dispatch.errorMode = true
	dispatch.errValue = value
	dispatch.continueAt(index)
}

func (dispatch *httpDispatch) await(promise *Promise, nextIndex int, errorHandler bool) {
	promise.Handled = true
	fulfilled := &NativeFunction{ParamCount: 1, Callable: func(vm *VM, _ []OSValue) OSValue {
		if dispatch.finished {
			return vm.Null
		}
		if errorHandler && nextIndex < 0 {
			if dispatch.data.sent {
				dispatch.finishResponse()
			} else {
				dispatch.defaultErrorResponse()
			}
		} else if errorHandler {
			if dispatch.data.sent {
				dispatch.finishResponse()
			} else if dispatch.data.hasErr {
				dispatch.startError(dispatch.data.errValue, nextIndex)
			} else if dispatch.data.next {
				dispatch.errorMode = false
				dispatch.continueAt(nextIndex)
			} else {
				dispatch.startError(dispatch.errValue, nextIndex)
			}
		} else {
			if dispatch.data.hasErr {
				dispatch.startError(dispatch.data.errValue, nextIndex)
			} else if dispatch.data.skipRoute {
				dispatch.continueAt(dispatch.nextRouteIndex(nextIndex))
			} else if dispatch.data.sent || !dispatch.data.next {
				dispatch.finish()
			} else {
				dispatch.continueAt(nextIndex)
			}
		}
		return vm.Null
	}}
	rejected := &NativeFunction{ParamCount: 1, Callable: func(vm *VM, args []OSValue) OSValue {
		if dispatch.finished {
			return vm.Null
		}
		if errorHandler && nextIndex < 0 {
			dispatch.defaultErrorResponse()
		} else {
			dispatch.startError(args[0], nextIndex)
		}
		return vm.Null
	}}
	promise.Reactions = append(promise.Reactions,
		PromiseReaction{Callback: OSValue{Type: OSVNativeFunction, Opaque: unsafe.Pointer(fulfilled)}, Next: &Promise{Handled: true}},
		PromiseReaction{Callback: OSValue{Type: OSVNativeFunction, Opaque: unsafe.Pointer(rejected)}, Catch: true, Next: &Promise{Handled: true}},
	)
}

func (dispatch *httpDispatch) nextRouteIndex(index int) int {
	if index > 0 && index-1 < len(dispatch.incoming.chain) {
		routeID := dispatch.incoming.chain[index-1].route.routeID
		for index < len(dispatch.incoming.chain) && dispatch.incoming.chain[index].route.routeID == routeID {
			index++
		}
	}
	return index
}

func (dispatch *httpDispatch) runParamHandlers(match httpMatch, layerIndex int) bool {
	if dispatch.paramCache == nil {
		dispatch.paramCache = make(map[string]bool)
	}
	for name, value := range match.params {
		for callbackIndex, callback := range dispatch.incoming.paramHandlers[name] {
			cacheKey := name + "\x00" + value + "\x00" + strconv.Itoa(callbackIndex)
			if dispatch.paramCache[cacheKey] {
				continue
			}
			dispatch.data.next = false
			dispatch.data.nextCalled = false
			completion := runHTTPHandler(dispatch.vm, callback, dispatch.request, dispatch.response, dispatch.next,
				coreHTTPString(value), coreHTTPString(name))
			if !completion.Resolved {
				dispatch.awaitParam(completion, match, layerIndex, name, value, callbackIndex)
				return false
			}
			if completion.Rejected {
				dispatch.startError(completion.Value, layerIndex+1)
				return false
			}
			if dispatch.data.hasErr {
				dispatch.startError(dispatch.data.errValue, layerIndex+1)
				return false
			}
			if dispatch.data.next {
				dispatch.paramCache[cacheKey] = true
				continue
			}
			if dispatch.data.sent {
				dispatch.finishResponse()
				return false
			}
			dispatch.paramCache[cacheKey] = true
		}
	}
	return true
}

func (dispatch *httpDispatch) awaitParam(promise *Promise, match httpMatch, layerIndex int, name, value string, callbackIndex int) {
	promise.Handled = true
	fulfilled := &NativeFunction{ParamCount: 1, Callable: func(vm *VM, _ []OSValue) OSValue {
		if dispatch.finished {
			return vm.Null
		}
		if dispatch.data.hasErr {
			dispatch.startError(dispatch.data.errValue, layerIndex+1)
			return vm.Null
		}
		if dispatch.data.sent {
			dispatch.finishResponse()
			return vm.Null
		}
		cacheKey := name + "\x00" + value + "\x00" + strconv.Itoa(callbackIndex)
		dispatch.paramCache[cacheKey] = true
		if dispatch.data.next {
			dispatch.data.next = false
			dispatch.data.nextCalled = false
			for nextCallback := callbackIndex + 1; nextCallback < len(dispatch.incoming.paramHandlers[name]); nextCallback++ {
				callback := dispatch.incoming.paramHandlers[name][nextCallback]
				completion := runHTTPHandler(vm, callback, dispatch.request, dispatch.response, dispatch.next,
					coreHTTPString(value), coreHTTPString(name))
				if !completion.Resolved {
					dispatch.awaitParam(completion, match, layerIndex, name, value, nextCallback)
					return vm.Null
				}
				if completion.Rejected || dispatch.data.hasErr {
					errValue := completion.Value
					if dispatch.data.hasErr {
						errValue = dispatch.data.errValue
					}
					dispatch.startError(errValue, layerIndex+1)
					return vm.Null
				}
				dispatch.paramCache[name+"\x00"+value+"\x00"+strconv.Itoa(nextCallback)] = true
				if !dispatch.data.next {
					break
				}
				dispatch.data.next = false
				dispatch.data.nextCalled = false
			}
		}
		dispatch.data.next = false
		dispatch.data.nextCalled = false
		dispatch.continueAt(layerIndex)
		return vm.Null
	}}
	rejected := &NativeFunction{ParamCount: 1, Callable: func(vm *VM, args []OSValue) OSValue {
		if !dispatch.finished {
			dispatch.startError(args[0], layerIndex+1)
		}
		return vm.Null
	}}
	promise.Reactions = append(promise.Reactions,
		PromiseReaction{Callback: OSValue{Type: OSVNativeFunction, Opaque: unsafe.Pointer(fulfilled)}, Next: &Promise{Handled: true}},
		PromiseReaction{Callback: OSValue{Type: OSVNativeFunction, Opaque: unsafe.Pointer(rejected)}, Catch: true, Next: &Promise{Handled: true}},
	)
}

func (dispatch *httpDispatch) finish() {
	if dispatch.data.hasErr && !dispatch.errorMode {
		dispatch.startError(dispatch.data.errValue, dispatch.index+1)
		return
	}
	if dispatch.errorMode {
		dispatch.runErrorHandler()
		return
	}
	if !dispatch.data.sent && dispatch.data.next {
		dispatch.data.status = http.StatusNotFound
		dispatch.data.body = "Cannot " + dispatch.incoming.method + " " + dispatch.incoming.path
	}
	dispatch.finishResponse()
}

func (dispatch *httpDispatch) runErrorHandler() {
	data := dispatch.data
	errValue := data.errValue
	dispatch.resetResponse(http.StatusInternalServerError, "", false)
	if dispatch.incoming.onError.Type != OSVFunction {
		dispatch.defaultErrorResponse()
		return
	}
	completion := runHTTPHandler(dispatch.vm, dispatch.incoming.onError, errValue, dispatch.request, dispatch.response, dispatch.next)
	if !completion.Resolved {
		dispatch.await(completion, 0, true)
		return
	}
	if completion.Rejected || !data.sent {
		dispatch.defaultErrorResponse()
		return
	}
	dispatch.finishResponse()
}

func (dispatch *httpDispatch) defaultErrorResponse() {
	status := http.StatusInternalServerError
	message := http.StatusText(status)
	if dispatch.errValue.Type == OSVObject && dispatch.errValue.Opaque != nil {
		errObject := (*Object)(dispatch.errValue.Opaque)
		for _, key := range []string{"status", "statusCode"} {
			if value, exists := errObject.Values[key]; exists && httpValidStatus(value) {
				status = int(value.Number)
				message = http.StatusText(status)
				break
			}
		}
		if messageValue, exists := errObject.Values["message"]; exists {
			message = messageValue.String()
		}
	}
	if dispatch.incoming.environment == "production" {
		message = http.StatusText(status)
	}
	dispatch.resetResponse(status, message, true)
	dispatch.finishResponse()
}

func (dispatch *httpDispatch) resetResponse(status int, body string, sent bool) {
	data := dispatch.data
	*data = httpResponseData{
		status: status, headers: make(http.Header), body: body, sent: sent,
		jsonIndent: data.jsonIndent, jsonEscape: data.jsonEscape,
		jsonpCallbackName: data.jsonpCallbackName, jsonpCallback: data.jsonpCallback,
		response: data.response,
	}
	if data.response != nil {
		data.response.Set("headersSent", OSValue{Type: OSVBool, Number: boolNumber(sent)})
	}
}

func (dispatch *httpDispatch) finishResponse() {
	if dispatch.finished {
		return
	}
	dispatch.finished = true
	data := dispatch.data
	if !data.sent {
		data.status = http.StatusNotFound
		data.body = "Cannot " + dispatch.incoming.method + " " + dispatch.incoming.path
	}
	if dispatch.incoming.poweredBy && data.headers.Get("X-Powered-By") == "" {
		data.headers.Set("X-Powered-By", "OpenScript")
	}
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "text/plain; charset=utf-8")
	}
	if dispatch.incoming.etagEnabled &&
		(dispatch.incoming.requestMethod == http.MethodGet || dispatch.incoming.requestMethod == http.MethodHead) &&
		data.status >= 200 && data.status < 300 {
		if data.headers.Get("ETag") == "" {
			if dispatch.incoming.etagMode == "strong" {
				digest := sha256.Sum256([]byte(data.body))
				data.headers.Set("ETag", `"`+base64.RawStdEncoding.EncodeToString(digest[:])+`"`)
			} else {
				digest := sha1.Sum([]byte(data.body))
				data.headers.Set("ETag", `W/"`+base64.RawStdEncoding.EncodeToString(digest[:])+`"`)
			}
		}
		if httpETagMatches(dispatch.incoming.headers.Values("If-None-Match"), data.headers.Get("ETag")) {
			data.status = http.StatusNotModified
			data.body = ""
		}
	}
	if (dispatch.incoming.requestMethod == http.MethodGet || dispatch.incoming.requestMethod == http.MethodHead) &&
		data.status >= 200 && data.status < 300 &&
		len(dispatch.incoming.headers.Values("If-None-Match")) == 0 &&
		httpModifiedSinceMatches(dispatch.incoming.headers.Get("If-Modified-Since"), data.headers.Get("Last-Modified")) {
		data.status = http.StatusNotModified
		data.body = ""
	}
	if data.status >= 100 && (data.status < 200 || data.status == http.StatusNoContent || data.status == http.StatusNotModified) {
		data.body = ""
		data.headers.Del("Content-Type")
		data.headers.Del("Content-Length")
	} else if data.headers.Get("Content-Length") == "" {
		data.headers.Set("Content-Length", strconv.Itoa(len(data.body)))
	}
	dispatch.incoming.write(data.status, data.headers, data.body)
	close(dispatch.incoming.done)
}

func httpETagMatches(values []string, current string) bool {
	if current == "" {
		return false
	}
	weak := strings.TrimPrefix(current, "W/")
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || strings.TrimPrefix(candidate, "W/") == weak {
				return true
			}
		}
	}
	return false
}

func httpModifiedSinceMatches(ifModifiedSince, lastModified string) bool {
	if strings.TrimSpace(ifModifiedSince) == "" || strings.TrimSpace(lastModified) == "" {
		return false
	}
	clientTime, err := http.ParseTime(ifModifiedSince)
	if err != nil {
		return false
	}
	modifiedTime, err := http.ParseTime(lastModified)
	if err != nil {
		return false
	}
	return !modifiedTime.Truncate(time.Second).After(clientTime.Truncate(time.Second))
}

func (dispatch *httpDispatch) timeout() {
	if dispatch.finished {
		return
	}
	dispatch.incoming.timedOut = true
	dispatch.resetResponse(http.StatusGatewayTimeout, http.StatusText(http.StatusGatewayTimeout), true)
	dispatch.finishResponse()
}

func serveHTTPIncoming(vm *VM, incoming *httpIncoming) {
	// ----- request object -----
	queryParams := make(map[string]OSValue)
	if incoming.queryParser == "extended" {
		queryParams = httpExtendedQuery(incoming.query)
	} else if parsed, err := url.ParseQuery(incoming.query); err == nil {
		for key, values := range parsed {
			if len(values) == 1 {
				queryParams[key] = coreHTTPString(values[0])
			} else if len(values) > 1 {
				elements := make([]OSValue, len(values))
				for index, value := range values {
					elements[index] = coreHTTPString(value)
				}
				queryParams[key] = OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
			}
		}
	}
	hostname := incoming.host
	if h, _, err := net.SplitHostPort(incoming.host); err == nil {
		hostname = h
	}
	ip := incoming.remote
	if h, _, err := net.SplitHostPort(incoming.remote); err == nil {
		ip = h
	}
	originalURL := incoming.path
	if incoming.query != "" {
		originalURL += "?" + incoming.query
	}
	request := &Object{
		Native: &httpRequestData{headers: incoming.headers, body: incoming.body, values: incoming.headers},
		Values: make(map[string]OSValue, 16),
	}
	requestValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(request)}
	if incoming.appState != nil {
		request.Set("app", incoming.appState.appValue)
	}
	request.Set("method", coreHTTPString(incoming.method))
	request.Set("path", coreHTTPString(incoming.path))
	request.Set("url", coreHTTPString(originalURL))
	request.Set("baseUrl", coreHTTPString(""))
	request.Set("originalUrl", coreHTTPString(originalURL))
	request.Set("query", httpValueObject(queryParams))
	request.Set("queryParams", httpValueObject(queryParams))
	request.Set("rawQuery", coreHTTPString(incoming.query))
	request.Set("body", coreHTTPString(incoming.body))
	request.Set("rawBody", coreHTTPString(incoming.body))
	request.Set("hostname", coreHTTPString(hostname))
	request.Set("ip", coreHTTPString(ip))
	request.Set("protocol", coreHTTPString(incoming.protocol))
	request.Set("secure", OSValue{Type: OSVBool, Number: boolNumber(incoming.secure)})
	request.Set("xhr", OSValue{Type: OSVBool, Number: boolNumber(strings.EqualFold(incoming.headers.Get("X-Requested-With"), "XMLHttpRequest"))})
	request.Set("headers", httpHeadersObject(incoming.headers))
	cookies := make(map[string]OSValue)
	for _, cookie := range (&http.Request{Header: incoming.headers}).Cookies() {
		value := cookie.Value
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
		cookies[cookie.Name] = coreHTTPString(value)
	}
	request.Set("cookies", httpValueObject(cookies))
	request.Set("get", httpBoundMethod(requestValue, 1, nativeHTTPRequestGet))
	request.Set("getAll", httpBoundMethod(requestValue, 1, nativeHTTPRequestGetAll))
	request.Set("is", httpBoundMethod(requestValue, 1, nativeHTTPRequestIs))
	request.Set("accepts", httpBoundMethod(requestValue, -1, nativeHTTPRequestAccepts))
	request.Set("acceptsCharsets", httpBoundMethod(requestValue, -1, nativeHTTPRequestAcceptsCharsets))
	request.Set("acceptsEncodings", httpBoundMethod(requestValue, -1, nativeHTTPRequestAcceptsEncodings))
	request.Set("acceptsLanguages", httpBoundMethod(requestValue, -1, nativeHTTPRequestAcceptsLanguages))
	request.Set("range", httpBoundMethod(requestValue, 1, nativeHTTPRequestRange))
	request.Set("json", httpBoundMethod(requestValue, 0, nativeHTTPRequestJSON))

	// ----- response object -----
	jsonpCallback := ""
	if callback, exists := queryParams[incoming.jsonpCallbackName]; exists && IsString(callback) {
		jsonpCallback = callback.StringValue()
	}
	data := &httpResponseData{status: 200, headers: make(http.Header), jsonIndent: incoming.jsonIndent, jsonEscape: incoming.jsonEscape, jsonpCallbackName: incoming.jsonpCallbackName, jsonpCallback: jsonpCallback}
	response := &Object{Native: data, Values: make(map[string]OSValue, 12)}
	data.response = response
	responseValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(response)}
	response.Set("headersSent", vm.False)
	response.Set("locals", httpValueObject(nil))
	request.Set("res", responseValue)
	response.Set("status", httpBoundMethod(responseValue, 1, nativeHTTPResponseStatus))
	response.Set("sendStatus", httpBoundMethod(responseValue, 1, nativeHTTPResponseSendStatus))
	response.Set("send", httpBoundMethod(responseValue, 1, nativeHTTPResponseSend))
	response.Set("json", httpBoundMethod(responseValue, 1, nativeHTTPResponseJSON))
	response.Set("jsonp", httpBoundMethod(responseValue, 1, nativeHTTPResponseJSONP))
	response.Set("set", httpBoundMethod(responseValue, 2, nativeHTTPResponseSet))
	response.Set("header", httpBoundMethod(responseValue, 2, nativeHTTPResponseSet))
	response.Set("append", httpBoundMethod(responseValue, 2, nativeHTTPResponseAppend))
	response.Set("cookie", httpBoundMethod(responseValue, -1, nativeHTTPResponseCookie))
	response.Set("clearCookie", httpBoundMethod(responseValue, -1, nativeHTTPResponseClearCookie))
	response.Set("get", httpBoundMethod(responseValue, 1, nativeHTTPResponseGet))
	response.Set("location", httpBoundMethod(responseValue, 1, nativeHTTPResponseLocation))
	response.Set("links", httpBoundMethod(responseValue, 1, nativeHTTPResponseLinks))
	response.Set("vary", httpBoundMethod(responseValue, 1, nativeHTTPResponseVary))
	response.Set("type", httpBoundMethod(responseValue, 1, nativeHTTPResponseType))
	response.Set("attachment", httpBoundMethod(responseValue, -1, nativeHTTPResponseAttachment))
	response.Set("sendFile", httpBoundMethod(responseValue, -1, nativeHTTPResponseSendFile))
	response.Set("download", httpBoundMethod(responseValue, -1, nativeHTTPResponseDownload))
	response.Set("redirect", httpBoundMethod(responseValue, -1, nativeHTTPResponseRedirect))
	response.Set("end", httpBoundMethod(responseValue, -1, nativeHTTPResponseEnd))
	nextValue := httpBoundMethod(responseValue, -1, nativeHTTPNext)

	dispatch := &httpDispatch{
		vm: vm, incoming: incoming, request: requestValue,
		response: responseValue, next: nextValue, data: data,
	}
	incoming.dispatch = dispatch
	if incoming.timeout > 0 {
		go func() {
			timer := time.NewTimer(incoming.timeout)
			defer timer.Stop()
			select {
			case <-timer.C:
				vm.HTTPTimeouts <- incoming
			case <-incoming.done:
			}
		}()
	}
	dispatch.continueAt(0)
}

// ---------------------------------------------------------------------------
// Request methods
// ---------------------------------------------------------------------------

func httpRequestDataFrom(this OSValue, vm *VM) *httpRequestData {
	if this.Type == OSVObject && this.Opaque != nil {
		if data, ok := (*Object)(this.Opaque).Native.(*httpRequestData); ok {
			return data
		}
	}
	vm.NativeError = "core:http request method called with an invalid request"
	return nil
}

func nativeHTTPRequestGet(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http req.get expects a header name"
		return vm.Null
	}
	return coreHTTPString(data.headers.Get(args[0].StringValue()))
}

func nativeHTTPRequestRange(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || !IsNumber(args[0]) || args[0].Number < 0 {
		vm.NativeError = "core:http req.range expects a non-negative size"
		return vm.Null
	}
	size := int64(args[0].Number)
	header := data.headers.Get("Range")
	if strings.TrimSpace(header) == "" {
		return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{})}
	}
	unit, values, found := strings.Cut(header, "=")
	if !found || !strings.EqualFold(strings.TrimSpace(unit), "bytes") || strings.TrimSpace(values) == "" {
		return OSValue{Type: OSVInt, Number: -2}
	}
	ranges := make([]OSValue, 0)
	for _, value := range strings.Split(values, ",") {
		startText, endText, found := strings.Cut(strings.TrimSpace(value), "-")
		if !found || strings.Contains(endText, "-") {
			return OSValue{Type: OSVInt, Number: -2}
		}
		var start, end int64
		if startText == "" {
			suffix, err := strconv.ParseInt(endText, 10, 64)
			if err != nil || suffix < 0 {
				return OSValue{Type: OSVInt, Number: -2}
			}
			if suffix == 0 || size == 0 {
				continue
			}
			start = max(size-suffix, 0)
			end = size - 1
		} else {
			parsedStart, err := strconv.ParseInt(startText, 10, 64)
			if err != nil || parsedStart < 0 {
				return OSValue{Type: OSVInt, Number: -2}
			}
			start = parsedStart
			if endText == "" {
				end = size - 1
			} else {
				parsedEnd, err := strconv.ParseInt(endText, 10, 64)
				if err != nil || parsedEnd < 0 {
					return OSValue{Type: OSVInt, Number: -2}
				}
				end = parsedEnd
			}
			if start >= size || start > end {
				continue
			}
			if end >= size {
				end = size - 1
			}
		}
		ranges = append(ranges, httpValueObject(map[string]OSValue{
			"start": {Type: OSVInt, Number: float64(start)},
			"end":   {Type: OSVInt, Number: float64(end)},
		}))
	}
	if len(ranges) == 0 {
		return OSValue{Type: OSVInt, Number: -1}
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: ranges})}
}

func nativeHTTPRequestIs(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http req.is expects a content type"
		return vm.False
	}
	actual, _, err := mime.ParseMediaType(data.headers.Get("Content-Type"))
	if err != nil {
		return vm.False
	}
	wanted := strings.TrimSpace(args[0].StringValue())
	if !strings.Contains(wanted, "/") {
		if !strings.HasPrefix(wanted, ".") {
			wanted = "." + wanted
		}
		wanted = mime.TypeByExtension(wanted)
	}
	wanted, _, err = mime.ParseMediaType(wanted)
	if err != nil {
		return vm.False
	}
	wantedParts := strings.SplitN(wanted, "/", 2)
	actualParts := strings.SplitN(actual, "/", 2)
	if len(wantedParts) != 2 || len(actualParts) != 2 {
		return vm.False
	}
	matched := (wantedParts[0] == "*" || strings.EqualFold(wantedParts[0], actualParts[0])) &&
		(wantedParts[1] == "*" || strings.EqualFold(wantedParts[1], actualParts[1]))
	return OSValue{Type: OSVBool, Number: boolNumber(matched)}
}

func nativeHTTPRequestAccepts(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || len(args) == 0 {
		vm.NativeError = "core:http req.accepts expects one or more content types"
		return vm.False
	}
	requested := make([]string, 0, len(args))
	for _, arg := range args {
		if arg.Type == OSVArray && arg.Opaque != nil {
			for _, value := range (*Array)(arg.Opaque).Elements {
				if IsString(value) {
					requested = append(requested, value.StringValue())
				}
			}
		} else if IsString(arg) {
			requested = append(requested, arg.StringValue())
		}
	}
	accepted := parseHTTPAccept(data.headers.Get("Accept"))
	bestCandidate := ""
	bestQuality := -1.0
	for _, candidate := range requested {
		mediaType := candidate
		if !strings.Contains(mediaType, "/") {
			extension := mediaType
			if !strings.HasPrefix(extension, ".") {
				extension = "." + extension
			}
			mediaType = mime.TypeByExtension(extension)
		}
		mediaType, _, err := mime.ParseMediaType(mediaType)
		if err != nil {
			continue
		}
		candidateParts := strings.SplitN(mediaType, "/", 2)
		if len(candidateParts) != 2 {
			continue
		}
		quality, specificity := -1.0, -1
		for _, acceptedType := range accepted {
			acceptedParts := strings.SplitN(acceptedType.mediaType, "/", 2)
			if len(acceptedParts) != 2 ||
				(acceptedParts[0] != "*" && !strings.EqualFold(acceptedParts[0], candidateParts[0])) ||
				(acceptedParts[1] != "*" && !strings.EqualFold(acceptedParts[1], candidateParts[1])) {
				continue
			}
			currentSpecificity := 0
			if acceptedParts[0] != "*" {
				currentSpecificity++
			}
			if acceptedParts[1] != "*" {
				currentSpecificity++
			}
			if currentSpecificity > specificity || (currentSpecificity == specificity && acceptedType.quality > quality) {
				quality = acceptedType.quality
				specificity = currentSpecificity
			}
		}
		if quality > 0 && quality > bestQuality {
			bestCandidate = candidate
			bestQuality = quality
		}
	}
	if bestQuality >= 0 {
		return coreHTTPString(bestCandidate)
	}
	return vm.False
}

type httpAcceptedType struct {
	mediaType string
	quality   float64
}

func parseHTTPAccept(header string) []httpAcceptedType {
	if strings.TrimSpace(header) == "" {
		return []httpAcceptedType{{mediaType: "*/*", quality: 1}}
	}
	accepted := make([]httpAcceptedType, 0)
	for _, part := range strings.Split(header, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		quality := 1.0
		if value, exists := params["q"]; exists {
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || parsed < 0 || parsed > 1 {
				quality = 0
			} else {
				quality = parsed
			}
		}
		accepted = append(accepted, httpAcceptedType{mediaType: strings.ToLower(mediaType), quality: quality})
	}
	return accepted
}

func nativeHTTPRequestAcceptsCharsets(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPRequestAcceptToken(this, vm, args, "Accept-Charset")
}

func nativeHTTPRequestAcceptsEncodings(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPRequestAcceptToken(this, vm, args, "Accept-Encoding")
}

func nativeHTTPRequestAcceptsLanguages(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPRequestAcceptToken(this, vm, args, "Accept-Language")
}

func nativeHTTPRequestAcceptToken(this OSValue, vm *VM, args []OSValue, headerName string) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || len(args) == 0 {
		vm.NativeError = "core:http request accepts method expects one or more values"
		return vm.False
	}
	var candidates []string
	for _, arg := range args {
		if arg.Type == OSVArray && arg.Opaque != nil {
			for _, item := range (*Array)(arg.Opaque).Elements {
				if IsString(item) {
					candidates = append(candidates, item.StringValue())
				}
			}
		} else if IsString(arg) {
			candidates = append(candidates, arg.StringValue())
		}
	}
	accepted := parseHTTPAcceptTokens(data.headers.Get(headerName))
	bestCandidate := ""
	bestQuality := -1.0
	for _, candidate := range candidates {
		for _, acceptedToken := range accepted {
			matches := acceptedToken.value == "*" || strings.EqualFold(acceptedToken.value, candidate)
			if headerName == "Accept-Language" && strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(acceptedToken.value)+"-") {
				matches = true
			}
			if matches && acceptedToken.quality > bestQuality {
				bestCandidate = candidate
				bestQuality = acceptedToken.quality
			}
		}
	}
	if bestQuality > 0 {
		return coreHTTPString(bestCandidate)
	}
	return vm.False
}

func parseHTTPAcceptTokens(header string) []httpAcceptedToken {
	if strings.TrimSpace(header) == "" {
		return []httpAcceptedToken{{value: "*", quality: 1}}
	}
	var tokens []httpAcceptedToken
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		value := strings.TrimSpace(fields[0])
		quality := 1.0
		for _, field := range fields[1:] {
			key, valuePart, ok := strings.Cut(strings.TrimSpace(field), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(valuePart), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if value != "" {
			tokens = append(tokens, httpAcceptedToken{value: value, quality: quality})
		}
	}
	return tokens
}

type httpAcceptedToken struct {
	value   string
	quality float64
}

func nativeHTTPRequestGetAll(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http req.getAll expects a header name"
		return vm.Null
	}
	values := data.values.Values(args[0].StringValue())
	elements := make([]OSValue, len(values))
	for index, value := range values {
		elements[index] = coreHTTPString(value)
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})}
}

func nativeHTTPRequestJSON(this OSValue, vm *VM, _ []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	return nativeJSONParse(vm, []OSValue{coreHTTPString(data.body)})
}

// ---------------------------------------------------------------------------
// Response methods
// ---------------------------------------------------------------------------

func httpResponseDataFrom(this OSValue, vm *VM) *httpResponseData {
	if this.Type == OSVObject && this.Opaque != nil {
		if data, ok := (*Object)(this.Opaque).Native.(*httpResponseData); ok {
			return data
		}
	}
	vm.NativeError = "core:http response method called with an invalid response"
	return nil
}

func httpValidStatus(value OSValue) bool {
	return IsNumber(value) && value.Number >= 100 && value.Number <= 599
}

func httpMarkResponseSent(data *httpResponseData) {
	data.sent = true
	if data.response != nil {
		data.response.Set("headersSent", OSValue{Type: OSVBool, Number: 1})
	}
}

func nativeHTTPNext(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	data.nextCalled = true
	if len(args) > 0 && args[0].Type != OSVNull {
		if IsString(args[0]) && args[0].StringValue() == "route" {
			data.skipRoute = true
		} else {
			data.hasErr = true
			data.errValue = args[0]
		}
	} else {
		data.next = true
	}
	return vm.Null
}

func nativeHTTPResponseStatus(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	if !httpValidStatus(args[0]) {
		vm.NativeError = "core:http response status must be between 100 and 599"
		return vm.Null
	}
	data.status = int(args[0].Number)
	return this
}

func nativeHTTPResponseSendStatus(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	if !httpValidStatus(args[0]) {
		vm.NativeError = "core:http response status must be between 100 and 599"
		return vm.Null
	}
	data.status = int(args[0].Number)
	data.body = http.StatusText(data.status)
	httpMarkResponseSent(data)
	return this
}

func nativeHTTPResponseSend(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	if args[0].Type == OSVObject {
		return nativeHTTPResponseJSON(this, vm, args)
	}
	data.body = args[0].String()
	httpMarkResponseSent(data)
	return this
}

func nativeHTTPResponseJSON(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	vm.NativeError = ""
	encoded := nativeJSONStringify(vm, args[:1])
	if vm.NativeError != "" {
		return vm.Null
	}
	data.body = encoded.StringValue()
	if data.jsonEscape {
		data.body = strings.NewReplacer("<", "\\u003c", ">", "\\u003e", "&", "\\u0026").Replace(data.body)
	}
	if data.jsonIndent != "" {
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, []byte(data.body), "", data.jsonIndent); err == nil {
			data.body = formatted.String()
		}
	}
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "application/json; charset=utf-8")
	}
	httpMarkResponseSent(data)
	return this
}

func nativeHTTPResponseJSONP(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	vm.NativeError = ""
	encoded := nativeJSONStringify(vm, args[:1])
	if vm.NativeError != "" {
		return vm.Null
	}
	body := encoded.StringValue()
	if data.jsonEscape {
		body = strings.NewReplacer("<", "\\u003c", ">", "\\u003e", "&", "\\u0026").Replace(body)
	}
	if data.jsonIndent != "" {
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, []byte(body), "", data.jsonIndent); err == nil {
			body = formatted.String()
		}
	}
	callback := ""
	if matched, _ := regexp.MatchString(`^[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$`, data.jsonpCallback); matched {
		callback = data.jsonpCallback
	}
	if callback != "" {
		data.body = "/**/" + callback + "(" + body + ");"
		data.headers.Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		data.body = body
		data.headers.Set("Content-Type", "application/json; charset=utf-8")
	}
	httpMarkResponseSent(data)
	return this
}

func nativeHTTPResponseSet(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	if !IsString(args[0]) {
		vm.NativeError = "core:http res.set expects a header name and value"
		return vm.Null
	}
	data.headers.Set(args[0].StringValue(), args[1].String())
	return this
}

func nativeHTTPResponseGet(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http res.get expects a header name"
		return vm.Null
	}
	if value := data.headers.Get(args[0].StringValue()); value != "" {
		return coreHTTPString(value)
	}
	return vm.Null
}

func nativeHTTPResponseAppend(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http res.append expects a header name and value"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	name := args[0].StringValue()
	value := args[1]
	if value.Type == OSVArray && value.Opaque != nil {
		for _, element := range (*Array)(value.Opaque).Elements {
			data.headers.Add(name, element.String())
		}
	} else {
		data.headers.Add(name, value.String())
	}
	return this
}

func nativeHTTPResponseCookie(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || len(args) < 2 || !IsString(args[0]) {
		vm.NativeError = "core:http res.cookie expects a name, value, and optional options object"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	options := map[string]OSValue{}
	if len(args) > 2 && args[2].Type == OSVObject && args[2].Opaque != nil {
		options = (*Object)(args[2].Opaque).Values
	}
	name := args[0].StringValue()
	value := url.QueryEscape(args[1].String())
	if strings.Contains(value, "+") {
		value = strings.ReplaceAll(value, "+", "%20")
	}
	cookie := &http.Cookie{Name: name, Value: value}
	if maxAge, exists := options["maxAge"]; exists && IsNumber(maxAge) {
		seconds := int(maxAge.Number / 1000)
		if maxAge.Number < 0 {
			seconds = -1
		}
		cookie.MaxAge = seconds
	}
	if expires, exists := options["expires"]; exists {
		switch expires.Type {
		case OSVString:
			cookie.Expires, _ = time.Parse(http.TimeFormat, expires.StringValue())
		case OSVNum:
			cookie.Expires = time.UnixMilli(int64(expires.Number))
		}
	}
	if option, exists := options["path"]; exists && IsString(option) {
		cookie.Path = option.StringValue()
	}
	if option, exists := options["domain"]; exists && IsString(option) {
		cookie.Domain = option.StringValue()
	}
	if option, exists := options["secure"]; exists && CoerceToBool(option) {
		cookie.Secure = true
	}
	if option, exists := options["httpOnly"]; exists && CoerceToBool(option) {
		cookie.HttpOnly = true
	}
	if option, exists := options["sameSite"]; exists {
		if IsString(option) {
			switch strings.ToLower(option.StringValue()) {
			case "lax":
				cookie.SameSite = http.SameSiteLaxMode
			case "strict":
				cookie.SameSite = http.SameSiteStrictMode
			case "none":
				cookie.SameSite = http.SameSiteNoneMode
			}
		} else if CoerceToBool(option) {
			cookie.SameSite = http.SameSiteStrictMode
		}
	}
	data.headers.Add("Set-Cookie", cookie.String())
	return this
}

func nativeHTTPResponseClearCookie(this OSValue, vm *VM, args []OSValue) OSValue {
	if len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http res.clearCookie expects a cookie name and optional options object"
		return vm.Null
	}
	optionValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{Values: make(map[string]OSValue)})}
	if len(args) > 1 && args[1].Type == OSVObject && args[1].Opaque != nil {
		options := (*Object)(args[1].Opaque)
		copyOptions := &Object{Values: make(map[string]OSValue, len(options.Values))}
		for key, value := range options.Values {
			copyOptions.Set(key, value)
		}
		optionValue = OSValue{Type: OSVObject, Opaque: unsafe.Pointer(copyOptions)}
	}
	(*Object)(optionValue.Opaque).Set("expires", coreHTTPString("Thu, 01 Jan 1970 00:00:00 GMT"))
	(*Object)(optionValue.Opaque).Set("maxAge", OSValue{Type: OSVNum, Number: -1})
	return nativeHTTPResponseCookie(this, vm, []OSValue{args[0], coreHTTPString(""), optionValue})
}

func nativeHTTPResponseLocation(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http res.location expects a URL string"
		return vm.Null
	}
	if !data.sent && !data.nextCalled {
		data.headers.Set("Location", args[0].StringValue())
	}
	return this
}

func nativeHTTPResponseLinks(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || args[0].Type != OSVObject || args[0].Opaque == nil {
		vm.NativeError = "core:http res.links expects an object"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	object := (*Object)(args[0].Opaque)
	links := make([]string, 0, len(object.Keys))
	for _, key := range object.Keys {
		if value, exists := object.Values[key]; exists {
			links = append(links, "<"+value.String()+">; rel=\""+key+"\"")
		}
	}
	data.headers.Add("Link", strings.Join(links, ", "))
	return this
}

func nativeHTTPResponseVary(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http res.vary expects a field string"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	field := args[0].StringValue()
	for _, existing := range data.headers.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), field) {
				return this
			}
		}
	}
	data.headers.Add("Vary", field)
	return this
}

func nativeHTTPResponseType(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http res.type expects a type string"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	kind := args[0].StringValue()
	switch kind {
	case "json":
		kind = "application/json; charset=utf-8"
	case "html":
		kind = "text/html; charset=utf-8"
	case "text", "txt":
		kind = "text/plain; charset=utf-8"
	case "xml":
		kind = "application/xml; charset=utf-8"
	}
	data.headers.Set("Content-Type", kind)
	return this
}

func nativeHTTPResponseAttachment(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || (len(args) > 0 && !IsString(args[0])) {
		vm.NativeError = "core:http res.attachment expects an optional filename string"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	filename := ""
	if len(args) > 0 {
		filename = args[0].StringValue()
		if mediaType := mime.TypeByExtension(path.Ext(filename)); mediaType != "" {
			data.headers.Set("Content-Type", mediaType)
		}
	}
	disposition := "attachment"
	if filename != "" {
		disposition = mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(filename)})
	}
	data.headers.Set("Content-Disposition", disposition)
	return this
}

func httpResolveFile(filename string, options OSValue) ([]byte, os.FileInfo, string, error) {
	if filename == "" {
		return nil, nil, "", errors.New("filename must not be empty")
	}
	root := ""
	if options.Type == OSVObject && options.Opaque != nil {
		if value, exists := (*Object)(options.Opaque).Values["root"]; exists && IsString(value) {
			root = value.StringValue()
		}
	}
	var target string
	if root != "" {
		if filepath.IsAbs(filename) {
			return nil, nil, "", errors.New("absolute filename is not allowed with a root")
		}
		rootPath, err := filepath.Abs(root)
		if err != nil {
			return nil, nil, "", err
		}
		rootPath, err = filepath.EvalSymlinks(rootPath)
		if err != nil {
			return nil, nil, "", err
		}
		target, err = filepath.EvalSymlinks(filepath.Join(rootPath, filepath.Clean(filename)))
		if err != nil {
			return nil, nil, "", err
		}
		relative, err := filepath.Rel(rootPath, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, nil, "", errors.New("filename escapes root")
		}
	} else {
		if !filepath.IsAbs(filename) {
			return nil, nil, "", errors.New("filename must be absolute when root is not set")
		}
		var err error
		target, err = filepath.EvalSymlinks(filename)
		if err != nil {
			return nil, nil, "", err
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, "", errors.New("path is not a regular file")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return nil, nil, "", err
	}
	return content, info, target, nil
}

func nativeHTTPResponseSendFile(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http res.sendFile expects a filename and optional options object"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	options := OSValue{}
	if len(args) > 1 {
		options = args[1]
	}
	content, info, filename, err := httpResolveFile(args[0].StringValue(), options)
	if err != nil {
		vm.NativeError = "core:http res.sendFile: " + err.Error()
		return vm.Null
	}
	data.body = string(content)
	if data.headers.Get("Content-Type") == "" {
		kind := mime.TypeByExtension(filepath.Ext(filename))
		if kind == "" {
			kind = "application/octet-stream"
		}
		data.headers.Set("Content-Type", kind)
	}
	data.headers.Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	data.headers.Set("Content-Length", strconv.Itoa(len(content)))
	httpMarkResponseSent(data)
	return this
}

func nativeHTTPResponseDownload(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http res.download expects a filename, optional download name, and options object"
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	options := OSValue{}
	if len(args) > 1 && args[len(args)-1].Type == OSVObject {
		options = args[len(args)-1]
	}
	content, info, filename, err := httpResolveFile(args[0].StringValue(), options)
	if err != nil {
		vm.NativeError = "core:http res.download: " + err.Error()
		return vm.Null
	}
	downloadName := filepath.Base(filename)
	if len(args) > 1 && IsString(args[1]) {
		downloadName = args[1].StringValue()
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": downloadName})
	data.headers.Set("Content-Disposition", disposition)
	kind := mime.TypeByExtension(filepath.Ext(filename))
	if kind == "" {
		kind = "application/octet-stream"
	}
	data.headers.Set("Content-Type", kind)
	data.headers.Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	data.headers.Set("Content-Length", strconv.Itoa(len(content)))
	data.body = string(content)
	httpMarkResponseSent(data)
	return this
}

func nativeHTTPResponseRedirect(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	status, location := http.StatusFound, ""
	switch {
	case len(args) == 1 && IsString(args[0]):
		location = args[0].StringValue()
	case len(args) == 2 && httpValidStatus(args[0]) && IsString(args[1]):
		status, location = int(args[0].Number), args[1].StringValue()
	default:
		vm.NativeError = "core:http res.redirect expects ([status,] url)"
		return vm.Null
	}
	data.status = status
	data.headers.Set("Location", location)
	data.body = http.StatusText(status) + ". Redirecting to " + location
	httpMarkResponseSent(data)
	return this
}

func nativeHTTPResponseEnd(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	if len(args) > 0 && args[0].Type != OSVNull {
		data.body = args[0].String()
	}
	data.sent = true
	return this
}

// ---------------------------------------------------------------------------
// Module exports
// ---------------------------------------------------------------------------

func getCoreHTTP() map[string]OSValue {
	class := OSValue{Type: OSVClass, Opaque: unsafe.Pointer(ensureCoreHTTPClass())}
	return map[string]OSValue{
		"Express": class,
		"Router":  class,
		"fetch":   coreHTTPFunction(-1, nativeHTTPFetch),
	}
}

func coreHTTPFunction(paramCount int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			ParamCount: paramCount,
			Callable:   callable,
		}),
	}
}

// ---------------------------------------------------------------------------
// fetch
// ---------------------------------------------------------------------------

func nativeHTTPFetch(vm *VM, args []OSValue) OSValue {
	if len(args) < 1 || len(args) > 2 || !IsString(args[0]) {
		vm.NativeError = "core:http.fetch expects a URL string and optional options object"
		return vm.Null
	}

	method := http.MethodGet
	body := ""
	headers := make(http.Header)
	if len(args) == 2 {
		if args[1].Type != OSVObject || args[1].Opaque == nil {
			vm.NativeError = "core:http.fetch options must be an object"
			return vm.Null
		}
		options := (*Object)(args[1].Opaque)
		if value, exists := options.Values["method"]; exists {
			if !IsString(value) {
				vm.NativeError = "core:http.fetch options.method must be a string"
				return vm.Null
			}
			method = strings.ToUpper(value.StringValue())
		}
		if value, exists := options.Values["body"]; exists && value.Type != OSVNull {
			body = value.String()
		}
		if value, exists := options.Values["headers"]; exists && value.Type != OSVNull {
			if value.Type != OSVObject || value.Opaque == nil {
				vm.NativeError = "core:http.fetch options.headers must be an object"
				return vm.Null
			}
			for key, value := range (*Object)(value.Opaque).Values {
				headers.Set(key, value.String())
			}
		}
	}

	requestURL := args[0].StringValue()
	promise := &Promise{}
	vm.HTTPFetchActive++
	go func() {
		client := &http.Client{Timeout: fetchTimeout}
		request, err := http.NewRequest(method, requestURL, strings.NewReader(body))
		if err != nil {
			vm.HTTPFetches <- httpFetchResult{promise: promise, err: err}
			return
		}
		request.Header = headers
		response, err := client.Do(request)
		if err != nil {
			vm.HTTPFetches <- httpFetchResult{promise: promise, err: err}
			return
		}
		responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxFetchResponseBytes+1))
		if err == nil && len(responseBody) > maxFetchResponseBytes {
			err = errors.New("response body exceeds 10 MiB limit")
		}
		if closeErr := response.Body.Close(); err == nil {
			err = closeErr
		}
		vm.HTTPFetches <- httpFetchResult{
			promise: promise, status: response.StatusCode, statusText: response.Status,
			url: response.Request.URL.String(), headers: response.Header.Clone(),
			body: unsafe.String(unsafe.SliceData(responseBody), len(responseBody)), err: err,
		}
	}()
	return OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
}

func settleHTTPFetch(vm *VM, result httpFetchResult) {
	if result.err != nil {
		message := "core:http.fetch: " + result.err.Error()
		result.promise.StringRoot = message
		rejectPromise(vm, result.promise, coreHTTPString(message))
		return
	}

	response := &Object{
		StringRoot: result.body,
		Native: &httpFetchResponse{
			status: result.status, statusText: result.statusText, url: result.url,
			headers: result.headers, body: result.body,
		},
		Values: make(map[string]OSValue, 8),
	}
	responseValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(response)}
	response.Set("status", OSValue{Type: OSVNum, Number: float64(result.status)})
	response.Set("statusText", coreHTTPString(result.statusText))
	response.Set("url", coreHTTPString(result.url))
	response.Set("ok", OSValue{Type: OSVBool, Number: boolNumber(result.status >= 200 && result.status < 300)})
	responseHeaders := &Object{Values: make(map[string]OSValue, len(result.headers))}
	for key, values := range result.headers {
		if len(values) == 1 {
			responseHeaders.Set(strings.ToLower(key), coreHTTPString(values[0]))
		} else if len(values) > 1 {
			elements := make([]OSValue, len(values))
			for index, value := range values {
				elements[index] = coreHTTPString(value)
			}
			responseHeaders.Set(strings.ToLower(key), OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: elements})})
		}
	}
	response.Set("headers", OSValue{Type: OSVObject, Opaque: unsafe.Pointer(responseHeaders)})
	response.Set("text", httpBoundMethod(responseValue, 0, nativeHTTPFetchText))
	response.Set("json", httpBoundMethod(responseValue, 0, nativeHTTPFetchJSON))
	resolvePromise(vm, result.promise, responseValue)
}

func resolvedHTTPPromise(vm *VM, value OSValue, stringRoot string) OSValue {
	promise := &Promise{StringRoot: stringRoot}
	resolvePromise(vm, promise, value)
	return OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
}

func nativeHTTPFetchText(this OSValue, vm *VM, _ []OSValue) OSValue {
	response := httpFetchResponseFrom(this, vm)
	if response == nil {
		return vm.Null
	}
	value := coreHTTPString(response.body)
	return resolvedHTTPPromise(vm, value, response.body)
}

func nativeHTTPFetchJSON(this OSValue, vm *VM, _ []OSValue) OSValue {
	response := httpFetchResponseFrom(this, vm)
	if response == nil {
		return vm.Null
	}
	vm.NativeError = ""
	value := nativeJSONParse(vm, []OSValue{coreHTTPString(response.body)})
	if vm.NativeError != "" {
		message := vm.NativeError
		vm.NativeError = ""
		promise := &Promise{}
		rejectPromise(vm, promise, newErrorValue(message, 0))
		return OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
	}
	return resolvedHTTPPromise(vm, value, "")
}

func httpFetchResponseFrom(this OSValue, vm *VM) *httpFetchResponse {
	if this.Type == OSVObject && this.Opaque != nil {
		if response, ok := (*Object)(this.Opaque).Native.(*httpFetchResponse); ok {
			return response
		}
	}
	vm.NativeError = "core:http response method called with an invalid response"
	return nil
}
