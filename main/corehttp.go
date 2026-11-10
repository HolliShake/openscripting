package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
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
	httpMaxMountDepth     = 32
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type httpRoute struct {
	status int
	path   string
	method string
	body   string
	// handler is a script function, or null for static/native-middleware routes.
	handler OSValue
	// middleware is set for native (Go) middleware such as express.json().
	middleware   *httpMiddleware
	routeID      int
	errorHandler bool
}

type httpRouteBuilder struct {
	app  OSValue
	path string
}

// httpLayer is one ordered entry in an app's stack: a route, a use() middleware or a mounted app.
type httpLayer struct {
	method  string
	pattern []string
	prefix  bool
	matcher *httpPathMatcher
	route   httpRoute
	// mount is the live child app for use(path, app); it is read at request time.
	mount *httpAppState
	// first is the leading static segment, used to skip layers without running a regexp.
	first     string
	foldFirst bool
}

type httpPathMatcher struct {
	compiled       *regexp.Regexp
	prefixCompiled *regexp.Regexp
	params         []httpPathParam
	prefix         bool
	regex          bool
}

type httpPathParam struct {
	name  string
	group string
}

// httpMatch is a layer that matched a request path (for any method).
type httpMatch struct {
	route    httpRoute
	params   map[string]string
	app      *httpAppState
	routerID int
	routers  []int
	baseURL  string
	// url is the pathname as the handler sees it (mount/use prefix stripped).
	url string
}

type httpIncoming struct {
	method, path, query, body string
	host, remote              string
	protocol                  string
	secure                    bool
	environment               string
	headers                   http.Header
	requestMethod             string
	etagEnabled               bool
	etagMode                  string
	poweredBy                 bool
	queryParser               string
	jsonIndent                string
	jsonEscape                bool
	jsonReplacer              OSValue
	jsonpCallbackName         string
	trustProxy                OSValue
	subdomainOffset           int
	respond405                bool
	chain                     []httpMatch
	paramSnap                 map[*httpAppState]map[string][]OSValue
	appState                  *httpAppState
	onError                   OSValue
	out                       *httpOutput
	done                      chan struct{}
	dispatch                  *httpDispatch
	timedOut                  bool
	timeout                   time.Duration
}

type httpRequestData struct {
	headers http.Header
	// body is the raw request body (also exposed as req.rawBody and req.json()).
	body   string
	values http.Header
	// secrets are cookie-signing secrets installed by cookieParser(secret).
	secrets    []string
	bodyParsed bool
	dispatch   *httpDispatch
}

type httpFileBody struct {
	path string
	info os.FileInfo
}

type httpResponseData struct {
	status            int
	headers           http.Header
	body              string
	jsonIndent        string
	jsonEscape        bool
	jsonReplacer      OSValue
	jsonpCallbackName string
	jsonpCallback     string
	response          *Object
	sent              bool
	next              bool
	nextCalled        bool
	skipRoute         bool
	skipRouter        bool
	hasErr            bool
	errValue          OSValue
	// streaming is set after res.write(); the response ends only on res.end().
	streaming bool
	// file, when set, is streamed from disk (with Range/If-Range support) at finish.
	file         *httpFileBody
	finishHooks  []func(*httpDispatch)
	refreshFresh func()
	app          *httpAppState
	dispatch     *httpDispatch
	requestData  *httpRequestData
}

func (data *httpResponseData) touch() {
	if data.refreshFresh != nil {
		data.refreshFresh()
	}
}

type httpDispatch struct {
	vm         *VM
	incoming   *httpIncoming
	request    OSValue
	response   OSValue
	next       OSValue
	data       *httpResponseData
	reqData    *httpRequestData
	method     string
	queryPart  string
	index      int
	errorMode  bool
	errValue   OSValue
	finished   bool
	paramCache map[string]bool
	listeners  map[string][]OSValue
	start      time.Time
}

type httpFetchResult struct {
	promise    *Promise
	status     int
	statusText string
	url        string
	headers    http.Header
	body       string
	err        error
}

type httpFetchResponse struct {
	status     int
	statusText string
	url        string
	headers    http.Header
	body       string
}

type httpAppState struct {
	mu            sync.RWMutex
	layers        []httpLayer
	paramHandlers map[string][]OSValue
	onError       OSValue
	server        *http.Server
	done          chan struct{}
	shutdownDone  chan struct{}
	address       string
	started       bool
	closed        bool
	settings      map[string]OSValue
	engines       map[string]OSValue
	viewCache     map[string]string
	listeners     map[string][]OSValue
	vm            *VM
	appValue      OSValue
	parent        *httpAppState
	mountPath     string
	mergeParams   bool
}

var (
	coreHTTPClass *Class
	coreHTTPOnce  sync.Once
	httpRouteID   atomic.Uint64
)

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func coreHTTPString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func httpNum(n float64) OSValue { return OSValue{Type: OSVNum, Number: n} }

func httpBool(b bool) OSValue { return OSValue{Type: OSVBool, Number: boolNumber(b)} }

func httpArray(values []OSValue) OSValue {
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: values})}
}

func httpStringArray(values []string) OSValue {
	elements := make([]OSValue, len(values))
	for index, value := range values {
		elements[index] = coreHTTPString(value)
	}
	return httpArray(elements)
}

func httpErrorValue(status int, message string) OSValue {
	value := newErrorValue(message, 0)
	if value.Type == OSVObject && value.Opaque != nil {
		object := (*Object)(value.Opaque)
		object.Set("status", httpNum(float64(status)))
		object.Set("statusCode", httpNum(float64(status)))
	}
	return value
}

func httpValueObject(values map[string]OSValue) OSValue {
	object := &Object{Values: make(map[string]OSValue, len(values))}
	for key, value := range values {
		object.Set(key, value)
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func httpStringObject(values map[string]string) OSValue {
	object := &Object{Values: make(map[string]OSValue, len(values))}
	for key, value := range values {
		object.Set(key, coreHTTPString(value))
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func httpContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func httpHasRouter(routers []int, id int) bool {
	for _, router := range routers {
		if router == id {
			return true
		}
	}
	return false
}

func httpMethodMatches(layerMethod, requestMethod string) bool {
	return layerMethod == "" || layerMethod == requestMethod ||
		(requestMethod == http.MethodHead && layerMethod == http.MethodGet)
}

func httpETag(body, mode string) string {
	if mode == "strong" {
		digest := sha256.Sum256([]byte(body))
		return `"` + base64.RawStdEncoding.EncodeToString(digest[:]) + `"`
	}
	digest := sha1.Sum([]byte(body))
	return `W/"` + base64.RawStdEncoding.EncodeToString(digest[:]) + `"`
}

// httpWithCharset appends charset=utf-8 to textual content types that lack one.
func httpWithCharset(value string) string {
	base, params, err := mime.ParseMediaType(value)
	if err != nil {
		return value
	}
	if _, has := params["charset"]; has {
		return value
	}
	lower := strings.ToLower(base)
	textual := strings.HasPrefix(lower, "text/") || lower == "application/json" ||
		lower == "application/javascript" || lower == "application/xml" ||
		strings.HasSuffix(lower, "+json") || strings.HasSuffix(lower, "+xml")
	if textual {
		return value + "; charset=utf-8"
	}
	return value
}

func httpMimeFor(kind string) string {
	switch kind {
	case "json":
		return "application/json"
	case "html":
		return "text/html"
	case "text", "txt":
		return "text/plain"
	case "xml":
		return "application/xml"
	case "urlencoded":
		return "application/x-www-form-urlencoded"
	case "multipart":
		return "multipart/form-data"
	}
	if strings.Contains(kind, "/") {
		return kind
	}
	extension := kind
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	if found := mime.TypeByExtension(extension); found != "" {
		base, _, err := mime.ParseMediaType(found)
		if err == nil {
			return base
		}
	}
	return ""
}

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
					ParamCount: -1,
					Callable:   nativeHTTPConstructor,
				}),
			},
		}
	})
	return coreHTTPClass
}

func nativeHTTPConstructor(_ *VM, args []OSValue) OSValue {
	state := &httpAppState{}
	if len(args) > 0 && args[0].Type == OSVObject && args[0].Opaque != nil {
		if value, ok := (*Object)(args[0].Opaque).Values["mergeParams"]; ok {
			state.mergeParams = CoerceToBool(value)
		}
	}
	instance := &Object{
		ClassName: "Express",
		Class:     ensureCoreHTTPClass(),
		Native:    state,
		Values:    make(map[string]OSValue, 24),
	}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
	state.appValue = value
	instance.Set("locals", httpValueObject(nil))
	instance.Set("mountpath", coreHTTPString("/"))
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
	instance.Set("engine", httpBoundMethod(value, 2, nativeHTTPEngine))
	instance.Set("on", httpBoundMethod(value, 2, nativeHTTPAppOn))
	instance.Set("emit", httpBoundMethod(value, -1, nativeHTTPAppEmit))
	instance.Set("path", httpBoundMethod(value, 0, nativeHTTPAppPath))
	instance.Set("listen", httpBoundMethod(value, -1, nativeHTTPListen))
	instance.Set("listenTLS", httpBoundMethod(value, -1, nativeHTTPListenTLS))
	instance.Set("address", httpBoundMethod(value, 0, nativeHTTPAddress))
	instance.Set("close", httpBoundMethod(value, -1, nativeHTTPClose))
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

// ---------------------------------------------------------------------------
// Settings (with inheritance from the parent app)
// ---------------------------------------------------------------------------

func httpSettingDefault(name string) (OSValue, bool) {
	switch name {
	case "env":
		env := os.Getenv("NODE_ENV")
		if env == "" {
			env = "development"
		}
		return coreHTTPString(env), true
	case "etag":
		return coreHTTPString("weak"), true
	case "x-powered-by":
		return httpBool(true), true
	case "query parser":
		return coreHTTPString("simple"), true
	case "subdomain offset":
		return httpNum(2), true
	case "trust proxy", "case sensitive routing", "strict routing", "json escape", "405":
		return httpBool(false), true
	case "jsonp callback name":
		return coreHTTPString("callback"), true
	case "views":
		return coreHTTPString("views"), true
	}
	return OSValue{}, false
}

// setting resolves a setting on this app, then its parents, then the defaults.
func (state *httpAppState) setting(name string) (OSValue, bool) {
	current := state
	for depth := 0; current != nil && depth < httpMaxMountDepth; depth++ {
		current.mu.RLock()
		value, ok := current.settings[name]
		parent := current.parent
		current.mu.RUnlock()
		if ok {
			return value, true
		}
		current = parent
	}
	return httpSettingDefault(name)
}

func (state *httpAppState) settingString(name, fallback string) string {
	if value, ok := state.setting(name); ok && IsString(value) {
		return value.StringValue()
	}
	return fallback
}

func (state *httpAppState) settingBool(name string, fallback bool) bool {
	if value, ok := state.setting(name); ok {
		return CoerceToBool(value)
	}
	return fallback
}

func (state *httpAppState) settingNumber(name string, fallback float64) float64 {
	if value, ok := state.setting(name); ok && IsNumber(value) {
		return value.Number
	}
	return fallback
}

func (state *httpAppState) engine(ext string) (OSValue, bool) {
	current := state
	for depth := 0; current != nil && depth < httpMaxMountDepth; depth++ {
		current.mu.RLock()
		value, ok := current.engines[ext]
		parent := current.parent
		current.mu.RUnlock()
		if ok {
			return value, true
		}
		current = parent
	}
	return OSValue{}, false
}

func httpETagConfig(value OSValue, ok bool) (bool, string) {
	if !ok {
		return true, "weak"
	}
	if IsString(value) {
		return true, value.StringValue()
	}
	return CoerceToBool(value), "weak"
}

func httpJSONIndent(value OSValue, ok bool) string {
	if !ok {
		return ""
	}
	indent := ""
	if IsString(value) {
		indent = value.StringValue()
	} else if IsNumber(value) && value.Number > 0 {
		indent = strings.Repeat(" ", min(int(value.Number), 10))
	}
	if len(indent) > 10 {
		indent = indent[:10]
	}
	return indent
}

func nativeHTTPSetSetting(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || len(args) < 2 || !IsString(args[0]) {
		vm.NativeError = "core:http app.set expects a setting name and value"
		return vm.Null
	}
	name := args[0].StringValue()
	value := args[1]
	switch name {
	case "body limit":
		if !IsNumber(value) || value.Number < 1 {
			vm.NativeError = "core:http body limit must be a positive number of bytes"
			return vm.Null
		}
	case "handler timeout", "shutdown timeout", "subdomain offset", "max url length", "max headers", "max query params":
		if !IsNumber(value) || value.Number < 0 {
			vm.NativeError = "core:http " + name + " must be a non-negative number"
			return vm.Null
		}
	case "env", "view engine":
		if !IsString(value) {
			vm.NativeError = "core:http " + name + " setting must be a string"
			return vm.Null
		}
	case "etag":
		if IsString(value) && value.StringValue() != "weak" && value.StringValue() != "strong" {
			vm.NativeError = "core:http etag setting must be false, true, weak, or strong"
			return vm.Null
		}
	case "query parser":
		if !IsString(value) || (value.StringValue() != "simple" && value.StringValue() != "extended") {
			vm.NativeError = "core:http query parser must be simple or extended"
			return vm.Null
		}
	case "json spaces":
		if !IsString(value) && !IsNumber(value) {
			vm.NativeError = "core:http json spaces must be a string or number"
			return vm.Null
		}
	case "json escape":
		value = httpBool(CoerceToBool(value))
	case "json replacer":
		if value.Type != OSVFunction && value.Type != OSVArray && value.Type != OSVNull {
			vm.NativeError = "core:http json replacer must be a function or an array of keys"
			return vm.Null
		}
	case "jsonp callback name":
		if !IsString(value) || value.StringValue() == "" {
			vm.NativeError = "core:http jsonp callback name must be a non-empty string"
			return vm.Null
		}
	case "views":
		if !IsString(value) && value.Type != OSVArray {
			vm.NativeError = "core:http views must be a string or an array of strings"
			return vm.Null
		}
	}
	state.mu.Lock()
	if state.settings == nil {
		state.settings = make(map[string]OSValue)
	}
	state.settings[name] = value
	state.mu.Unlock()
	return this
}

func nativeHTTPGetSetting(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || !IsString(args[0]) {
		vm.NativeError = "core:http app.get expects a setting name"
		return vm.Null
	}
	if value, exists := state.setting(args[0].StringValue()); exists {
		return value
	}
	return vm.Null
}

func nativeHTTPSetFlag(this OSValue, vm *VM, args []OSValue, enabled bool) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http app flag expects a setting name"
		return vm.Null
	}
	state.mu.Lock()
	if state.settings == nil {
		state.settings = make(map[string]OSValue)
	}
	state.settings[args[0].StringValue()] = httpBool(enabled)
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
	if !ok || len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http flag query expects a setting name"
		return vm.False
	}
	return httpBool(state.settingBool(args[0].StringValue(), false) == expected)
}

