//go:build !amd64 && !arm64 && !386

package main

func emitNativeCode([]nativeIROp) ([]byte, bool) { return nil, false }
