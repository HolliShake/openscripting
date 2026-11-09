package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	// body is the static response payload used for routes that do not invoke handler.
	body string
	// handler is the script callback invoked for a matching request, or null for a static route.
	handler OSValue
}

// httpLayer is one ordered entry in the app's stack (a route or a middleware).
type httpLayer struct {
	// method is the HTTP verb this layer answers; "" matches every method (use/all).
	method string
	// pattern is the path split into segments; segments may be ":name" params or "*".
	pattern []string
	// prefix is true for use(): the layer matches any path below the pattern.
	prefix bool
	// route is the static response or script handler for this layer.
	route httpRoute
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
	// headers carries request metadata without sharing the mutable net/http request object.
	headers http.Header
	// chain is the ordered list of matching layers selected on the listener thread.
	chain []httpMatch
	// onError is the app's error handler snapshot, or null.
	onError OSValue
	// write sends status, headers, and body back to the listener goroutine.
	write func(int, http.Header, string)
	// done signals that the response was written and the listener may release request resources.
	done chan struct{}
}

type httpRequestData struct {
	// headers are the request headers, used by req.get.
	headers http.Header
	// body is the raw request body, used by req.json.
	body string
}

type httpResponseData struct {
	// status is the response code selected by the script.
	status int
	// headers are the response headers selected by the script.
	headers http.Header
	// body is the response payload.
	body string
	// sent is set once a handler produced a final response.
	sent bool
	// next is set when a handler called next() with no error.
	next bool
	// hasErr is set when a handler called next(err).
	hasErr bool
	// errValue is the value passed to next(err).
	errValue OSValue
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
	layers []httpLayer
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
	// vm is the owning event loop that receives request and listener-completion messages.
	vm *VM
}

var (
	coreHTTPClass *Class
	coreHTTPOnce  sync.Once
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
	state := &httpAppState{}
	instance := &Object{
		ClassName: "Express",
		Class:     ensureCoreHTTPClass(),
		Native:    state,
		Values:    make(map[string]OSValue, 16),
	}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
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
	instance.Set("route", httpBoundMethod(value, 4, nativeHTTPRoute))
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

func splitRequestPath(escaped string) []string {
	parts := splitPath(escaped)
	for i, part := range parts {
		if decoded, err := url.PathUnescape(part); err == nil {
			parts[i] = decoded
		}
	}
	return parts
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
		if (*Function)(value.Opaque).Async {
			vm.NativeError = "core:http route handlers must be synchronous"
			return false
		}
		return true
	}
	vm.NativeError = "core:http expects a handler function (only the last route target may be a string)"
	return false
}

func nativeHTTPVerb(this OSValue, vm *VM, args []OSValue, method string) OSValue {
	if len(args) < 2 || !IsString(args[0]) {
		vm.NativeError = "core:http route expects a path followed by one or more handlers"
		return vm.Null
	}
	return httpRegister(this, vm, method, args[0].StringValue(), false, 200, args[1:])
}

