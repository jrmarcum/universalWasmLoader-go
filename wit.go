// Package wasmloader is the Go port of the Universal WASM Loader — a WIT-aware
// WebAssembly loader. It loads a `.wasm` library (the reactor/library shape that
// `wasmtk modc` produces), reads its companion `.wit` interface, and exposes each
// export as an idiomatic Go call with Canonical-ABI marshalling handled internally
// (numbers direct, strings encoded/decoded through linear memory, booleans
// normalized). When no `.wit` is found, raw exports are used.
//
// Runtime: built on pure-Go [wazero] — no CGO, no native library to ship. WASI
// Preview 1 is provided by wazero's built-in shim (SPEC §10), so I/O-using
// libraries instantiate with zero configuration; pure-compute modules need nothing.
//
// Conforms to the cross-language SPEC.md v3.0.0 (Canonical ABI, callee-allocated
// string returns + cabi_post_<name>).
//
// [wazero]: https://github.com/tetratelabs/wazero
package wasmloader

import (
	"regexp"
	"strings"
)

// witType is one of the loader's supported WIT primitive types.
type witType string

const (
	typeS32    witType = "s32"
	typeS64    witType = "s64"
	typeF32    witType = "f32"
	typeF64    witType = "f64"
	typeBool   witType = "bool"
	typeString witType = "string"
)

// witParam is a single function parameter: its camelCase name and WIT type.
type witParam struct {
	Name string
	Type witType
}

// witFunc is one WIT import or export. A WIT name has three forms (see SPEC / PORT_PROMPT):
// kebab-case (source), camelCase (the WASM binary export/import name — used for lookups),
// and a lang-idiomatic API name. The camelCase form is authoritative for runtime lookups.
type witFunc struct {
	Name   string // kebab-case source name (WIT identity)
	TsName string // camelCase — the actual WASM export/import symbol
	Params []witParam
	Result witType // "" means no result (void)
}

// parsedWit is the result of parsing a `.wit` source produced by wasmtk.
type parsedWit struct {
	PackageName string
	WorldName   string
	Imports     []witFunc
	Exports     []witFunc
}

// kebabToCamel converts a kebab-case WIT name to camelCase (e.g. is-positive → isPositive).
func kebabToCamel(name string) string {
	var b strings.Builder
	upper := false
	for _, r := range name {
		if r == '-' {
			upper = true
			continue
		}
		if upper {
			b.WriteRune(toUpper(r))
			upper = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// kebabToWasmImportKey converts a kebab-case WIT import name to the underscore WASM
// import key (e.g. env-mul → env_mul).
func kebabToWasmImportKey(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func toUpper(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 32
	}
	return r
}

// parseWitType maps a raw WIT type token to a witType, defaulting to s32 (matching the
// JS reference: unknown/aggregate types fall back to s32 so a pointer still passes through).
func parseWitType(raw string) witType {
	switch strings.TrimSpace(raw) {
	case "s32":
		return typeS32
	case "s64":
		return typeS64
	case "f32":
		return typeF32
	case "f64":
		return typeF64
	case "bool":
		return typeBool
	case "string":
		return typeString
	default:
		return typeS32
	}
}

func parseWitParams(raw string) []witParam {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	params := make([]witParam, 0, len(parts))
	for _, part := range parts {
		colon := strings.Index(part, ":")
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(part[:colon])
		params = append(params, witParam{
			Name: kebabToCamel(name),
			Type: parseWitType(part[colon+1:]),
		})
	}
	return params
}

// funcRe matches `<keyword> name: func(params) [-> result];` — keyword is filled in per call.
func funcRe(keyword string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + keyword + `\s+([\w-]+)\s*:\s*func\s*\(([^)]*)\)(?:\s*->\s*([\w-]+))?\s*;`)
}

func parseWitFuncs(body, keyword string) []witFunc {
	re := funcRe(keyword)
	matches := re.FindAllStringSubmatch(body, -1)
	funcs := make([]witFunc, 0, len(matches))
	for _, m := range matches {
		name := m[1]
		var result witType
		if m[3] != "" {
			result = parseWitType(m[3])
		}
		funcs = append(funcs, witFunc{
			Name:   name,
			TsName: kebabToCamel(name),
			Params: parseWitParams(m[2]),
			Result: result,
		})
	}
	return funcs
}

var (
	pkgRe   = regexp.MustCompile(`package\s+([\w:/-]+)\s*;`)
	worldRe = regexp.MustCompile(`world\s+([\w-]+)\s*\{([\s\S]*)\}`)
)

// parseWit parses a `.wit` source string produced by wasmtk. Mirrors the JS reference
// wit-parser.js exactly (regex shapes, kebab→camel conversion, s32 fallback).
func parseWit(src string) parsedWit {
	var p parsedWit
	if m := pkgRe.FindStringSubmatch(src); m != nil {
		p.PackageName = m[1]
	}
	body := ""
	if m := worldRe.FindStringSubmatch(src); m != nil {
		p.WorldName = m[1]
		body = m[2]
	}
	p.Imports = parseWitFuncs(body, "import")
	p.Exports = parseWitFuncs(body, "export")
	return p
}
