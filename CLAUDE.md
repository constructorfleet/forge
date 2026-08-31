## Agent skills

### Issue tracker

Issues are tracked as GitHub issues on `constructorfleet/forge` (use the `gh` CLI).
Dependencies between issues go in a `## Dependencies` block in the issue body (the
canonical Dependency Source — see `CONTEXT.md`). The `.scratch/` directory is for
in-progress specs and wayfinding maps only, not the issue backlog.

### Handling issues

Implement a ready issue by driving it through Forge itself:
`forge execute <issue-number>`. Forge is the orchestrator this repo builds — dogfood it to
handle issues (implement → gates → PR) rather than editing by hand. Merge each prerequisite
before its dependents, and ensure the quality-gate binaries (notably `golangci-lint`) are on
`PATH`. See `docs/agents/handling-issues.md`.

### Triage labels

Default five-role vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout — one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
