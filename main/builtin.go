package main

import (
	"sync"
)

var (
	builtinModules map[string]map[string]OSValue
	builtinOnce    sync.Once
)

func getBuiltinModule(name string) (map[string]OSValue, bool) {
	builtinOnce.Do(func() {
		builtinModules = map[string]map[string]OSValue{
			"core:io":    getCoreIo(),
			"core:error": getCoreError(),
			"core:file":  getCoreFile(),
			"core:math":  getCoreMath(),
			"core:path":  getCorePath(),
			"core:json":  getCoreJSON(),
		}
	})
	m, ok := builtinModules[name]
	return m, ok
}
