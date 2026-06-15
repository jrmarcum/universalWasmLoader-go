# Overview — universalWasmLoader-go

## What this is

`universalWasmLoader-go` is intended to be the **Go port** of the Universal WASM Loader — the
cross-language family of WIT-aware WebAssembly loaders. Like its siblings (the reference JS/TS
`universalWasmLoader`, plus the Rust and Python ports), its job is to load a `.wasm` component,
read its WIT-described interface, and expose each export as an idiomatic, typed Go function with
Canonical-ABI marshalling handled for the caller (numbers direct, strings/aggregates encoded and
decoded through linear memory, booleans normalized).

- **Language / runtime:** Go, built on the **wazero** pure-Go WebAssembly runtime (no cgo, no
  external WASM engine).
- **Package registry / distribution:** **pkg.go.dev** (consumed via `go get`).

## Current state — EARLY STUB (verified 2026-06-15)

This repo is a **pre-implementation stub**. There is **no Go source code yet**. The repo contains
only:

- `README.md` — one line ("Universal wasm loader for Go")
- `CLAUDE.md` — archive notes (now with a `cmem/` pointer prepended)
- `LICENSE`
- `.gitignore` — standard Go ignore rules
- `cmem/` — this portable memory folder

There is **no `go.mod`**, **no `*.go` files**, no `wazero` dependency wired up, and no tests. Git
history is two commits (`Initial commit`, `update docs`).

### What's missing (everything functional)

- `go.mod` declaring the module path + `wazero` dependency
- The loader API itself (see below)
- WIT parsing / interface introspection
- Canonical ABI marshalling code
- Any test suite

## Intended public API surface (NOT yet implemented)

The Go-idiomatic equivalents of the reference loader's `wasmImport` / `createSingleton` /
`InstancePool` are **not present**. When implemented they should mirror the cross-language SPEC
while reading naturally in Go — likely something like a `Load(ctx, path, opts) (*Module, error)`
singleton entry point and a pool type (`NewPool(...)` with `Acquire`/`Release`/`Run`) backed by
fresh wazero instances. Treat the exact shape as undecided until the first package lands; record
the decision here when it is made.

## Canonical ABI / SPEC conformance status

- **Cross-language `SPEC.md` is at v3.0.0 (2026-06-15)** — a BREAKING change. String/aggregate
  **returns** moved from the OLD caller-allocated out-parameter convention (host allocates an
  8-byte return area, passes its pointer as a trailing arg, reads `[ptr,len]` back) to the **NEW
  canonical callee-allocated convention**: the export returns an i32 pointer to a callee-allocated
  `[ptr,len]` pair, the host reads it, then calls a paired **`cabi_post_<name>(retPtr)`** export to
  release that memory.
- **This port's string-return handling: NONE EXISTS YET.** Because there is no source code, this
  repo implements **neither** the old out-param convention **nor** the new callee-allocated +
  `cabi_post_<name>` convention. There is nothing to migrate — when the loader is first written it
  should be built **directly against SPEC v3.0.0** (callee-allocated returns + `cabi_post_<name>`),
  skipping the deprecated out-param style entirely.

## Tests

- **Test command:** `go test ./...`
- **Status:** no tests exist (no source code). The command currently has nothing to run.

## Build / release flow

- **Build:** `go build ./...` (once `go.mod` and packages exist).
- **Release:** Go modules are published by tagging a semver version (`git tag vX.Y.Z` + push); the
  module then becomes installable via `go get` and indexed on **pkg.go.dev**. No registry upload
  step is required beyond the tag.
