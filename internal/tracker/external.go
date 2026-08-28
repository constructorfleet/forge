package tracker

import "context"

// ExternalState is the satisfaction state of an External Issue (CONTEXT.md
// "External Issue" — an Issue referenced as a Dependency but not included
// in the Execution set, loaded into the DAG as an observed node and tracked
// for satisfaction but never executed).
//
// This is deliberately a separate type from domain.IssueState: an External
// Issue never runs through Forge's Worker pipeline, so it has no
// PENDING/READY/CLAIMED/... progression — only whether Forge has
// determined its merged code is reachable from the applicable base yet.
// Conflating it with domain.IssueState would let External Issues creep
// into transitions and storage paths meant only for Managed Issues (see
// domain.Issue.IsManaged/IsExternal and ADR 0008).
type ExternalState string

const (
	// ExternalPending is an External Issue's default state: satisfaction
	// has not yet been established, either because it has not been checked
	// or because the last check found no merged-and-reachable PR yet (the
	// Issue may still be open, or its PR may be merged but not yet
	// reachable from the applicable base). A dependent stays
	// BLOCKED_DEPENDENCY while its prerequisite is EXTERNAL_PENDING.
	ExternalPending ExternalState = "EXTERNAL_PENDING"

	// ExternalSatisfied means the External Issue's associated PR is merged
	// and that merge commit is reachable from the applicable base — the
	// same bar CONTEXT.md's "Dependency" sets for a Managed prerequisite.
	ExternalSatisfied ExternalState = "EXTERNAL_SATISFIED"

	// ExternalInvalid means the External Issue is closed without ever
	// having a merged PR reachable from the applicable base. Closed does
	// not equal satisfied — issues get closed for reasons other than
	// successful implementation (ADR 0008) — so this is a distinct,
	// never-satisfied state rather than being folded into
	// ExternalPending, which a dependent could otherwise wait on forever.
	ExternalInvalid ExternalState = "EXTERNAL_INVALID"
)

// ExternalChecker determines an External Issue's current ExternalState:
// whether it has an associated merged PR and, if so, whether that PR's
// merge commit is reachable from the applicable base branch. Injected so
// scheduler-facing code (cmd/forge's DependencyResolver) stays
// backend-agnostic and testable with a fake; internal/tracker/github
// implements it against GitHub plus a local git checkout.
//
// Every call re-derives the state from current tracker/git data — nothing
// about it is cached by the checker itself — so repeated calls (the
// scheduler's poll loop, or a later `forge resume`/`forge execute`
// re-invocation) always re-evaluate against whatever has since landed on
// the applicable base, rather than trusting a stale prior answer.
type ExternalChecker interface {
	CheckExternal(ctx context.Context, issueID, baseBranch string) (ExternalState, error)
}
