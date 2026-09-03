# Language Server Catalog — TS / Pyright / rust-analyzer

RESEARCH ticket deliverable for generalizing Forge's gopls-only semantic-navigation
driver (`internal/semantic/gopls/`) to three more languages. Every capability and
response-shape row below was captured by running a **real LSP `initialize` +
`textDocument/hover` + `textDocument/documentSymbol` handshake** against each server
on this machine (see "Method / evidence"), not read from docs. Doc URLs are in Sources.

## Baseline: what Forge assumes today (gopls)

From `internal/semantic/gopls/driver.go`, `query.go`, `internal/lsp/detect.go`:

- **Invocation**: `ServerSpec{Command: []string{"gopls"}}` — bare binary, no args; gopls
  defaults to stdio LSP mode. (`detect.go:28`)
- **Initialize**: empty `ClientCapabilities{}`, a single workspace folder = `Dir`, and
  **no `initializationOptions`** (`driver.go:315` `handshake`). gopls needs nothing more.
- **Capability gating**: only `CallHierarchyProvider` and `TypeHierarchyProvider` are
  checked before use; if nil → `ErrCapabilityUnsupported` (`query.go:183`, `:225`).
  Definition/references/implementation/hover/documentSymbol/workspaceSymbol are called
  unconditionally.
