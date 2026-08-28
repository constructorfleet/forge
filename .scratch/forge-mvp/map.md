Status: resolved
Type: wayfinder:map

# Forge MVP

## Destination

A working Forge MVP: `forge execute 345 344 343` autonomously produces Issue → Workspace → Agent → Quality Gates → Review → commit → PR → CI green, with Dependency resolution, concurrent Workers, restart recovery, and operational CLI. The full 22-ticket scope from IDEATION.md, minus the Codex adapter (deferred post-MVP).

## Notes

- Domain glossary: `CONTEXT.md`
- Architecture decisions: `docs/adr/` (9 ADRs)
- Ideation source: `IDEATION.md`
- Skills: always consult `/domain-modeling` when terms are in play
- Implementation language: Go. Storage: SQLite. See ADRs 0001, 0002.
- The orchestrator never asks the LLM questions that deterministic code can answer reliably.

## Decisions so far

- [Dependency metadata source](issues/01-dependency-metadata-source.md) — Dependencies live in issue body (`## Dependencies` block), not `.forge.yaml`; config overrides as escape hatch. See ADR 0003.
- [Review invocation model](issues/02-review-invocation-model.md) — Fresh second Agent invocation, same Workspace, no implementation conversation history. See ADR 0004.
- [Codex adapter timing](issues/03-codex-adapter-timing.md) — Deferred to post-MVP; Claude Code is the only MVP backend.
- [forge init scope](issues/04-forge-init-scope.md) — MVP, sharply scoped to deterministic repo-policy discovery and `.forge.yaml` generation. No LLM involvement.
- [CI required checks source](issues/05-ci-required-checks-source.md) — GitHub branch protection/rulesets are authoritative; `explicit` mode as override. See Merge Requirements in `CONTEXT.md`.
- [Dependency satisfaction semantics](issues/06-dependency-satisfaction-semantics.md) — Satisfied = prerequisite PR merged into applicable base. Not locally complete, not CI green. See ADR 0005.
- [Needs-info resume flow](issues/07-needs-info-resume-flow.md) — Manual `forge resume` for MVP; re-fetches issue/comments and detects new human input. Daemon/webhooks deferred.
- [External dependencies](issues/08-external-dependencies.md) — Loaded as observed nodes in the DAG; tracked but never executed. Closed ≠ satisfied. See ADR 0008.
- [Review retry budget](issues/09-review-retry-budget.md) — Separate budgets for gates, review, and CI. Full gate rerun after every repair. See ADR 0007.
- [Concurrent execution isolation](issues/10-concurrent-execution-isolation.md) — Multiple Executions allowed with resource-scoped locks. Worker base captured at READY, not Execution start. See ADRs 0006, 0009.

## Not yet specified

(none — the way to the destination is clear)

## Out of scope

- Codex adapter (post-MVP, see decision above)
- Stacked dependency branches (explicitly deferred in IDEATION.md §18)
- GitLab / Linear / Jira adapters
- Container isolation / remote Workers
- Graphical UI
- Automatic merging
- Wayfinder / to-spec / to-tickets automation
- Daemon mode / webhook-driven needs-info resume (post-MVP)
