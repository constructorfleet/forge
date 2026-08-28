# 29 — `forge init`

**What to build:** Deterministic repository-policy discovery that generates a `.forge.yaml`. Inspect the repository for base branch, package/build system, test/lint/format/typecheck/build commands from known config formats (package.json, Makefile, Taskfile, justfile, pyproject.toml, Cargo.toml, go.mod) and CI workflow files. Priority: explicit known config → CI workflow inspection → conventional defaults → leave unresolved fields clearly marked. No LLM involvement.

**Blocked by:** 12 — Configuration loading and validation

**Status:** ready-for-agent

- [ ] Detects base branch from Git config
- [ ] Detects package manager from lockfiles and manifests
- [ ] Detects test, lint, format-check, typecheck, and build commands
- [ ] Reads CI workflow files for command hints
- [ ] Reads AGENTS.md / CLAUDE.md for agent instruction presence
- [ ] Detects tracker type from Git remote
- [ ] Priority: explicit config formats > CI workflows > conventional defaults
- [ ] Unresolved fields left with clear markers (not silently defaulted to wrong values)
- [ ] Generated `.forge.yaml` is valid and loadable
- [ ] Does not modify issue bodies, create labels, or configure branch protection
- [ ] No LLM invocation
- [ ] Tests cover Go, Node/pnpm, Python, and Rust project detection
