package gitea

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// dependencyEdges resolves gi's prerequisite IDs from the canonical `##
// Dependencies` body block, applies any configured DependencyOverrides last
// (ADR 0003), and maps the result to neutral, provider-qualified
// DependencyEdges. This is the DependencyStore capability's shared computation:
// both GetDependencies and GetIssue's normalization resolve edges through it,
// so the two never drift onto separate encodings.
//
// Gitea does expose typed issue dependencies, but this adapter reads only the
// body block, the same canonical Dependency Source the github adapter falls
// back to. Its strict syntax fails closed on freeform text rather than guessing
// (see tracker.ParseDependencyBlock).
func (c *Client) dependencyEdges(gi giteaIssue) ([]tracker.DependencyEdge, error) {
	base, err := tracker.ParseDependencyBlock(gi.Body)
	if err != nil {
		return nil, fmt.Errorf("gitea: issue #%d: %w", gi.Number, err)
	}

	issueID := strconv.Itoa(gi.Number)
	final := tracker.ApplyOverrides(issueID, base, c.DependencyOverrides)
	provider := c.providerID()

	edges := make([]tracker.DependencyEdge, len(final))
	for i, dependsOn := range final {
		edges[i] = tracker.DependencyEdge{
			Issue:     domain.IssueRef{Provider: provider, ID: issueID},
			DependsOn: domain.IssueRef{Provider: provider, ID: dependsOn},
			Kind:      tracker.DependencyBlocks,
		}
	}
	return edges, nil
}

// GetDependencies implements the DependencyStore read capability: it fetches
// id's issue, then resolves and returns its prerequisite DependencyEdges (see
// dependencyEdges).
func (c *Client) GetDependencies(ctx context.Context, id string) ([]tracker.DependencyEdge, error) {
	gi, err := c.fetchIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.dependencyEdges(gi)
}

// WriteDependencies implements the DependencyStore write capability: it fetches
// id's current issue body, replaces its canonical `## Dependencies` block (ADR
// 0003) with dependsOn via tracker.ReplaceDependencyBlock — the same encoding
// dependencyEdges reads — and PATCHes the rewritten body back. Every other
// section of the body is preserved.
func (c *Client) WriteDependencies(ctx context.Context, id string, dependsOn []string) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	var gi giteaIssue
	if err := c.do(ctx, http.MethodGet, c.issuePath(number, ""), nil, &gi); err != nil {
		return fmt.Errorf("gitea: fetch issue %s: %w", id, err)
	}

	newBody := tracker.ReplaceDependencyBlock(gi.Body, dependsOn)
	if err := c.UpdateIssue(ctx, id, tracker.UpdateIssueRequest{Body: newBody}); err != nil {
		return fmt.Errorf("gitea: write dependencies for issue %s: %w", id, err)
	}
	return nil
}