func nativeHTTPEnabled(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPFlag(this, vm, args, true)
}

func nativeHTTPDisabled(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPFlag(this, vm, args, false)
}

// ---------------------------------------------------------------------------
// Events, mounting metadata, engines
// ---------------------------------------------------------------------------

func nativeHTTPAppOn(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || len(args) < 2 || !IsString(args[0]) || args[1].Type != OSVFunction {
		vm.NativeError = "core:http app.on expects an event name and a function"
		return vm.Null
	}
	state.mu.Lock()
	if state.listeners == nil {
		state.listeners = make(map[string][]OSValue)
	}
	state.listeners[args[0].StringValue()] = append(state.listeners[args[0].StringValue()], args[1])
	state.mu.Unlock()
	return this
}

func httpEmit(vm *VM, state *httpAppState, event string, args ...OSValue) {
	state.mu.RLock()
	handlers := append([]OSValue(nil), state.listeners[event]...)
	state.mu.RUnlock()
	for _, handler := range handlers {
		runHTTPHandler(vm, handler, args...)
	}
}

func nativeHTTPAppEmit(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http app.emit expects an event name"
		return vm.Null
	}
	httpEmit(vm, state, args[0].StringValue(), args[1:]...)
	return this
}

func nativeHTTPAppPath(this OSValue, vm *VM, _ []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	result := ""
	current := state
	for depth := 0; current != nil && depth < httpMaxMountDepth; depth++ {
		current.mu.RLock()
		mountPath, parent := current.mountPath, current.parent
		current.mu.RUnlock()
		result = strings.TrimSuffix(mountPath, "/") + result
		current = parent
	}
	return coreHTTPString(result)
}

func nativeHTTPEngine(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok || len(args) < 2 || !IsString(args[0]) || args[1].Type != OSVFunction {
		vm.NativeError = "core:http app.engine expects an extension and a render function"
		return vm.Null
	}
	ext := args[0].StringValue()
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	state.mu.Lock()
	if state.engines == nil {
		state.engines = make(map[string]OSValue)
	}
	state.engines[ext] = args[1]
	state.mu.Unlock()
	return this
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

func httpFirstSegment(path string) string {
	path = strings.TrimPrefix(path, "/")
	if cut := strings.IndexByte(path, '/'); cut >= 0 {
		return path[:cut]
	}
	return path
}

func compileHTTPPath(pattern []string) (*httpPathMatcher, error) {
	return compileHTTPPathWithOptions(pattern, false, false)
}

func compileHTTPPathWithOptions(pattern []string, caseSensitive, strict bool) (*httpPathMatcher, error) {
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
			capture := "httpParam" + strconv.Itoa(group)
			if repeated {
				expression.WriteString("(?:/(?P<" + capture + ">(?:" + constraint + ")(?:/(?:" + constraint + "))*))")
				if repeatOptional {
					expression.WriteByte('?')
				}
			} else if optional {
				expression.WriteString("(?:/(?P<" + capture + ">" + constraint + "))?")
			} else {
				expression.WriteString("/(?P<" + capture + ">" + constraint + ")")
			}
			params = append(params, httpPathParam{name: name, group: capture})
			group++
			continue
		}
		expression.WriteByte('/')
		expression.WriteString(regexp.QuoteMeta(segment))
	}
	if len(pattern) == 0 {
		expression.WriteString("/")
	}
	base := expression.String()
	prefixExpression := base + "(?P<httpRest>/.*)?$"
	if len(pattern) == 0 {
		prefixExpression = "^(?P<httpRest>/.*)?$"
	}
	full := base
	if !strict && len(pattern) != 0 {
		full += "/?"
	}
	full += "$"
	if !caseSensitive {
		full = "(?i:" + full + ")"
		prefixExpression = "(?i:" + prefixExpression + ")"
	}
	compiled, err := regexp.Compile(full)
	if err != nil {
		return nil, err
	}
	prefixCompiled, err := regexp.Compile(prefixExpression)
	if err != nil {
		return nil, err
	}
	return &httpPathMatcher{compiled: compiled, prefixCompiled: prefixCompiled, params: params}, nil
}

// compileHTTPRegex builds a matcher for regex routes; capture groups become req.params[0], [1], ...
func compileHTTPRegex(expression, flags string) (*httpPathMatcher, error) {
	if strings.Contains(flags, "i") {
		expression = "(?i:" + expression + ")"
	}
	compiled, err := regexp.Compile(expression)
	if err != nil {
		return nil, err
	}
	return &httpPathMatcher{compiled: compiled, regex: true}, nil
}

func httpRegexParams(compiled *regexp.Regexp, values []string) map[string]string {
	params := make(map[string]string, len(values))
	names := compiled.SubexpNames()
	for index := 1; index < len(values); index++ {
		params[strconv.Itoa(index-1)] = values[index]
		if names[index] != "" {
			params[names[index]] = values[index]
		}
	}
	return params
}

