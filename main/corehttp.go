package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	maxFetchResponseBytes = 10 << 20
	fetchTimeout          = 30 * time.Second
)

type httpRoute struct {
	status  int
	body    string
	handler OSValue
}

type httpIncoming struct {
	method  string
	path    string
	query   string
	body    string
	headers http.Header
	route   httpRoute
	write   func(int, string, string)
	done    chan struct{}
	status  int
	text    string
	json    string
}

type httpResponseData struct {
	status int
	text   string
	json   string
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
	mu      sync.RWMutex
	routes  map[string]httpRoute
	server  *http.Server
	done    chan struct{}
	address string
	started bool
	closed  bool
	vm      *VM
}

var (
	coreHTTPClass *Class
	coreHTTPOnce  sync.Once
)

func ensureCoreHTTPClass() *Class {
	coreHTTPOnce.Do(func() {
		coreHTTPClass = &Class{
			Name: "Express",
			Constructor: OSValue{
				Type: OSVNativeFunction,
				Opaque: unsafe.Pointer(&NativeFunction{
					Paramc:   0,
					Callable: nativeHTTPConstructor,
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

func nativeHTTPConstructor(vm *VM, _ []OSValue) OSValue {
	state := &httpAppState{routes: make(map[string]httpRoute)}
	instance := &Object{
		ClassName: "Express",
		Class:     ensureCoreHTTPClass(),
		Native:    state,
		Values:    make(map[string]OSValue, 6),
	}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
	instance.Set("get", httpBoundMethod(value, 2, nativeHTTPGet))
	instance.Set("post", httpBoundMethod(value, 2, nativeHTTPPost))
	instance.Set("route", httpBoundMethod(value, 4, nativeHTTPRoute))
	instance.Set("listen", httpBoundMethod(value, 1, nativeHTTPListen))
	instance.Set("address", httpBoundMethod(value, 0, nativeHTTPAddress))
	instance.Set("close", httpBoundMethod(value, 0, nativeHTTPClose))
	return value
}

func httpBoundMethod(this OSValue, paramc int, callable func(OSValue, *VM, []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeMethod,
		Opaque: unsafe.Pointer(&NativeMethod{
			This:     this,
			Paramc:   paramc,
			Callable: callable,
		}),
	}
}

func httpApp(this OSValue, vm *VM) (*httpAppState, bool) {
	if this.Type == OSVObject && this.Opaque != nil {
		if state, ok := (*Object)(this.Opaque).Native.(*httpAppState); ok {
			return state, true
		}
	}
	vm.NativeError = "core:http method called with an invalid Express instance"
	return nil, false
}

func nativeHTTPGet(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPRegister(this, vm, args, http.MethodGet, 200)
}

func nativeHTTPPost(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeHTTPRegister(this, vm, args, http.MethodPost, 200)
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
	return httpRegister(this, vm, strings.ToUpper(args[0].StringValue()), args[1].StringValue(), status, args[3])
}

func nativeHTTPRegister(this OSValue, vm *VM, args []OSValue, method string, status int) OSValue {
	if !IsString(args[0]) || (args[1].Type != OSVString && args[1].Type != OSVFunction) {
		vm.NativeError = "core:http route expects a path and response string or handler function"
		return vm.Null
	}
	return httpRegister(this, vm, method, args[0].StringValue(), status, args[1])
}

func httpRegister(this OSValue, vm *VM, method, path string, status int, handlerOrBody OSValue) OSValue {
	state, ok := httpApp(this, vm)
	if !ok {
		return vm.Null
	}
	if path == "" || path[0] != '/' {
		vm.NativeError = "core:http route path must start with /"
		return vm.Null
	}
	if handlerOrBody.Type == OSVFunction {
		if handlerOrBody.Opaque == nil {
			vm.NativeError = "core:http route handler cannot be nil"
			return vm.Null
		}
		if (*Function)(handlerOrBody.Opaque).Async {
			vm.NativeError = "core:http route handlers must be synchronous"
			return vm.Null
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		vm.NativeError = "core:http cannot add routes after close"
		return vm.Null
	}
	route := httpRoute{status: status}
	if IsString(handlerOrBody) {
		route.body = handlerOrBody.StringValue()
	} else if handlerOrBody.Type == OSVFunction {
		route.handler = handlerOrBody
	} else {
		vm.NativeError = "core:http route response must be a string or function"
		return vm.Null
	}
	state.routes[httpRouteKey(method, path)] = route
	return this
}

func httpRouteKey(method, path string) string {
	return method + " " + path
}

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

func (state *httpAppState) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	state.mu.RLock()
	route, exists := state.routes[httpRouteKey(request.Method, request.URL.Path)]
	state.mu.RUnlock()
	if !exists {
		http.NotFound(writer, request)
		return
	}
	if route.handler.Type == OSVFunction {
		incoming := &httpIncoming{
			method: request.Method, path: request.URL.Path, query: request.URL.RawQuery,
			body: string(body), headers: request.Header.Clone(), route: route,
			done: make(chan struct{}),
		}
		incoming.write = func(status int, contentType, responseBody string) {
			writer.Header().Set("Content-Type", contentType)
			writer.WriteHeader(status)
			_, _ = io.WriteString(writer, responseBody)
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
			return
		}
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(route.status)
	_, _ = io.WriteString(writer, route.body)
}

func serveHTTPIncoming(vm *VM, incoming *httpIncoming) {
	request := &Object{Values: make(map[string]OSValue, 5)}
	request.Set("method", coreHTTPString(incoming.method))
	request.Set("path", coreHTTPString(incoming.path))
	request.Set("query", coreHTTPString(incoming.query))
	request.Set("body", coreHTTPString(incoming.body))
	headers := &Object{Values: make(map[string]OSValue, len(incoming.headers))}
	for key, values := range incoming.headers {
		if len(values) != 0 {
			headers.Set(strings.ToLower(key), coreHTTPString(values[0]))
		}
	}
	request.Set("headers", OSValue{Type: OSVObject, Opaque: unsafe.Pointer(headers)})
	responseData := &httpResponseData{status: incoming.route.status}
	response := &Object{Values: make(map[string]OSValue, 3)}
	responseValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(response)}
	response.Set("status", httpBoundMethod(responseValue, 1, nativeHTTPResponseStatus))
	response.Set("send", httpBoundMethod(responseValue, 1, nativeHTTPResponseSend))
	response.Set("json", httpBoundMethod(responseValue, 1, nativeHTTPResponseJSON))
	response.Native = responseData
	function := (*Function)(incoming.route.handler.Opaque)
	coroutine := NewCoroutine(nil, function, InitialStack(2))
	for index := range function.CaptureMeta {
		capture := &function.CaptureMeta[index]
		coroutine.Frame.Env[capture.Dst] = capture.Cell
	}
	coroutine.OperandStack[0] = OSValue{Type: OSVObject, Opaque: unsafe.Pointer(request)}
	coroutine.OperandStack[1] = responseValue
	coroutine.Sp = 2
	vm.Current = coroutine
	runCoroutine(nil, vm, coroutine)
	if vm.Current != nil {
		vm.Current = nil
	}
	contentType := "text/plain; charset=utf-8"
	body := responseData.text
	if responseData.json != "" {
		contentType = "application/json; charset=utf-8"
		body = responseData.json
	}
	incoming.write(responseData.status, contentType, body)
	close(incoming.done)
}

func httpResponseDataFrom(this OSValue, vm *VM) *httpResponseData {
	if this.Type == OSVObject && this.Opaque != nil {
		if data, ok := (*Object)(this.Opaque).Native.(*httpResponseData); ok {
			return data
		}
	}
	vm.NativeError = "core:http response method called with an invalid response"
	return nil
}

func nativeHTTPResponseStatus(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil || !IsNumber(args[0]) || args[0].Number < 100 || args[0].Number > 599 {
		vm.NativeError = "core:http response status must be between 100 and 599"
		return vm.Null
	}
	data.status = int(args[0].Number)
	return this
}

func nativeHTTPResponseSend(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data != nil {
		data.text = args[0].String()
	}
	return vm.Null
}

func nativeHTTPResponseJSON(this OSValue, vm *VM, args []OSValue) OSValue {
	data := httpResponseDataFrom(this, vm)
	if data == nil {
		return vm.Null
	}
	vm.NativeError = ""
	encoded := nativeJSONStringify(vm, args)
	if vm.NativeError != "" {
		return vm.Null
	}
	data.json = encoded.StringValue()
	return vm.Null
}

func getCoreHTTP() map[string]OSValue {
	return map[string]OSValue{
		"Express": {Type: OSVClass, Opaque: unsafe.Pointer(ensureCoreHTTPClass())},
		"fetch":   coreHTTPFunction(-1, nativeHTTPFetch),
	}
}

func coreHTTPFunction(paramc int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			Paramc:   paramc,
			Callable: callable,
		}),
	}
}

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
		defer response.Body.Close()
		responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxFetchResponseBytes+1))
		if err == nil && len(responseBody) > maxFetchResponseBytes {
			err = errors.New("response body exceeds 10 MiB limit")
		}
		vm.HTTPFetches <- httpFetchResult{
			promise: promise, status: response.StatusCode, statusText: response.Status,
			url: response.Request.URL.String(), headers: response.Header.Clone(),
			body: string(responseBody), err: err,
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
