package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// ghDependency is the subset of a GitHub issue-dependency relationship the
// adapter reads. The `/dependencies/blocked_by` subresource returns full
// issue objects; only the number crosses into the domain (see CONTEXT.md
// "Tracker Adapter").
type ghDependency struct {
	Number int `json:"number"`
}

// fetchBlockedBy reads number's native GitHub "blocked by" issue
// dependencies — the issues that must complete before this one — and
// returns them as Forge Issue IDs (decimal strings).
//
// Native relationships are the canonical GitHub Dependency Source (ADR
// 0003): when present they take precedence over the `## Dependencies` body
// block, which remains a fallback for repositories or GitHub hosts that do
// not expose the issue-dependencies API. A 404 from the subresource means
// the feature is unavailable there; fetchBlockedBy reports that via
// ok=false so the caller can fall back to the body block rather than fail.
// Any other error propagates — silently dropping a real dependency would
// let an Issue schedule as if it had no prerequisites, so the adapter fails
// closed rather than guessing.
func (c *Client) fetchBlockedBy(ctx context.Context, number int) (ids []string, ok bool, err error) {
	var deps []ghDependency
	// per_page=100 keeps all but pathological dependency counts to a single
	// page; the DAG at MVP scale never approaches it (see GetIssues on why
	// round-trip minimization is not the priority here).
	path := c.issuePath(number, "/dependencies/blocked_by?per_page=100")
	if e := c.do(ctx, http.MethodGet, path, nil, &deps); e != nil {
		var notFound *NotFoundError
		if errors.As(e, &notFound) {
			return nil, false, nil
		}
		return nil, false, e
	}
	ids = make([]string, 0, len(deps))
	for _, d := range deps {
		ids = append(ids, strconv.Itoa(d.Number))
	}
	return ids, true, nil
}

// dependencyEdges resolves gh's final prerequisite IDs — native relationships
// when available, else the `## Dependencies` body block, with configured
// DependencyOverrides applied last (ADR 0003) — and maps them to neutral,
// provider-qualified DependencyEdges. This is the DependencyStore capability's
// shared computation: both GetDependencies and GetIssue's normalization
// resolve edges through it, so the two never drift onto separate encodings.
func (c *Client) dependencyEdges(gh ghIssue, native []string, nativeOK bool) ([]tracker.DependencyEdge, error) {
	base := native
	if !nativeOK || len(native) == 0 {
		// No native relationships to read here — fall back to the body
		// block. Its strict syntax still fails closed on freeform text
		// rather than guessing (see tracker.ParseDependencyBlock).
		parsed, err := tracker.ParseDependencyBlock(gh.Body)
		if err != nil {
			return nil, fmt.Errorf("github: issue #%d: %w", gh.Number, err)
		}
		base = parsed
	}

	issueID := strconv.Itoa(gh.Number)
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
// id's issue and native "blocked by" relationships, then resolves and
// returns its prerequisite DependencyEdges (see dependencyEdges).
func (c *Client) GetDependencies(ctx context.Context, id string) ([]tracker.DependencyEdge, error) {
	gh, native, nativeOK, err := c.fetchIssueAndDeps(ctx, id)
	if err != nil {
		return nil, err
	}

	return c.dependencyEdges(gh, native, nativeOK)
}

// WriteDependencies implements the DependencyStore write capability: it
// fetches id's current issue body, replaces its canonical `## Dependencies`
// block (ADR 0003) with dependsOn via tracker.ReplaceDependencyBlock — the
// same encoding dependencyEdges falls back to reading — and PATCHes the
// rewritten body back. Every other section of the body is preserved.
func (c *Client) WriteDependencies(ctx context.Context, id string, dependsOn []string) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	var gh ghIssue
	path := c.issuePath(number, "")
	if err := c.do(ctx, http.MethodGet, path, nil, &gh); err != nil {
		return fmt.Errorf("github: fetch issue %s: %w", id, err)
	}

	newBody := tracker.ReplaceDependencyBlock(gh.Body, dependsOn)
	if err := c.UpdateIssue(ctx, id, tracker.UpdateIssueRequest{Body: newBody}); err != nil {
		return fmt.Errorf("github: write dependencies for issue %s: %w", id, err)
	}
	return nil
}
