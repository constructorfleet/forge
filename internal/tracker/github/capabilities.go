package github

import (
	"context"

	"github.com/Teagan42/forge/internal/tracker"
)

// CreateChangeRequest adapts the neutral SCM capability to GitHub's existing
// pull-request creation behavior. The neutral request is mapped field by
// field rather than whole-struct converted so the neutral ChangeRequestRequest
// and the provider-native PullRequestRequest can evolve independently.
func (c *Client) CreateChangeRequest(ctx context.Context, req tracker.ChangeRequestRequest) (tracker.ChangeRequest, error) {
	// The neutral and native structs are identical today, so staticcheck
	// (S1016) suggests a whole-struct conversion; we deliberately map field
	// by field instead to keep the neutral SCM vocabulary decoupled from the
	// provider-native PR shape as either evolves.
	//nolint:staticcheck // S1016: intentional field mapping, not a struct convert
	pr, err := c.CreatePullRequest(ctx, tracker.PullRequestRequest{
		Base:  req.Base,
		Head:  req.Head,
		Title: req.Title,
		Body:  req.Body,
	})
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
	// The neutral and legacy structs are identical today, so staticcheck
	// (S1016) suggests a whole-struct conversion; mapped field by field
	// instead to keep the neutral SCM vocabulary decoupled as either evolves
	// (see CreateChangeRequest above).
	//nolint:staticcheck // S1016: intentional field mapping, not a struct convert
	return tracker.ChangeRequestMergeStatus{
		Merged:     status.Merged,
		Conflicted: status.Conflicted,
		Behind:     status.Behind,
		RawDetail:  status.RawDetail,
	}, nil
}

// GetChecks adapts the neutral CI capability to GitHub's existing pull-request
// check reporting behavior.
func (c *Client) GetChecks(ctx context.Context, ref tracker.ChangeRequestRef) ([]tracker.Check, error) {
	checks, err := c.GetPullRequestChecks(ctx, ref.Number)
	if err != nil {
		return nil, err
	}
	out := make([]tracker.Check, 0, len(checks))
	for _, check := range checks {
		out = append(out, tracker.Check(check))
	}
	return out, nil
}

// GetReviews adapts SCM's optional neutral review sub-capability to GitHub's
// existing pull-request review behavior.
func (c *Client) GetReviews(ctx context.Context, ref tracker.ChangeRequestRef) ([]tracker.Review, error) {
	reviews, err := c.GetPullRequestReviews(ctx, ref.Number)
	if err != nil {
		return nil, err
	}
	out := make([]tracker.Review, 0, len(reviews))
	for _, review := range reviews {
		out = append(out, tracker.Review{
			Author:      review.Author,
			State:       review.State,
			Body:        review.Body,
			SubmittedAt: review.SubmittedAt,
			RawDetail:   string(review.State),
		})
	}
	return out, nil
}
