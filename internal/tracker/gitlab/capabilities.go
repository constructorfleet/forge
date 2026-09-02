package gitlab

import (
	"context"

	"github.com/Teagan42/forge/internal/tracker"
)

// Capabilities reports the optional behaviors this Client supports.
//
// PlanningMirror is false: no planning-mirror projection behavior is built
// yet (see the doc comment on tracker.Capabilities).
//
// NativeDependencyLinks reports what the last issue-link probe found (see
// fetchBlockedBy). Capabilities makes no network request, so the value is
// false until the Client reads dependencies for the first time. A caller
// that wants an accurate answer must read one Issue first.
func (c *Client) Capabilities() tracker.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return tracker.Capabilities{
		PlanningMirror:        false,
		NativeDependencyLinks: c.linksProbed && c.linksAvailable,
	}
}

// CreateChangeRequest adapts the neutral SCM capability to GitLab merge
// request creation. See createMergeRequest for the idempotency behavior.
func (c *Client) CreateChangeRequest(ctx context.Context, req tracker.ChangeRequestRequest) (tracker.ChangeRequest, error) {
	return c.createMergeRequest(ctx, req)
}

// GetChangeRequestMergeStatus adapts the neutral SCM capability to GitLab's
// merge request state. See mergeStatusOf for the normalization.
func (c *Client) GetChangeRequestMergeStatus(ctx context.Context, ref tracker.ChangeRequestRef) (tracker.ChangeRequestMergeStatus, error) {
	mr, err := c.getMergeRequest(ctx, ref.Number)
	if err != nil {
		return tracker.ChangeRequestMergeStatus{}, err
	}
	return mergeStatusOf(mr), nil
}

// GetChecks adapts the neutral CI capability to GitLab's merge request
// pipeline. It reads the pipeline the merge request already carries, so it
// makes one request. See checksOf for the normalization.
func (c *Client) GetChecks(ctx context.Context, ref tracker.ChangeRequestRef) ([]tracker.Check, error) {
	mr, err := c.getMergeRequest(ctx, ref.Number)
	if err != nil {
		return nil, err
	}
	return checksOf(mr), nil
}

// GetMergeRequirements adapts the neutral CI capability to the Merge
// Requirements GitLab enforces. GitLab enforces them per project, not per
// branch, so branch is unused. See getMergeRequirements for the aggregation.
func (c *Client) GetMergeRequirements(ctx context.Context, branch string) (tracker.MergeRequirements, error) {
	_ = branch
	return c.getMergeRequirements(ctx)
}

// GetReviews adapts SCM's optional neutral review sub-capability to GitLab
// merge request approvals. See getApprovals for the tier degradation.
func (c *Client) GetReviews(ctx context.Context, ref tracker.ChangeRequestRef) ([]tracker.Review, error) {
	return c.getApprovals(ctx, ref.Number)
}
