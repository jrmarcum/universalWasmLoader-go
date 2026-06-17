package wasmloader

// Callbacks holds host import functions the WASM module calls into Go. Register each under
// the camelCase WIT import name (e.g. "envMul" for the WIT import `env-mul`). The callback
// receives decoded native Go values and its return is encoded back — no manual marshalling.
//
//	cbs := wasmloader.NewCallbacks().
//		On("envMul", func(a, b float64) float64 { return a * b }).
//		On("envAdd", func(a, b int32) int32 { return a + b })
//	m, _ := wasmloader.Load("imports.wasm", cbs)
type Callbacks struct {
	fns map[string]any
}

// NewCallbacks returns an empty Callbacks builder.
func NewCallbacks() *Callbacks {
	return &Callbacks{fns: make(map[string]any)}
}

// On registers a host callback under its camelCase WIT import name. fn must be a function
// whose parameters and return match the import's WIT signature (numbers/bool/string). It
// returns the receiver for chaining.
func (c *Callbacks) On(name string, fn any) *Callbacks {
	c.fns[name] = fn
	return c
}
