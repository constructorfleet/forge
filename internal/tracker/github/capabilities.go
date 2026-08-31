package github

import (
	"context"

	"github.com/Teagan42/forge/internal/tracker"
)

// CreateChangeRequest adapts the neutral SCM capability to GitHub's existing
// pull-request creation behavior.
func (c *Client) CreateChangeRequest(ctx context.Context, req tracker.ChangeRequestRequest) (tracker.ChangeRequest, error) {
	pr, err := c.CreatePullRequest(ctx, req)
	if err != nil {
		return tracker.ChangeRequest{}, err
	}
	return tracker.ChangeRequest{
		Ref: tracker.ChangeRequestRef{
			Provider: "github",
			Number:   pr.Number,
		},
		URL: pr.URL,
	}, nil
}

// GetChangeRequestMergeStatus adapts the neutral SCM capability to GitHub's
// existing pull-request merge-status behavior.
func (c *Client) GetChangeRequestMergeStatus(ctx context.Context, ref tracker.ChangeRequestRef) (tracker.ChangeRequestMergeStatus, error) {
	status, err := c.GetPullRequestMergeStatus(ctx, ref.Number)
	if err != nil {
		return tracker.ChangeRequestMergeStatus{}, err
	}
	return tracker.ChangeRequestMergeStatus{
		Merged:     status.Merged,
		Conflicted: status.Conflicted,
		Behind:     status.Behind,
	}, nil
}

// GetChecks adapts the neutral CI capability to GitHub's existing pull-request
// check reporting behavior.
func (c *Client) GetChecks(ctx context.Context, ref tracker.ChangeRequestRef) ([]tracker.Check, error) {
	return c.GetPullRequestChecks(ctx, ref.Number)
}

// GetReviews adapts SCM's optional neutral review sub-capability to GitHub's
// existing pull-request review behavior.
func (c *Client) GetReviews(ctx context.Context, ref tracker.ChangeRequestRef) ([]tracker.Review, error) {
	return c.GetPullRequestReviews(ctx, ref.Number)
}
