package github

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// ghIssue is the subset of GitHub's issue JSON shape Client normalizes.
// Unexported: this shape never leaves the github package (see CONTEXT.md
// "Tracker Adapter").
type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// GetIssue fetches a single Issue by ID (a GitHub issue number, with or
// without a leading "#") and normalizes it to domain.Issue. Dependencies
// are parsed from the canonical `## Dependencies` block in the issue body
// and any configured DependencyOverrides are applied (config takes
// precedence; see CONTEXT.md "Dependency Source").
func (c *Client) GetIssue(ctx context.Context, id string) (domain.Issue, error) {
	number, err := parseIssueID(id)
	if err != nil {
		return domain.Issue{}, err
	}

	var gh ghIssue
	path := c.issuePath(number, "")
	if err := c.do(ctx, http.MethodGet, path, nil, &gh); err != nil {
		return domain.Issue{}, err
	}

	return c.normalizeIssue(gh)
}

// GetIssues fetches multiple Issues by ID, normalized to domain.Issue.
//
// Fetches are sequential rather than concurrent. This is a deliberate
// choice, not an oversight: it keeps GitHub API rate-limit consumption
// predictable and DAG-construction ordering deterministic, both of which
// matter more at MVP scale than the latency of a few extra round trips.
func (c *Client) GetIssues(ctx context.Context, ids []string) ([]domain.Issue, error) {
	issues := make([]domain.Issue, 0, len(ids))
	for _, id := range ids {
		issue, err := c.GetIssue(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("github: fetch issue %s: %w", id, err)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

func (c *Client) normalizeIssue(gh ghIssue) (domain.Issue, error) {
	issueID := strconv.Itoa(gh.Number)

	parsed, err := tracker.ParseDependencyBlock(gh.Body)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("github: issue #%d: %w", gh.Number, err)
	}
	final := tracker.ApplyOverrides(issueID, parsed, c.DependencyOverrides)

	deps := make([]domain.Dependency, len(final))
	for i, dependsOn := range final {
		deps[i] = domain.Dependency{IssueID: issueID, DependsOnID: dependsOn}
	}

	// Scope (Managed vs External) is execution-set membership, which the
	// scheduler/DAG assigns (see CONTEXT.md "External Issue"; tickets
	// 26/27) — not the tracker adapter. Leave it at its zero value here so
	// there is exactly one writer of the field.
	return domain.Issue{
		ID:           issueID,
		Dependencies: deps,
	}, nil
}

// parseIssueID normalizes an Issue ID that may carry a leading "#" (as
// referenced in Dependency syntax) into GitHub's plain numeric issue
// number.
func parseIssueID(id string) (int, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(id), "#")
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("github: invalid issue id %q: %w", id, err)
	}
	return n, nil
}
