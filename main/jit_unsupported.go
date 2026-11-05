//go:build !windows && !(linux && arm.7) && !(darwin && arm64 && cgo)

package main

const nativeJITAllocatorAvailable = false

func allocateNativeCode([]byte) (nativeCode, error) { return nil, nil }
