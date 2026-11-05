//go:build linux && arm.7

package main

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

const nativeJITAllocatorAvailable = true

type executableNativeCode struct {
	address uintptr
	bytes   []byte
}

func allocateNativeCode(instructions []byte) (nativeCode, error) {
	if len(instructions) == 0 {
		return nil, errors.New("cannot allocate empty native code")
	}
	memory, err := syscall.Mmap(-1, 0, len(instructions), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, err
	}
	copy(memory, instructions)
	start := uintptr(unsafe.Pointer(&memory[0]))
	_, _, errno := syscall.Syscall(0x0f0002, start, start+uintptr(len(instructions)), 0)
	if errno != 0 {
		_ = syscall.Munmap(memory)
		return nil, errno
	}
	if err = syscall.Mprotect(memory, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		_ = syscall.Munmap(memory)
		return nil, err
	}
	runtime.KeepAlive(instructions)
	return &executableNativeCode{address: start, bytes: memory}, nil
}

func (code *executableNativeCode) run(left, right float64) (float64, bool) {
	if code == nil || code.address == 0 {
		return 0, false
	}
	var result float64
	status := uintptr(1)
	callARM32(code.address,
		uintptr(unsafe.Pointer(&left)),
		uintptr(unsafe.Pointer(&right)),
		uintptr(unsafe.Pointer(&result)),
		uintptr(unsafe.Pointer(&status)),
	)
	runtime.KeepAlive(code.bytes)
	runtime.KeepAlive(left)
	runtime.KeepAlive(right)
	runtime.KeepAlive(result)
	return result, status != 0
}

func (code *executableNativeCode) release() {
	if code == nil || len(code.bytes) == 0 {
		return
	}
	_ = syscall.Munmap(code.bytes)
	code.bytes = nil
	code.address = 0
}

func callARM32(code, left, right, result, status uintptr)
