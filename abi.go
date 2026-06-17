package wasmloader

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// --- value conversion helpers (native Go any -> primitive) ---

func asInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int:
		return int64(n), nil
	case int32:
		return int64(n), nil
	case int64:
		return n, nil
	case uint:
		return int64(n), nil
	case uint32:
		return int64(n), nil
	case uint64:
		return int64(n), nil
	case float32:
		return int64(n), nil
	case float64:
		return int64(n), nil
	case bool:
		if n {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("expected an integer argument, got %T", v)
	}
}

func asFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("expected a float argument, got %T", v)
	}
}

func asBool(v any) (bool, error) {
	switch b := v.(type) {
	case bool:
		return b, nil
	case int:
		return b != 0, nil
	case int32:
		return b != 0, nil
	case int64:
		return b != 0, nil
	default:
		return false, fmt.Errorf("expected a bool argument, got %T", v)
	}
}

func asString(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", fmt.Errorf("expected a string argument, got %T", v)
}

// --- export call path (Go -> WASM -> Go), Canonical ABI ---

// encodeParam encodes one native argument into its WASM value(s) for a parameter of the
// given WIT type. A string param expands to two i32 values (ptr, len), allocating the
// bytes via cabi_realloc and writing them into linear memory.
func encodeParam(ctx context.Context, m *Module, t witType, v any) ([]uint64, error) {
	switch t {
	case typeS32:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		return []uint64{api.EncodeI32(int32(n))}, nil
	case typeS64:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		return []uint64{api.EncodeI64(n)}, nil
	case typeF32:
		f, err := asFloat64(v)
		if err != nil {
			return nil, err
		}
		return []uint64{api.EncodeF32(float32(f))}, nil
	case typeF64:
		f, err := asFloat64(v)
		if err != nil {
			return nil, err
		}
		return []uint64{api.EncodeF64(f)}, nil
	case typeBool:
		b, err := asBool(v)
		if err != nil {
			return nil, err
		}
		if b {
			return []uint64{1}, nil
		}
		return []uint64{0}, nil
	case typeString:
		s, err := asString(v)
		if err != nil {
			return nil, err
		}
		bytes := []byte(s)
		ptr, err := m.realloc(ctx, uint32(len(bytes)))
		if err != nil {
			return nil, err
		}
		if !m.memory.Write(ptr, bytes) {
			return nil, fmt.Errorf("string param write out of bounds (ptr=%d len=%d)", ptr, len(bytes))
		}
		return []uint64{api.EncodeI32(int32(ptr)), api.EncodeI32(int32(len(bytes)))}, nil
	default:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		return []uint64{api.EncodeI32(int32(n))}, nil
	}
}

// decodeResult turns a WASM export's raw return into a native Go value per the function's
// result type. A string result follows the Canonical ABI callee-allocated convention: the
// export returns an i32 pointer to a callee-allocated [ptr, len] pair; we read it, decode,
// then call the paired cabi_post_<name> export to release the buffer (no-op under a bump
// allocator, but part of the contract — decoding copies the bytes, so the string survives).
func (m *Module) decodeResult(ctx context.Context, fn witFunc, raw []uint64) (any, error) {
	if fn.Result == "" {
		return nil, nil
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("export %q returned no value but WIT declares result %q", fn.TsName, fn.Result)
	}
	switch fn.Result {
	case typeS32:
		return api.DecodeI32(raw[0]), nil
	case typeS64:
		return int64(raw[0]), nil
	case typeF32:
		return api.DecodeF32(raw[0]), nil
	case typeF64:
		return api.DecodeF64(raw[0]), nil
	case typeBool:
		// bool is an i32 0/1 — mask to the low 32 bits (an f64-producing body can leave
		// high-bit garbage in the result register, which a raw 64-bit != 0 would misread).
		return uint32(raw[0]) != 0, nil
	case typeString:
		retArea := uint32(raw[0])
		hdr, ok := m.memory.Read(retArea, 8)
		if !ok {
			return nil, fmt.Errorf("string return header read out of bounds (retArea=%d)", retArea)
		}
		strPtr := binary.LittleEndian.Uint32(hdr[0:4])
		strLen := binary.LittleEndian.Uint32(hdr[4:8])
		body, ok := m.memory.Read(strPtr, strLen)
		if !ok {
			return nil, fmt.Errorf("string return body read out of bounds (ptr=%d len=%d)", strPtr, strLen)
		}
		s := string(body) // copies the bytes — safe across cabi_post
		if post := m.mod.ExportedFunction("cabi_post_" + fn.TsName); post != nil {
			if _, err := post.Call(ctx, uint64(retArea)); err != nil {
				return nil, fmt.Errorf("cabi_post_%s failed: %w", fn.TsName, err)
			}
		}
		return s, nil
	default:
		return api.DecodeI32(raw[0]), nil
	}
}

// --- import side (WASM -> Go -> WASM): build the "env" host module ---

