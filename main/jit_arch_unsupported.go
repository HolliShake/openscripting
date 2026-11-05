//go:build !amd64 && !arm64 && !386 && !arm.7

package main

const nativeJITEmitterAvailable = false

func emitNativeCode([]nativeIROp) ([]byte, bool) { return nil, false }
