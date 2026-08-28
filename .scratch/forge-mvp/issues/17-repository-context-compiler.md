# 17 — Repository Context compiler

**What to build:** Compile one shared Repository Context per Execution from `.forge.yaml`, AGENTS.md, CLAUDE.md, project manifests, and configured quality gates. Repository Context is immutable for a given Execution. Workers consume the compiled context — they never independently rediscover quality commands or project structure.

**Blocked by:** 12 — Configuration loading and validation

**Status:** resolved

- [x] Repository Context includes: base revision, detected languages, package managers, quality gate commands, normalized agent instructions
- [x] Compiled once per Execution
- [x] Reads and normalizes AGENTS.md and CLAUDE.md when present
- [x] Missing instruction files are handled silently (no error)
- [x] Workers receive pre-compiled context, not raw file paths
- [x] Tests verify worker invocation does not independently rediscover quality commands
