# Claude native-LSP plugin recipes: TypeScript, Python, Rust

**Ticket:** Multi-language LSP — can Claude Code's native-LSP plugin mechanism drive
`typescript-language-server`, `pyright` (`pyright-langserver`), and `rust-analyzer`?
**Extends:** Forge issue #90 (Go-only finding: session-only plugin via `--plugin-dir`,
`.claude-plugin/plugin.json` + `.lsp.json`, native `LSP` tool auto-enables once the
server connects).
**Date:** 2026-08-30. **Docs baseline:** code.claude.com/docs (Claude Code current docs).

## TL;DR verdict

| Language | Server binary | Verdict | Notes |
| :--- | :--- | :--- | :--- |
| TypeScript/JS | `typescript-language-server` | **NATIVE-DRIVABLE** | Anthropic ships an official `typescript-lsp` plugin; 8-extension map |
| Python | `pyright-langserver` | **NATIVE-DRIVABLE** | Official `pyright-lsp` plugin; `.py`/`.pyi` |
| Rust | `rust-analyzer` | **NATIVE-DRIVABLE** | Official `rust-analyzer-lsp` plugin; `.rs` |

All three are the **same mechanism as gopls**. Anthropic itself publishes an official
code-intelligence plugin for each in the `claude-plugins-official` marketplace, and each
one is just an inline `lspServers` block with `command` + `extensionToLanguage` (+ `args`).
**No MCP fallback is needed for any of the three.** The one caveat is Claude-side, not
per-language: op availability "may vary by language and environment" (docs, verbatim), and
cloud/web sessions never start plugin servers at all.

---

## 1. The exact `.lsp.json` entries (ground truth)

These are copied verbatim from the inline `lspServers` blocks in Anthropic's official
marketplace manifest (`anthropics/claude-plugins-official`, `.claude-plugin/marketplace.json`,
`main`, fetched 2026-08-30). This is the authoritative shape Claude Code itself ships.

### TypeScript / JavaScript
```json
{
  "typescript": {
    "command": "typescript-language-server",
    "args": ["--stdio"],
    "extensionToLanguage": {
      ".ts": "typescript",
      ".tsx": "typescriptreact",
      ".js": "javascript",
      ".jsx": "javascriptreact",
      ".mts": "typescript",
      ".cts": "typescript",
      ".mjs": "javascript",
      ".cjs": "javascript"
    }
  }
}
```
**Note the language IDs are not uniform:** `.tsx` → `typescriptreact`, `.jsx` →
`javascriptreact` (LSP language identifiers, not the plain "typescript"/"javascript" name).
A design that derives the language id by lowercasing a single "language" field will emit the
wrong id for `.tsx`/`.jsx`.

### Python (Pyright)
```json
{
  "pyright": {
    "command": "pyright-langserver",
    "args": ["--stdio"],
    "extensionToLanguage": {
      ".py": "python",
      ".pyi": "python"
    }
  }
}
```

### Rust (rust-analyzer)
```json
{
  "rust-analyzer": {
    "command": "rust-analyzer",
    "extensionToLanguage": {
      ".rs": "rust"
    }
  }
}
```

### Go (gopls) — baseline, for comparison
```json
{
  "gopls": {
    "command": "gopls",
    "extensionToLanguage": { ".go": "go" }
  }
}
```
(Anthropic's official gopls entry passes **no** `args`. The plugins-reference *example* JSON
shows `"args": ["serve"]`, but the shipped official plugin omits it — the two Anthropic
sources disagree on gopls args. Not load-bearing for this ticket; Forge drives gopls from its
own registry.)

### `.lsp.json` schema (from plugins-reference)

An `.lsp.json` (or inline `plugin.json` `lspServers`) is a **map keyed by an
arbitrary server name** → config object.

- **Required:** `command` (the binary to execute, **"must be in PATH"**),
  `extensionToLanguage` (map of file-extension → language identifier; **multi-key**).
- **Optional:** `args` (string array), `transport` (`stdio` default / `socket`), `env`
  (env vars map), `initializationOptions`, `settings` (sent via
  `workspace/didChangeConfiguration`), `workspaceFolder`, `startupTimeout`,
  `shutdownTimeout`, `restartOnCrash` (default `true`), `maxRestarts`, `diagnostics`
  (default `true`).
- **Skip rule:** a server "missing `command` or `extensionToLanguage`" is skipped and the
  rest still start (`claude --debug` shows why).
- **Extension collision:** if two enabled servers declare the same extension, the
  first registered wins and the others never start.

---