func nativeHTTPRoute(this OSValue, vm *VM, args []OSValue) OSValue {
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

func httpRegister(this OSValue, vm *VM, method, path string, prefix bool, status int, targets []OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	if path == "" || path[0] != '/' {
		vm.NativeError = "core:http route path must start with /"
		return vm.Null
	}
	layers := make([]httpLayer, 0, len(targets))
	for i, target := range targets {
		if !httpValidTarget(vm, target, i == len(targets)-1) {
			return vm.Null
		}
		route := httpRoute{status: status}
		if target.Type == OSVFunction {
			route.handler = target
		} else {
			route.body = target.StringValue()
		}
		layers = append(layers, httpLayer{method: method, pattern: splitPath(path), prefix: prefix, route: route})
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
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	segments := splitRequestPath(request.URL.EscapedPath())
	var chain []httpMatch
	state.mu.RLock()
	for _, layer := range state.layers {
		if layer.method != "" && layer.method != request.Method &&
			!(request.Method == http.MethodHead && layer.method == http.MethodGet) {
			continue
		}
		if params, ok := matchSegments(layer.pattern, segments, layer.prefix); ok {
			chain = append(chain, httpMatch{route: layer.route, params: params})
		}
	}
	onError := state.onError
	state.mu.RUnlock()
	if len(chain) == 0 {
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
		write(chain[0].route.status, http.Header{"Content-Type": {"text/plain; charset=utf-8"}}, chain[0].route.body)
		return
	}
	incoming := &httpIncoming{
		method: request.Method, path: request.URL.Path, query: request.URL.RawQuery,
		body: unsafe.String(unsafe.SliceData(body), len(body)), host: request.Host,
		remote: request.RemoteAddr, headers: request.Header.Clone(),
		chain: chain, onError: onError, write: write, done: make(chan struct{}),
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
		http.Error(writer, "server is closed", http.StatusServiceUnavailable)
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

func httpHeadersObject(headers http.Header) OSValue {
	object := &Object{Values: make(map[string]OSValue, len(headers))}
	for key, values := range headers {
		if len(values) != 0 {
			object.Set(strings.ToLower(key), coreHTTPString(values[0]))
		}
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func runHTTPHandler(vm *VM, handler OSValue, args ...OSValue) {
	function := (*Function)(handler.Opaque)
	coroutine := NewCoroutine(nil, function, InitialStack(len(args)))
	for index := range function.CaptureMeta {
		capture := &function.CaptureMeta[index]
		coroutine.Frame.Env[capture.Dst] = capture.Cell
	}
	copy(coroutine.OperandStack[:], args)
	coroutine.Sp = len(args)
	vm.Current = coroutine
	runCoroutine(nil, vm, coroutine)
	vm.Current = nil
}

func serveHTTPIncoming(vm *VM, incoming *httpIncoming) {
	// ----- request object -----
	queryParams := make(map[string]string)
	if parsed, err := url.ParseQuery(incoming.query); err == nil {
		for key, values := range parsed {
			if len(values) != 0 {
				queryParams[key] = values[0]
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
		Native: &httpRequestData{headers: incoming.headers, body: incoming.body},
		Values: make(map[string]OSValue, 16),
	}
	requestValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(request)}
	request.Set("method", coreHTTPString(incoming.method))
	request.Set("path", coreHTTPString(incoming.path))
	request.Set("originalUrl", coreHTTPString(originalURL))
	request.Set("query", coreHTTPString(incoming.query))
	request.Set("queryParams", httpStringObject(queryParams))
	request.Set("body", coreHTTPString(incoming.body))
	request.Set("hostname", coreHTTPString(hostname))
	request.Set("ip", coreHTTPString(ip))
	request.Set("headers", httpHeadersObject(incoming.headers))
	request.Set("get", httpBoundMethod(requestValue, 1, nativeHTTPRequestGet))
	request.Set("json", httpBoundMethod(requestValue, 0, nativeHTTPRequestJSON))

	// ----- response object -----
	data := &httpResponseData{status: 200, headers: make(http.Header)}
	response := &Object{Native: data, Values: make(map[string]OSValue, 12)}
	responseValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(response)}
	response.Set("status", httpBoundMethod(responseValue, 1, nativeHTTPResponseStatus))
	response.Set("sendStatus", httpBoundMethod(responseValue, 1, nativeHTTPResponseSendStatus))
	response.Set("send", httpBoundMethod(responseValue, 1, nativeHTTPResponseSend))
	response.Set("json", httpBoundMethod(responseValue, 1, nativeHTTPResponseJSON))
	response.Set("set", httpBoundMethod(responseValue, 2, nativeHTTPResponseSet))
	response.Set("header", httpBoundMethod(responseValue, 2, nativeHTTPResponseSet))
	response.Set("get", httpBoundMethod(responseValue, 1, nativeHTTPResponseGet))
	response.Set("type", httpBoundMethod(responseValue, 1, nativeHTTPResponseType))
	response.Set("redirect", httpBoundMethod(responseValue, -1, nativeHTTPResponseRedirect))
	response.Set("end", httpBoundMethod(responseValue, -1, nativeHTTPResponseEnd))
	nextValue := httpBoundMethod(responseValue, -1, nativeHTTPNext)

	// ----- middleware / route chain -----
	for index := 0; index < len(incoming.chain) && !data.sent && !data.hasErr; index++ {
		match := incoming.chain[index]
		if match.route.handler.Type != OSVFunction {
			data.status = match.route.status
			data.body = match.route.body
			data.sent = true
			break
		}
		request.Set("params", httpStringObject(match.params))
		data.next = false
		runHTTPHandler(vm, match.route.handler, requestValue, responseValue, nextValue)
		if !data.next {
			break // handler finished the request (or chose not to continue)
		}
	}

	// ----- error handling / fallbacks -----
	switch {
	case data.hasErr:
		errValue := data.errValue
		*data = httpResponseData{status: 500, headers: make(http.Header)}
		if incoming.onError.Type == OSVFunction {
			runHTTPHandler(vm, incoming.onError, errValue, requestValue, responseValue, nextValue)
		}
		if data.hasErr || !data.sent {
			*data = httpResponseData{status: 500, headers: make(http.Header), body: "Internal Server Error"}
		}
	case !data.sent && data.next:
		data.status = 404
		data.body = "Cannot " + incoming.method + " " + incoming.path
	}
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "text/plain; charset=utf-8")
	}
	incoming.write(data.status, data.headers, data.body)
	close(incoming.done)
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
	if value := data.headers.Get(args[0].StringValue()); value != "" {
		return coreHTTPString(value)
	}
	return vm.Null
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

func nativeHTTPNext(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if len(args) > 0 && args[0].Type != OSVNull {
		data.hasErr = true
		data.errValue = args[0]
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
	if !httpValidStatus(args[0]) {
		vm.NativeError = "core:http response status must be between 100 and 599"
		return vm.Null
	}
	data.status = int(args[0].Number)
	data.body = http.StatusText(data.status)
	data.sent = true
	return this
}

func nativeHTTPResponseSend(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	if args[0].Type == OSVObject {
		return nativeHTTPResponseJSON(this, vm, args)
	}
	data.body = args[0].String()
	data.sent = true
	return this
}

func nativeHTTPResponseJSON(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	vm.NativeError = ""
	encoded := nativeJSONStringify(vm, args[:1])
	if vm.NativeError != "" {
		return vm.Null
	}
	data.body = encoded.StringValue()
	if data.headers.Get("Content-Type") == "" {
		data.headers.Set("Content-Type", "application/json; charset=utf-8")
	}
	data.sent = true
	return this
}

func nativeHTTPResponseSet(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
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

func nativeHTTPResponseType(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || !IsString(args[0]) {
		vm.NativeError = "core:http res.type expects a type string"
		return vm.Null
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

func nativeHTTPResponseRedirect(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
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
	data.sent = true
	return this
}

func nativeHTTPResponseEnd(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
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
		if len(values) != 0 {
			responseHeaders.Set(strings.ToLower(key), coreHTTPString(values[0]))
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
