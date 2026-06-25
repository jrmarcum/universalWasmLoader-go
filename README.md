# universalWasmLoader-go

The **Go port** of the [Universal WASM Loader](https://github.com/jrmarcum/universalWasmLoader-js) —
a WIT-aware WebAssembly loader. Load a `.wasm` library, read its companion `.wit` interface, and call
each export as an idiomatic Go function with Canonical-ABI marshalling handled for you (numbers
direct, strings encoded/decoded through linear memory, booleans normalized).

- **Pure Go — no CGO.** Built on [wazero](https://github.com/tetratelabs/wazero), so `go get` just
  works: no C toolchain, no native `libwasmtime` to ship, trivial cross-compilation.
- **WASI Preview 1 built in** (SPEC §10) — I/O-using libraries (`console.log` → `fd_write`)
  instantiate with zero config; pure-compute modules need nothing.
- Conforms to the cross-language **SPEC.md v3.0.0** (Canonical ABI, callee-allocated string returns +
  `cabi_post_<name>`).

## Install

```sh
go get github.com/jrmarcum/universalWasmLoader-go
```

## Usage

```go
package main

import (
	"fmt"

	wasmloader "github.com/jrmarcum/universalWasmLoader-go"
)

func main() {
	// Load a .wasm library. The companion .wit (math.wit) is auto-detected.
	m, err := wasmloader.Load("math.wasm")
	if err != nil {
		panic(err)
	}
	defer m.Close()

	// Namespace pattern — call exports by camelCase name.
	sum, _ := m.Call("add", 3, 4) // int32(7)
	fmt.Println(sum)

	// Destructure pattern — bind an export as a standalone callable.
	square := m.Bind("square")
	sq, _ := square(5) // int32(25)
	fmt.Println(sq)

	// Strings round-trip through the Canonical ABI automatically.
	greeting, _ := m.Call("greet", "World") // "Hello, World!"
	fmt.Println(greeting)
}
```

### Host import callbacks

Register host functions the module calls into, keyed by the **camelCase** WIT import name. Callbacks
receive decoded native values:

```go
cbs := wasmloader.NewCallbacks().
	On("envMul", func(a, b float64) float64 { return a * b }).
	On("envAdd", func(a, b int32) int32 { return a + b })

m, _ := wasmloader.Load("imports.wasm", cbs)
v, _ := m.Call("scale", 3.0, 4.0) // float64(12)
```

### Version pinning

Append `@N` to assert the module's exported `version` global (the C SONAME convention):

```go
m, err := wasmloader.Load("math.wasm@2") // errors if version != 2
```

### Singleton (load once, share)

```go
get := wasmloader.CreateSingleton("math.wasm")
m1, _ := get() // loads
m2, _ := get() // same instance (m1 == m2)
```

### Instance pool (servers / concurrency)

Each instance has its own linear memory; `Run` acquires, calls, and releases atomically:

```go
pool := wasmloader.NewInstancePool("math.wasm", 4)
defer pool.Close()

result, _ := pool.Run(func(m *wasmloader.Module) (any, error) {
	return m.Call("add", 20, 22) // int32(42)
})
```

### Loading from a URL

`Load` is synchronous and file-only. For `http://`/`https://` sources use `Import`:

```go
m, err := wasmloader.Import(ctx, "https://example.com/math.wasm")
```

## Type mapping

| WIT type | Go argument | Go return |
| --- | --- | --- |
| `s32` | `int` / `int32` | `int32` |
| `s64` | `int64` | `int64` |
| `f32` | `float32` / `float64` | `float32` |
| `f64` | `float64` / numeric | `float64` |
| `bool` | `bool` | `bool` |
| `string` | `string` | `string` |

A module with no companion `.wit` is loaded with raw exports; `Call` then marshals by Go argument
type (strings require a `.wit`).

## Test

```sh
go test ./...
```

The suite (`loader_test.go`) covers the cross-language SPEC §8 fixtures — numeric round-trip, bool
normalization, string params/returns (Canonical ABI), host import callbacks — plus `Bind`, the
singleton, and the concurrent instance pool.

## License

MIT (see `LICENSE`).