## 2. Auto-enable + op-set per language

**Auto-enable — YES, language-agnostic.** "Claude Code keeps the tool inactive until you
install a code intelligence plugin for your language." Once a code-intelligence plugin is
installed *and its server binary is on PATH*, the built-in `LSP` tool activates. This is the
generic mechanism — nothing gopls-specific — so it holds for ts/py/rust identically. (Forge's
`--plugin-dir` injection is exactly this "install a plugin" path.)

**Op-set — a FIXED Claude-side surface, but availability may vary by server (docs say so
explicitly).** The `LSP` tool advertises these navigation capabilities (tools-reference,
"LSP tool behavior"): jump to definition, find references, type info at a position, list
symbols in a file, workspace symbol search, find implementations, trace call hierarchies —
plus automatic post-edit diagnostics. That maps to the 9 concrete ops observed for gopls
(goToDefinition, goToImplementation, findReferences, workspaceSymbol, documentSymbol, hover,
prepareCallHierarchy, incomingCalls, outgoingCalls). **No `typeHierarchy`** in the docs —
consistent with the #90 finding.

Crucially, discover-plugins states verbatim: *"These operations give Claude more precise
navigation than grep-based search, though **availability may vary by language and
environment**."* So the docs **do not guarantee** the exact 9-op set for
typescript-language-server / pyright / rust-analyzer — Claude exposes a fixed tool, but which
ops actually resolve depends on each server's advertised LSP capabilities. In practice all
three servers implement definition/references/hover/documentSymbol/workspaceSymbol/
implementation/callHierarchy, so the 9-op set is *expected* to hold — but treat exact
per-server parity as an **open question** (see §5); it should be verified empirically, not
asserted from docs.

---

## 3. Per-server config Claude's loader must pass

**None of the three official plugins pass `initializationOptions`, `env`, `settings`, or
`workspaceFolder`.** They rely on each server's own default discovery:

- **rust-analyzer:** no config. Discovers the Cargo workspace from the project root/cwd
  automatically. The official plugin passes only `command` + `extensionToLanguage`. (High
  memory on large projects is a documented caveat, not a config requirement.)
- **pyright:** no config. `pyright-langserver --stdio`; no `pythonPath`/venv is set by the
  official plugin — Pyright uses its own environment discovery. If a project needs a specific
  interpreter, that would be `initializationOptions`/`settings` on the entry, but Claude's
  shipped plugin does **not** set it, and the docs give no guidance for it.
