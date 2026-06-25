# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-06-24

First public release.

### Added

- WIT-aware WebAssembly loader built on pure-Go [wazero](https://github.com/tetratelabs/wazero)
  (no CGO) — `go get` with trivial cross-compilation.
- `Load` (synchronous, file) and `Import` (file or `http(s)` URL) entry points.
- `Module.Call` / `Module.Bind` — call exports by camelCase name with Canonical-ABI
  marshalling: numbers direct, strings encoded/decoded through linear memory (including
  callee-allocated string returns + `cabi_post_<name>`), booleans normalized.
- Host import callbacks via `NewCallbacks().On(camelName, fn)`, dispatched by reflection.
- `@N` version pinning against a module's exported `version` global (C SONAME convention).
- `CreateSingleton` — load-once, identity-stable `*Module`.
- `NewInstancePool` — bounded pool of independent runtimes for servers/concurrency
  (`Acquire` / `Release` / `Run` / `Close`).
- WASI Preview 1 support (SPEC §10) via wazero's built-in `wasi_snapshot_preview1`;
  pure-compute modules need no configuration.
- Conforms to the cross-language SPEC v3.0.0.

[v0.1.0]: https://github.com/jrmarcum/universalWasmLoader-go/releases/tag/v0.1.0
