# Linear: a tracker-only provider with native-relation Dependencies

The Linear Tracker implements the `Tracker` and `DependencyStore` capabilities only. It supplies no `SCM` or `CI` capability. This ADR records why that split is right for Linear, how Linear issue identity works, and why its Dependency Source has no body-block fallback.

## Linear supplies no SCM or CI

Linear tracks issues. It does not host branches, change requests, or CI. A team that tracks work in Linear hosts code and CI on GitHub or GitLab. Forge's tracker/scm/ci capability split (see CONTEXT.md and the split that introduced `tracker.SCM`/`tracker.CI` as interfaces distinct from `tracker.Tracker`) exists to let a composition name a different provider for each capability.

The Linear adapter proves that split for its most important shape: a provider that implements `Tracker` and nothing else. `scm.type: linear` and `ci.type: linear` are rejected at config load, the same way `scm.type: gitlab` and `ci.type: gitlab` are (ADR 0027's "Scope" section makes the same point for GitLab).

## Issue identity: the team-prefixed identifier, resolved to an internal id

Linear gives every issue two identifiers: a team-prefixed, human-readable key (`FOR-345`) and an internal UUID. Forge uses the team-prefixed key as the Issue ID everywhere — branch names, logs, the CLI, and `domain.IssueRef.ID`. It is stable, short, and safe in a branch name or a file path, matching why the GitLab adapter uses a project-scoped `iid` rather than GitLab's global issue id (see `internal/tracker/gitlab/issues.go`).

The adapter resolves that identifier to Linear's internal UUID once per call, through a filtered `issues` query, and uses the UUID only for the GraphQL calls that key on it. The UUID never surfaces on a `domain.Issue`, a `tracker.DependencyEdge`, or any other value this package returns.

## Dependencies: native relations only, no body-block fallback

GitHub and GitLab both fall back to the canonical `## Dependencies` body block (ADR 0003) when their native typed-link feature is unavailable — a paid tier, an older host version, or (for GitHub) simply not yet probed. Linear has no such gate: native issue relations are an unconditional part of its data model, available on every plan. There is therefore no fallback to probe for, and no body-block encoding for Linear at all. `Capabilities().NativeDependencyLinks` is always `true`.

Forge maps only Linear's `blocks`-type relation to a `tracker.DependencyBlocks` edge. On the target issue's `inverseRelations`, an entry of type `blocks` names the issue that blocks it — that issue is the prerequisite. Every other relation type (`duplicate`, `related`, ...) is read and ignored, the same "read but ignore the rest" rule ADR 0027 applies to GitLab's `blocks`/`relates_to` entries.

`WriteDependencies` diffs the desired prerequisite set against the issue's current `blocks`-type inverse relations: it creates an `issueRelationCreate` for each missing entry and an `issueRelationDelete` for each stale one, so the native relation set ends up matching exactly. `UpdateIssue`'s body write therefore never touches Dependencies on Linear — unlike GitHub and GitLab, where `UpdateIssue` and `WriteDependencies` both rewrite the same body block, Linear's two writes are fully independent.

Configured overrides (`dependencies.overrides` in `.forge.yaml`) still take precedence over the native relations the read path resolves, matching every other adapter.

## Status reflection is Forge's job, not a code-host side effect

Forge reflects Issue status onto Linear through the same `statusreflect` package every tracker uses — a label swap and, optionally, a start comment — driven by `AddLabel`/`RemoveLabel`/`AddComment` on the `Tracker` capability (see `internal/statusreflect`). No Linear-specific status code exists: because Linear implements the same narrow `Tracker` subset `statusreflect.Tracker` already depends on, it gets this behavior automatically.

This matters because a GitHub pull request merging does not, and must not, close or update the Linear issue: GitHub has no reason to know a Linear issue exists. Forge is the one system that holds both halves of the composition, so Forge — through the Tracker capability, on its own schedule — is what reflects status onto Linear. Moving a Linear issue through its own configured workflow states (e.g. to a team's "Done") is deferred; MVP reflects status through comments and labels only.
