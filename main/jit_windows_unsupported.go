//go:build windows && !amd64 && !arm64 && !386

package main

func runNativeCode(*executableNativeCode, float64, float64) (float64, bool) { return 0, false }
