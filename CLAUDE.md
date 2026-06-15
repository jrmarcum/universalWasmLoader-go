> **⚠️ PORTABLE PROJECT MEMORY NOW LIVES IN `cmem/`** — start at [`cmem/INDEX.md`](cmem/INDEX.md).
> When saving new project memory, write it into the matching `cmem/` topic file (and refresh its
> pointer in `cmem/INDEX.md`). The **"update the project memory"** and **"look for code issues"**
> triggers are defined in `cmem/INDEX.md` and are binding. This `CLAUDE.md` remains as the auto-loaded
> historical archive; `cmem/` is the source of truth.

# universalWasmLoader-go

Universal WebAssembly loader library written in Go.

## Project status

Early-stage / pre-implementation. Only a README stub and Go `.gitignore` exist; no source code has been committed yet.

## Repository layout

| Path | Purpose |
|------|---------|
| `README.md` | One-line project description |
| `.gitignore` | Standard Go ignore rules (binaries, test artifacts, `go.work`) |

## Toolchain

- Language: Go
- Build artifacts ignored: `*.exe`, `*.dll`, `*.so`, `*.dylib`, `*.test`, `*.out`, `coverage.*`
- Workspace files (`go.work`, `go.work.sum`) are gitignored — do not commit them

## Conventions

- No source code conventions established yet; update this section once the first packages are added.

## Project context

All project context, conventions, and decisions live in this file. No machine-local Claude memory is used for this project.
