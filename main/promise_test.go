package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"unsafe"
)

func runScriptLogs(t *testing.T, source string) []string {
	t.Helper()

	var output bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	}()

	state := &State{}
	program := Compile(NewParser(state, &File{Name: "test.os", Content: []rune(source)}))
	Run(state, program)

	result := strings.TrimSpace(output.String())
	if result == "" {
		return nil
	}
	return strings.Split(result, "\n")
}

func TestAsyncExecutionOrderMatchesJavaScript(t *testing.T) {
	got := runScriptLogs(t, `
log "sync:start";
var asyncFn = fn() async {
    log "async:body";
    return 1;
};
var promise = asyncFn();
log "sync:end";
promise->then(fn(value) { log "then:callback"; });
var awaitFn = fn() async {
    log "await:before";
    let value = await asyncFn();
    log "await:after";
};
awaitFn();
log "sync:after-await-call";
`)
	want := []string{
		"sync:start",
		"async:body",
		"sync:end",
		"await:before",
		"async:body",
		"sync:after-await-call",
		"then:callback",
		"await:after",
	}
	if !equalStrings(got, want) {
		t.Fatalf("execution order mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestPromiseReactionsFollowRegistrationOrder(t *testing.T) {
	got := runScriptLogs(t, `
var immediate = fn() async {
    log "immediate:body";
    return "ready";
};
var pending = fn() async {
    log "pending:before-await";
    let value = await immediate();
    log "pending:after-await";
    return value;
};
var promise = pending();
promise->then(fn(value) { log "then:first"; });
promise->then(fn(value) { log "then:second"; });
var waiter = fn(label) async {
    log label + ":before-await";
    let value = await promise;
    log label + ":after-await";
};
waiter("waiter:one");
waiter("waiter:two");
log "sync:end";
`)
	want := []string{
		"pending:before-await",
		"immediate:body",
		"waiter:one:before-await",
		"waiter:two:before-await",
		"sync:end",
		"pending:after-await",
		"then:first",
		"then:second",
		"waiter:one:after-await",
		"waiter:two:after-await",
	}
	if !equalStrings(got, want) {
		t.Fatalf("promise reaction order mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestAwaitFulfilledPromiseResumesAsMicrotask(t *testing.T) {
	got := runScriptLogs(t, `
var immediate = fn() async {
    return "ready";
};
var promise = immediate();
var waiter = fn() async {
    log "await:before";
    let value = await promise;
    log "await:after";
};
waiter();
promise->then(fn(value) { log "then:callback"; });
log "sync:end";
`)
	want := []string{
		"await:before",
		"sync:end",
		"await:after",
		"then:callback",
	}
	if !equalStrings(got, want) {
		t.Fatalf("fulfilled await order mismatch\n got: %q\nwant: %q", got, want)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestPromiseThenNativeFunction(t *testing.T) {
	input := OSValue{Type: OSVNum, Number: 5}
	var received OSValue

	vm := &VM{Null: OSValue{Type: OSVNull}}
	next := &Promise{}
	promise := &Promise{
		Value:    input,
		Resolved: true,
	}
	reaction := PromiseReaction{
		Next: next,
		Callback: OSValue{
			Type: OSVNativeFunction,
			Opaque: unsafe.Pointer(&NativeFunction{
				Paramc: 1,
				Callable: func(_ *VM, args []OSValue) {
					received = args[0]
				},
			}),
		},
	}

	schedulePromiseReaction(vm, promise, reaction)

	if !IsEqual(received, input) {
		t.Fatalf("native callback received %#v, want %#v", received, input)
	}
	if !next.Resolved {
		t.Fatal("chained promise was not resolved")
	}
	if next.Value.Type != OSVNull {
		t.Fatalf("chained promise resolved to %v, want null", next.Value)
	}
}

func TestPromiseCatchNativeFunction(t *testing.T) {
	input := OSValue{Type: OSVNum, Number: 5}
	var received OSValue

	vm := &VM{Null: OSValue{Type: OSVNull}}
	next := &Promise{}
	promise := &Promise{
		Value:    input,
		Resolved: true,
		Rejected: true,
	}
	reaction := PromiseReaction{
		Catch: true,
		Next:  next,
		Callback: OSValue{
			Type: OSVNativeFunction,
			Opaque: unsafe.Pointer(&NativeFunction{
				Paramc: 1,
				Callable: func(_ *VM, args []OSValue) {
					received = args[0]
				},
			}),
		},
	}

	schedulePromiseReaction(vm, promise, reaction)

	if !IsEqual(received, input) {
		t.Fatalf("catch callback received %#v, want %#v", received, input)
	}
	if !next.Resolved || next.Rejected {
		t.Fatalf("catch result state = resolved:%t rejected:%t, want fulfilled", next.Resolved, next.Rejected)
	}
	if next.Value.Type != OSVNull {
		t.Fatalf("catch callback promise resolved to %v, want null", next.Value)
	}
}

func TestPromiseThenPassesRejectionThrough(t *testing.T) {
	input := OSValue{Type: OSVNum, Number: 5}
	vm := &VM{Null: OSValue{Type: OSVNull}}
	next := &Promise{}
	promise := &Promise{
		Value:    input,
		Resolved: true,
		Rejected: true,
	}
	reaction := PromiseReaction{Next: next, Callback: OSValue{Type: OSVNativeFunction}}

	schedulePromiseReaction(vm, promise, reaction)

	if !next.Resolved || !next.Rejected {
		t.Fatalf("then result state = resolved:%t rejected:%t, want rejected", next.Resolved, next.Rejected)
	}
	if !IsEqual(next.Value, input) {
		t.Fatalf("then propagated %#v, want %#v", next.Value, input)
	}
}
