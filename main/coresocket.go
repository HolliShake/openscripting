package main

import (
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

const (
	maxSocketReadBytes = 1 << 20
	socketTimeout      = 5 * time.Second
	socketAFInet       = 2
	socketSockStream   = 1
)

type socketListenerState struct {
	listener net.Listener
	mu       sync.Mutex
	closed   bool
}

type pythonSocketState struct {
	listener     *socketListenerState
	conn         *socketConnState
	bindAddress  string
	acceptedPair bool
	closed       bool
}

type socketConnState struct {
	conn    net.Conn
	mu      sync.Mutex
	readMu  sync.Mutex
	writeMu sync.Mutex
	closed  bool
}

type socketAcceptResult struct {
	promise  *Promise
	listener *socketListenerState
	conn     net.Conn
	err      error
	pair     bool
}

type socketIOResult struct {
	promise *Promise
	value   OSValue
	text    string
	textSet bool
	err     error
}

type socketResult struct {
	accept *socketAcceptResult
	io     *socketIOResult
}

func coreSocketFunction(paramc int, callable func(vm *VM, args []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeFunction,
		Opaque: unsafe.Pointer(&NativeFunction{
			Paramc:   paramc,
			Callable: callable,
		}),
	}
}

func coreSocketString(value string) OSValue {
	return OSValue{
		Type:   OSVString,
		Number: float64(len(value)),
		Opaque: unsafe.Pointer(unsafe.StringData(value)),
	}
}

func coreSocketBoundMethod(this OSValue, paramc int, callable func(OSValue, *VM, []OSValue) OSValue) OSValue {
	return OSValue{
		Type: OSVNativeMethod,
		Opaque: unsafe.Pointer(&NativeMethod{
			This:     this,
			Paramc:   paramc,
			Callable: callable,
		}),
	}
}

func loopbackAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("address must be host:port")
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("only loopback IP addresses are allowed")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return "", errors.New("port must be between 0 and 65535")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(portNumber)), nil
}

func socketAddressArg(value OSValue) (string, error) {
	if IsString(value) {
		return loopbackAddress(value.StringValue())
	}
	if value.Type != OSVArray || value.Opaque == nil {
		return "", errors.New("address must be a host:port string or [host, port] array")
	}
	parts := (*Array)(value.Opaque).Elements
	if len(parts) != 2 || !IsString(parts[0]) || !IsNumber(parts[1]) || parts[1].Number != float64(int(parts[1].Number)) {
		return "", errors.New("address array must contain a host string and integer port")
	}
	return loopbackAddress(net.JoinHostPort(parts[0].StringValue(), strconv.Itoa(int(parts[1].Number))))
}

func socketAddressValue(address net.Addr) OSValue {
	host, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		return coreSocketString(address.String())
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return coreSocketString(address.String())
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: []OSValue{
		coreSocketString(host),
		{Type: OSVNum, Number: float64(port)},
	}})}
}

func nativeSocketListen(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:socket.listen expects a loopback host:port string"
		return vm.Null
	}
	address, err := loopbackAddress(args[0].StringValue())
	if err != nil {
		vm.NativeError = "core:socket.listen: " + err.Error()
		return vm.Null
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		vm.NativeError = "core:socket.listen: " + err.Error()
		return vm.Null
	}
	state := &socketListenerState{listener: listener}
	instance := &Object{
		ClassName: "TcpListener",
		Native:    state,
		Values:    make(map[string]OSValue, 3),
	}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
	instance.Set("accept", coreSocketBoundMethod(value, 0, nativeSocketAccept))
	instance.Set("address", coreSocketBoundMethod(value, 0, nativeSocketAddress))
	instance.Set("getsockname", coreSocketBoundMethod(value, 0, nativeSocketAddress))
	instance.Set("close", coreSocketBoundMethod(value, 0, nativeSocketListenerClose))
	vm.SocketActive++
	return value
}

func nativeSocketDial(vm *VM, args []OSValue) OSValue {
	if !IsString(args[0]) {
		vm.NativeError = "core:socket.dial expects a loopback host:port string"
		return vm.Null
	}
	address, err := loopbackAddress(args[0].StringValue())
	if err != nil {
		vm.NativeError = "core:socket.dial: " + err.Error()
		return vm.Null
	}
	conn, err := net.DialTimeout("tcp", address, socketTimeout)
	if err != nil {
		vm.NativeError = "core:socket.dial: " + err.Error()
		return vm.Null
	}
	vm.SocketActive++
	return socketConnectionValue(&socketConnState{conn: conn})
}