- **typescript-language-server:** `--stdio`; no extra config. (Requires `typescript` to be
  installed alongside it — `npm install -g typescript-language-server typescript` — but
  that's a binary-install concern, not an `.lsp.json` field.)

The only non-default flag any of them use is `args: ["--stdio"]` for pyright and typescript
(rust-analyzer and gopls default to stdio with no flag).

**Binary-on-PATH is the one hard runtime requirement** for all of them — Claude Code returns
an error per LSP call on a file whose server it can't start, and the plugin never installs
the binary.

---

## 4. Implications for Forge (`buildLSPServerConfig` / `languageFileExtensions`)

Forge's current emitter (`internal/agent/claude/lspplugin.go`) is **structurally wrong for
these three** in three concrete ways, even though it "works" for gopls by luck of gopls
needing no args and one extension:

1. **`command` must be the bare binary; args go in `args`.** Today
   `buildLSPServerConfig` does `strings.Join(server.Command, " ")` into the `command`
   string. For pyright/typescript that produces `command: "pyright-langserver --stdio"`,
   which **is not a PATH-resolvable executable** — Claude's schema says `command` "must be
   in PATH". Forge must emit `command = server.Command[0]` and `args = server.Command[1:]`.
   (`lspServerEntry` needs an `Args []string` field with `json:"args,omitempty"`.)

2. **`extensionToLanguage` must be multi-key, and the value is an LSP language ID, not a
   lowercased language name.** Today `languageFileExtensions` is `map[string]string`
   (one ext per language) and the emitter builds
   `{ext: strings.ToLower(server.Language)}`. That cannot express `.ts`+`.tsx`+`.mts`+... and
   would emit `.tsx → "typescript"` where Claude ships `.tsx → "typescriptreact"`. The
   registry needs to carry, per server, a **map of extension → language-id** (the full 8-way
   TS map, the 2-way Python map, the 1-way Rust map), copied from the official plugin values
   in §1 — not derived by lowercasing.

3. **The silent-skip-on-unknown-extension branch stops being a no-op.** Once the registry
   has ts/py/rust servers with populated extension maps, they'll actually emit — good — but
   the skip logic (`if !ok { continue }`) should key off "this server carries an extension
   map," not off a global single-ext lookup table.

Minor/cosmetic: Forge keys `.lsp.json` by `filepath.Base(server.Command[0])`, giving keys
`pyright-langserver` / `typescript-language-server` / `rust-analyzer` / `gopls`, whereas
Anthropic uses `pyright` / `typescript` / `rust-analyzer` / `gopls`. The key is just a
registration label (collisions are resolved by extension, not by key), so this is
functionally fine but diverges from the official naming.

**No MCP fallback needed for ts/py/rust.** All three are first-class native
code-intelligence languages that Anthropic itself ships plugins for; the forge-managed MCP
channel is only warranted for languages *not* covered by the native LSP tool (e.g. a server
whose language Claude's tool doesn't recognize, or environments where native LSP is
unavailable — see below).

**Environment caveat (applies to all languages, gopls included):** in **cloud / web
sessions** (claude-code-on-the-web) Claude Code "doesn't start plugin language servers, so
the LSP tool stays inactive there." Forge's native-LSP path is a **local-CLI-only**
capability; the MCP fallback (or plain grep) is the only semantic-tooling option in cloud
sessions.

---

## 5. Confidence / open questions

**High confidence (primary source, Anthropic-authored):**
- The exact `.lsp.json` for all three servers — copied verbatim from Anthropic's official
  marketplace manifest. Not inferred.
- Schema fields (`command`/`args`/`extensionToLanguage` + optionals), PATH requirement,
  skip-on-invalid, extension-collision rule, multi-extension support — all stated in the
  plugins-reference.
- Auto-enable is language-agnostic (activates on any installed code-intelligence plugin whose
  binary is present).
- All three are officially supported native languages (discover-plugins language table + the
  shipped plugin dirs `typescript-lsp` / `pyright-lsp` / `rust-analyzer-lsp`).

**Open questions / not confirmable from docs:**
1. **Exact op-set per server.** The 9 ops are the gopls-observed set. Claude's docs list a
   fixed capability surface but explicitly warn availability "may vary by language and
   environment," and do **not** enumerate per-server op lists. Whether
   typescript-language-server / pyright / rust-analyzer each expose the full 9 is *expected*
   (all three implement the underlying LSP methods) but **must be verified empirically** —
   don't assert parity from docs. Recommend a smoke test per server (open a file, call each
   of the 9 ops, record which resolve).
2. **`typeHierarchy` absence** is consistent with #90 but is inferred from the op list Claude
   exposes, not from an explicit "typeHierarchy is unsupported" statement.
3. **Pyright interpreter/venv selection.** The official plugin sets no `pythonPath`. Whether
   Pyright picks up a project venv automatically in Claude's spawned-subprocess context, or
   needs `initializationOptions.python.pythonPath` / a `workspaceFolder`, is not documented.
   If Forge targets projects with non-default interpreters, this needs its own test.
4. **rust-analyzer workspace root.** Assumed to discover `Cargo.toml` from cwd; the official
   plugin sets no `workspaceFolder`. In a multi-crate or non-root-manifest layout this may
   need `workspaceFolder`. Not documented; verify if targeting workspaces.
5. **gopls `args` discrepancy** between the plugins-reference example (`["serve"]`) and the
   shipped official plugin (none) — cosmetic here, flagged for completeness.

---

## Sources

- Claude Code — Plugins reference (LSP servers section, `.lsp.json` schema, field tables,
  skip/collision rules): https://code.claude.com/docs/en/plugins-reference
- Claude Code — Tools reference ("LSP tool behavior", capability list, inactive-until-plugin,
  cloud-session note): https://code.claude.com/docs/en/tools-reference
- Claude Code — Discover and install plugins ("Code intelligence" section: language→plugin→
  binary table incl. `pyright-langserver`/`rust-analyzer`/`typescript-language-server`,
  "availability may vary by language and environment"): https://code.claude.com/docs/en/discover-plugins
- Anthropic official marketplace — inline `lspServers` configs (authoritative `.lsp.json`
  shapes for gopls/pyright/rust-analyzer/typescript):
  `anthropics/claude-plugins-official` → `.claude-plugin/marketplace.json` (branch `main`),
  plugin dirs `plugins/{typescript-lsp,pyright-lsp,rust-analyzer-lsp,gopls-lsp}`:
  https://github.com/anthropics/claude-plugins-official
- Forge current emitter under generalization:
  `/Users/tglenn/src/forge/internal/agent/claude/lspplugin.go`
