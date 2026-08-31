# Dependencies live in the tracker, not in Forge config

Dependency metadata belongs in the issue tracker, not in `.forge.yaml`. Putting the dependency graph in repo config turns every ticket dependency into a config change — recreating an issue tracker with extra steps. The dependency graph is read from the tracker; `.forge.yaml` overrides (`dependencies.overrides`) exist only as an escape hatch with higher precedence.

## GitHub Dependency Source: native relationships, then the body block

The GitHub adapter reads dependencies from two tracker-local sources, in precedence order:

1. **Native GitHub "blocked by" relationships** — the canonical source when the repository/host exposes them. `GetIssue` reads `GET /repos/{owner}/{repo}/issues/{n}/dependencies/blocked_by` and maps each returned issue to a prerequisite edge. This lets maintainers manage dependencies through GitHub's own UI/API rather than hand-editing issue text.
2. **The canonical `## Dependencies` body block** — the fallback, used when native relationships are unavailable (the subresource answers `404` on hosts without the feature) or when an issue has none set. Parsed with strict syntax (`- #123`, or `Depends on: #123`); no freeform NLP — malformed text fails closed rather than being guessed at.

The native probe fails closed: a `404` degrades to the body block, but any other error propagates rather than silently dropping a dependency (which would let an Issue schedule as if it had no prerequisites). An issue with native relationships present ignores its body block entirely; an issue with an empty native set still falls back to the body block, so relationships need not be backfilled before body blocks stop mattering.

Config overrides (`dependencies.overrides` in `.forge.yaml`) still take precedence over whichever tracker source was used.

## Scope

This covers the **read** path used for scheduling (`forge execute`). The planning-compiler **write** path (`internal/materialize`) still authors `## Dependencies` body blocks and verifies its own round-trip; mirroring dependencies into native GitHub relationships on write is a separate, later concern (see the `DependencyStore` write capability, issue #298).
