package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// ghBranchProtection is the subset of GitHub's branch-protection JSON shape
// Client normalizes.
type ghBranchProtection struct {
	RequiredStatusChecks *struct {
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
		} `json:"checks"`
	} `json:"required_status_checks"`
}

// ghRulesetRule is one entry from GitHub's "get rules for a branch"
// endpoint, which reports rules inherited from repository rulesets.
type ghRulesetRule struct {
	Type       string `json:"type"`
	Parameters struct {
		RequiredStatusChecks []struct {
			Context string `json:"context"`
		} `json:"required_status_checks"`
	} `json:"parameters"`
}

// GetMergeRequirements returns the Merge Requirements for branch, sourced
// from GitHub's native branch protection settings, falling back to
// repository rulesets if classic branch protection is not configured (see
// CONTEXT.md "Merge Requirements" and ADR-referenced decision 05: GitHub
// branch protection/rulesets are authoritative). If neither is configured,
// it returns an empty MergeRequirements and no error — an unprotected
// branch simply has no required checks.
func (c *Client) GetMergeRequirements(ctx context.Context, branch string) (tracker.MergeRequirements, error) {
	var protection ghBranchProtection
	protPath := fmt.Sprintf("/repos/%s/%s/branches/%s/protection", c.owner, c.repo, branch)
	err := c.do(ctx, http.MethodGet, protPath, nil, &protection)

	var notFound *NotFoundError
	switch {
	case err == nil:
		if protection.RequiredStatusChecks == nil {
			return tracker.MergeRequirements{}, nil
		}
		checks := protection.RequiredStatusChecks.Contexts
		for _, chk := range protection.RequiredStatusChecks.Checks {
			checks = append(checks, chk.Context)
		}
		return tracker.MergeRequirements{RequiredChecks: dedupe(checks)}, nil
	case errors.As(err, &notFound):
		return c.getRulesetMergeRequirements(ctx, branch)
	default:
		return tracker.MergeRequirements{}, err
	}
}

func (c *Client) getRulesetMergeRequirements(ctx context.Context, branch string) (tracker.MergeRequirements, error) {
	var rules []ghRulesetRule
	rulesPath := fmt.Sprintf("/repos/%s/%s/rules/branches/%s", c.owner, c.repo, branch)
	err := c.do(ctx, http.MethodGet, rulesPath, nil, &rules)

	var notFound *NotFoundError
	switch {
	case err == nil:
		var checks []string
		for _, rule := range rules {
			if rule.Type != "required_status_checks" {
				continue
			}
			for _, chk := range rule.Parameters.RequiredStatusChecks {
				checks = append(checks, chk.Context)
			}
		}
		return tracker.MergeRequirements{RequiredChecks: dedupe(checks)}, nil
	case errors.As(err, &notFound):
		return tracker.MergeRequirements{}, nil
	default:
		return tracker.MergeRequirements{}, err
	}
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
