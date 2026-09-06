package gitea

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

// giteaReview is the subset of Gitea's pull-request review JSON shape Client
// normalizes. Unexported: this shape never leaves the gitea package (see
// CONTEXT.md "Tracker Adapter").
type giteaReview struct {
	ID          int64     `json:"id"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	Dismissed   bool      `json:"dismissed"`
	SubmittedAt time.Time `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetPullRequestReviews returns every review submitted against pull request
// number, oldest first, exactly as Gitea reports them — unreduced per-author
// history (see tracker.PullRequestReview's doc comment).
func (c *Client) GetPullRequestReviews(ctx context.Context, number int) ([]tracker.PullRequestReview, error) {
	var reviews []giteaReview
	path := fmt.Sprintf("%s/pulls/%d/reviews", c.repoPath(), number)
	if err := c.do(ctx, http.MethodGet, path, nil, &reviews); err != nil {
		return nil, err
	}

	out := make([]tracker.PullRequestReview, 0, len(reviews))
	for _, r := range reviews {
		out = append(out, tracker.PullRequestReview{
			ID:          r.ID,
			Author:      r.User.Login,
			State:       normalizeReviewState(r.State, r.Dismissed),
			Body:        r.Body,
			SubmittedAt: r.SubmittedAt,
		})
	}
	return out, nil
}

// normalizeReviewState maps a Gitea review state onto the neutral review state.
// Gitea names the "changes requested" verdict "REQUEST_CHANGES" and the plain
// comment verdict "COMMENT", unlike GitHub's "CHANGES_REQUESTED" and
// "COMMENTED". A dismissed review carries no verdict, so it maps to
// ReviewDismissed regardless of its original state.
func normalizeReviewState(state string, dismissed bool) tracker.ReviewState {
	if dismissed {
		return tracker.ReviewDismissed
	}
	switch state {
	case "APPROVED":
		return tracker.ReviewApproved
	case "REQUEST_CHANGES":
		return tracker.ReviewChangesRequested
	default:
		// COMMENT, PENDING (a review still being drafted), and REQUEST_REVIEW
		// carry no merge-blocking verdict, so they all normalize to COMMENTED.
		return tracker.ReviewCommented
	}
}
