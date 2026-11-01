package main

import "os"

type Import struct {
	// IsImporting detects recursive imports before the module initializer completes.
	IsImporting bool
	// Initialized distinguishes a fully executed module from one whose Value is not ready yet.
	Initialized bool
	// Path is the canonical cache key and source filename for this imported module.
	Path string
	// FileInfo supports same-file detection when different paths reference the same file.
	FileInfo os.FileInfo
	// Function indexes the module initializer in State.Functions.
	Function int
	// Value stores the initializer's exported object and is reused for subsequent imports.
	Value OSValue
	// Exports records names made visible to importers without retaining another value copy.
	Exports map[string]struct{}
}
