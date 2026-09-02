package gitlab

import (
	"context"
	"errors"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// approvalsRequirementName is the Merge Requirement name the adapter uses for
// a project that requires an approval. It is not a check name: GetChecks
// never reports it, so it stays out of MergeRequirements.RequiredChecks.
const approvalsRequirementName = "approvals"

// glProject is the subset of GitLab's project JSON shape the Merge
// Requirements aggregation reads.
type glProject struct {
	// OnlyAllowMergeIfPipelineSucceeds is the project setting GitLab enforces
	// at merge time. It is the pipeline gate the neutral model reports as a
	// required check.
	OnlyAllowMergeIfPipelineSucceeds bool `json:"only_allow_merge_if_pipeline_succeeds"`
}

// glApprovalRule is the subset of GitLab's approval rule JSON shape the Merge
// Requirements aggregation reads. GitLab exposes approval rules on the
// Premium and Ultimate tiers only.
type glApprovalRule struct {
	ApprovalsRequired int `json:"approvals_required"`
}

// getMergeRequirements aggregates the Merge Requirements GitLab enforces for
// the project, from the project settings and from the approval rules.
//
// The branch argument is accepted for the neutral CI capability, but GitLab
// enforces both sources at the project level, not per branch, so the adapter
// does not read it.
//
// An ungated project returns the zero MergeRequirements and no error, the
// same as an unprotected branch on another provider.
func (c *Client) getMergeRequirements(ctx context.Context) (tracker.MergeRequirements, error) {
	var project glProject
	if err := c.do(ctx, http.MethodGet, c.projectPath(), nil, &project); err != nil {
		return tracker.MergeRequirements{}, err
	}

	var reqs []tracker.MergeRequirement
	var requiredChecks []string
	if project.OnlyAllowMergeIfPipelineSucceeds {
		reqs = append(reqs, tracker.MergeRequirement{CheckName: pipelineCheckName})
		requiredChecks = append(requiredChecks, pipelineCheckName)
	}

	approvalsRequired, err := c.approvalsRequired(ctx)
	if err != nil {
		return tracker.MergeRequirements{}, err
	}
	if approvalsRequired {
		// Approvals are a Merge Requirement, but not a check. GetChecks never
		// reports an "approvals" check, so naming it in RequiredChecks would
		// block the merge forever.
		reqs = append(reqs, tracker.MergeRequirement{CheckName: approvalsRequirementName})
	}

	return tracker.MergeRequirements{Requirements: reqs, RequiredChecks: requiredChecks}, nil
}

// approvalsRequired reports whether the project has an approval rule that
// requires at least one approval.
//
// GitLab exposes approval rules on the Premium and Ultimate tiers only. A
// tier without them answers 403 or 404. That answer means "this project has
// no approval requirement", so the adapter degrades to false rather than
// failing the Execution.
func (c *Client) approvalsRequired(ctx context.Context) (bool, error) {
	var rules []glApprovalRule
	err := c.do(ctx, http.MethodGet, c.projectPath()+"/approval_rules", nil, &rules)

	var notFound *NotFoundError
	var forbidden *AuthorizationError
	switch {
	case err == nil:
		for _, rule := range rules {
			if rule.ApprovalsRequired > 0 {
				return true, nil
			}
		}
		return false, nil
	case errors.As(err, &notFound), errors.As(err, &forbidden):
		return false, nil
	default:
		return false, err
	}
}
