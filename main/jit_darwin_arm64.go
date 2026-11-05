//go:build darwin && arm64 && cgo

package main

/*
#define _DARWIN_C_SOURCE
#include <pthread.h>
#include <libkern/OSCacheControl.h>
#include <stdint.h>
#include <stddef.h>
#include <sys/mman.h>
#include <string.h>

static void *openscript_jit_allocate(size_t size) {
	void *memory = mmap(NULL, size, PROT_READ | PROT_WRITE | PROT_EXEC,
		MAP_PRIVATE | MAP_ANON | MAP_JIT, -1, 0);
	return memory == MAP_FAILED ? NULL : memory;
}

static int openscript_jit_copy(void *destination, const void *source, size_t size) {
	pthread_jit_write_protect_np(0);
	memcpy(destination, source, size);
	sys_icache_invalidate(destination, size);
	pthread_jit_write_protect_np(1);
	return 1;
}

static void openscript_jit_release(void *memory, size_t size) {
	munmap(memory, size);
}

static void openscript_jit_call(void *entry, double *left, double *right,
		double *result, uintptr_t *status) {
	typedef void (*jit_entry)(double *, double *, double *, uintptr_t *);
	((jit_entry)entry)(left, right, result, status);
}
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"
)

const nativeJITAllocatorAvailable = true

type executableNativeCode struct {
	address unsafe.Pointer
	size    C.size_t
}

func allocateNativeCode(instructions []byte) (nativeCode, error) {
	if len(instructions) == 0 {
		return nil, errors.New("cannot allocate empty native code")
	}
	size := C.size_t(len(instructions))
	address := C.openscript_jit_allocate(size)
	if address == nil {
		return nil, errors.New("mmap MAP_JIT failed")
	}
	if C.openscript_jit_copy(address, unsafe.Pointer(&instructions[0]), size) == 0 {
		C.openscript_jit_release(address, size)
		return nil, errors.New("copying JIT code failed")
	}
	runtime.KeepAlive(instructions)
	return &executableNativeCode{address: address, size: size}, nil
}

func (code *executableNativeCode) run(left, right float64) (float64, bool) {
	if code == nil || code.address == nil {
		return 0, false
	}
	var result float64
	status := uintptr(1)
	C.openscript_jit_call(
		code.address,
		(*C.double)(unsafe.Pointer(&left)),
		(*C.double)(unsafe.Pointer(&right)),
		(*C.double)(unsafe.Pointer(&result)),
		(*C.uintptr_t)(unsafe.Pointer(&status)),
	)
	runtime.KeepAlive(code.address)
	runtime.KeepAlive(left)
	runtime.KeepAlive(right)
	runtime.KeepAlive(result)
	return result, status != 0
}

func (code *executableNativeCode) release() {
	if code == nil || code.address == nil {
		return
	}
	C.openscript_jit_release(code.address, code.size)
	code.address = nil
	code.size = 0
}
