package wasmloader

import "sync"

// CreateSingleton returns an accessor that loads the module on the first call and caches it;
// every subsequent call returns the same *Module (SPEC §6.1). Concurrent first-callers all
// block on the single load and then share the result. Appropriate for CLI tools and
// bounded-call scenarios.
//
//	get := wasmloader.CreateSingleton("math.wasm")
//	m1, _ := get() // loads
//	m2, _ := get() // same instance: m1 == m2
func CreateSingleton(path string, cbs ...*Callbacks) func() (*Module, error) {
	var once sync.Once
	var (
		mod *Module
		err error
	)
	cb := firstCallbacks(cbs)
	return func() (*Module, error) {
		once.Do(func() {
			if cb != nil {
				mod, err = Load(path, cb)
			} else {
				mod, err = Load(path)
			}
		})
		return mod, err
	}
}
