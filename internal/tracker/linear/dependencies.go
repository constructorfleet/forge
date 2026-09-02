package linear

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// relationTypeBlocks is Linear's typed relation value Forge maps to a
// prerequisite edge. On an issue's inverseRelations, an entry with this type
// names the issue that blocks it — the issue at inverseRelations.nodes[].
// Issue is the prerequisite. Every other Linear relation type ("duplicate",
// "related", ...) carries no order and is ignored (this ticket's "native
// relations" decision).
const relationTypeBlocks = "blocks"

// nativeBlockedBy extracts ln's native prerequisite identifiers — the
// issues that must complete before it — from its inverse "blocks"
// relations. There is no fallback encoding for Linear (unlike GitHub/GitLab,
// which fall back to a `## Dependencies` body block): Linear has native
// relations, so the capability-probed strategy (see CONTEXT.md and ADR
// 0027) always resolves to native here.
func nativeBlockedBy(ln lnIssue) []string {
	ids := make([]string, 0, len(ln.InverseRelations.Nodes))
	for _, rel := range ln.InverseRelations.Nodes {
		if rel.Type != relationTypeBlocks {
			continue
		}
		ids = append(ids, rel.Issue.Identifier)
	}
	return ids
}

// dependencyEdges resolves ln's final prerequisite IDs — its native
// "blocks" relations, with any configured DependencyOverrides applied last
// — and maps them to neutral, provider-qualified DependencyEdges. This is
// the DependencyStore capability's shared computation: both GetDependencies
// and GetIssue's normalization resolve edges through it, so the two never
// drift onto separate encodings (see the GitHub/GitLab adapters'
// dependencyEdges).
func (c *Client) dependencyEdges(ln lnIssue) []tracker.DependencyEdge {
	native := nativeBlockedBy(ln)
	final := tracker.ApplyOverrides(ln.Identifier, native, c.DependencyOverrides)
	provider := c.providerID()

	edges := make([]tracker.DependencyEdge, len(final))
	for i, dependsOn := range final {
		edges[i] = tracker.DependencyEdge{
			Issue:     domain.IssueRef{Provider: provider, ID: ln.Identifier},
			DependsOn: domain.IssueRef{Provider: provider, ID: dependsOn},
			Kind:      tracker.DependencyBlocks,
		}
	}
	return edges
}

// GetDependencies implements the DependencyStore read capability: it
// fetches id's issue and its native "blocks" relations, then resolves and
// returns its prerequisite DependencyEdges (see dependencyEdges).
func (c *Client) GetDependencies(ctx context.Context, id string) ([]tracker.DependencyEdge, error) {
	ln, err := c.fetchIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.dependencyEdges(ln), nil
}

const issueRelationCreateMutation = `
mutation($issueId: String!, $relatedIssueId: String!) {
  issueRelationCreate(input: { issueId: $issueId, relatedIssueId: $relatedIssueId, type: blocks }) {
    success
  }
}`

const issueRelationDeleteMutation = `
mutation($id: String!) {
  issueRelationDelete(id: $id) {
    success
  }
}`

// WriteDependencies implements the DependencyStore write capability through
// Linear's native issue relations — there is no body-block path for Linear
// (this ticket's "DependencyStore via native relations" decision). It reads
// id's current native "blocks" prerequisites, then creates a "blocks"
// relation (prerequisite -> id) for each entry in dependsOn not already
// present, and deletes each existing "blocks" relation not named in
// dependsOn, so the native relation set ends up exactly matching dependsOn.
func (c *Client) WriteDependencies(ctx context.Context, id string, dependsOn []string) error {
	internalID, err := c.resolveInternalID(ctx, id)
	if err != nil {
		return err
	}
	ln, err := c.fetchIssue(ctx, id)
	if err != nil {
		return fmt.Errorf("linear: write dependencies for issue %s: %w", id, err)
	}

	want := make(map[string]bool, len(dependsOn))
	for _, dep := range dependsOn {
		want[dep] = true
	}

	// Delete every existing "blocks" relation not named in dependsOn.
	for _, rel := range ln.InverseRelations.Nodes {
		if rel.Type != relationTypeBlocks {
			continue
		}
		if want[rel.Issue.Identifier] {
			continue
		}
		var out struct {
			IssueRelationDelete struct {
				Success bool `json:"success"`
			} `json:"issueRelationDelete"`
		}
		vars := map[string]interface{}{"id": rel.ID}
		if err := c.graphQL(ctx, issueRelationDeleteMutation, vars, &out); err != nil {
			return fmt.Errorf("linear: write dependencies for issue %s: remove relation to %s: %w", id, rel.Issue.Identifier, err)
		}
	}

	// Create a "blocks" relation for each entry in dependsOn not already
	// present.
	have := make(map[string]bool, len(ln.InverseRelations.Nodes))
	for _, rel := range ln.InverseRelations.Nodes {
		if rel.Type == relationTypeBlocks {
			have[rel.Issue.Identifier] = true
		}
	}
	for _, dep := range dependsOn {
		if have[dep] {
			continue
		}
		blockerID, err := c.resolveInternalID(ctx, dep)
		if err != nil {
			return fmt.Errorf("linear: write dependencies for issue %s: resolve prerequisite %s: %w", id, dep, err)
		}
		var out struct {
			IssueRelationCreate struct {
				Success bool `json:"success"`
			} `json:"issueRelationCreate"`
		}
		vars := map[string]interface{}{"issueId": blockerID, "relatedIssueId": internalID}
		if err := c.graphQL(ctx, issueRelationCreateMutation, vars, &out); err != nil {
			return fmt.Errorf("linear: write dependencies for issue %s: add relation to %s: %w", id, dep, err)
		}
	}
	return nil
}
