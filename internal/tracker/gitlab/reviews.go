package gitlab

import (
	"context"
	"errors"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// glApprovals is the subset of GitLab's merge request approvals JSON shape
// the review sub-capability reads.
type glApprovals struct {
	Approved   bool `json:"approved"`
	ApprovedBy []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	} `json:"approved_by"`
}

// approvalsRawDetail is the provider diagnostic the adapter records on each
// approval. GitLab's approvals endpoint reports approvals only, so every
// review it produces is an approval.
const approvalsRawDetail = "APPROVED"

// getApprovals reads the approvals on one merge request and normalizes each
// approver into a neutral review.
//
// GitLab exposes approval rules on the Premium and Ultimate tiers only. A
// tier without them answers 403 or 404. That answer means "this project has
// no approvals", so the adapter reports an empty list rather than failing.
// Forge then gates on nothing, which is the correct behavior for a tier with
// no approvals.
//
// GitLab reports no timestamp per approval, so SubmittedAt stays zero and
// Body stays empty.
func (c *Client) getApprovals(ctx context.Context, iid int) ([]tracker.Review, error) {
	var approvals glApprovals
	err := c.do(ctx, http.MethodGet, c.mergeRequestPath(iid, "/approvals"), nil, &approvals)

	var notFound *NotFoundError
	var forbidden *AuthorizationError
	switch {
	case err == nil:
	case errors.As(err, &notFound), errors.As(err, &forbidden):
		return []tracker.Review{}, nil
	default:
		return nil, err
	}

	reviews := make([]tracker.Review, 0, len(approvals.ApprovedBy))
	for _, approver := range approvals.ApprovedBy {
		reviews = append(reviews, tracker.Review{
			Author:    approver.User.Username,
			State:     tracker.ReviewApproved,
			RawDetail: approvalsRawDetail,
		})
	}
	return reviews, nil
}