func socketConnectionValue(state *socketConnState) OSValue {
	instance := &Object{
		ClassName: "TcpConnection",
		Native:    state,
		Values:    make(map[string]OSValue, 5),
	}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
	instance.Set("read", coreSocketBoundMethod(value, 1, nativeSocketRead))
	instance.Set("write", coreSocketBoundMethod(value, 1, nativeSocketWrite))
	instance.Set("recv", coreSocketBoundMethod(value, 1, nativeSocketRead))
	instance.Set("send", coreSocketBoundMethod(value, 1, nativeSocketWrite))
	instance.Set("close", coreSocketBoundMethod(value, 0, nativeSocketConnectionClose))
	return value
}

func nativeSocketAccept(this OSValue, vm *VM, _ []OSValue) OSValue {
	if this.Type == OSVObject && this.Opaque != nil {
		if objectState, ok := (*Object)(this.Opaque).Native.(*pythonSocketState); ok && !objectState.acceptedPair {
			vm.NativeError = "core:socket.accept requires listen() to be called first"
			return vm.Null
		}
	}
	state, ok := socketListener(this, vm)
	if !ok {
		return vm.Null
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		vm.NativeError = "core:socket.accept called on a closed listener"
		return vm.Null
	}
	promise := &Promise{}
	pair := false
	if this.Type == OSVObject && this.Opaque != nil {
		if objectState, ok := (*Object)(this.Opaque).Native.(*pythonSocketState); ok {
			pair = objectState.acceptedPair
		}
	}
	vm.SocketPending++
	go func() {
		conn, err := state.listener.Accept()
		vm.SocketResults <- socketResult{accept: &socketAcceptResult{promise: promise, listener: state, conn: conn, err: err, pair: pair}}
	}()
	return OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
}

func socketListener(this OSValue, vm *VM) (*socketListenerState, bool) {
	if this.Type == OSVObject && this.Opaque != nil {
		object := (*Object)(this.Opaque)
		if state, ok := object.Native.(*socketListenerState); ok {
			return state, true
		}
		if state, ok := object.Native.(*pythonSocketState); ok && state.listener != nil {
			return state.listener, true
		}
	}
	vm.NativeError = "core:socket method called with an invalid TcpListener"
	return nil, false
}

func socketConnection(this OSValue, vm *VM) (*socketConnState, bool) {
	if this.Type == OSVObject && this.Opaque != nil {
		object := (*Object)(this.Opaque)
		if state, ok := object.Native.(*socketConnState); ok {
			return state, true
		}
		if state, ok := object.Native.(*pythonSocketState); ok && state.conn != nil {
			return state.conn, true
		}
	}
	vm.NativeError = "core:socket method called with an invalid TcpConnection"
	return nil, false
}

func nativeSocketAddress(this OSValue, vm *VM, _ []OSValue) OSValue {
	if this.Type == OSVObject && this.Opaque != nil {
		if objectState, ok := (*Object)(this.Opaque).Native.(*pythonSocketState); ok {
			if objectState.listener != nil {
				return socketAddressValue(objectState.listener.listener.Addr())
			}
			if objectState.conn != nil {
				return socketAddressValue(objectState.conn.conn.LocalAddr())
			}
			if objectState.bindAddress != "" {
				if address, err := net.ResolveTCPAddr("tcp", objectState.bindAddress); err == nil {
					return socketAddressValue(address)
				}
				return coreSocketString(objectState.bindAddress)
			}
			vm.NativeError = "core:socket.getsockname called before bind or connect"
			return vm.Null
		}
	}
	state, ok := socketListener(this, vm)
	if !ok {
		return vm.Null
	}
	return coreSocketString(state.listener.Addr().String())
}

func nativeSocketListenerClose(this OSValue, vm *VM, _ []OSValue) OSValue {
	state, ok := socketListener(this, vm)
	if !ok {
		return vm.False
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return vm.False
	}
	state.closed = true
	err := state.listener.Close()
	state.mu.Unlock()
	vm.SocketActive--
	if err != nil && !errors.Is(err, net.ErrClosed) {
		vm.NativeError = "core:socket.close listener: " + err.Error()
		return vm.False
	}
	return vm.True
}

func nativeSocketRead(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := socketConnection(this, vm)
	if !ok {
		return vm.Null
	}
	if !IsNumber(args[0]) || args[0].Number != float64(int(args[0].Number)) || args[0].Number < 1 || args[0].Number > maxSocketReadBytes {
		vm.NativeError = "core:socket.read expects a byte count between 1 and 1048576"
		return vm.Null
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		vm.NativeError = "core:socket.read called on a closed connection"
		return vm.Null
	}
	state.mu.Unlock()
	promise := &Promise{}
	vm.SocketPending++
	readSize := int(args[0].Number)
	go func() {
		state.readMu.Lock()
		defer state.readMu.Unlock()
		if err := state.conn.SetReadDeadline(time.Now().Add(socketTimeout)); err != nil {
			vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, err: err}}
			return
		}
		buffer := make([]byte, readSize)
		count, err := state.conn.Read(buffer)
		if err != nil && !errors.Is(err, io.EOF) {
			vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, err: err}}
			return
		}
		if count == 0 && errors.Is(err, io.EOF) {
			vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, value: vm.Null}}
			return
		}
		vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, text: string(buffer[:count]), textSet: true}}
	}()
	return OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
}

