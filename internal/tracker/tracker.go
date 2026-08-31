package tracker

import (
	"context"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// Comment is Forge's normalized representation of a tracker comment. It
// carries no tracker-specific fields.
type Comment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

// MergeRequirements is the normalized set of conditions the target branch
// requires before a PR can merge, queried from the Tracker rather than
// configured in Forge (see CONTEXT.md "Merge Requirements").
type MergeRequirements struct {
	// RequiredChecks lists the names of checks that must pass. Optional
	// checks are not included.
	RequiredChecks []string
}

// CheckState is Forge's normalized state for one pull-request check.
type CheckState string

const (
	CheckPending CheckState = "PENDING"
	CheckSuccess CheckState = "SUCCESS"
	CheckFailure CheckState = "FAILURE"
)

// PullRequestCheck is one normalized check reported against a pull request.
// Optional checks are included here too; callers decide which names are
// required by comparing them against MergeRequirements.
type PullRequestCheck struct {
	Name    string
	State   CheckState
	Details string
}

// PullRequest is Forge's normalized representation of a created — or
// idempotently recovered — pull request (CONTEXT.md "COMMITTING",
// "PR_CREATING"; ticket 22).
type PullRequest struct {
	Number int
	URL    string
}

// ReviewState is Forge's normalized state for one pull-request review
// (issue 109, "Review Rectification").
type ReviewState string

const (
	ReviewApproved         ReviewState = "APPROVED"
	ReviewChangesRequested ReviewState = "CHANGES_REQUESTED"
	ReviewCommented        ReviewState = "COMMENTED"
	ReviewDismissed        ReviewState = "DISMISSED"
)

// PullRequestReview is one normalized review submitted against a pull
// request. GitHub (and trackers generally) let a reviewer submit multiple
// reviews over a PR's lifetime; callers that need "the current verdict"
// per reviewer must reduce to each Author's most recent, non-dismissed
// entry themselves (see internal/ci's review classification) — this type
// carries the raw, unreduced history.
type PullRequestReview struct {
	// ID is the tracker-native review identifier.
	ID int64
	// Author is the reviewer's tracker username.
	Author string
	State  ReviewState
	// Body is the reviewer's summary comment, if any. A CHANGES_REQUESTED
	// review with an empty Body carries nothing actionable to repair from.
	Body        string
	SubmittedAt time.Time
}

// PullRequestMergeStatus is the normalized mergeability of a pull request
// against its base branch (issue 109, "Merge Conflicts").
type PullRequestMergeStatus struct {
	// Merged is true once the pull request has actually been merged. The CI
	// Supervisor uses this as the only terminal-success signal when the
	// tracker can report it: green checks mean the PR is healthy, not that
	// the Issue's work has landed.
	Merged bool
	// Conflicted is true when the tracker reports the pull request cannot
	// be merged into its base due to a conflict. False covers every other
	// state, including "not yet computed" — callers that need to
	// distinguish "known clean" from "unknown" should treat Conflicted as
	// authoritative only once the tracker has finished computing it (see
	// the github adapter's doc comment on its mergeable_state mapping).
	Conflicted bool
	// Behind is true when the tracker reports the pull request's branch
	// has fallen behind its base branch (issue 233: GitHub's
	// mergeable_state == "behind") and would benefit from being rebased
	// onto the base branch's current tip before its checks are trusted.
	// False covers every other state, including "not yet computed" — same
	// caveat as Conflicted.
	Behind bool
}

// IssueRequest carries everything CreateIssue needs to create a new Issue
// on the tracker.
type IssueRequest struct {
	// Title is the Issue title.
	Title string
	// Body is the Issue body/description.
	Body string
}

// UpdateIssueRequest carries the fields UpdateIssue overwrites on an
// existing Issue. Body is always a full replacement, not a patch/merge —
// callers (e.g. internal/materialize's Phase B/C) are responsible for
// composing the complete new body before calling UpdateIssue.
type UpdateIssueRequest struct {
	// Body is the new Issue body/description, replacing the existing one.
	Body string
}

// CreatedIssue is the identity CreateIssue returns for a newly created
// Issue — enough to fetch it back via GetIssue and to link to it for a
// human reader, without pulling in the full ghIssue/domain.Issue shape a
// bare create response doesn't populate (e.g. Dependencies).
type CreatedIssue struct {
	// ID is the tracker-native Issue identifier, suitable for passing to
	// GetIssue.
	ID string
	// URL is the tracker's web URL for the created Issue.
	URL string
}

// Capabilities reports which optional behaviors a Tracker implementation
// supports, so callers can branch on capability rather than concrete type
// (see CONTEXT.md "Tracker Adapter"). Capabilities is additive: a zero
// Capabilities value means no optional behaviors are supported.
type Capabilities struct {
	// PlanningMirror reports whether the Tracker projects Planning
	// Artifacts onto tracker Issues. Always false for MVP — no
	// planning-mirror projection behavior is built yet; this flag reserves
	// the surface for when it is.
	PlanningMirror bool
}

// PullRequestRequest carries everything CreatePullRequest needs to create,
// or idempotently recover, a pull request.
type PullRequestRequest struct {
	// Base is the target branch name (e.g. "main"), not a commit SHA or a
	// remote-qualified ref such as "origin/main".
	Base string
	// Head is the source branch name Forge pushed the Workspace to.
	Head string
	// Title is the pull request title.
	Title string
	// Body is the pull request description.
	Body string
}

// Tracker is the normalized interface to an external issue tracker
// (GitHub, GitLab, etc). Scheduler-facing code depends only on this
// interface and the domain-typed values it returns — it contains no
// tracker-specific models (see CONTEXT.md "Tracker Adapter").
type Tracker interface {
	// GetIssue fetches a single Issue, normalized to domain.Issue, with its
	// Dependencies parsed from the canonical `## Dependencies` block and
	// any configured overrides applied.
	GetIssue(ctx context.Context, id string) (domain.Issue, error)

	// GetIssues fetches multiple Issues, normalized to domain.Issue.
	GetIssues(ctx context.Context, ids []string) ([]domain.Issue, error)

	// GetComments fetches the comments on an Issue, oldest first.
	GetComments(ctx context.Context, id string) ([]Comment, error)

	// AddComment posts a new comment on an Issue and returns it normalized,
	// including the author identity and the tracker-server-clock CreatedAt
	// the tracker assigned — callers that need to later distinguish their
	// own posted comment from subsequent human replies (see the NEEDS_INFO
	// resume flow, CONTEXT.md issue 07) must use these tracker-reported
	// values rather than a locally captured identity/clock, which could
	// diverge from the tracker's under clock skew.
	AddComment(ctx context.Context, id string, body string) (Comment, error)

	// AddLabel idempotently ensures label is set on the Issue. Adding a
	// label that is already present is not an error.
	AddLabel(ctx context.Context, id string, label string) error

	// RemoveLabel idempotently ensures label is not set on the Issue.
	// Removing a label that is not present is not an error.
	RemoveLabel(ctx context.Context, id string, label string) error

	// GetMergeRequirements returns the Merge Requirements for branch,
	// sourced from the tracker's native branch protection/rulesets (see
	// CONTEXT.md "Merge Requirements").
	GetMergeRequirements(ctx context.Context, branch string) (MergeRequirements, error)

	// GetPullRequestChecks returns the current normalized checks attached to
	// pull request number.
	GetPullRequestChecks(ctx context.Context, number int) ([]PullRequestCheck, error)

	// CreatePullRequest idempotently creates a pull request from
	// req.Head into req.Base. If an open pull request already exists for
	// req.Head, it is recovered (returned) rather than duplicated —
	// CONTEXT.md "COMMITTING"/"PR_CREATING" (ticket 22).
	CreatePullRequest(ctx context.Context, req PullRequestRequest) (PullRequest, error)

	// CreateIssue creates a new Issue on the tracker and returns enough
	// identity (CreatedIssue) to fetch it back via GetIssue and to
	// validate it was created as expected.
	CreateIssue(ctx context.Context, req IssueRequest) (CreatedIssue, error)

	// UpdateIssue replaces id's body with req.Body. Used by Issue
	// materialization (see internal/materialize) to rewrite temporary
	// ticket keys to tracker IDs in the canonical `## Dependencies` block
	// and to stamp/advance the `## Forge Provenance` block once the whole
	// materialized graph validates.
	UpdateIssue(ctx context.Context, id string, req UpdateIssueRequest) error

	// Capabilities reports which optional behaviors this Tracker
	// implementation supports (see the Capabilities doc comment).
	Capabilities() Capabilities
}

// AuthPreflighter is an optional capability a Tracker adapter implements
// when it needs a credential and can verify it cheaply up front. cmd/forge
// type-asserts for this interface and, when present, calls VerifyAuth
// before any side-effecting work begins (opening the state store, creating
// a workspace, invoking an agent, or transitioning an Issue). A Tracker
// that needs no credential (e.g. a fake/offline tracker) simply does not
// implement AuthPreflighter — the type assertion fails and the preflight
// is silently skipped, which is the escape hatch such contexts need.
type AuthPreflighter interface {
	VerifyAuth(ctx context.Context) error
}

// ReviewsGetter is an optional capability a Tracker adapter implements when
// it can report the reviews submitted against a pull request (issue 109,
// "Review Rectification"). internal/ci's Supervisor type-asserts for this
// interface each poll; a Tracker that doesn't implement it (e.g. a fake
// used by tests unrelated to review rectification) simply skips review
// classification, matching AuthPreflighter's escape-hatch pattern above.
type ReviewsGetter interface {
	GetPullRequestReviews(ctx context.Context, number int) ([]PullRequestReview, error)
}

// MergeStatusGetter is an optional capability a Tracker adapter implements
// when it can report a pull request's mergeability against its base branch
// (issue 109, "Merge Conflicts"). Optional like ReviewsGetter — see
// internal/ci's Supervisor.
type MergeStatusGetter interface {
	GetPullRequestMergeStatus(ctx context.Context, number int) (PullRequestMergeStatus, error)
}

// RateLimitError is returned by a Tracker implementation when a request is
// rejected because the tracker's API rate limit has been exhausted. It is
// tracker-agnostic so scheduler-facing code can detect and react to rate
// limiting without depending on a specific tracker's error shape.
type RateLimitError struct {
	// ResetAt is when the tracker reports the rate limit will reset, if
	// known. Zero if the tracker did not report a reset time.
	ResetAt time.Time
	// Message is additional detail from the tracker, if any.
	Message string
}

func (e *RateLimitError) Error() string {
	if e.ResetAt.IsZero() {
		if e.Message != "" {
			return fmt.Sprintf("tracker: rate limited: %s", e.Message)
		}
		return "tracker: rate limited"
	}
	return fmt.Sprintf("tracker: rate limited until %s", e.ResetAt.Format(time.RFC3339))
}