func (matcher *httpPathMatcher) match(path string) (map[string]string, bool) {
	if matcher == nil || matcher.compiled == nil {
		return nil, false
	}
	values := matcher.compiled.FindStringSubmatch(path)
	if values == nil {
		return nil, false
	}
	if matcher.regex {
		return httpRegexParams(matcher.compiled, values), true
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

// matchPrefix matches use()/mount layers and returns the path remainder (starting with "/", or "").
func (matcher *httpPathMatcher) matchPrefix(path string) (map[string]string, string, bool) {
	if matcher == nil || matcher.prefixCompiled == nil {
		return nil, "", false
	}
	values := matcher.prefixCompiled.FindStringSubmatch(path)
	if values == nil {
		return nil, "", false
	}
	params := make(map[string]string, len(matcher.params))
	for _, param := range matcher.params {
		group := matcher.prefixCompiled.SubexpIndex(param.group)
		if group >= 0 && group < len(values) && values[group] != "" {
			params[param.name] = values[group]
		}
	}
	rest := ""
	if group := matcher.prefixCompiled.SubexpIndex("httpRest"); group >= 0 && group < len(values) {
		rest = values[group]
	}
	return params, rest, true
}

// matchSegments is a regexp-free matcher kept for unit tests and simple callers.
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

func httpAllowedFromMatches(chain []httpMatch) []string {
	methods := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	appendMethod := func(method string) {
		if _, exists := seen[method]; exists {
			return
		}
		seen[method] = struct{}{}
		methods = append(methods, method)
	}
	for _, match := range chain {
		if match.route.method == "" || match.route.middleware != nil {
			continue
		}
		appendMethod(match.route.method)
		if match.route.method == http.MethodGet {
			appendMethod(http.MethodHead)
		}
	}
	if len(methods) != 0 {
		appendMethod(http.MethodOptions)
	}
	return methods
}

// ---------------------------------------------------------------------------
// Chain collection (live router tree)
// ---------------------------------------------------------------------------

type httpCollector struct {
	nextRouter int
	snap       map[*httpAppState]map[string][]OSValue
	onError    OSValue
}

func (c *httpCollector) collect(state *httpAppState, path, baseURL string, inherit map[string]string, routers []int, chain *[]httpMatch, depth int) {
	if depth > httpMaxMountDepth {
		return
	}
	state.mu.RLock()
	layers := state.layers
	onErr := state.onError
	mergeParams := state.mergeParams
	var handlers map[string][]OSValue
	if len(state.paramHandlers) != 0 {
		handlers = make(map[string][]OSValue, len(state.paramHandlers))
		for name, list := range state.paramHandlers {
			handlers[name] = append([]OSValue(nil), list...)
		}
	}
	state.mu.RUnlock()
	if _, seen := c.snap[state]; !seen {
		c.snap[state] = handlers
	}
	if c.onError.Type != OSVFunction && onErr.Type == OSVFunction {
		c.onError = onErr
	}
	routerID := routers[len(routers)-1]
	first := httpFirstSegment(path)
	for index := range layers {
		layer := &layers[index]
		if layer.first != "" {
			if layer.foldFirst {
				if !strings.EqualFold(layer.first, first) {
					continue
				}
			} else if layer.first != first {
				continue
			}
		}
		var params map[string]string
		matched := false
		rest := ""
		if layer.mount != nil || layer.prefix {
			params, rest, matched = layer.matcher.matchPrefix(path)
		} else {
			params, matched = layer.matcher.match(path)
		}
		if !matched {
			continue
		}
		matchedPrefix := path[:len(path)-len(rest)]
		childPath := rest
		if childPath == "" {
			childPath = "/"
		}
		if layer.mount != nil {
			c.nextRouter++
			childRouters := append(append([]int(nil), routers...), c.nextRouter)
			merged := make(map[string]string, len(inherit)+len(params))
			for key, value := range inherit {
				merged[key] = value
			}
			for key, value := range params {
				merged[key] = value
			}
			c.collect(layer.mount, childPath, baseURL+matchedPrefix, merged, childRouters, chain, depth+1)
			continue
		}
		if mergeParams && len(inherit) != 0 {
			merged := make(map[string]string, len(inherit)+len(params))
			for key, value := range inherit {
				merged[key] = value
			}
			for key, value := range params {
				merged[key] = value
			}
			params = merged
		}
		match := httpMatch{route: layer.route, params: params, app: state, routerID: routerID, routers: routers, baseURL: baseURL, url: path}
		if layer.prefix {
			match.baseURL = baseURL + matchedPrefix
			match.url = childPath
		}
		*chain = append(*chain, match)
	}
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
	case httpMiddlewareFrom(value) != nil:
		return true
	}
	vm.NativeError = "core:http expects a handler function (only the last route target may be a string)"
	return false
}

// httpCompilePathValue accepts a path string or a {regex, flags} object.
func httpCompilePathValue(state *httpAppState, vm *VM, value OSValue, prefix bool) (string, []string, *httpPathMatcher, bool) {
	if IsString(value) {
		route := value.StringValue()
		if route == "" || route[0] != '/' {
			vm.NativeError = "core:http route path must start with /"
			return "", nil, nil, false
		}
		pattern := splitPath(route)
		caseSensitive := state.settingBool("case sensitive routing", false)
		strict := state.settingBool("strict routing", false)
		if strict && strings.HasSuffix(route, "/") && len(pattern) != 0 {
			pattern = append(pattern, "")
		}
		matcher, err := compileHTTPPathWithOptions(pattern, caseSensitive, strict)
		if err != nil {
			vm.NativeError = "core:http invalid route pattern: " + err.Error()
			return "", nil, nil, false
		}
		return route, pattern, matcher, true
	}
	if value.Type == OSVObject && value.Opaque != nil && !prefix {
		object := (*Object)(value.Opaque)
		if expression, ok := object.Values["regex"]; ok && IsString(expression) {
			flags := ""
			if f, ok := object.Values["flags"]; ok && IsString(f) {
				flags = f.StringValue()
			}
			matcher, err := compileHTTPRegex(expression.StringValue(), flags)
			if err != nil {
				vm.NativeError = "core:http invalid route regex: " + err.Error()
				return "", nil, nil, false
			}
			return "/" + expression.StringValue() + "/", nil, matcher, true
		}
	}
	vm.NativeError = "core:http route path must be a string starting with / or a {regex} object"
	return "", nil, nil, false
}

func httpLayerFirst(pattern []string, caseSensitive bool) (string, bool) {
	if len(pattern) == 0 {
		return "", false
	}
	segment := pattern[0]
	if segment == "" || segment == "*" || strings.HasPrefix(segment, ":") {
		return "", false
	}
	return segment, !caseSensitive
}

func nativeHTTPVerb(this OSValue, vm *VM, args []OSValue, method string) OSValue {
	if method == http.MethodGet && len(args) == 1 && IsString(args[0]) {
		return nativeHTTPGetSetting(this, vm, args)
	}
	if len(args) >= 2 && args[0].Type == OSVArray && args[0].Opaque != nil {
		for _, route := range (*Array)(args[0].Opaque).Elements {
			if httpRegisterPath(this, vm, method, route, false, 200, args[1:]).Type == OSVNull {
				return vm.Null
			}
		}
		return this
	}
	if len(args) < 2 {
		vm.NativeError = "core:http route expects a path followed by one or more handlers"
		return vm.Null
	}
	return httpRegisterPath(this, vm, method, args[0], false, 200, args[1:])
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
			if httpRegister(data.app, vm, method, data.path, false, 200, args).Type == OSVNull {
				return vm.Null
			}
			return this
		}))
	}
	return value
}

func httpRegister(this OSValue, vm *VM, method, path string, prefix bool, status int, targets []OSValue) OSValue {
	return httpRegisterPath(this, vm, method, coreHTTPString(path), prefix, status, targets)
}

