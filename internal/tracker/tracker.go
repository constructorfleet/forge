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

// IssueRequest carries everything CreateIssue needs to create a new Issue
// on the tracker.
type IssueRequest struct {
	// Title is the Issue title.
	Title string
	// Body is the Issue body/description.
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

	// Capabilities reports which optional behaviors this Tracker
	// implementation supports (see the Capabilities doc comment).
	Capabilities() Capabilities
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
