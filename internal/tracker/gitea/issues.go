package gitea

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// giteaIssue is the subset of Gitea's issue JSON shape Client normalizes.
// Unexported: this shape never leaves the gitea package (see CONTEXT.md
// "Tracker Adapter").
type giteaIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// GetIssue fetches a single Issue by ID (a Gitea issue index, with or without
// a leading "#") and normalizes it to domain.Issue. Dependencies come from the
// canonical `## Dependencies` block in the issue body; any configured
// DependencyOverrides then take precedence (see CONTEXT.md "Dependency Source"
// and ADR 0003).
func (c *Client) GetIssue(ctx context.Context, id string) (domain.Issue, error) {
	gi, err := c.fetchIssue(ctx, id)
	if err != nil {
		return domain.Issue{}, err
	}
	return c.normalizeIssue(gi)
}

// fetchIssue fetches id's raw Gitea issue. GetIssue and GetDependencies both
// need this same fetch before they diverge into domain.Issue normalization vs.
// DependencyEdge resolution; sharing it here keeps that fetch — and any future
// change to it, such as retry behavior or error wrapping — from drifting
// between the two callers.
func (c *Client) fetchIssue(ctx context.Context, id string) (giteaIssue, error) {
	number, err := parseIssueID(id)
	if err != nil {
		return giteaIssue{}, err
	}

	var gi giteaIssue
	if err := c.do(ctx, http.MethodGet, c.issuePath(number, ""), nil, &gi); err != nil {
		return giteaIssue{}, err
	}
	return gi, nil
}

// GetIssues fetches multiple Issues by ID, normalized to domain.Issue.
//
// Fetches are sequential rather than concurrent. This is a deliberate choice,
// not an oversight: it keeps Gitea API load predictable and DAG-construction
// ordering deterministic, both of which matter more at MVP scale than the
// latency of a few extra round trips.
func (c *Client) GetIssues(ctx context.Context, ids []string) ([]domain.Issue, error) {
	issues := make([]domain.Issue, 0, len(ids))
	for _, id := range ids {
		issue, err := c.GetIssue(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("gitea: fetch issue %s: %w", id, err)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// CreateIssue creates a new Issue on the repository and returns enough identity
// (tracker.CreatedIssue) to fetch it back via GetIssue.
func (c *Client) CreateIssue(ctx context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error) {
	reqBody := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Title: req.Title, Body: req.Body}

	var resp giteaIssue
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/issues", reqBody, &resp); err != nil {
		return tracker.CreatedIssue{}, err
	}
	return tracker.CreatedIssue{ID: strconv.Itoa(resp.Number), URL: resp.HTMLURL}, nil
}

// UpdateIssue replaces id's body via a PATCH request.
func (c *Client) UpdateIssue(ctx context.Context, id string, req tracker.UpdateIssueRequest) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	reqBody := struct {
		Body string `json:"body"`
	}{Body: req.Body}

	return c.do(ctx, http.MethodPatch, c.issuePath(number, ""), reqBody, nil)
}

// Capabilities reports the optional behaviors this Client supports.
// PlanningMirror is false: no planning-mirror projection behavior is built yet.
// NativeDependencyLinks is false: this adapter reads Dependencies only from the
// canonical `## Dependencies` body block (see dependencies.go), so it never
// reports typed links (see tracker.Capabilities).
func (c *Client) Capabilities() tracker.Capabilities {
	return tracker.Capabilities{PlanningMirror: false, NativeDependencyLinks: false}
}

// normalizeIssue converts a fetched Gitea issue into a domain.Issue. Its
// Dependencies come from the canonical `## Dependencies` body block, with any
// configured DependencyOverrides applied last.
func (c *Client) normalizeIssue(gi giteaIssue) (domain.Issue, error) {
	issueID := strconv.Itoa(gi.Number)

	edges, err := c.dependencyEdges(gi)
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

	// Scope (Managed vs External) is execution-set membership, which the
	// scheduler/DAG assigns (see CONTEXT.md "External Issue") — not the tracker
	// adapter. Leave it at its zero value here so there is exactly one writer of
	// the field.
	return domain.Issue{
		ID:           issueID,
		Provider:     c.providerID(),
		Title:        gi.Title,
		Body:         gi.Body,
		Dependencies: deps,
	}, nil
}

// parseIssueID normalizes an Issue ID that may carry a leading "#" (as
// referenced in Dependency syntax) into Gitea's plain numeric issue index.
func parseIssueID(id string) (int, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(id), "#")
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("gitea: invalid issue id %q: %w: %w", id, tracker.ErrInvalidIssueID, err)
	}
	return n, nil
}
