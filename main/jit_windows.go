//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	nativeJITAllocatorAvailable = true
	memCommit                   = 0x1000
	memReserve                  = 0x2000
	memRelease                  = 0x8000
	pageReadWrite               = 0x04
	pageExecuteRead             = 0x20
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	virtualAllocProc      = kernel32.NewProc("VirtualAlloc")
	virtualProtectProc    = kernel32.NewProc("VirtualProtect")
	virtualFreeProc       = kernel32.NewProc("VirtualFree")
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

	_, _, _ = rtlMoveMemoryProc.Call(address, uintptr(unsafe.Pointer(&instructions[0])), uintptr(len(instructions)))
	runtime.KeepAlive(instructions)
	var oldProtection uint32
	changed, _, protectErr := virtualProtectProc.Call(address, uintptr(len(instructions)), pageExecuteRead, uintptr(unsafe.Pointer(&oldProtection)))
	if changed == 0 {
		protectError := fmt.Errorf("VirtualProtect failed: %w", protectErr)
		if freeErr := freeNativeAllocation(address); freeErr != nil {
			return nil, errors.Join(protectError, freeErr)
		}
		return nil, protectError
	}

	process, processErr := syscall.GetCurrentProcess()
	if processErr != nil {
		if freeErr := freeNativeAllocation(address); freeErr != nil {
			return nil, errors.Join(fmt.Errorf("GetCurrentProcess failed: %w", processErr), freeErr)
		}
		return nil, fmt.Errorf("GetCurrentProcess failed: %w", processErr)
	}
	flushed, _, flushErr := flushInstructionCache.Call(uintptr(process), address, uintptr(len(instructions)))
	if flushed == 0 {
		var flushError error
		if flushErr != nil {
			flushError = fmt.Errorf("FlushInstructionCache failed: %w", flushErr)
		} else {
			flushError = errors.New("FlushInstructionCache failed")
		}
		if freeErr := freeNativeAllocation(address); freeErr != nil {
			return nil, errors.Join(flushError, freeErr)
		}
		return nil, flushError
	}
	return &executableNativeCode{address: address, size: uintptr(len(instructions))}, nil
}

func (code *executableNativeCode) release() {
	if code == nil || code.address == 0 {
		return
	}
	if err := freeNativeAllocation(code.address); err != nil {
		log.Printf("release native JIT code: %v", err)
		return
	}
	code.address = 0
	code.size = 0
}

func freeNativeAllocation(address uintptr) error {
	freed, _, callErr := virtualFreeProc.Call(address, 0, memRelease)
	if freed != 0 {
		return nil
	}
	if callErr != nil {
		return fmt.Errorf("VirtualFree failed: %w", callErr)
	}
	return errors.New("VirtualFree failed")
}
