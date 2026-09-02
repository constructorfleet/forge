package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// glIssue is the subset of GitLab's issue JSON shape Client normalizes.
// Unexported: this shape never leaves the gitlab package (see CONTEXT.md
// "Tracker Adapter").
//
// IID is the project-scoped internal id GitLab shows in its UI and accepts
// in its issue endpoints. Forge uses it, not the global "id", as the Issue
// ID: it is short, decimal, stable inside the project, and safe in a branch
// name or a file path.
type glIssue struct {
	IID int `json:"iid"`
	// ProjectID identifies the project the issue belongs to. The adapter
	// compares it against each native link's project to reject a
	// cross-project prerequisite (see fetchBlockedBy).
	ProjectID   int    `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	WebURL      string `json:"web_url"`
}

// GetIssue fetches a single Issue by ID (a GitLab issue iid, with or without
// a leading "#") and normalizes it to domain.Issue. Dependencies come from
// GitLab's native typed issue links when the instance and the project tier
// expose them, and from the canonical `## Dependencies` block in the issue
// description otherwise. Any configured DependencyOverrides then take
// precedence (see CONTEXT.md "Dependency Source", ADR 0003, and ADR 0027).
func (c *Client) GetIssue(ctx context.Context, id string) (domain.Issue, error) {
	gl, native, nativeOK, err := c.fetchIssueAndDeps(ctx, id)
	if err != nil {
		return domain.Issue{}, err
	}

	return c.normalizeIssue(gl, native, nativeOK)
}

// fetchIssueAndDeps fetches id's raw GitLab issue and its native prerequisite
// IDs in one round-trip sequence. GetIssue and GetDependencies both need this
// exact sequence (issue description, then native links) before they diverge
// into domain.Issue normalization and DependencyEdge resolution. They share
// it here so the sequence — and any later change to it, such as retry
// behavior or error wrapping — cannot drift between the two callers.
func (c *Client) fetchIssueAndDeps(ctx context.Context, id string) (gl glIssue, native []string, nativeOK bool, err error) {
	iid, err := parseIssueID(id)
	if err != nil {
		return glIssue{}, nil, false, err
	}

	if err := c.do(ctx, http.MethodGet, c.issuePath(iid, ""), nil, &gl); err != nil {
		return glIssue{}, nil, false, err
	}

	native, nativeOK, err = c.fetchBlockedBy(ctx, gl)
	if err != nil {
		return glIssue{}, nil, false, err
	}

	return gl, native, nativeOK, nil
}

// GetIssues fetches multiple Issues by ID, normalized to domain.Issue.
//
// The fetches are sequential, not concurrent. This is a deliberate choice: it
// keeps GitLab API rate-limit use predictable and DAG-construction order
// deterministic. Both matter more at this scale than the latency of a few
// extra round trips.
func (c *Client) GetIssues(ctx context.Context, ids []string) ([]domain.Issue, error) {
	issues := make([]domain.Issue, 0, len(ids))
	for _, id := range ids {
		issue, err := c.GetIssue(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("gitlab: fetch issue %s: %w", id, err)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// CreateIssue creates a new Issue on the project and returns enough identity
// (tracker.CreatedIssue) to fetch it back with GetIssue. GitLab names the
// issue body "description", so the neutral Body maps onto that field.
func (c *Client) CreateIssue(ctx context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error) {
	reqBody := struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}{Title: req.Title, Description: req.Body}

	var resp glIssue
	if err := c.do(ctx, http.MethodPost, c.projectPath()+"/issues", reqBody, &resp); err != nil {
		return tracker.CreatedIssue{}, err
	}
	return tracker.CreatedIssue{ID: strconv.Itoa(resp.IID), URL: resp.WebURL}, nil
}

// UpdateIssue replaces id's description with a PUT request. GitLab uses PUT,
// not PATCH, to update an issue.
func (c *Client) UpdateIssue(ctx context.Context, id string, req tracker.UpdateIssueRequest) error {
	iid, err := parseIssueID(id)
	if err != nil {
		return err
	}

	reqBody := struct {
		Description string `json:"description"`
	}{Description: req.Body}

	return c.do(ctx, http.MethodPut, c.issuePath(iid, ""), reqBody, nil)
}

// normalizeIssue converts a fetched GitLab issue into a domain.Issue. native
// carries the issue's native prerequisite IDs and nativeOK reports whether
// that source is available. See dependencyEdges for the precedence rules.
func (c *Client) normalizeIssue(gl glIssue, native []string, nativeOK bool) (domain.Issue, error) {
	issueID := strconv.Itoa(gl.IID)

	edges, err := c.dependencyEdges(gl, native, nativeOK)
	if err != nil {
		return domain.Issue{}, err
	}
	deps := make([]domain.Dependency, len(edges))
	for i, edge := range edges {
		deps[i] = domain.Dependency{
			IssueID:      edge.Issue.ID,
			DependsOnID:  edge.DependsOn.ID,
			IssueRef:     edge.Issue,
			DependsOnRef: edge.DependsOn,
		}
	}

	// Scope (Managed or External) is execution-set membership, which the
	// scheduler and the DAG assign (see CONTEXT.md "External Issue") — not
	// the tracker adapter. Leave it at its zero value here so the field has
	// exactly one writer.
	return domain.Issue{
		ID:           issueID,
		Provider:     c.providerID(),
		Title:        gl.Title,
		Body:         gl.Description,
		Dependencies: deps,
	}, nil
}

// parseIssueID normalizes an Issue ID that can carry a leading "#" (the form
// the Dependency syntax uses) into GitLab's plain numeric issue iid.
func parseIssueID(id string) (int, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(id), "#")
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("gitlab: invalid issue id %q: %w", id, err)
	}
	return n, nil
}
