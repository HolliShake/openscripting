//go:build windows && amd64

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

func (code *executableNativeCode) run(left, right float64) (float64, bool) {
	if code == nil || code.address == 0 {
		return 0, false
	}
	var result float64
	status := uintptr(1)
	_, _, _ = syscall.SyscallN(code.address,
		uintptr(unsafe.Pointer(&left)),
		uintptr(unsafe.Pointer(&right)),
		uintptr(unsafe.Pointer(&result)),
		uintptr(unsafe.Pointer(&status)),
	)
	runtime.KeepAlive(left)
	runtime.KeepAlive(right)
	runtime.KeepAlive(result)
	return result, status != 0
}