func httpRegisterPath(this OSValue, vm *VM, method string, pathValue OSValue, prefix bool, status int, targets []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	label, pattern, matcher, ok := httpCompilePathValue(state, vm, pathValue, prefix)
	if !ok {
		return vm.Null
	}
	caseSensitive := state.settingBool("case sensitive routing", false)
	first, fold := httpLayerFirst(pattern, caseSensitive)
	layers := make([]httpLayer, 0, len(targets))
	routeID := int(httpRouteID.Add(1))
	for i, target := range targets {
		if !httpValidTarget(vm, target, i == len(targets)-1) {
			return vm.Null
		}
		route := httpRoute{status: status, path: label, method: method, routeID: routeID}
		switch {
		case target.Type == OSVFunction:
			route.handler = target
			route.errorHandler = (*Function)(target.Opaque).ParamCount == 4
		case target.Type == OSVString:
			route.body = target.StringValue()
		default:
			route.middleware = httpMiddlewareFrom(target)
		}
		layers = append(layers, httpLayer{method: method, pattern: pattern, matcher: matcher, prefix: prefix, route: route, first: first, foldFirst: fold})
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
	prefixes := []OSValue{coreHTTPString("/")}
	if len(args) > 0 && IsString(args[0]) {
		prefixes = []OSValue{args[0]}
		args = args[1:]
	} else if len(args) > 0 && args[0].Type == OSVArray && args[0].Opaque != nil {
		prefixes = (*Array)(args[0].Opaque).Elements
		args = args[1:]
	}
	var handlers []OSValue
	for _, arg := range args {
		if arg.Type == OSVArray && arg.Opaque != nil {
			handlers = append(handlers, (*Array)(arg.Opaque).Elements...)
		} else {
			handlers = append(handlers, arg)
		}
	}
	if len(handlers) == 0 {
		vm.NativeError = "core:http.use expects a handler function or an Express/Router instance"
		return vm.Null
	}
	for _, prefixValue := range prefixes {
		if !IsString(prefixValue) {
			vm.NativeError = "core:http.use path must be a string starting with /"
			return vm.Null
		}
		prefix := prefixValue.StringValue()
		for _, handler := range handlers {
			if sub, isApp := httpAppFromValue(handler); isApp {
				if sub == state {
					vm.NativeError = "core:http.use cannot mount an app on itself"
					return vm.Null
				}
				label, pattern, matcher, ok := httpCompilePathValue(state, vm, prefixValue, true)
				if !ok {
					return vm.Null
				}
				first, fold := httpLayerFirst(pattern, state.settingBool("case sensitive routing", false))
				state.mu.Lock()
				if state.closed {
					state.mu.Unlock()
					vm.NativeError = "core:http cannot add routes after close"
					return vm.Null
				}
				state.layers = append(state.layers, httpLayer{
					pattern: pattern, matcher: matcher, prefix: true, mount: sub, first: first, foldFirst: fold,
					route: httpRoute{path: label, routeID: int(httpRouteID.Add(1))},
				})
				state.mu.Unlock()
				sub.mu.Lock()
				sub.parent = state
				sub.mountPath = prefix
				sub.mu.Unlock()
				child := (*Object)(handler.Opaque)
				child.Set("mountpath", coreHTTPString(prefix))
				child.Set("parent", state.appValue)
				httpEmit(vm, sub, "mount", state.appValue)
				continue
			}
			if handler.Type != OSVFunction && httpMiddlewareFrom(handler) == nil {
				vm.NativeError = "core:http.use expects handler functions or Express/Router instances"
				return vm.Null
			}
			if httpRegisterPath(this, vm, "", prefixValue, true, 200, []OSValue{handler}).Type == OSVNull {
				return vm.Null
			}
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

func httpLoadCertificate(cert, key string) (tls.Certificate, error) {
	if strings.Contains(cert, "-----BEGIN") {
		return tls.X509KeyPair([]byte(cert), []byte(key))
	}
	return tls.LoadX509KeyPair(cert, key)
}

func httpOptDuration(options *Object, key string) time.Duration {
	if options == nil {
		return 0
	}
	if value, ok := options.Values[key]; ok && IsNumber(value) && value.Number > 0 {
		return time.Duration(value.Number * float64(time.Millisecond))
	}
	return 0
}

func nativeHTTPListen(this OSValue, vm *VM, args []OSValue) OSValue {
	return httpListen(this, vm, args)
}

// listenTLS(addr, cert, key [, callback]) is shorthand for listen(addr, {cert, key}).
func nativeHTTPListenTLS(this OSValue, vm *VM, args []OSValue) OSValue {
	if len(args) < 3 || !IsString(args[1]) || !IsString(args[2]) {
		vm.NativeError = "core:http.listenTLS expects an address, certificate and key"
		return vm.Null
	}
	options := httpValueObject(map[string]OSValue{"cert": args[1], "key": args[2]})
	rebuilt := []OSValue{args[0], options}
	if len(args) > 3 {
		rebuilt = append(rebuilt, args[3])
	}
	return httpListen(this, vm, rebuilt)
}

func httpListen(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	var callback OSValue
	if n := len(args); n > 0 && args[n-1].Type == OSVFunction {
		callback = args[n-1]
		args = args[:n-1]
	}
	if len(args) == 0 {
		vm.NativeError = "core:http.listen expects an address, port or options object"
		return vm.Null
	}
	var options *Object
	addr := ""
	switch {
	case IsString(args[0]):
		addr = args[0].StringValue()
		if _, err := strconv.Atoi(addr); err == nil {
			addr = ":" + addr
		}
	case IsNumber(args[0]):
		addr = ":" + strconv.Itoa(int(args[0].Number))
	case args[0].Type == OSVObject && args[0].Opaque != nil:
		options = (*Object)(args[0].Opaque)
	default:
		vm.NativeError = "core:http.listen expects an address string, port number or options object"
		return vm.Null
	}
	if len(args) > 1 && args[1].Type == OSVObject && args[1].Opaque != nil {
		options = (*Object)(args[1].Opaque)
	}
	if addr == "" {
		host, port := "", "0"
		if value, ok := options.Values["host"]; ok && IsString(value) {
			host = value.StringValue()
		}
		if value, ok := options.Values["port"]; ok {
			port = value.String()
		}
		addr = net.JoinHostPort(host, port)
	}
	server := &http.Server{
		Handler:           state,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       httpOptDuration(options, "readTimeout"),
		WriteTimeout:      httpOptDuration(options, "writeTimeout"),
		IdleTimeout:       httpOptDuration(options, "idleTimeout"),
	}
	if timeout := httpOptDuration(options, "readHeaderTimeout"); timeout > 0 {
		server.ReadHeaderTimeout = timeout
	}
	if options != nil {
		if value, ok := options.Values["maxHeaderBytes"]; ok && IsNumber(value) && value.Number > 0 {
			server.MaxHeaderBytes = int(value.Number)
		}
		if value, ok := options.Values["handlerTimeout"]; ok && IsNumber(value) {
			state.mu.Lock()
			if state.settings == nil {
				state.settings = make(map[string]OSValue)
			}
			state.settings["handler timeout"] = value
			state.mu.Unlock()
		}
	}
	secure := false
	if options != nil {
		source := options
		if nested, ok := options.Values["tls"]; ok && nested.Type == OSVObject && nested.Opaque != nil {
			source = (*Object)(nested.Opaque)
		}
		cert, hasCert := source.Values["cert"]
		key, hasKey := source.Values["key"]
		if hasCert && hasKey && IsString(cert) && IsString(key) {
			pair, err := httpLoadCertificate(cert.StringValue(), key.StringValue())
			if err != nil {
				vm.NativeError = "core:http.listen: " + err.Error()
				return vm.Null
			}
			// ServeTLS enables HTTP/2 when the TLS config offers h2.
			server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12}
			secure = true
		}
	}
	state.mu.Lock()
	if state.started || state.closed {
		state.mu.Unlock()
		vm.NativeError = "core:http server can only be started once"
		return vm.Null
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		state.mu.Unlock()
		vm.NativeError = "core:http.listen: " + err.Error()
		return vm.Null
	}
	state.server = server
	state.vm = vm
	state.done = make(chan struct{})
	state.address = listener.Addr().String()
	state.started = true
	address := state.address
	go func(server *http.Server, listener net.Listener, secure bool) {
		defer close(state.done)
		defer func() { vm.HTTPDone <- state }()
		if secure {
			_ = server.ServeTLS(listener, "", "")
		} else {
			_ = server.Serve(listener)
		}
		state.mu.RLock()
		wait := state.shutdownDone
		state.mu.RUnlock()
		if wait != nil {
			<-wait
		}
	}(server, listener, secure)
	vm.HTTPApps = append(vm.HTTPApps, state)
	vm.HTTPActive++
	state.mu.Unlock()
	if callback.Type == OSVFunction {
		runHTTPHandler(vm, callback)
	}
	return coreHTTPString(address)
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

// close([timeoutMs]) drains in-flight requests, then closes. close(0) or close(false) is abrupt.
func nativeHTTPClose(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.False
	}
	graceful := true
	timeout := time.Duration(state.settingNumber("shutdown timeout", 10000) * float64(time.Millisecond))
	if len(args) > 0 {
		switch {
		case IsNumber(args[0]) && args[0].Number <= 0:
			graceful = false
		case IsNumber(args[0]):
			timeout = time.Duration(args[0].Number * float64(time.Millisecond))
		case args[0].Type == OSVBool && !CoerceToBool(args[0]):
			graceful = false
		}
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
	if !graceful {
		if err := state.server.Close(); err != nil {
			vm.NativeError = "core:http.close: " + err.Error()
			return vm.False
		}
		return vm.True
	}
	state.shutdownDone = make(chan struct{})
	go func(server *http.Server, finished chan struct{}) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
		}
		close(finished)
	}(state.server, state.shutdownDone)
	return vm.True
}

// ---------------------------------------------------------------------------
// Output (listener-side writer, safe to use from the VM thread while the listener waits)
// ---------------------------------------------------------------------------

type httpOutput struct {
	mu       sync.Mutex
	w        http.ResponseWriter
	r        *http.Request
	detached bool
	started  bool
}

func (o *httpOutput) closedByClient() bool { return o.r.Context().Err() != nil }

// begin writes the status line and headers once. The caller holds o.mu.
func (o *httpOutput) begin(status int, headers http.Header) bool {
	if o.detached {
		return false
	}
	if !o.started {
		for key, values := range headers {
			for _, value := range values {
				o.w.Header().Add(key, value)
			}
		}
		o.w.WriteHeader(status)
		o.started = true
	}
	return true
}

func (o *httpOutput) writeFull(status int, headers http.Header, body string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.begin(status, headers) {
		return
	}
	if o.r.Method != http.MethodHead {
		_, _ = io.WriteString(o.w, body)
	}
}

func (o *httpOutput) chunk(status int, headers http.Header, data string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closedByClient() || !o.begin(status, headers) {
		return errors.New("connection closed")
	}
	if o.r.Method != http.MethodHead && data != "" {
		if _, err := io.WriteString(o.w, data); err != nil {
			return err
		}
	}
	if flusher, ok := o.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (o *httpOutput) flush(status int, headers http.Header) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.begin(status, headers) {
		if flusher, ok := o.w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func (o *httpOutput) detach() {
	o.mu.Lock()
	o.detached = true
	o.mu.Unlock()
}

func (o *httpOutput) fail(status int, message string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.started && !o.detached {
		http.Error(o.w, message, status)
	}
}

// serveFile streams a file; 200 responses use http.ServeContent for Range/If-Range/conditional support.
func (o *httpOutput) serveFile(status int, headers http.Header, file *httpFileBody) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.detached || o.started {
		return
	}
	handle, err := os.Open(file.path)
	if err != nil {
		o.started = true
		http.Error(o.w, "file unavailable", http.StatusInternalServerError)
		return
	}
	defer handle.Close()
	if status == http.StatusOK {
		for key, values := range headers {
			for _, value := range values {
				o.w.Header().Add(key, value)
			}
		}
		o.started = true
		http.ServeContent(o.w, o.r, filepath.Base(file.path), file.info.ModTime(), handle)
		return
	}
	headers.Set("Content-Length", strconv.FormatInt(file.info.Size(), 10))
	o.begin(status, headers)
	if o.r.Method != http.MethodHead {
		_, _ = io.Copy(o.w, handle)
	}
}

// ---------------------------------------------------------------------------
// Listener goroutine
// ---------------------------------------------------------------------------

func (state *httpAppState) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	state.mu.RLock()
	closed, vm := state.closed, state.vm
	state.mu.RUnlock()
	if closed || vm == nil {
		http.Error(writer, "server is closed", http.StatusServiceUnavailable)
		return
	}
	if limit := int(state.settingNumber("max url length", 16384)); limit > 0 && len(request.RequestURI) > limit {
		http.Error(writer, "request URI too long", http.StatusRequestURITooLong)
		return
	}
	if limit := int(state.settingNumber("max headers", 100)); limit > 0 && len(request.Header) > limit {
		http.Error(writer, "too many request headers", http.StatusRequestHeaderFieldsTooLarge)
		return
	}
	if request.URL.RawQuery != "" {
		if limit := int(state.settingNumber("max query params", 1000)); limit > 0 && strings.Count(request.URL.RawQuery, "&")+1 > limit {
			http.Error(writer, "too many query parameters", http.StatusBadRequest)
			return
		}
	}
	bodyLimit := int64(state.settingNumber("body limit", 1<<20))
	if bodyLimit <= 0 {
		bodyLimit = 1 << 20
	}
	// body is private to this request and never mutated, so the unsafe.String alias below is safe.
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, bodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(writer, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(writer, "request body unreadable", http.StatusBadRequest)
		}
		return
	}
	if _, err := splitRequestPath(request.URL.EscapedPath()); err != nil {
		http.Error(writer, "malformed path encoding", http.StatusBadRequest)
		return
	}
	etagSetting, hasETag := state.setting("etag")
	etagEnabled, etagMode := httpETagConfig(etagSetting, hasETag)
	jsonSpaces, hasSpaces := state.setting("json spaces")
	jsonReplacer, _ := state.setting("json replacer")
	trustProxy, _ := state.setting("trust proxy")
	respond405 := state.settingBool("405", false)
	collector := &httpCollector{snap: make(map[*httpAppState]map[string][]OSValue)}
	var chain []httpMatch
	collector.collect(state, request.URL.Path, "", nil, []int{0}, &chain, 0)
	out := &httpOutput{w: writer, r: request}
	activeCount, firstActive := 0, -1
	for index, match := range chain {
		if !match.route.errorHandler && httpMethodMatches(match.route.method, request.Method) {
			activeCount++
			if firstActive < 0 {
				firstActive = index
			}
		}
	}
	if activeCount == 0 {
		allowed := httpAllowedFromMatches(chain)
		if request.Method == http.MethodOptions && len(allowed) != 0 {
			writer.Header().Set("Allow", strings.Join(allowed, ", "))
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if len(allowed) != 0 && !httpContains(allowed, request.Method) && respond405 {
			writer.Header().Set("Allow", strings.Join(allowed, ", "))
			http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		http.NotFound(writer, request)
		return
	}
	poweredBy := state.settingBool("x-powered-by", true)
	// Fast path: a single static route needs no VM round trip.
	if activeCount == 1 {
		only := chain[firstActive].route
		if only.handler.Type != OSVFunction && only.middleware == nil {
			status, responseBody := only.status, only.body
			headers := make(http.Header)
			headers.Set("Content-Type", "text/plain; charset=utf-8")
			if poweredBy {
				headers.Set("X-Powered-By", "OpenScript")
			}
			if etagEnabled && (request.Method == http.MethodGet || request.Method == http.MethodHead) && status >= 200 && status < 300 {
				headers.Set("ETag", httpETag(responseBody, etagMode))
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
			out.writeFull(status, headers, responseBody)
			return
		}
	}
	onError := collector.onError
	incoming := &httpIncoming{
		method: request.Method, path: request.URL.Path, query: request.URL.RawQuery,
		body: unsafe.String(unsafe.SliceData(body), len(body)), host: request.Host,
		remote: request.RemoteAddr, headers: request.Header.Clone(),
		requestMethod:     request.Method,
		etagEnabled:       etagEnabled,
		etagMode:          etagMode,
		poweredBy:         poweredBy,
		queryParser:       state.settingString("query parser", "simple"),
		jsonIndent:        httpJSONIndent(jsonSpaces, hasSpaces),
		jsonEscape:        state.settingBool("json escape", false),
		jsonReplacer:      jsonReplacer,
		jsonpCallbackName: state.settingString("jsonp callback name", "callback"),
		trustProxy:        trustProxy,
		subdomainOffset:   int(state.settingNumber("subdomain offset", 2)),
		respond405:        respond405,
		paramSnap:         collector.snap,
		protocol:          "http", secure: request.TLS != nil,
		environment: state.settingString("env", "development"),
		timeout:     time.Duration(state.settingNumber("handler timeout", 0) * float64(time.Millisecond)),
		chain:       chain, appState: state, onError: onError, out: out, done: make(chan struct{}),
	}
	if incoming.secure {
		incoming.protocol = "https"
	}
	select {
	case vm.HTTPRequests <- incoming:
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
			out.fail(http.StatusServiceUnavailable, "server is closed")
		}
	case <-request.Context().Done():
		// The client went away: give the script a grace period, then detach so late writes are no-ops.
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-incoming.done:
		case <-state.done:
		case <-timer.C:
		}
		timer.Stop()
	}
	out.detach()
}

// ---------------------------------------------------------------------------
// Query parsing
// ---------------------------------------------------------------------------

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
	if key == "__proto__" || key == "constructor" || key == "prototype" {
		return
	}
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
		return httpArray(elements)
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

// httpParseQuery parses a query string or urlencoded body with the "simple" or "extended" parser.
func httpParseQuery(raw, parser string) map[string]OSValue {
	if parser == "extended" {
		if result := httpExtendedQuery(raw); result != nil {
			return result
		}
		return map[string]OSValue{}
	}
	result := make(map[string]OSValue)
	if parsed, err := url.ParseQuery(raw); err == nil {
		for key, values := range parsed {
			if len(values) == 1 {
				result[key] = coreHTTPString(values[0])
			} else if len(values) > 1 {
				result[key] = httpStringArray(values)
			}
		}
	}
	return result
}

func httpHeadersObject(headers http.Header) OSValue {
	object := &Object{Values: make(map[string]OSValue, len(headers))}
	for key, values := range headers {
		if len(values) == 1 {
			object.Set(strings.ToLower(key), coreHTTPString(values[0]))
		} else if len(values) > 1 {
			object.Set(strings.ToLower(key), httpStringArray(values))
		}
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

// ---------------------------------------------------------------------------
// Trust proxy
// ---------------------------------------------------------------------------

func httpParseCIDRs(specs []string) []*net.IPNet {
	var nets []*net.IPNet
	add := func(cidr string) {
		if _, network, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, network)
		}
	}
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		switch spec {
		case "loopback":
			add("127.0.0.0/8")
			add("::1/128")
		case "linklocal":
			add("169.254.0.0/16")
			add("fe80::/10")
		case "uniquelocal":
			add("10.0.0.0/8")
			add("172.16.0.0/12")
			add("192.168.0.0/16")
			add("fc00::/7")
		case "":
		default:
			if strings.Contains(spec, "/") {
				add(spec)
			} else if ip := net.ParseIP(spec); ip != nil {
				if ip.To4() != nil {
					add(spec + "/32")
				} else {
					add(spec + "/128")
				}
			}
		}
	}
	return nets
}

// httpTrustFunc compiles the "trust proxy" setting into a predicate like Express's compileTrust.
func httpTrustFunc(setting OSValue) func(string, int) bool {
	never := func(string, int) bool { return false }
	switch {
	case setting.Type == OSVBool:
		if CoerceToBool(setting) {
			return func(string, int) bool { return true }
		}
		return never
	case IsNumber(setting):
		hops := int(setting.Number)
		return func(_ string, hop int) bool { return hop < hops }
	case IsString(setting) || (setting.Type == OSVArray && setting.Opaque != nil):
		var specs []string
		if IsString(setting) {
			specs = strings.Split(setting.StringValue(), ",")
		} else {
			for _, element := range (*Array)(setting.Opaque).Elements {
				specs = append(specs, element.String())
			}
		}
		nets := httpParseCIDRs(specs)
		return func(address string, _ int) bool {
			ip := net.ParseIP(address)
			if ip == nil {
				return false
			}
			for _, network := range nets {
				if network.Contains(ip) {
					return true
				}
			}
			return false
		}
	}
	return never
}

// httpProxyAddrs mirrors proxy-addr's all(): [socket, X-Forwarded-For reversed...] truncated at the first untrusted hop.
func httpProxyAddrs(socket string, forwarded []string, trust func(string, int) bool) []string {
	var parts []string
	for _, header := range forwarded {
		for _, part := range strings.Split(header, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
	}
	list := []string{socket}
	for i := len(parts) - 1; i >= 0; i-- {
		list = append(list, parts[i])
	}
	i := 0
	for ; i < len(list)-1; i++ {
		if !trust(list[i], i) {
			break
		}
	}
	return list[:i+1]
}

func httpHostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

func httpSubdomains(hostname string, offset int) []string {
	if hostname == "" || net.ParseIP(hostname) != nil {
		return nil
	}
	parts := strings.Split(hostname, ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	if offset >= len(parts) {
		return nil
	}
	return parts[offset:]
}

// ---------------------------------------------------------------------------
// Freshness
// ---------------------------------------------------------------------------

func httpIsFresh(reqHeaders http.Header, method string, status int, resHeaders http.Header) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	if (status < 200 || status >= 300) && status != http.StatusNotModified {
		return false
	}
	noneMatch := strings.TrimSpace(reqHeaders.Get("If-None-Match"))
	modifiedSince := strings.TrimSpace(reqHeaders.Get("If-Modified-Since"))
	if noneMatch == "" && modifiedSince == "" {
		return false
	}
	if strings.Contains(strings.ToLower(reqHeaders.Get("Cache-Control")), "no-cache") {
		return false
	}
	if noneMatch != "" && noneMatch != "*" {
		if !httpETagMatches(reqHeaders.Values("If-None-Match"), resHeaders.Get("ETag")) {
			return false
		}
	}
	if modifiedSince != "" {
		if !httpModifiedSinceMatches(modifiedSince, resHeaders.Get("Last-Modified")) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// VM thread: handler execution
// ---------------------------------------------------------------------------

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
	runCoroutine(vm.State, vm, coroutine)
	vm.Current = previous
	return completion
}

func runHTTPSyncCallback(vm *VM, handler OSValue, args ...OSValue) *Promise {
	if handler.Type != OSVFunction || handler.Opaque == nil {
		return &Promise{Rejected: true, Resolved: true, Value: newErrorValue("HTTP callback must be a function", 0), Handled: true}
	}
	function := (*Function)(handler.Opaque)
	if function.Async {
		return &Promise{Rejected: true, Resolved: true, Value: newErrorValue("HTTP callback must be synchronous", 0), Handled: true}
	}
	if function.VarArgIndex < 0 && len(args) > function.ParamCount {
		args = args[:function.ParamCount]
	}
	result, ok := invokeSyncArrayFunction(vm, function, args)
	completion := &Promise{Resolved: true, Value: result, Handled: true}
	if !ok {
		message := vm.NativeError
		vm.NativeError = ""
		completion.Rejected = true
		completion.Value = newErrorValue(message, 0)
	}
	return completion
}

// prepare exposes the matched layer to the handler: route, params, baseUrl, url, path and app.
func (dispatch *httpDispatch) prepare(match httpMatch) {
	request := (*Object)(dispatch.request.Opaque)
	request.Set("route", httpRouteObject(dispatch.vm, match.route))
	request.Set("params", httpStringObject(match.params))
	request.Set("baseUrl", coreHTTPString(match.baseURL))
	request.Set("url", coreHTTPString(match.url+dispatch.queryPart))
	request.Set("path", coreHTTPString(match.url))
	if match.app != nil {
		request.Set("app", match.app.appValue)
		dispatch.data.app = match.app
	}
}

func (dispatch *httpDispatch) skipFrom(current int, router bool) int {
	chain := dispatch.incoming.chain
	base := chain[current]
	index := current + 1
	for index < len(chain) {
		candidate := chain[index]
		if router {
			if !httpHasRouter(candidate.routers, base.routerID) {
				break
			}
		} else if candidate.route.routeID != base.route.routeID {
			break
		}
		index++
	}
	return index
}

func (dispatch *httpDispatch) resetFlags() {
	data := dispatch.data
	data.next, data.nextCalled, data.skipRoute, data.skipRouter = false, false, false, false
}

func (dispatch *httpDispatch) continueAt(index int) {
	chain := dispatch.incoming.chain
	for ; index < len(chain) && !dispatch.data.sent; index++ {
		dispatch.index = index
		match := chain[index]
		if !httpMethodMatches(match.route.method, dispatch.method) {
			continue
		}
		if dispatch.errorMode {
			if !match.route.errorHandler {
				continue
			}
			dispatch.prepare(match)
			dispatch.data.hasErr = false
			dispatch.resetFlags()
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
		dispatch.prepare(match)
		dispatch.resetFlags()
		if mw := match.route.middleware; mw != nil {
			result := mw.run(dispatch, match)
			if result.hasErr {
				dispatch.startError(result.err, index+1)
				return
			}
			if dispatch.data.hasErr {
				dispatch.startError(dispatch.data.errValue, index+1)
				return
			}
			if dispatch.data.sent || !result.next {
				break
			}
			continue
		}
		if match.route.handler.Type != OSVFunction {
			dispatch.data.status = match.route.status
			dispatch.data.body = match.route.body
			httpMarkResponseSent(dispatch.data)
			break
		}
		if !dispatch.runParamHandlers(match, index) {
			return
		}
		dispatch.resetFlags()
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
		if dispatch.data.skipRouter {
			index = dispatch.skipFrom(index, true) - 1
			continue
		}
		if dispatch.data.skipRoute {
			index = dispatch.skipFrom(index, false) - 1
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
			switch {
			case dispatch.data.hasErr:
				dispatch.startError(dispatch.data.errValue, nextIndex)
			case dispatch.data.skipRouter:
				dispatch.continueAt(dispatch.skipFrom(nextIndex-1, true))
			case dispatch.data.skipRoute:
				dispatch.continueAt(dispatch.skipFrom(nextIndex-1, false))
			case dispatch.data.sent || !dispatch.data.next:
				dispatch.finish()
			default:
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

func (dispatch *httpDispatch) paramHandlersFor(match httpMatch, name string) []OSValue {
	return dispatch.incoming.paramSnap[match.app][name]
}

func (dispatch *httpDispatch) paramKey(match httpMatch, name, value string, callbackIndex int) string {
	return strconv.FormatUint(uint64(uintptr(unsafe.Pointer(match.app))), 16) + "\x00" + name + "\x00" + value + "\x00" + strconv.Itoa(callbackIndex)
}

func (dispatch *httpDispatch) runParamHandlers(match httpMatch, layerIndex int) bool {
	if dispatch.paramCache == nil {
		dispatch.paramCache = make(map[string]bool)
	}
	for name, value := range match.params {
		for callbackIndex, callback := range dispatch.paramHandlersFor(match, name) {
			cacheKey := dispatch.paramKey(match, name, value, callbackIndex)
			if dispatch.paramCache[cacheKey] {
				continue
			}
			dispatch.resetFlags()
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
		dispatch.paramCache[dispatch.paramKey(match, name, value, callbackIndex)] = true
		handlers := dispatch.paramHandlersFor(match, name)
		for nextCallback := callbackIndex + 1; nextCallback < len(handlers); nextCallback++ {
			dispatch.resetFlags()
			completion := runHTTPHandler(vm, handlers[nextCallback], dispatch.request, dispatch.response, dispatch.next,
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
			dispatch.paramCache[dispatch.paramKey(match, name, value, nextCallback)] = true
		}
		dispatch.resetFlags()
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

// notFound handles falling off the end of the chain: automatic OPTIONS, optional 405, otherwise 404.
func (dispatch *httpDispatch) notFound() {
	data := dispatch.data
	allowed := httpAllowedFromMatches(dispatch.incoming.chain)
	switch {
	case dispatch.method == http.MethodOptions && len(allowed) != 0:
		data.status = http.StatusNoContent
		data.headers.Set("Allow", strings.Join(allowed, ", "))
		data.body = ""
	case len(allowed) != 0 && !httpContains(allowed, dispatch.method) && dispatch.incoming.respond405:
		data.status = http.StatusMethodNotAllowed
		data.headers.Set("Allow", strings.Join(allowed, ", "))
		data.body = http.StatusText(http.StatusMethodNotAllowed)
	default:
		data.status = http.StatusNotFound
		data.body = "Cannot " + dispatch.method + " " + dispatch.incoming.path
	}
	httpMarkResponseSent(data)
}

func (dispatch *httpDispatch) finish() {
	if dispatch.data.streaming {
		return
	}
	if dispatch.data.hasErr && !dispatch.errorMode {
		dispatch.startError(dispatch.data.errValue, dispatch.index+1)
		return
	}
	if dispatch.errorMode {
		dispatch.runErrorHandler()
		return
	}
	if !dispatch.data.sent && dispatch.data.next {
		dispatch.notFound()
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
	if data.streaming {
		data.sent = true
		return
	}
	*data = httpResponseData{
		status: status, headers: make(http.Header), body: body, sent: sent,
		jsonIndent: data.jsonIndent, jsonEscape: data.jsonEscape, jsonReplacer: data.jsonReplacer,
		jsonpCallbackName: data.jsonpCallbackName, jsonpCallback: data.jsonpCallback,
		response: data.response, app: data.app, dispatch: data.dispatch, requestData: data.requestData,
		refreshFresh: data.refreshFresh, finishHooks: data.finishHooks,
	}
	if data.response != nil {
		data.response.Set("headersSent", httpBool(sent))
	}
}

func (dispatch *httpDispatch) fireEvents() {
	for _, name := range []string{"res:finish", "res:close", "req:close"} {
		for _, handler := range dispatch.listeners[name] {
			runHTTPHandler(dispatch.vm, handler)
		}
	}
}

// finishStream ends a streamed response started with res.write().
func (dispatch *httpDispatch) finishStream() {
	if dispatch.finished {
		return
	}
	dispatch.finished = true
	dispatch.incoming.out.flush(dispatch.data.status, dispatch.data.headers)
	dispatch.fireEvents()
	close(dispatch.incoming.done)
}

func (dispatch *httpDispatch) finishResponse() {
	if dispatch.finished {
		return
	}
	data := dispatch.data
	if data.streaming {
		dispatch.finishStream()
		return
	}
	dispatch.finished = true
	if !data.sent {
		dispatch.notFound()
	}
	if dispatch.incoming.poweredBy && data.headers.Get("X-Powered-By") == "" {
		data.headers.Set("X-Powered-By", "OpenScript")
	}
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "text/plain; charset=utf-8")
	}
	isRead := dispatch.method == http.MethodGet || dispatch.method == http.MethodHead
	if data.file != nil {
		for _, hook := range data.finishHooks {
			hook(dispatch)
		}
		dispatch.incoming.out.serveFile(data.status, data.headers, data.file)
		dispatch.fireEvents()
		close(dispatch.incoming.done)
		return
	}
	if dispatch.incoming.etagEnabled && isRead && data.status >= 200 && data.status < 300 {
		if data.headers.Get("ETag") == "" {
			data.headers.Set("ETag", httpETag(data.body, dispatch.incoming.etagMode))
		}
	}
	if isRead && data.status >= 200 && data.status < 300 {
		if httpIsFresh(dispatch.incoming.headers, dispatch.method, data.status, data.headers) {
			data.status = http.StatusNotModified
			data.body = ""
		}
	}
	for _, hook := range data.finishHooks {
		hook(dispatch)
	}
	if data.status >= 100 && (data.status < 200 || data.status == http.StatusNoContent || data.status == http.StatusNotModified) {
		data.body = ""
		data.headers.Del("Content-Type")
		data.headers.Del("Content-Length")
	} else {
		data.headers.Set("Content-Length", strconv.Itoa(len(data.body)))
	}
	dispatch.incoming.out.writeFull(data.status, data.headers, data.body)
	dispatch.fireEvents()
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
	if dispatch.finished || dispatch.data.streaming {
		return
	}
	dispatch.incoming.timedOut = true
	dispatch.resetResponse(http.StatusGatewayTimeout, http.StatusText(http.StatusGatewayTimeout), true)
	dispatch.finishResponse()
}

func serveHTTPIncoming(vm *VM, incoming *httpIncoming) {
	queryParams := httpParseQuery(incoming.query, incoming.queryParser)
	socketHost := incoming.remote
	if h, _, err := net.SplitHostPort(incoming.remote); err == nil {
		socketHost = h
	}
	trust := httpTrustFunc(incoming.trustProxy)
	addrs := httpProxyAddrs(socketHost, incoming.headers.Values("X-Forwarded-For"), trust)
	ip := addrs[len(addrs)-1]
	ips := make([]string, 0, len(addrs))
	for i := len(addrs) - 1; i >= 1; i-- {
		ips = append(ips, addrs[i])
	}
	protocol, hostHeader := incoming.protocol, incoming.host
	if trust(socketHost, 0) {
		if forwarded := incoming.headers.Get("X-Forwarded-Proto"); forwarded != "" {
			protocol = strings.ToLower(strings.TrimSpace(strings.Split(forwarded, ",")[0]))
		}
		if forwarded := incoming.headers.Get("X-Forwarded-Host"); forwarded != "" {
			hostHeader = strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
	}
	hostname := httpHostname(hostHeader)
	originalURL := incoming.path
	queryPart := ""
	if incoming.query != "" {
		queryPart = "?" + incoming.query
		originalURL += queryPart
	}
	requestData := &httpRequestData{headers: incoming.headers, body: incoming.body, values: incoming.headers}
	request := &Object{Native: requestData, Values: make(map[string]OSValue, 40)}
	requestValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(request)}
	if incoming.appState != nil {
		request.Set("app", incoming.appState.appValue)
	}
	request.Set("method", coreHTTPString(incoming.method))
	request.Set("originalMethod", coreHTTPString(incoming.method))
	request.Set("path", coreHTTPString(incoming.path))
	request.Set("url", coreHTTPString(originalURL))
	request.Set("baseUrl", coreHTTPString(""))
	request.Set("originalUrl", coreHTTPString(originalURL))
	request.Set("query", httpValueObject(queryParams))
	request.Set("queryParams", httpValueObject(queryParams))
	request.Set("rawQuery", coreHTTPString(incoming.query))
	// req.body stays undefined until a body parser matches; the raw text is always in req.rawBody.
	request.Set("rawBody", coreHTTPString(incoming.body))
	request.Set("hostname", coreHTTPString(hostname))
	request.Set("subdomains", httpStringArray(httpSubdomains(hostname, incoming.subdomainOffset)))
	request.Set("ip", coreHTTPString(ip))
	request.Set("ips", httpStringArray(ips))
	request.Set("protocol", coreHTTPString(protocol))
	request.Set("secure", httpBool(protocol == "https"))
	request.Set("xhr", httpBool(strings.EqualFold(incoming.headers.Get("X-Requested-With"), "XMLHttpRequest")))
	request.Set("headers", httpHeadersObject(incoming.headers))
	request.Set("fresh", httpBool(false))
	request.Set("stale", httpBool(true))
	request.Set("signedCookies", httpValueObject(nil))
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
	request.Set("header", httpBoundMethod(requestValue, 1, nativeHTTPRequestGet))
	request.Set("getAll", httpBoundMethod(requestValue, 1, nativeHTTPRequestGetAll))
	request.Set("is", httpBoundMethod(requestValue, 1, nativeHTTPRequestIs))
	request.Set("accepts", httpBoundMethod(requestValue, -1, nativeHTTPRequestAccepts))
	request.Set("acceptsCharsets", httpBoundMethod(requestValue, -1, nativeHTTPRequestAcceptsCharsets))
	request.Set("acceptsEncodings", httpBoundMethod(requestValue, -1, nativeHTTPRequestAcceptsEncodings))
	request.Set("acceptsLanguages", httpBoundMethod(requestValue, -1, nativeHTTPRequestAcceptsLanguages))
	request.Set("range", httpBoundMethod(requestValue, 1, nativeHTTPRequestRange))
	request.Set("json", httpBoundMethod(requestValue, 0, nativeHTTPRequestJSON))
	request.Set("on", httpBoundMethod(requestValue, 2, nativeHTTPRequestOn))
	request.Set("isAborted", httpBoundMethod(requestValue, 0, nativeHTTPRequestIsAborted))

	jsonpCallback := ""
	if callback, exists := queryParams[incoming.jsonpCallbackName]; exists && IsString(callback) {
		jsonpCallback = callback.StringValue()
	}
	data := &httpResponseData{
		status: 200, headers: make(http.Header), jsonIndent: incoming.jsonIndent, jsonEscape: incoming.jsonEscape,
		jsonReplacer: incoming.jsonReplacer, jsonpCallbackName: incoming.jsonpCallbackName, jsonpCallback: jsonpCallback,
		app: incoming.appState, requestData: requestData,
	}
	response := &Object{Native: data, Values: make(map[string]OSValue, 32)}
	data.response = response
	responseValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(response)}
	data.refreshFresh = func() {
		fresh := httpIsFresh(incoming.headers, incoming.dispatch.method, data.status, data.headers)
		request.Set("fresh", httpBool(fresh))
		request.Set("stale", httpBool(!fresh))
	}
	response.Set("headersSent", vm.False)
	response.Set("locals", httpValueObject(nil))
	if incoming.appState != nil {
		response.Set("app", incoming.appState.appValue)
	}
	response.Set("req", requestValue)
	request.Set("res", responseValue)
	response.Set("status", httpBoundMethod(responseValue, 1, nativeHTTPResponseStatus))
	response.Set("sendStatus", httpBoundMethod(responseValue, 1, nativeHTTPResponseSendStatus))
	response.Set("send", httpBoundMethod(responseValue, -1, nativeHTTPResponseSend))
	response.Set("json", httpBoundMethod(responseValue, 1, nativeHTTPResponseJSON))
	response.Set("jsonp", httpBoundMethod(responseValue, 1, nativeHTTPResponseJSONP))
	response.Set("set", httpBoundMethod(responseValue, -1, nativeHTTPResponseSet))
	response.Set("header", httpBoundMethod(responseValue, -1, nativeHTTPResponseSet))
	response.Set("append", httpBoundMethod(responseValue, 2, nativeHTTPResponseAppend))
	response.Set("cookie", httpBoundMethod(responseValue, -1, nativeHTTPResponseCookie))
	response.Set("clearCookie", httpBoundMethod(responseValue, -1, nativeHTTPResponseClearCookie))
	response.Set("get", httpBoundMethod(responseValue, 1, nativeHTTPResponseGet))
	response.Set("location", httpBoundMethod(responseValue, 1, nativeHTTPResponseLocation))
	response.Set("links", httpBoundMethod(responseValue, 1, nativeHTTPResponseLinks))
	response.Set("vary", httpBoundMethod(responseValue, 1, nativeHTTPResponseVary))
	response.Set("type", httpBoundMethod(responseValue, 1, nativeHTTPResponseType))
	response.Set("format", httpBoundMethod(responseValue, 1, nativeHTTPResponseFormat))
	response.Set("render", httpBoundMethod(responseValue, -1, nativeHTTPResponseRender))
	response.Set("attachment", httpBoundMethod(responseValue, -1, nativeHTTPResponseAttachment))
	response.Set("sendFile", httpBoundMethod(responseValue, -1, nativeHTTPResponseSendFile))
	response.Set("download", httpBoundMethod(responseValue, -1, nativeHTTPResponseDownload))
	response.Set("redirect", httpBoundMethod(responseValue, -1, nativeHTTPResponseRedirect))
	response.Set("write", httpBoundMethod(responseValue, 1, nativeHTTPResponseWrite))
	response.Set("flushHeaders", httpBoundMethod(responseValue, 0, nativeHTTPResponseFlush))
	response.Set("flush", httpBoundMethod(responseValue, 0, nativeHTTPResponseFlush))
	response.Set("end", httpBoundMethod(responseValue, -1, nativeHTTPResponseEnd))
	response.Set("on", httpBoundMethod(responseValue, 2, nativeHTTPResponseOn))
	response.Set("isClosed", httpBoundMethod(responseValue, 0, nativeHTTPRequestIsAborted))
	nextValue := httpBoundMethod(responseValue, -1, nativeHTTPNext)

	dispatch := &httpDispatch{
		vm: vm, incoming: incoming, request: requestValue,
		response: responseValue, next: nextValue, data: data, reqData: requestData,
		method: incoming.method, queryPart: queryPart, start: time.Now(),
		listeners: make(map[string][]OSValue),
	}
	data.dispatch = dispatch
	requestData.dispatch = dispatch
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
	name := args[0].StringValue()
	if strings.EqualFold(name, "referer") || strings.EqualFold(name, "referrer") {
		if value := data.headers.Get("Referer"); value != "" {
			return coreHTTPString(value)
		}
		return coreHTTPString(data.headers.Get("Referrer"))
	}
	return coreHTTPString(data.headers.Get(name))
}

func nativeHTTPRequestOn(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || len(args) < 2 || !IsString(args[0]) || args[1].Type != OSVFunction {
		vm.NativeError = "core:http req.on expects an event name and a function"
		return vm.Null
	}
	// 'close' fires when the exchange finishes (see isAborted() for early disconnect polling).
	key := "req:" + args[0].StringValue()
	data.dispatch.listeners[key] = append(data.dispatch.listeners[key], args[1])
	return this
}

func nativeHTTPRequestIsAborted(this OSValue, vm *VM, _ []OSValue) OSValue {
	var dispatch *httpDispatch
	if this.Type == OSVObject && this.Opaque != nil {
		switch native := (*Object)(this.Opaque).Native.(type) {
		case *httpRequestData:
			dispatch = native.dispatch
		case *httpResponseData:
			dispatch = native.dispatch
		}
	}
	if dispatch == nil {
		return vm.False
	}
	return httpBool(dispatch.incoming.out.closedByClient())
}

type httpByteRange struct{ start, end int64 }

// httpParseRange parses a Range header: code 0 = ok, -1 = unsatisfiable, -2 = malformed.
func httpParseRange(header string, size int64) ([]httpByteRange, int) {
	if strings.TrimSpace(header) == "" {
		return nil, 0
	}
	unit, values, found := strings.Cut(header, "=")
	if !found || !strings.EqualFold(strings.TrimSpace(unit), "bytes") || strings.TrimSpace(values) == "" {
		return nil, -2
	}
	var ranges []httpByteRange
	for _, value := range strings.Split(values, ",") {
		startText, endText, found := strings.Cut(strings.TrimSpace(value), "-")
		if !found || strings.Contains(endText, "-") {
			return nil, -2
		}
		var start, end int64
		if startText == "" {
			suffix, err := strconv.ParseInt(endText, 10, 64)
			if err != nil || suffix < 0 {
				return nil, -2
			}
			if suffix == 0 || size == 0 {
				continue
			}
			start = max(size-suffix, 0)
			end = size - 1
		} else {
			parsedStart, err := strconv.ParseInt(startText, 10, 64)
			if err != nil || parsedStart < 0 {
				return nil, -2
			}
			start = parsedStart
			if endText == "" {
				end = size - 1
			} else {
				parsedEnd, err := strconv.ParseInt(endText, 10, 64)
				if err != nil || parsedEnd < 0 {
					return nil, -2
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
		ranges = append(ranges, httpByteRange{start, end})
	}
	if len(ranges) == 0 {
		return nil, -1
	}
	return ranges, 0
}

func nativeHTTPRequestRange(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || !IsNumber(args[0]) || args[0].Number < 0 {
		vm.NativeError = "core:http req.range expects a non-negative size"
		return vm.Null
	}
	ranges, code := httpParseRange(data.headers.Get("Range"), int64(args[0].Number))
	if code != 0 {
		return OSValue{Type: OSVInt, Number: float64(code)}
	}
	elements := make([]OSValue, len(ranges))
	for index, r := range ranges {
		elements[index] = httpValueObject(map[string]OSValue{
			"start": {Type: OSVInt, Number: float64(r.start)},
			"end":   {Type: OSVInt, Number: float64(r.end)},
		})
	}
	return httpArray(elements)
}

func nativeHTTPRequestIs(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http req.is expects a content type"
		return vm.False
	}
	return httpBool(httpTypeIs(data.headers.Get("Content-Type"), []string{args[0].StringValue()}))
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

// httpBestMediaType returns the index of the best candidate for an Accept header, or -1.
func httpBestMediaType(header string, candidates []string) int {
	accepted := parseHTTPAccept(header)
	best, bestQuality := -1, 0.0
	for index, candidate := range candidates {
		mediaType := httpMimeFor(candidate)
		if mediaType == "" {
			continue
		}
		candidateParts := strings.SplitN(strings.ToLower(mediaType), "/", 2)
		if len(candidateParts) != 2 {
			continue
		}
		quality, specificity := -1.0, -1
		for _, acceptedType := range accepted {
			acceptedParts := strings.SplitN(acceptedType.mediaType, "/", 2)
			if len(acceptedParts) != 2 ||
				(acceptedParts[0] != "*" && acceptedParts[0] != candidateParts[0]) ||
				(acceptedParts[1] != "*" && acceptedParts[1] != candidateParts[1]) {
				continue
			}
			current := 0
			if acceptedParts[0] != "*" {
				current++
			}
			if acceptedParts[1] != "*" {
				current++
			}
			if current > specificity || (current == specificity && acceptedType.quality > quality) {
				quality, specificity = acceptedType.quality, current
			}
		}
		if quality > bestQuality {
			best, bestQuality = index, quality
		}
	}
	return best
}

func httpCollectStrings(args []OSValue) []string {
	var values []string
	for _, arg := range args {
		if arg.Type == OSVArray && arg.Opaque != nil {
			for _, value := range (*Array)(arg.Opaque).Elements {
				if IsString(value) {
					values = append(values, value.StringValue())
				}
			}
		} else if IsString(arg) {
			values = append(values, arg.StringValue())
		}
	}
	return values
}

func nativeHTTPRequestAccepts(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || len(args) == 0 {
		vm.NativeError = "core:http req.accepts expects one or more content types"
		return vm.False
	}
	requested := httpCollectStrings(args)
	if index := httpBestMediaType(data.headers.Get("Accept"), requested); index >= 0 {
		return coreHTTPString(requested[index])
	}
	return vm.False
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

type httpAcceptedToken struct {
	value   string
	quality float64
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

func httpBestToken(header, headerName string, candidates []string) string {
	accepted := parseHTTPAcceptTokens(header)
	best, bestQuality := "", 0.0
	for _, candidate := range candidates {
		for _, token := range accepted {
			matches := token.value == "*" || strings.EqualFold(token.value, candidate)
			if headerName == "Accept-Language" && strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(token.value)+"-") {
				matches = true
			}
			if matches && token.quality > bestQuality {
				best, bestQuality = candidate, token.quality
			}
		}
	}
	return best
}

func nativeHTTPRequestAcceptToken(this OSValue, vm *VM, args []OSValue, headerName string) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || len(args) == 0 {
		vm.NativeError = "core:http request accepts method expects one or more values"
		return vm.False
	}
	if best := httpBestToken(data.headers.Get(headerName), headerName, httpCollectStrings(args)); best != "" {
		return coreHTTPString(best)
	}
	return vm.False
}

func nativeHTTPRequestGetAll(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpRequestDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http req.getAll expects a header name"
		return vm.Null
	}
	return httpStringArray(data.values.Values(args[0].StringValue()))
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
		} else if IsString(args[0]) && args[0].StringValue() == "router" {
			data.skipRouter = true
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
	data.touch()
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
	data.headers.Set("Content-Type", "text/plain; charset=utf-8")
	httpMarkResponseSent(data)
	return this
}

// res.send: string -> text/html, Buffer-like strings stay as-is, object/array/number/boolean -> JSON, null -> empty.
func nativeHTTPResponseSend(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	if len(args) == 0 || args[0].Type == OSVNull {
		data.body = ""
		httpMarkResponseSent(data)
		return this
	}
	value := args[0]
	if value.Type == OSVObject || value.Type == OSVArray || value.Type == OSVBool || IsNumber(value) {
		return nativeHTTPResponseJSON(this, vm, args)
	}
	data.body = value.String()
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "text/html; charset=utf-8")
	}
	httpMarkResponseSent(data)
	return this
}

// httpStringify serialises a value honoring json replacer, json escape and json spaces.
func httpStringify(vm *VM, data *httpResponseData, value OSValue) (string, bool) {
	if data.jsonReplacer.Type == OSVFunction || data.jsonReplacer.Type == OSVArray {
		value = httpApplyReplacer(vm, data.jsonReplacer, value)
	}
	vm.NativeError = ""
	encoded := nativeJSONStringify(vm, []OSValue{value})
	if vm.NativeError != "" {
		return "", false
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
	return body, true
}

func nativeHTTPResponseJSON(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	body, ok := httpStringify(vm, data, args[0])
	if !ok {
		return vm.Null
	}
	data.body = body
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "application/json; charset=utf-8")
	}
	httpMarkResponseSent(data)
	return this
}

var httpJSONPCallbackPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$`)

func nativeHTTPResponseJSONP(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	body, ok := httpStringify(vm, data, args[0])
	if !ok {
		return vm.Null
	}
	if httpJSONPCallbackPattern.MatchString(data.jsonpCallback) {
		data.body = "/**/" + data.jsonpCallback + "(" + body + ");"
		data.headers.Set("Content-Type", "text/javascript; charset=utf-8")
		data.headers.Set("X-Content-Type-Options", "nosniff")
	} else {
		data.body = body
		data.headers.Set("Content-Type", "application/json; charset=utf-8")
	}
	httpMarkResponseSent(data)
	return this
}

func httpSetHeader(data *httpResponseData, field string, value OSValue) {
	if value.Type == OSVArray && value.Opaque != nil {
		data.headers.Del(field)
		for _, element := range (*Array)(value.Opaque).Elements {
			data.headers.Add(field, element.String())
		}
		return
	}
	text := value.String()
	if strings.EqualFold(field, "Content-Type") {
		text = httpWithCharset(text)
	}
	data.headers.Set(field, text)
}

// res.set(field, value) or res.set({field: value, ...})
func nativeHTTPResponseSet(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	if len(args) == 1 && args[0].Type == OSVObject && args[0].Opaque != nil {
		object := (*Object)(args[0].Opaque)
		for _, key := range object.Keys {
			if value, exists := object.Values[key]; exists {
				httpSetHeader(data, key, value)
			}
		}
		data.touch()
		return this
	}
	if len(args) < 2 || !IsString(args[0]) {
		vm.NativeError = "core:http res.set expects a header name and value"
		return vm.Null
	}
	httpSetHeader(data, args[0].StringValue(), args[1])
	data.touch()
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
	if args[1].Type == OSVArray && args[1].Opaque != nil {
		for _, element := range (*Array)(args[1].Opaque).Elements {
			data.headers.Add(name, element.String())
		}
	} else {
		data.headers.Add(name, args[1].String())
	}
	data.touch()
	return this
}

func httpParseDurationMillis(value OSValue) int64 {
	if IsNumber(value) {
		return int64(value.Number)
	}
	if !IsString(value) {
		return 0
	}
	text := strings.ToLower(strings.TrimSpace(value.StringValue()))
	units := []struct {
		suffix string
		ms     float64
	}{{"ms", 1}, {"s", 1000}, {"m", 60000}, {"h", 3600000}, {"d", 86400000}, {"w", 604800000}, {"y", 31557600000}}
	for _, unit := range units {
		if strings.HasSuffix(text, unit.suffix) {
			if number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(text, unit.suffix)), 64); err == nil {
				return int64(number * unit.ms)
			}
		}
	}
	if number, err := strconv.ParseFloat(text, 64); err == nil {
		return int64(number)
	}
	return 0
}

// httpBuildCookie applies Express res.cookie options (maxAge is milliseconds).
func httpBuildCookie(name, value string, options map[string]OSValue) *http.Cookie {
	value = url.QueryEscape(value)
	value = strings.ReplaceAll(value, "+", "%20")
	cookie := &http.Cookie{Name: name, Value: value, Path: "/"}
	if maxAge, exists := options["maxAge"]; exists && IsNumber(maxAge) {
		cookie.MaxAge = int(maxAge.Number / 1000)
		if maxAge.Number < 0 {
			cookie.MaxAge = -1
		} else {
			cookie.Expires = time.Now().Add(time.Duration(maxAge.Number) * time.Millisecond)
		}
	}
	if expires, exists := options["expires"]; exists {
		switch {
		case IsString(expires):
			cookie.Expires, _ = time.Parse(http.TimeFormat, expires.StringValue())
		case IsNumber(expires):
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
	return cookie
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
	text := args[1].String()
	if args[1].Type == OSVObject || args[1].Type == OSVArray {
		vm.NativeError = ""
		encoded := nativeJSONStringify(vm, args[1:2])
		if vm.NativeError != "" {
			return vm.Null
		}
		text = "j:" + encoded.StringValue()
	}
	if signed, exists := options["signed"]; exists && CoerceToBool(signed) {
		if data.requestData == nil || len(data.requestData.secrets) == 0 {
			vm.NativeError = "core:http res.cookie signed cookies require cookieParser(secret)"
			return vm.Null
		}
		text = httpSignCookie(text, data.requestData.secrets[0])
	}
	data.headers.Add("Set-Cookie", httpBuildCookie(args[0].StringValue(), text, options).String())
	return this
}

func nativeHTTPResponseClearCookie(this OSValue, vm *VM, args []OSValue) OSValue {
	if len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http res.clearCookie expects a cookie name and optional options object"
		return vm.Null
	}
	copyOptions := &Object{Values: make(map[string]OSValue)}
	if len(args) > 1 && args[1].Type == OSVObject && args[1].Opaque != nil {
		for key, value := range (*Object)(args[1].Opaque).Values {
			copyOptions.Set(key, value)
		}
	}
	copyOptions.Set("expires", coreHTTPString("Thu, 01 Jan 1970 00:00:00 GMT"))
	copyOptions.Set("maxAge", httpNum(-1))
	return nativeHTTPResponseCookie(this, vm, []OSValue{args[0], coreHTTPString(""), {Type: OSVObject, Opaque: unsafe.Pointer(copyOptions)}})
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

func httpVary(data *httpResponseData, field string) {
	for _, existing := range data.headers.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.TrimSpace(part) == "*" || strings.EqualFold(strings.TrimSpace(part), field) {
				return
			}
		}
	}
	data.headers.Add("Vary", field)
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
	httpVary(data, args[0].StringValue())
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
	if resolved := httpMimeFor(kind); resolved != "" && !strings.Contains(kind, "/") {
		kind = resolved
	}
	data.headers.Set("Content-Type", httpWithCharset(kind))
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
			data.headers.Set("Content-Type", httpWithCharset(mediaType))
		}
	}
	disposition := "attachment"
	if filename != "" {
		disposition = mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(filename)})
	}
	data.headers.Set("Content-Disposition", disposition)
	return this
}

// ---------------------------------------------------------------------------
// Files (streamed at finish with Range / If-Range support)
// ---------------------------------------------------------------------------

type httpFileOptions struct {
	maxAge       int64
	immutable    bool
	lastModified bool
	etag         bool
	cacheControl bool
	dotfiles     string
	headers      map[string]OSValue
}

func httpFileOptionsFrom(options *Object, defaultETag bool) httpFileOptions {
	result := httpFileOptions{lastModified: true, etag: defaultETag, cacheControl: true, dotfiles: "allow"}
	if options == nil {
		return result
	}
	if value, ok := options.Values["maxAge"]; ok {
		result.maxAge = httpParseDurationMillis(value)
	}
	if value, ok := options.Values["immutable"]; ok {
		result.immutable = CoerceToBool(value)
	}
	if value, ok := options.Values["lastModified"]; ok {
		result.lastModified = CoerceToBool(value)
	}
	if value, ok := options.Values["etag"]; ok {
		result.etag = CoerceToBool(value)
	}
	if value, ok := options.Values["cacheControl"]; ok {
		result.cacheControl = CoerceToBool(value)
	}
	if value, ok := options.Values["dotfiles"]; ok && IsString(value) {
		result.dotfiles = value.StringValue()
	}
	if value, ok := options.Values["headers"]; ok && value.Type == OSVObject && value.Opaque != nil {
		result.headers = (*Object)(value.Opaque).Values
	}
	return result
}

func httpResolveFile(filename string, options OSValue) (os.FileInfo, string, error) {
	if filename == "" {
		return nil, "", errors.New("filename must not be empty")
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
			return nil, "", errors.New("absolute filename is not allowed with a root")
		}
		rootPath, err := filepath.Abs(root)
		if err != nil {
			return nil, "", err
		}
		rootPath, err = filepath.EvalSymlinks(rootPath)
		if err != nil {
			return nil, "", err
		}
		target, err = filepath.EvalSymlinks(filepath.Join(rootPath, filepath.Clean(filename)))
		if err != nil {
			return nil, "", err
		}
		relative, err := filepath.Rel(rootPath, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, "", errors.New("filename escapes root")
		}
	} else {
		if !filepath.IsAbs(filename) {
			return nil, "", errors.New("filename must be absolute when root is not set")
		}
		var err error
		target, err = filepath.EvalSymlinks(filename)
		if err != nil {
			return nil, "", err
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("path is not a regular file")
	}
	return info, target, nil
}

// httpAttachFile configures the response to stream target; headers are set, the body is read at finish.
func httpAttachFile(data *httpResponseData, info os.FileInfo, target string, options httpFileOptions) {
	data.file = &httpFileBody{path: target, info: info}
	if data.headers.Get("Content-Type") == "" {
		kind := mime.TypeByExtension(filepath.Ext(target))
		if kind == "" {
			kind = "application/octet-stream"
		}
		data.headers.Set("Content-Type", httpWithCharset(kind))
	}
	if options.lastModified {
		data.headers.Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	}
	if options.etag && data.headers.Get("ETag") == "" {
		data.headers.Set("ETag", `W/"`+strconv.FormatInt(info.Size(), 16)+"-"+strconv.FormatInt(info.ModTime().UnixMilli(), 16)+`"`)
	}
	if options.cacheControl && data.headers.Get("Cache-Control") == "" {
		seconds := max(options.maxAge/1000, 0)
		control := "public, max-age=" + strconv.FormatInt(seconds, 10)
		if options.immutable {
			control += ", immutable"
		}
		data.headers.Set("Cache-Control", control)
	}
	for key, value := range options.headers {
		data.headers.Set(key, value.String())
	}
	data.headers.Set("Accept-Ranges", "bytes")
	httpMarkResponseSent(data)
	data.touch()
}

func httpSplitFileArgs(args []OSValue) (name string, options OSValue, callback OSValue) {
	for _, arg := range args {
		switch {
		case IsString(arg):
			name = arg.StringValue()
		case arg.Type == OSVObject:
			options = arg
		case arg.Type == OSVFunction:
			callback = arg
		}
	}
	return
}

func httpDotfileBlocked(filename, policy string) bool {
	if policy != "ignore" && policy != "deny" {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(filename), "/") {
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
			return true
		}
	}
	return false
}

// httpSendFileCommon implements sendFile/download; callback receives errors instead of throwing.
func httpSendFileCommon(this OSValue, vm *VM, filename string, options, callback OSValue, downloadName string, download bool, label string) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.sent || data.nextCalled {
		return this
	}
	var optionObject *Object
	if options.Type == OSVObject && options.Opaque != nil {
		optionObject = (*Object)(options.Opaque)
	}
	fileOptions := httpFileOptionsFrom(optionObject, data.dispatch.incoming.etagEnabled)
	fail := func(err error) OSValue {
		if callback.Type == OSVFunction {
			runHTTPHandler(vm, callback, httpErrorValue(404, err.Error()))
			return this
		}
		vm.NativeError = "core:http " + label + ": " + err.Error()
		return vm.Null
	}
	if httpDotfileBlocked(filename, fileOptions.dotfiles) {
		return fail(errors.New("dotfiles are not served"))
	}
	info, target, err := httpResolveFile(filename, options)
	if err != nil {
		return fail(err)
	}
	if download {
		if downloadName == "" {
			downloadName = filepath.Base(target)
		}
		data.headers.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": downloadName}))
	}
	httpAttachFile(data, info, target, fileOptions)
	if callback.Type == OSVFunction {
		data.finishHooks = append(data.finishHooks, func(d *httpDispatch) { runHTTPHandler(d.vm, callback, d.vm.Null) })
	}
	return this
}

func nativeHTTPResponseSendFile(this OSValue, vm *VM, args []OSValue) OSValue {
	name, options, callback := httpSplitFileArgs(args)
	if name == "" {
		vm.NativeError = "core:http res.sendFile expects a filename, optional options object and callback"
		return vm.Null
	}
	return httpSendFileCommon(this, vm, name, options, callback, "", false, "res.sendFile")
}

func nativeHTTPResponseDownload(this OSValue, vm *VM, args []OSValue) OSValue {
	if len(args) == 0 || !IsString(args[0]) {
		vm.NativeError = "core:http res.download expects a filename, optional download name, options object and callback"
		return vm.Null
	}
	_, options, callback := httpSplitFileArgs(args[1:])
	downloadName := ""
	if len(args) > 1 && IsString(args[1]) {
		downloadName = args[1].StringValue()
	}
	return httpSendFileCommon(this, vm, args[0].StringValue(), options, callback, downloadName, true, "res.download")
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

// res.write streams a chunk (chunked transfer encoding); the response stays open until res.end().
func nativeHTTPResponseWrite(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.False
	}
	if data.dispatch.finished || data.nextCalled || (data.sent && !data.streaming) {
		return vm.False
	}
	if !data.streaming {
		data.streaming = true
		httpMarkResponseSent(data)
		data.headers.Del("Content-Length")
		if data.dispatch.incoming.poweredBy && data.headers.Get("X-Powered-By") == "" {
			data.headers.Set("X-Powered-By", "OpenScript")
		}
		if data.headers.Get("Content-Type") == "" {
			data.headers.Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	chunk := ""
	if len(args) > 0 && args[0].Type != OSVNull {
		chunk = args[0].String()
	}
	err := data.dispatch.incoming.out.chunk(data.status, data.headers, chunk)
	return httpBool(err == nil)
}

func nativeHTTPResponseFlush(this OSValue, vm *VM, _ []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if !data.streaming && !data.sent && !data.nextCalled {
		data.streaming = true
		httpMarkResponseSent(data)
		data.headers.Del("Content-Length")
		if data.headers.Get("Content-Type") == "" {
			data.headers.Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	if data.streaming {
		data.dispatch.incoming.out.flush(data.status, data.headers)
	}
	return this
}

func nativeHTTPResponseOn(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || len(args) < 2 || !IsString(args[0]) || args[1].Type != OSVFunction {
		vm.NativeError = "core:http res.on expects an event name ('finish' or 'close') and a function"
		return vm.Null
	}
	key := "res:" + args[0].StringValue()
	data.dispatch.listeners[key] = append(data.dispatch.listeners[key], args[1])
	return this
}

func nativeHTTPResponseEnd(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if data.streaming {
		if len(args) > 0 && args[0].Type != OSVNull && IsString(args[0]) {
			_ = data.dispatch.incoming.out.chunk(data.status, data.headers, args[0].StringValue())
		}
		data.dispatch.finishStream()
		return this
	}
	if data.sent || data.nextCalled {
		return this
	}
	if len(args) > 0 && args[0].Type != OSVNull {
		data.body = args[0].String()
	}
	httpMarkResponseSent(data)
	return this
}

// ---------------------------------------------------------------------------
// Module exports
// ---------------------------------------------------------------------------

func getCoreHTTP() map[string]OSValue {
	class := OSValue{Type: OSVClass, Opaque: unsafe.Pointer(ensureCoreHTTPClass())}
	exports := map[string]OSValue{
		"Express": class,
		"Router":  class,
		"fetch":   coreHTTPFunction(-1, nativeHTTPFetch),
	}
	for name, factory := range httpMiddlewareFactories() {
		exports[name] = coreHTTPFunction(-1, factory)
	}
	return exports
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
		// responseBody is owned by this goroutine and never mutated afterwards.
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
	response.Set("status", httpNum(float64(result.status)))
	response.Set("statusText", coreHTTPString(result.statusText))
	response.Set("url", coreHTTPString(result.url))
	response.Set("ok", httpBool(result.status >= 200 && result.status < 300))
	response.Set("headers", httpHeadersObject(result.headers))
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
	return resolvedHTTPPromise(vm, coreHTTPString(response.body), response.body)
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
