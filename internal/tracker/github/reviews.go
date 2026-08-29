package github

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

// ghReview is the subset of GitHub's pull-request review JSON shape Client
// normalizes. Unexported: this shape never leaves the github package (see
// CONTEXT.md "Tracker Adapter").
type ghReview struct {
	ID          int64     `json:"id"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetPullRequestReviews returns every review submitted against pull
// request number, oldest first, exactly as GitHub reports them — unreduced
// per-author history (see tracker.PullRequestReview's doc comment).
func (c *Client) GetPullRequestReviews(ctx context.Context, number int) ([]tracker.PullRequestReview, error) {
	var reviews []ghReview
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", c.owner, c.repo, number)
	if err := c.do(ctx, http.MethodGet, path, nil, &reviews); err != nil {
		return nil, err
	}

	out := make([]tracker.PullRequestReview, 0, len(reviews))
	for _, r := range reviews {
		out = append(out, tracker.PullRequestReview{
			ID:          r.ID,
			Author:      r.User.Login,
			State:       normalizeReviewState(r.State),
			Body:        r.Body,
			SubmittedAt: r.SubmittedAt,
		})
	}
	return out, nil
}

func normalizeReviewState(state string) tracker.ReviewState {
	switch state {
	case "APPROVED":
		return tracker.ReviewApproved
	case "CHANGES_REQUESTED":
		return tracker.ReviewChangesRequested
	case "DISMISSED":
		return tracker.ReviewDismissed
	default:
		// COMMENTED and GitHub's PENDING (a review still being drafted,
		// never returned for someone else's PR) both normalize to
		// COMMENTED — neither carries a merge-blocking verdict.
		return tracker.ReviewCommented
	}
}
