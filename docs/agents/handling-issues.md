# Handling issues with `forge execute`

This repository builds **Forge** — a deterministic orchestrator for software-engineering
agents — and dogfoods it. To implement a ready issue, drive it through Forge rather than
editing the code by hand.

## Command

```
forge execute <issue-number> [<issue-number> ...]
```

Forge resolves the Dependency DAG across the requested Issues and drives each through its
state machine — implement (TDD) → quality gates → commit → pull request — running
independent Issues up to `execution.max_parallel` and holding dependency-blocked Issues
until their prerequisites are satisfied. It prints each Issue's final state and exits
non-zero if any errored. The PR it opens closes the Issue (`Closes #<n>`).

Build the binary first: `go install ./cmd/forge` (installs to `$(go env GOPATH)/bin`), or
`go build -o forge ./cmd/forge`.

## Prerequisites

- **Quality-gate binaries on `PATH`.** The gates declared in `.forge.yaml` (`go test`,
  `golangci-lint`, `gofmt`, `go vet`, `go build`) run in Forge's own environment. If a gate
  binary isn't on `PATH` — most commonly `golangci-lint`, which lives at
  `$(go env GOPATH)/bin` — that gate exits `127` ("command not found") and the Issue fails
  in `VALIDATING`. Ensure `$(go env GOPATH)/bin` is on `PATH` before running.
- **`GITHUB_TOKEN`** for PR creation: `GITHUB_TOKEN=$(gh auth token) forge execute …`.
  Without it, PR creation fails with a 401 mid-run.

## Dependencies and merge order

Forge gates an Issue on the `## Dependencies` block in its body (the canonical Dependency
Source — see `CONTEXT.md`). A dependency is **satisfied only when the prerequisite Issue's
PR is merged and reachable from the base branch**, associated via a PR that closed it with a
**closing keyword** (`Closes #<n>`, `Fixes #<n>`, `Resolves #<n>`).

Consequences:

- **Merge each prerequisite before running its dependents.** Running a dependent whose
  prerequisite is not yet merged fails fast with
  `scheduler: issue X has an unsatisfiable dependency on Y`.
- **A hand-authored PR must use a closing keyword** (`Closes #<n>`). `Refs #<n>` or
  `Implements #<n>` create no closing association, so the dependent will not see the
  prerequisite as satisfied. Forge's own auto-created PRs already use `Closes #<n>`.

## Diagnosing a failed run

`forge execute` prints only each Issue's final state; the detail lives in the state DB
(`.forge/forge.db`, or the `-db` path you passed):

- `agent_runs` — each agent attempt's `result` and `summary`.
- `events` — state transitions plus `gate.run` / `gate.failed` rows (which gate, exit code).
- `gate_runs` — per-gate command, exit code, pass/fail.

The agent's work-in-progress lives in its worktree under
`.forge/worktrees/<execution>/<issue>` — inspectable, and re-runnable after the underlying
cause is fixed.
