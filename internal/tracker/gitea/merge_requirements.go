package gitea

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Teagan42/forge/internal/tracker"
)

// giteaBranchProtection is the subset of Gitea's branch-protection JSON shape
// Client normalizes. Gitea reports the required status checks as a list of
// context strings.
type giteaBranchProtection struct {
	EnableStatusCheck   bool     `json:"enable_status_check"`
	StatusCheckContexts []string `json:"status_check_contexts"`
}

// GetMergeRequirements returns the Merge Requirements for branch, sourced from
// Gitea's native branch-protection settings (see CONTEXT.md "Merge
// Requirements"). If the branch has no protection rule, Gitea answers 404 and
// GetMergeRequirements returns an empty MergeRequirements and no error — an
// unprotected branch simply has no required checks. Status checks that a
// protection rule lists only take effect when the rule enables status checks,
// so a rule with enable_status_check false also yields no required checks.
func (c *Client) GetMergeRequirements(ctx context.Context, branch string) (tracker.MergeRequirements, error) {
	var protection giteaBranchProtection
	path := fmt.Sprintf("%s/branch_protections/%s", c.repoPath(), escapeBranchPath(branch))
	err := c.do(ctx, http.MethodGet, path, nil, &protection)

	var notFound *NotFoundError
	switch {
	case err == nil:
		if !protection.EnableStatusCheck {
			return tracker.MergeRequirements{}, nil
		}
		return mergeRequirementsFromChecks(dedupe(protection.StatusCheckContexts)), nil
	case errors.As(err, &notFound):
		return tracker.MergeRequirements{}, nil
	default:
		return tracker.MergeRequirements{}, err
	}
}

// escapeBranchPath percent-escapes a branch name for use as a URL path
// segment, preserving a literal '/' — Gitea's branch-protection endpoint
// expects a branch name like "feature/foo" to appear as literal segments, not
// percent-encoded — while still escaping any other reserved characters.
func escapeBranchPath(branch string) string {
	parts := strings.Split(branch, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func dedupe(in []string) []string {
	if in == nil {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func mergeRequirementsFromChecks(checks []string) tracker.MergeRequirements {
	reqs := make([]tracker.MergeRequirement, 0, len(checks))
	for _, check := range checks {
		reqs = append(reqs, tracker.MergeRequirement{CheckName: check})
	}
	return tracker.MergeRequirements{
		Requirements:   reqs,
		RequiredChecks: checks,
	}
}
