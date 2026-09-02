package linear

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// lnRelation is the subset of one Linear IssueRelation entry Client reads.
// Unexported: this shape never leaves the linear package.
//
// inverseRelations on an issue lists relations where that issue is the
// target — for a "blocks" entry, Issue names the blocking issue. See
// dependencyEdges.
type lnRelation struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Issue struct {
		Identifier string `json:"identifier"`
	} `json:"issue"`
}

// lnIssue is the subset of Linear's Issue GraphQL shape Client normalizes.
// Unexported: this shape never leaves the linear package.
//
// ID is Linear's internal UUID, used only to address subsequent GraphQL
// calls; it never surfaces as a Forge Issue ID (see identity.go). Identifier
// is the team-prefixed, human-facing key (e.g. "FOR-345") Forge uses as the
// Issue ID.
type lnIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	State       struct {
		Type string `json:"type"`
	} `json:"state"`
	InverseRelations struct {
		Nodes []lnRelation `json:"nodes"`
	} `json:"inverseRelations"`
}

// getIssueQuery fetches one Issue by its internal UUID, along with the
// inverse relations (see lnRelation) dependencyEdges needs.
const getIssueQuery = `
query($id: String!) {
  issue(id: $id) {
    id
    identifier
    title
    description
    url
    state { type }
    inverseRelations {
      nodes {
        id
        type
        issue { identifier }
      }
    }
  }
}`

// GetIssue fetches a single Issue by its team-prefixed identifier (e.g.
// "FOR-345"), resolves it to Linear's internal id, and normalizes it to
// domain.Issue. Dependencies come from Linear's native "blocks" issue
// relations (see dependencyEdges); any configured DependencyOverrides then
// take precedence (CONTEXT.md "Dependency Source").
func (c *Client) GetIssue(ctx context.Context, id string) (domain.Issue, error) {
	ln, err := c.fetchIssue(ctx, id)
	if err != nil {
		return domain.Issue{}, err
	}
	return c.normalizeIssue(ln), nil
}

// fetchIssue resolves id (a team-prefixed identifier) to Linear's internal
// UUID and fetches the full issue by that UUID.
func (c *Client) fetchIssue(ctx context.Context, id string) (lnIssue, error) {
	internalID, err := c.resolveInternalID(ctx, id)
	if err != nil {
		return lnIssue{}, err
	}

	var out struct {
		Issue *lnIssue `json:"issue"`
	}
	if err := c.graphQL(ctx, getIssueQuery, map[string]interface{}{"id": internalID}, &out); err != nil {
		return lnIssue{}, fmt.Errorf("linear: fetch issue %s: %w", id, err)
	}
	if out.Issue == nil {
		return lnIssue{}, &NotFoundError{ID: id}
	}
	return *out.Issue, nil
}

// GetIssues fetches multiple Issues by identifier, normalized to
// domain.Issue.
//
// The fetches are sequential, not concurrent, matching the GitHub/GitLab
// adapters: it keeps Linear API rate-limit use predictable and DAG
// construction order deterministic.
func (c *Client) GetIssues(ctx context.Context, ids []string) ([]domain.Issue, error) {
	issues := make([]domain.Issue, 0, len(ids))
	for _, id := range ids {
		issue, err := c.GetIssue(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("linear: fetch issue %s: %w", id, err)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

const issueCreateMutation = `
mutation($teamId: String!, $title: String!, $description: String!) {
  issueCreate(input: { teamId: $teamId, title: $title, description: $description }) {
    success
    issue { id identifier url }
  }
}`

// CreateIssue creates a new Issue on the configured team and returns enough
// identity (tracker.CreatedIssue) to fetch it back with GetIssue. Linear's
// team-prefixed identifier is the returned ID, matching every other
// tracker.Tracker method's Issue ID shape.
func (c *Client) CreateIssue(ctx context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error) {
	teamID, err := c.resolveTeamID(ctx)
	if err != nil {
		return tracker.CreatedIssue{}, err
	}

	var out struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
				URL        string `json:"url"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	vars := map[string]interface{}{"teamId": teamID, "title": req.Title, "description": req.Body}
	if err := c.graphQL(ctx, issueCreateMutation, vars, &out); err != nil {
		return tracker.CreatedIssue{}, fmt.Errorf("linear: create issue: %w", err)
	}
	if !out.IssueCreate.Success {
		return tracker.CreatedIssue{}, fmt.Errorf("linear: create issue: request was not accepted")
	}
	return tracker.CreatedIssue{
		ID:  out.IssueCreate.Issue.Identifier,
		URL: out.IssueCreate.Issue.URL,
	}, nil
}

const issueUpdateMutation = `
mutation($id: String!, $description: String!) {
  issueUpdate(id: $id, input: { description: $description }) {
    success
  }
}`

// UpdateIssue replaces id's description. Dependencies are not part of the
// body on Linear: WriteDependencies (dependencies.go) manages them through
// native relations instead, so UpdateIssue's body write handles only
// non-dependency content.
func (c *Client) UpdateIssue(ctx context.Context, id string, req tracker.UpdateIssueRequest) error {
	internalID, err := c.resolveInternalID(ctx, id)
	if err != nil {
		return err
	}

	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	vars := map[string]interface{}{"id": internalID, "description": req.Body}
	if err := c.graphQL(ctx, issueUpdateMutation, vars, &out); err != nil {
		return fmt.Errorf("linear: update issue %s: %w", id, err)
	}
	if !out.IssueUpdate.Success {
		return fmt.Errorf("linear: update issue %s: request was not accepted", id)
	}
	return nil
}

// normalizeIssue converts a fetched Linear issue into a domain.Issue.
func (c *Client) normalizeIssue(ln lnIssue) domain.Issue {
	edges := c.dependencyEdges(ln)
	deps := make([]domain.Dependency, len(edges))
	for i, edge := range edges {
		deps[i] = domain.Dependency{
			IssueID:      edge.Issue.ID,
			DependsOnID:  edge.DependsOn.ID,
			IssueRef:     edge.Issue,
			DependsOnRef: edge.DependsOn,
		}
	}

	// State (Forge's orchestration lifecycle) and Scope (Managed or
	// External) are engine/scheduler-assigned, not tracker-adapter-assigned
	// (see the GitHub/GitLab adapters' normalizeIssue). Leave them at their
	// zero value here so each field has exactly one writer. Linear's own
	// workflow-state type (backlog/unstarted/started/completed/canceled) is
	// read but not mapped onto domain.Issue.State — see CONTEXT.md and this
	// ticket's "Workflow-state mapping" decision.
	return domain.Issue{
		ID:           ln.Identifier,
		Provider:     c.providerID(),
		Title:        ln.Title,
		Body:         ln.Description,
		Dependencies: deps,
	}
}