// registerEnvImports instantiates a wazero host module named "env" satisfying each WIT
// import, dispatching to the user's registered callback. Memory for string-param imports
// is read from the calling module's memory at call time (it does not exist at build time).
func registerEnvImports(ctx context.Context, r wazero.Runtime, imports []witFunc, cbs *Callbacks) error {
	hb := r.NewHostModuleBuilder("env")
	for i := range imports {
		imp := imports[i]
		var cb any
		if cbs != nil {
			cb = cbs.fns[imp.TsName]
		}
		paramTypes := flattenParamValueTypes(imp.Params)
		var resultTypes []api.ValueType
		if imp.Result != "" {
			resultTypes = []api.ValueType{resultValueType(imp.Result)}
		}
		fn := makeHostFunc(imp, cb)
		hb.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(fn), paramTypes, resultTypes).
			Export(kebabToWasmImportKey(imp.Name))
	}
	_, err := hb.Instantiate(ctx)
	return err
}

// makeHostFunc builds the wazero host-function body for one WIT import. It decodes the raw
// stack per the import's params, invokes the user callback via reflection (converting each
// arg to the callback's declared parameter type), and writes the encoded result to stack[0].
func makeHostFunc(imp witFunc, cb any) func(context.Context, api.Module, []uint64) {
	var fnVal reflect.Value
	if cb != nil {
		fnVal = reflect.ValueOf(cb)
	}
	return func(ctx context.Context, mod api.Module, stack []uint64) {
		// Decode the flattened stack into native Go args.
		args := make([]any, 0, len(imp.Params))
		si := 0
		for _, p := range imp.Params {
			switch p.Type {
			case typeString:
				ptr := uint32(stack[si])
				ln := uint32(stack[si+1])
				si += 2
				s := ""
				if b, ok := mod.Memory().Read(ptr, ln); ok {
					s = string(b)
				}
				args = append(args, s)
			case typeBool:
				args = append(args, stack[si] != 0)
				si++
			case typeF32:
				args = append(args, api.DecodeF32(stack[si]))
				si++
			case typeF64:
				args = append(args, api.DecodeF64(stack[si]))
				si++
			case typeS64:
				args = append(args, int64(stack[si]))
				si++
			default: // s32
				args = append(args, api.DecodeI32(stack[si]))
				si++
			}
		}

		// No callback registered: satisfy the import with a zero/void result.
		if !fnVal.IsValid() {
			if imp.Result != "" {
				stack[0] = 0
			}
			return
		}

		// Reflect-call the user callback, converting each arg to its declared param type.
		ft := fnVal.Type()
		in := make([]reflect.Value, len(args))
		for i, a := range args {
			av := reflect.ValueOf(a)
			if i < ft.NumIn() && av.Type().ConvertibleTo(ft.In(i)) {
				av = av.Convert(ft.In(i))
			}
			in[i] = av
		}
		out := fnVal.Call(in)
		if imp.Result == "" || len(out) == 0 {
			return
		}
		stack[0] = encodeReflectResult(imp.Result, out[0])
	}
}

// encodeReflectResult encodes a reflected callback return value into a single WASM value.
func encodeReflectResult(t witType, rv reflect.Value) uint64 {
	switch t {
	case typeF64:
		return api.EncodeF64(reflectFloat(rv))
	case typeF32:
		return api.EncodeF32(float32(reflectFloat(rv)))
	case typeS64:
		return api.EncodeI64(reflectInt(rv))
	case typeBool:
		if rv.Kind() == reflect.Bool && rv.Bool() {
			return 1
		}
		if rv.Kind() != reflect.Bool && reflectInt(rv) != 0 {
			return 1
		}
		return 0
	default: // s32
		return api.EncodeI32(int32(reflectInt(rv)))
	}
}

func reflectFloat(rv reflect.Value) float64 {
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int())
	default:
		return 0
	}
}

func reflectInt(rv reflect.Value) int64 {
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Float32, reflect.Float64:
		return int64(rv.Float())
	case reflect.Bool:
		if rv.Bool() {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// flattenParamValueTypes maps WIT params to wazero value types, expanding each string to
// two i32 values (ptr, len).
func flattenParamValueTypes(params []witParam) []api.ValueType {
	types := make([]api.ValueType, 0, len(params))
	for _, p := range params {
		switch p.Type {
		case typeString:
			types = append(types, api.ValueTypeI32, api.ValueTypeI32)
		case typeF32:
			types = append(types, api.ValueTypeF32)
		case typeF64:
			types = append(types, api.ValueTypeF64)
		case typeS64:
			types = append(types, api.ValueTypeI64)
		default: // s32, bool
			types = append(types, api.ValueTypeI32)
		}
	}
	return types
}

func resultValueType(t witType) api.ValueType {
	switch t {
	case typeF32:
		return api.ValueTypeF32
	case typeF64:
		return api.ValueTypeF64
	case typeS64:
		return api.ValueTypeI64
	default: // s32, bool, string(ptr)
		return api.ValueTypeI32
	}
}