func nativeSocketWrite(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeSocketWriteMode(this, vm, args, false)
}

func nativeSocketWriteAll(this OSValue, vm *VM, args []OSValue) OSValue {
	return nativeSocketWriteMode(this, vm, args, true)
}

func nativeSocketWriteMode(this OSValue, vm *VM, args []OSValue, writeAll bool) OSValue {
	state, ok := socketConnection(this, vm)
	if !ok {
		return vm.Null
	}
	if !IsString(args[0]) {
		vm.NativeError = "core:socket.write expects a string"
		return vm.Null
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		vm.NativeError = "core:socket.write called on a closed connection"
		return vm.Null
	}
	state.mu.Unlock()
	promise := &Promise{}
	vm.SocketPending++
	text := args[0].StringValue()
	go func() {
		state.writeMu.Lock()
		defer state.writeMu.Unlock()
		if err := state.conn.SetWriteDeadline(time.Now().Add(socketTimeout)); err != nil {
			vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, err: err}}
			return
		}
		count := 0
		for count < len(text) {
			written, err := io.WriteString(state.conn, text[count:])
			count += written
			if err != nil {
				vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, err: err}}
				return
			}
			if written == 0 {
				vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, err: io.ErrShortWrite}}
				return
			}
			if !writeAll {
				break
			}
		}
		vm.SocketResults <- socketResult{io: &socketIOResult{promise: promise, value: OSValue{Type: OSVNum, Number: float64(count)}}}
	}()
	return OSValue{Type: OSVPromise, Opaque: unsafe.Pointer(promise)}
}

func nativeSocketConnectionClose(this OSValue, vm *VM, _ []OSValue) OSValue {
	state, ok := socketConnection(this, vm)
	if !ok {
		return vm.False
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return vm.False
	}
	state.closed = true
	err := state.conn.Close()
	vm.SocketActive--
	if err != nil && !errors.Is(err, net.ErrClosed) {
		vm.NativeError = "core:socket.close connection: " + err.Error()
		return vm.False
	}
	return vm.True
}

func settleSocketAccept(vm *VM, result socketAcceptResult) {
	vm.SocketPending--
	if result.err != nil {
		message := "core:socket.accept: " + result.err.Error()
		result.listener.mu.Lock()
		closed := result.listener.closed
		result.listener.mu.Unlock()
		if closed || errors.Is(result.err, net.ErrClosed) {
			message = "core:socket.accept: listener closed before accepting a connection"
		}
		rejectPromise(vm, result.promise, newErrorValue(message, 0))
		return
	}
	result.listener.mu.Lock()
	closed := result.listener.closed
	result.listener.mu.Unlock()
	if closed {
		_ = result.conn.Close()
		rejectPromise(vm, result.promise, newErrorValue("core:socket.accept: listener is closed", 0))
		return
	}
	vm.SocketActive++
	connection := socketConnectionValue(&socketConnState{conn: result.conn})
	if result.pair {
		connectionState := (*Object)(connection.Opaque).Native.(*socketConnState)
		pythonConnection := newPythonSocketValue(&pythonSocketState{conn: connectionState})
		resolvePromise(vm, result.promise, OSValue{
			Type: OSVArray,
			Opaque: unsafe.Pointer(&Array{Elements: []OSValue{
				pythonConnection,
				socketAddressValue(result.conn.RemoteAddr()),
			}}),
		})
		return
	}
	resolvePromise(vm, result.promise, connection)
}

func settleSocketIO(vm *VM, result socketIOResult) {
	vm.SocketPending--
	if result.err != nil {
		rejectPromise(vm, result.promise, newErrorValue("core:socket I/O: "+result.err.Error(), 0))
		return
	}
	if result.textSet {
		result.value = coreSocketString(result.text)
	}
	resolvePromise(vm, result.promise, result.value)
}

func getCoreSocket() map[string]OSValue {
	return map[string]OSValue{
		"AF_INET":     {Type: OSVNum, Number: socketAFInet},
		"SOCK_STREAM": {Type: OSVNum, Number: socketSockStream},
		"socket":      coreSocketFunction(-1, nativePythonSocket),
		"listen":      coreSocketFunction(1, nativeSocketListen),
		"dial":        coreSocketFunction(1, nativeSocketDial),
	}
}

