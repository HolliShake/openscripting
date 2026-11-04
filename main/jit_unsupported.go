//go:build !windows

package main

func allocateNativeCode([]byte) (nativeCode, error) { return nil, nil }
