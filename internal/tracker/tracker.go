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

	// AddComment posts a new comment on an Issue.
	AddComment(ctx context.Context, id string, body string) error

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
