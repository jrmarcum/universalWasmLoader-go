package wasmloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Module is a loaded WebAssembly library with its WIT interface applied. Call exports by
// their camelCase name via [Module.Call] or [Module.Bind]. Close it with [Module.Close]
// when done to release the underlying wazero runtime and linear memory.
type Module struct {
	ctx     context.Context
	runtime wazero.Runtime
	mod     api.Module
	memory  api.Memory
	realloc func(ctx context.Context, n uint32) (uint32, error)
	exports map[string]witFunc // camelName -> signature; nil for a raw (no-WIT) module
	raw     bool
}

var versionSuffixRe = regexp.MustCompile(`@(\d+)$`)

// parseVersionSuffix splits an optional `@N` version suffix from a path (SPEC §3).
func parseVersionSuffix(path string) (clean string, requested int, hasVersion bool) {
	if m := versionSuffixRe.FindStringSubmatch(path); m != nil {
		n, _ := strconv.Atoi(m[1])
		return path[:len(path)-len(m[0])], n, true
	}
	return path, 0, false
}

// Load reads and instantiates a `.wasm` library from a file path (synchronous — no
// goroutine/await ceremony). It auto-detects the companion `.wit` (replacing the `.wasm`
// suffix) and applies the Canonical ABI; with no `.wit`, raw exports are used. An optional
// `@N` suffix on the path pins the module's exported `version` global (SPEC §3). Pass an
// optional [Callbacks] for host imports.
func Load(path string, cbs ...*Callbacks) (*Module, error) {
	ctx := context.Background()
	clean, requested, hasVersion := parseVersionSuffix(path)
	wasmBytes, err := os.ReadFile(clean)
	if err != nil {
		return nil, fmt.Errorf("wasmloader: read %q: %w", clean, err)
	}
	witSrc := readSidecarWit(clean)
	return loadCore(ctx, wasmBytes, witSrc, clean, requested, hasVersion, firstCallbacks(cbs))
}

// Import loads a `.wasm` library from either a file path or an `http://`/`https://` URL.
// IO happens first, then instantiation. Use this when the source may be a URL; for plain
// file paths, [Load] is simpler. Honors the same `@N` version-pin suffix.
func Import(ctx context.Context, source string, cbs ...*Callbacks) (*Module, error) {
	clean, requested, hasVersion := parseVersionSuffix(source)
	var wasmBytes []byte
	var err error
	if strings.HasPrefix(clean, "http://") || strings.HasPrefix(clean, "https://") {
		wasmBytes, err = fetchURL(ctx, clean)
	} else {
		wasmBytes, err = os.ReadFile(clean)
	}
	if err != nil {
		return nil, fmt.Errorf("wasmloader: fetch %q: %w", clean, err)
	}
	witSrc := readSidecarWitFrom(ctx, clean)
	return loadCore(ctx, wasmBytes, witSrc, clean, requested, hasVersion, firstCallbacks(cbs))
}

// loadCore is the shared instantiation path: build a runtime, provide the WASI-P1 shim
// (SPEC §10), wire host imports, instantiate, call `_initialize`, and verify the version.
func loadCore(ctx context.Context, wasmBytes []byte, witSrc, path string, requested int, hasVersion bool, cbs *Callbacks) (*Module, error) {
	r := wazero.NewRuntime(ctx)
	ok := false
	defer func() {
		if !ok {
			_ = r.Close(ctx)
		}
	}()

	// SPEC §10: always provide the built-in WASI Preview 1 shim so an I/O-using library
	// (e.g. a modc library that calls console.log → fd_write) instantiates. Unused import
	// namespaces are ignored, so pure-compute modules are unaffected.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return nil, fmt.Errorf("wasmloader: instantiate WASI shim: %w", err)
	}

	var parsed parsedWit
	hasWit := strings.TrimSpace(witSrc) != ""
	if hasWit {
		parsed = parseWit(witSrc)
		if len(parsed.Imports) > 0 {
			if err := registerEnvImports(ctx, r, parsed.Imports, cbs); err != nil {
				return nil, fmt.Errorf("wasmloader: register host imports: %w", err)
			}
		}
	}

	// Do not auto-run _start; we control initialization via _initialize (SPEC §10).
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithStdout(os.Stdout).
		WithStderr(os.Stderr).
		WithStartFunctions()
	mod, err := r.InstantiateWithConfig(ctx, wasmBytes, cfg)
	if err != nil {
		return nil, fmt.Errorf("wasmloader: instantiate %q: %w", path, err)
	}

	m := &Module{ctx: ctx, runtime: r, mod: mod, memory: mod.Memory()}
	if cabi := mod.ExportedFunction("cabi_realloc"); cabi != nil {
		m.realloc = func(ctx context.Context, n uint32) (uint32, error) {
			res, err := cabi.Call(ctx, 0, 0, 1, uint64(n))
			if err != nil {
				return 0, err
			}
			return uint32(res[0]), nil
		}
	}

	// SPEC §10: run the reactor initializer once before any other export.
	if init := mod.ExportedFunction("_initialize"); init != nil {
		if _, err := init.Call(ctx); err != nil {
			return nil, fmt.Errorf("wasmloader: _initialize failed: %w", err)
		}
	}

	if hasVersion {
		if err := assertVersion(m, requested, path); err != nil {
			return nil, err
		}
	}

	if hasWit {
		m.exports = make(map[string]witFunc, len(parsed.Exports))
		for _, e := range parsed.Exports {
			m.exports[e.TsName] = e
		}
	} else {
		m.raw = true
	}

	ok = true
	return m, nil
}

