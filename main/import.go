package main

import "os"

type Import struct {
	IsImporting bool
	Initialized bool
	Path        string
	FileInfo    os.FileInfo
	Function    int32
	Value       OSValue
	Exports     map[string]struct{}
}