func nativePythonSocket(vm *VM, args []OSValue) OSValue {
	if len(args) > 0 && (!IsNumber(args[0]) || args[0].Number != socketAFInet) {
		vm.NativeError = "core:socket.socket currently supports only AF_INET"
		return vm.Null
	}
	if len(args) > 1 && (!IsNumber(args[1]) || args[1].Number != socketSockStream) {
		vm.NativeError = "core:socket.socket currently supports only SOCK_STREAM"
		return vm.Null
	}
	return newPythonSocketValue(&pythonSocketState{})
}

func newPythonSocketValue(state *pythonSocketState) OSValue {
	instance := &Object{
		ClassName: "Socket",
		Native:    state,
		Values:    make(map[string]OSValue, 9),
	}
	value := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(instance)}
	instance.Set("bind", coreSocketBoundMethod(value, 1, nativePythonSocketBind))
	instance.Set("listen", coreSocketBoundMethod(value, 1, nativePythonSocketListen))
	instance.Set("accept", coreSocketBoundMethod(value, 0, nativeSocketAccept))
	instance.Set("connect", coreSocketBoundMethod(value, 1, nativePythonSocketConnect))
	instance.Set("send", coreSocketBoundMethod(value, 1, nativeSocketWrite))
	instance.Set("sendall", coreSocketBoundMethod(value, 1, nativeSocketWriteAll))
	instance.Set("recv", coreSocketBoundMethod(value, 1, nativeSocketRead))
	instance.Set("getsockname", coreSocketBoundMethod(value, 0, nativeSocketAddress))
	instance.Set("address", coreSocketBoundMethod(value, 0, nativeSocketAddress))
	instance.Set("close", coreSocketBoundMethod(value, 0, nativePythonSocketClose))
	return value
}

func pythonSocketStateFor(this OSValue, vm *VM) (*pythonSocketState, bool) {
	if this.Type == OSVObject && this.Opaque != nil {
		if state, ok := (*Object)(this.Opaque).Native.(*pythonSocketState); ok {
			return state, true
		}
	}
	vm.NativeError = "core:socket method called with an invalid Socket"
	return nil, false
}

func nativePythonSocketBind(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := pythonSocketStateFor(this, vm)
	if !ok {
		return vm.Null
	}
	address, err := socketAddressArg(args[0])
	if err != nil {
		vm.NativeError = "core:socket.bind: " + err.Error()
		return vm.Null
	}
	if state.closed || state.listener != nil || state.conn != nil || state.bindAddress != "" {
		vm.NativeError = "core:socket.bind called on an already bound or connected socket"
		return vm.Null
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		vm.NativeError = "core:socket.bind: " + err.Error()
		return vm.Null
	}
	state.bindAddress = listener.Addr().String()
	state.listener = &socketListenerState{listener: listener}
	vm.SocketActive++
	return vm.Null
}

func nativePythonSocketListen(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := pythonSocketStateFor(this, vm)
	if !ok {
		return vm.Null
	}
	if state.closed || state.bindAddress == "" || state.listener == nil || state.conn != nil || state.acceptedPair {
		vm.NativeError = "core:socket.listen requires a bound, unused socket"
		return vm.Null
	}
	state.acceptedPair = true
	return vm.Null
}

func nativePythonSocketConnect(this OSValue, vm *VM, args []OSValue) OSValue {
	state, ok := pythonSocketStateFor(this, vm)
	if !ok {
		return vm.Null
	}
	if state.closed || state.conn != nil || state.listener != nil {
		vm.NativeError = "core:socket.connect expects an unused socket"
		return vm.Null
	}
	address, err := socketAddressArg(args[0])
	if err != nil {
		vm.NativeError = "core:socket.connect: " + err.Error()
		return vm.Null
	}
	dialed := nativeSocketDial(vm, []OSValue{coreSocketString(address)})
	if vm.NativeError != "" || dialed.Type != OSVObject {
		return vm.Null
	}
	state.conn = (*Object)(dialed.Opaque).Native.(*socketConnState)
	return vm.Null
}

func nativePythonSocketClose(this OSValue, vm *VM, _ []OSValue) OSValue {
	state, ok := pythonSocketStateFor(this, vm)
	if !ok {
		return vm.False
	}
	if state.closed {
		return vm.True
	}
	if state.listener != nil {
		listenerValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{Native: state.listener})}
		nativeSocketListenerClose(listenerValue, vm, nil)
	}
	if state.conn != nil {
		connectionValue := OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{Native: state.conn})}
		nativeSocketConnectionClose(connectionValue, vm, nil)
	}
	state.closed = true
	return vm.True
}