// assertVersion enforces an `@N` version pin against the module's exported `version` global.
func assertVersion(m *Module, requested int, path string) error {
	g := m.mod.ExportedGlobal("version")
	if g == nil {
		return fmt.Errorf("wasmloader: version @%d requested for %q but the module exports no \"version\" global", requested, path)
	}
	actual := int32(uint32(g.Get()))
	if int(actual) != requested {
		return fmt.Errorf("wasmloader: version mismatch for %q — requested @%d, module exports version %d", path, requested, actual)
	}
	return nil
}

// Call invokes an export by its camelCase name, marshalling native Go arguments to/from the
// WASM Canonical ABI. Numbers pass through (int/int32/int64 ↔ s32/s64, float ↔ f32/f64),
// bool is normalized, and strings are encoded/decoded through linear memory. Returns the
// decoded result (or nil for a void export).
func (m *Module) Call(name string, args ...any) (any, error) {
	if m.raw {
		return m.callRaw(name, args...)
	}
	fn, ok := m.exports[name]
	if !ok {
		return nil, fmt.Errorf("wasmloader: export %q not found in WIT interface", name)
	}
	wasmFn := m.mod.ExportedFunction(fn.TsName)
	if wasmFn == nil {
		return nil, fmt.Errorf("wasmloader: WIT declares export %q but the module has no such WASM export", fn.TsName)
	}
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("wasmloader: %q expects %d argument(s), got %d", name, len(fn.Params), len(args))
	}

	wasmArgs := make([]uint64, 0, len(fn.Params))
	for i, p := range fn.Params {
		vals, err := encodeParam(m.ctx, m, p.Type, args[i])
		if err != nil {
			return nil, fmt.Errorf("wasmloader: %q arg %d (%s): %w", name, i, p.Name, err)
		}
		wasmArgs = append(wasmArgs, vals...)
	}

	raw, err := wasmFn.Call(m.ctx, wasmArgs...)
	if err != nil {
		return nil, fmt.Errorf("wasmloader: calling %q: %w", name, err)
	}
	return m.decodeResult(m.ctx, fn, raw)
}

// callRaw is the best-effort path for a module with no companion `.wit`: arguments are
// marshalled by their Go type (int→i32, int64→i64, float64→f64, bool→i32) and the first
// raw result (if any) is returned as a uint64. Strings are unsupported without a WIT.
func (m *Module) callRaw(name string, args ...any) (any, error) {
	wasmFn := m.mod.ExportedFunction(name)
	if wasmFn == nil {
		return nil, fmt.Errorf("wasmloader: raw export %q not found", name)
	}
	wasmArgs := make([]uint64, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case int:
			wasmArgs[i] = api.EncodeI32(int32(v))
		case int32:
			wasmArgs[i] = api.EncodeI32(v)
		case int64:
			wasmArgs[i] = api.EncodeI64(v)
		case float32:
			wasmArgs[i] = api.EncodeF32(v)
		case float64:
			wasmArgs[i] = api.EncodeF64(v)
		case bool:
			if v {
				wasmArgs[i] = 1
			}
		default:
			return nil, fmt.Errorf("wasmloader: raw call %q: unsupported argument type %T (a WIT file is required for strings)", name, a)
		}
	}
	raw, err := wasmFn.Call(m.ctx, wasmArgs...)
	if err != nil {
		return nil, fmt.Errorf("wasmloader: calling raw %q: %w", name, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return raw[0], nil
}

// Bind returns a standalone callable for a named export, approximating JS destructuring:
//
//	add := m.Bind("add")
//	sum, _ := add(3, 4)
func (m *Module) Bind(name string) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		return m.Call(name, args...)
	}
}

// Exports returns the camelCase names of the module's WIT exports (nil for a raw module).
func (m *Module) Exports() []string {
	names := make([]string, 0, len(m.exports))
	for n := range m.exports {
		names = append(names, n)
	}
	return names
}

// Close releases the module's wazero runtime and linear memory.
func (m *Module) Close() error {
	return m.runtime.Close(m.ctx)
}

// --- IO helpers ---

func readSidecarWit(wasmPath string) string {
	witPath := strings.TrimSuffix(wasmPath, ".wasm") + ".wit"
	b, err := os.ReadFile(witPath)
	if err != nil {
		return ""
	}
	return string(b)
}

func readSidecarWitFrom(ctx context.Context, source string) string {
	witSource := strings.TrimSuffix(source, ".wasm") + ".wit"
	if strings.HasPrefix(witSource, "http://") || strings.HasPrefix(witSource, "https://") {
		b, err := fetchURL(ctx, witSource)
		if err != nil {
			return ""
		}
		return string(b)
	}
	b, err := os.ReadFile(witSource)
	if err != nil {
		return ""
	}
	return string(b)
}

func fetchURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func firstCallbacks(cbs []*Callbacks) *Callbacks {
	if len(cbs) > 0 {
		return cbs[0]
	}
	return nil
}