- **Hover parse** (`query.go:436 splitHoverContents` → `:460 splitMarkdown`): takes the
  **first ```-fenced block** as the signature; everything else is documentation. This is
  gopls's convention.
- **SymbolKind** (`query.go:489 symbolKindName`): maps the standard LSP enum; unknown →
  `"unknown"`.

The rest of this doc is the **delta** each new server introduces against that baseline.

---

## Per-server fact table

| Dimension | **typescript-language-server** | **pyright** (pyright-langserver) | **rust-analyzer** | gopls (baseline) |
|---|---|---|---|---|
| **Canonical binary + argv** | `typescript-language-server --stdio` — `--stdio` flag is **required** (bare invocation errors: *"Connection input stream is not set… set '--node-ipc', '--stdio' or '--socket='"*). Do **not** spawn raw `tsserver`. | `pyright-langserver --stdio` — `--stdio` required. Not `pyright` (that's the CLI type-checker, no LSP). | `rust-analyzer` — **bare binary, no subcommand, no flag** = the LSP server over stdio. `--help` confirms: *"LSP server for the Rust programming language."* Subcommands (`parse`, `symbols`, …) are for other uses. | `gopls` (bare) |
| **Version tested** | 5.3.0 (+ typescript 5.9.3) | 1.1.413 | 1.97.1 (2026-07-14) | — |
| **Runtime dep** | Node.js **+ a separate `typescript` package** (see init reqs). | Node.js (even the PyPI `pyright` shells out to a bundled Node). | None (self-contained native binary). | Go toolchain (implicit). |
| **Install / detection** | npm pkg `typescript-language-server`, global (`npm i -g`) or project-local (`node_modules/.bin/`). Detect: bin on PATH **or** in workspace `node_modules/.bin`. **PATH presence is not sufficient — it also needs a resolvable `typescript` (see init).** | npm pkg `pyright` (provides both `pyright` and `pyright-langserver`) **or** PyPI `pyright` (wrapper). On this box `pip install pyright` was **blocked by PEP 668** (externally-managed env); `npm i -g pyright` worked. Detect: `pyright-langserver` on PATH. | `rustup component add rust-analyzer` → resolve via **`rustup which rust-analyzer`**; or a standalone binary on PATH (GitHub releases / distro pkg). **Detection gotcha:** a `rust-analyzer` on PATH is often a *rustup proxy* that errors *"Unknown binary 'rust-analyzer' in official toolchain"* when the component isn't installed — PATH presence ≠ working. Verify by actually running `--version`, or use `rustup which`. | `gopls` on PATH (`go install`). |
| **Init requirements / project discovery** | **Hard requirement:** must locate a `typescript` install or it **refuses `initialize`** with error -32603 *"Could not find a valid TypeScript installation… or a valid `tsserver.path`."* Resolution order: workspace `node_modules/typescript`, else `initializationOptions.tsserver.path` (absolute path to `.../typescript/lib/tsserver.js`). **Also version-sensitive:** typescript 7.x (native port) has no classic `tsserver.js` and is rejected — needs a 5.x tsserver. Project via tsconfig.json/jsconfig.json under `rootUri`. `initializationOptions` also carries `preferences`, `plugins`, `tsserver.logVerbosity`. | Optional. Runs in **single-file mode** with no config. Project root from `rootUri`/workspaceFolders. Python env resolved from `python.pythonPath` / `python.venvPath` / `venv` (delivered via `initializationOptions` `settings` or `workspace/didChangeConfiguration`), or `pyrightconfig.json` / `[tool.pyright]` in `pyproject.toml`. No env → assumes ambient interpreter (may mis-resolve third-party imports). | Optional `initializationOptions` = the rust-analyzer settings object (`{cargo, check, procMacro, …}`; `--print-config-schema` dumps the schema). **Project discovery is load-bearing for accuracy:** needs `Cargo.toml` (or a `rust-project.json` for non-Cargo builds) at/under the root. With a manifest it runs `cargo metadata` + builds proc-macros → **first queries block until the project loads** (hover returned `null` at ~2.5 s, populated by ~6 s in testing). No manifest → "detached file" mode, badly degraded. | **None** — empty init works. |
| **definition / references / hover / documentSymbol / workspaceSymbol** | All advertised: `definitionProvider`, `referencesProvider`, `hoverProvider`, `documentSymbolProvider`, `workspaceSymbolProvider` = true. | All advertised (each as `{workDoneProgress:true}` object form, still truthy). | All advertised: `definitionProvider`, `referencesProvider`, `hoverProvider`, `documentSymbolProvider`, `workspaceSymbolProvider` = true. | All advertised. |
| **implementation** | ✅ `implementationProvider: true` | ❌ **NOT advertised** — pyright has no `implementationProvider`. Forge's `FindImplementations` (which gopls calls unconditionally) would fail/return nothing. Also lacks it because Python has no interface/impl model; pyright offers `declarationProvider` + `typeDefinitionProvider` instead. | ✅ `implementationProvider: true` (used for trait ⇄ impl navigation). | ✅ |
| **callHierarchy** (prepare/incoming/outgoing) | ✅ `callHierarchyProvider: true` | ✅ `callHierarchyProvider: true` | ✅ `callHierarchyProvider: true` | ✅ |
| **typeHierarchy** (prepare/super/sub) | ❌ **NOT advertised** | ❌ **NOT advertised** | ❌ **NOT advertised** | ✅ (gopls advertises it) |
| **Hover response shape** | markdown, single fence = signature. Value: `` \n```typescript\nfunction add(a: number, b: number): number\n```\nAdds two numbers together. `` → **first fence IS the signature; matches gopls.** `splitMarkdown` works as-is (lang tag `typescript`; note leading `\n`). | markdown, `` ```python\n(function) def add(\n    a: int,\n    b: int\n) -> int\n```\n---\nAdds two numbers together. `` → first fence is the signature **but** (a) prefixed with a `(function)`/`(variable)`/`(class)` **kind annotation**, (b) the signature is often **multi-line inside the fence**, (c) a `---` HR separates sig from docs and would bleed into `documentation`. First-fence heuristic mostly works; expect the annotation prefix + stray `---`. | markdown, **MULTIPLE fences**: `` \n```rust\nprobe\n```\n\n```rust\nfn add(a: i32, b: i32) -> i32\n```\n\n---\n\nAdds two numbers together. `` → **the FIRST fence is the crate/module path (`probe`), the SECOND fence is the real signature.** gopls's `splitMarkdown` takes the *first* fence → would return the module path as the signature and demote the real signature into docs. **Breaks — needs rust-specific handling (take the last/type fence, or skip the leading path block).** | single fence = signature; `splitMarkdown` designed for this. |
| **documentSymbol shape** | Hierarchical `DocumentSymbol[]` (children present), `detail: ""` (empty — no signature in detail). Kinds standard: fn=12, and **`const y` → kind 14 (Constant)**. | Hierarchical `DocumentSymbol[]`. **Quirk: emits function *parameters* as child symbols** (`a`,`b` as kind 13 Variable under the function) — extra nodes the flattening walk in `documentSymbolResultSymbols` will surface. Module-level `y` → kind 13 (Variable). No `detail`. | Hierarchical `DocumentSymbol[]` with **`detail` = the signature** (e.g. `"fn(a: i32, b: i32) -> i32"`, `"fn()"`), fn=12. Extra `tags: []` and `deprecated: false` fields (harmless; ignored by Forge's mapper). | Hierarchical or flat; standard kinds. |
| **Notable non-standard fields** | Rich `executeCommandProvider` (`_typescript.*` commands), semanticTokens legend uses TS-specific `member` type. None affect Forge's normalized ops. | Every provider is the object form `{workDoneProgress:true}` rather than bare `true` — still truthy, but code that does `== true` (not `!= nil`) would misjudge. Forge checks `!= nil`, so OK. | Large `experimental` block (`parentModule`, `openCargoToml`, `runnables`, `ssr`, `hoverRange`, …) — rust-analyzer LSP extensions, safe to ignore. `positionEncoding: "utf-16"` explicitly stated. | — |

---

## Method / evidence

All servers installed and driven locally; capabilities/hover/documentSymbol are verbatim
from live handshakes (probe scripts in `$CLAUDE_JOB_DIR/tmp/`):

- **rust-analyzer 1.97.1** via `rustup component add rust-analyzer`; probed against a minimal
  Cargo project.
- **typescript-language-server 5.3.0** + **typescript 5.9.3** via local `npm install`; probed
  against a tsconfig project with `initializationOptions.tsserver.path` set.
- **pyright 1.1.413** via `npm i -g pyright`; probed against a single `.py` file.

The `initialize` requests advertised client support for callHierarchy + typeHierarchy +
hierarchical documentSymbol + markdown hover, so a missing provider in the response is a real
server-side gap, not a client-capability artifact.

## Sources (docs corroborating the live results)

- LSP 3.17 spec (capability names, hover/documentSymbol shapes): https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/
- typescript-language-server README (`--stdio`, `initializationOptions`, `tsserver.path`, plugins): https://github.com/typescript-language-server/typescript-language-server
- Pyright command-line & language-server docs: https://microsoft.github.io/pyright/#/command-line and https://microsoft.github.io/pyright/#/settings
- Pyright import/environment resolution (pythonPath/venv, pyrightconfig): https://microsoft.github.io/pyright/#/import-resolution
- rust-analyzer manual — LSP server, project (`Cargo.toml` / `rust-project.json`) discovery, config: https://rust-analyzer.github.io/book/ (Installation / Non-Cargo Projects / Configuration)
- rust-analyzer `--help` output (this machine): bare invocation = LSP server; `--print-config-schema` for init options.

## Confidence / open questions

- **HIGH** — invocation argv, install/detection, capability advertisement (definition,
  references, implementation, hover, documentSymbol, workspaceSymbol, callHierarchy,
  typeHierarchy), and hover/documentSymbol shapes: all directly observed from live handshakes
  on pinned versions.
- **typeHierarchy = none of the three**: HIGH for these versions. rust-analyzer has an
  *experimental* `typeHierarchy` request historically but it is **not advertised** in standard
  `ServerCapabilities` (not in the captured init) → Forge's `TypeHierarchyProvider != nil` gate
  correctly yields `ErrCapabilityUnsupported`. So gopls remains the only one of the four with
  typeHierarchy.
- **pyright parameters-as-child-symbols** and **rust-analyzer two-fence hover**: observed on
  simple samples; MEDIUM that the exact shape is stable across all symbol categories (generics,
  impl blocks, overloads) — worth a broader fixture sweep before finalizing the parser.
- **pyright env resolution**: whether `initializationOptions.settings.python.pythonPath` vs a
  post-init `workspace/didChangeConfiguration` is the more reliable channel was not exercised
  (single-file mode used). MEDIUM — verify against a venv project.
- **typescript version floor**: confirmed 7.x native port is rejected and 5.x works; the exact
  minimum tsserver version typescript-language-server 5.3 accepts was not bisected. LOW impact
  (pin a 5.x typescript).
