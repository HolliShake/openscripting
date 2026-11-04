//go:build windows && !amd64 && !arm64 && !386

package main

func (code *executableNativeCode) run(float64, float64) (float64, bool) { return 0, false }
