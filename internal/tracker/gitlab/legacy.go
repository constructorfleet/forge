package gitlab

import (
	"context"

	"github.com/Teagan42/forge/internal/tracker"
)

// CreatePullRequest adapts the legacy PR creation seam to GitLab merge
// requests. Engine still uses this seam while SCM is split out.
func (c *Client) CreatePullRequest(ctx context.Context, req tracker.PullRequestRequest) (tracker.PullRequest, error) {
	cr, err := c.CreateChangeRequest(ctx, tracker.ChangeRequestRequest(req))
	if err != nil {
		return tracker.PullRequest{}, err
	}
	return tracker.PullRequest{Number: cr.Ref.Number, URL: cr.URL}, nil
}

// GetPullRequestChecks adapts the legacy CI seam to GitLab's neutral checks.
func (c *Client) GetPullRequestChecks(ctx context.Context, number int) ([]tracker.PullRequestCheck, error) {
	checks, err := c.GetChecks(ctx, tracker.ChangeRequestRef{Provider: c.providerID(), Number: number})
	if err != nil {
		return nil, err
	}
	out := make([]tracker.PullRequestCheck, 0, len(checks))
	for _, check := range checks {
		out = append(out, tracker.PullRequestCheck(check))
	}
	return out, nil
}

// GetPullRequestMergeStatus adapts the legacy merge-status seam to GitLab's
// neutral SCM merge status.
func (c *Client) GetPullRequestMergeStatus(ctx context.Context, number int) (tracker.PullRequestMergeStatus, error) {
	status, err := c.GetChangeRequestMergeStatus(ctx, tracker.ChangeRequestRef{Provider: c.providerID(), Number: number})
	if err != nil {
		return tracker.PullRequestMergeStatus{}, err
	}
	return tracker.PullRequestMergeStatus(status), nil
}

// GetPullRequestReviews adapts the legacy review seam to GitLab approvals.
func (c *Client) GetPullRequestReviews(ctx context.Context, number int) ([]tracker.PullRequestReview, error) {
	reviews, err := c.GetReviews(ctx, tracker.ChangeRequestRef{Provider: c.providerID(), Number: number})
	if err != nil {
		return nil, err
	}
	out := make([]tracker.PullRequestReview, 0, len(reviews))
	for _, review := range reviews {
		out = append(out, tracker.PullRequestReview{
			Author:      review.Author,
			State:       review.State,
			Body:        review.Body,
			SubmittedAt: review.SubmittedAt,
		})
	}
	return out, nil
}

// GetPullRequestTargetBranch returns a merge request's current target branch.
func (c *Client) GetPullRequestTargetBranch(ctx context.Context, number int) (string, error) {
	mr, err := c.getMergeRequest(ctx, number)
	if err != nil {
		return "", err
	}
	return mr.TargetBranch, nil
}
