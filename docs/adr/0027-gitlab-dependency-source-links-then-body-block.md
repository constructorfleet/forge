# The GitLab Dependency Source: typed issue links, then the body block

The GitLab Tracker reads Dependencies from two tracker-local sources, in precedence order. This ADR records that order, the direction rule that maps a GitLab link onto a Forge edge, and the tier probe that selects the source.

ADR 0003 records the same two-source decision for GitHub. This ADR is its GitLab parallel: the two adapters make the same choice for the same reason, and neither one invents a third source.

## The two sources

1. **Native typed issue links** — the canonical source when the instance and the project tier expose them. `GET /projects/:id/issues/:issue_iid/links` returns the related issues, each with a `link_type`. This lets a maintainer manage Dependencies in GitLab's own UI and API instead of editing issue text.
2. **The canonical `## Dependencies` body block** — the fallback. Forge uses it when typed links are unavailable, and when an issue has typed links but none of them names a prerequisite. The block uses the strict syntax `tracker.ParseDependencyBlock` accepts (`- #123`, or `Depends on: #123`). There is no freeform inference: malformed text fails closed rather than being guessed at.

GitLab stores the issue body in a field named `description`, not `body`. The block is the same text in both providers; only the field name differs.

## The direction rule

GitLab reports a `link_type` from the point of view of the issue in the request path. For issue A, an entry `{iid: B, link_type: "is_blocked_by"}` means "B blocks A", so B is a prerequisite of A. An entry with `link_type: "blocks"` means the reverse, "A blocks B". An entry with `link_type: "relates_to"` carries no order at all.

Forge models one relation, `tracker.DependencyBlocks`, and its edge reads `{Issue: A, DependsOn: B}` — B must complete before A can begin. Only an `is_blocked_by` entry produces such an edge. `blocks` and `relates_to` entries are read and ignored.

Getting this backwards would invert the whole DAG, so a test pins the direction against a link set that holds all three types at once.

## The tier probe

GitLab includes the `blocks` and `is_blocked_by` link types in its paid tiers. A project on a lower tier, and an older self-managed instance, answer `403` or `404` on the links endpoint. Either answer means "typed links are unavailable here", and the adapter falls back to the body block instead of failing.

The probe is sticky per client. Once an answer reports the endpoint is unavailable, the adapter stops calling it: the tier does not change inside one run, so a second call would only add a round trip per Issue. A `403` or `404` is the one class of failure the probe treats this way. Every other error propagates, because a silently dropped prerequisite would let an Issue schedule as if it had none.

The probe result is also what `Capabilities().NativeDependencyLinks` reports. `tracker.Capabilities` holds that field, not the GitLab package, because the question is provider-neutral: more than one tracker exposes typed links and gates them behind a host version or a paid tier, so a caller cannot answer it from the provider name alone. An adapter that never probes leaves the field false, which is the correct answer for a body-block-only adapter.

## Cross-project links are rejected, not dropped

A Forge Issue ID is a project-scoped GitLab `iid`. A typed link can name an issue in another project, and that issue's `iid` means nothing in this project.

Forge cannot use such a link. It also must not drop it: the Issue would then schedule as if the prerequisite did not exist. The adapter therefore returns an error that names the other project and issue. Cross-project and group-level links stay out of scope until Forge models a cross-project Issue reference.

## The write path syncs the active source

`WriteDependencies` writes the `## Dependencies` body block on every tier. This keeps a fallback source for instances that do not expose typed links.

When GitLab exposes typed links, `WriteDependencies` also syncs the native `is_blocked_by` links. It creates missing prerequisite links and deletes stale prerequisite links. It does not touch `blocks`, `relates_to`, or other unordered links.

This keeps the write path consistent with the native-first read path. Without this sync, a write can update the body block while a later read still returns stale native links.

Configured overrides (`dependencies.overrides` in `.forge.yaml`) still take precedence over whichever tracker source the read path used.

## Scope

This covers the Tracker capability only. GitLab merge requests (SCM) and GitLab pipelines (CI) are separate work, and `scm.type: gitlab` and `ci.type: gitlab` stay rejected at config load.
