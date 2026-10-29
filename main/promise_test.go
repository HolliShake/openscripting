package main

import (
	"testing"
	"unsafe"
)

func TestPromiseThenNativeFunction(t *testing.T) {
	input := OSValue{Type: OSVNum, Number: 5}
	var received OSValue

	vm := &VM{Null: OSValue{Type: OSVNull}}
	next := &Promise{}
	promise := &Promise{
		Value:    input,
		Resolved: true,
		Next:     next,
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

	schedulePromiseCallback(vm, promise)

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
		Value:         input,
		Resolved:      true,
		Rejected:      true,
		CallbackCatch: true,
		Next:          next,
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

	schedulePromiseCallback(vm, promise)

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
		Next:     next,
		Callback: OSValue{Type: OSVNativeFunction},
	}

	schedulePromiseCallback(vm, promise)

	if !next.Resolved || !next.Rejected {
		t.Fatalf("then result state = resolved:%t rejected:%t, want rejected", next.Resolved, next.Rejected)
	}
	if !IsEqual(next.Value, input) {
		t.Fatalf("then propagated %#v, want %#v", next.Value, input)
	}
}
