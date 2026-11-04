//go:build windows

package main

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	memCommit       = 0x1000
	memReserve      = 0x2000
	memRelease      = 0x8000
	pageReadWrite   = 0x04
	pageExecuteRead = 0x20
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	virtualAllocProc      = kernel32.NewProc("VirtualAlloc")
	virtualProtectProc    = kernel32.NewProc("VirtualProtect")
	virtualFreeProc       = kernel32.NewProc("VirtualFree")
	getCurrentProcessProc = kernel32.NewProc("GetCurrentProcess")
	flushInstructionCache = kernel32.NewProc("FlushInstructionCache")
	rtlMoveMemoryProc     = kernel32.NewProc("RtlMoveMemory")
)

type executableNativeCode struct {
	address uintptr
	size    uintptr
}

func allocateNativeCode(instructions []byte) (nativeCode, error) {
	if len(instructions) == 0 {
		return nil, errors.New("cannot allocate empty native code")
	}
	address, _, callErr := virtualAllocProc.Call(0, uintptr(len(instructions)), memCommit|memReserve, pageReadWrite)
	if address == 0 {
		return nil, fmt.Errorf("VirtualAlloc failed: %w", callErr)
	}

	rtlMoveMemoryProc.Call(address, uintptr(unsafe.Pointer(&instructions[0])), uintptr(len(instructions)))
	runtime.KeepAlive(instructions)
	var oldProtection uint32
	changed, _, protectErr := virtualProtectProc.Call(address, uintptr(len(instructions)), pageExecuteRead, uintptr(unsafe.Pointer(&oldProtection)))
	if changed == 0 {
		virtualFreeProc.Call(address, 0, memRelease)
		return nil, fmt.Errorf("VirtualProtect failed: %w", protectErr)
	}

	process, _, _ := getCurrentProcessProc.Call()
	flushed, _, flushErr := flushInstructionCache.Call(process, address, uintptr(len(instructions)))
	if flushed == 0 {
		virtualFreeProc.Call(address, 0, memRelease)
		if flushErr != nil {
			return nil, fmt.Errorf("FlushInstructionCache failed: %w", flushErr)
		}
		return nil, errors.New("FlushInstructionCache failed")
	}
	return &executableNativeCode{address: address, size: uintptr(len(instructions))}, nil
}

func (code *executableNativeCode) release() {
	if code == nil || code.address == 0 {
		return
	}
	virtualFreeProc.Call(code.address, 0, memRelease)
	code.address = 0
	code.size = 0
}
