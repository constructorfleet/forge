package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// GitLab's typed issue-link values. GitLab reports each value from the point
// of view of the issue in the request path (see glIssueLink).
const (
	// linkTypeIsBlockedBy names an issue that must complete before the
	// issue in the request path can begin. It is the only link type Forge
	// maps to a prerequisite edge.
	linkTypeIsBlockedBy = "is_blocked_by"
)

// glIssueLink is the subset of one entry in GitLab's issue-link list the
// adapter reads. Unexported: this shape never leaves the gitlab package.
//
// Each entry describes the other issue in the relation. LinkType names the
// relation from the point of view of the issue in the request path. For
// issue A, an entry {IID: B, LinkType: "is_blocked_by"} means "B blocks A".
// B is therefore a prerequisite of A. An entry with LinkType "blocks" means
// the reverse, "A blocks B". An entry with LinkType "relates_to" carries no
// order. Forge reads only "is_blocked_by" entries.
type glIssueLink struct {
	ID          int    `json:"id"`
	IssueLinkID int    `json:"issue_link_id"`
	IID         int    `json:"iid"`
	ProjectID   int    `json:"project_id"`
	LinkType    string `json:"link_type"`
}

func (l glIssueLink) relationshipID() int {
	if l.IssueLinkID != 0 {
		return l.IssueLinkID
	}
	return l.ID
}

// fetchBlockedBy reads the issue's native GitLab prerequisites — the issues
// that must complete before it — and returns them as Forge Issue IDs
// (decimal strings).
//
// Native links are the canonical GitLab Dependency Source (ADR 0027). When
// they name a prerequisite, they take precedence over the `## Dependencies`
// body block. The body block stays the fallback for an instance or a
// project tier that does not expose typed issue links.
//
// GitLab answers 404 or 403 on the links endpoint in that case.
// fetchBlockedBy reports it with ok=false, so the caller falls back to the
// body block instead of failing. It also remembers the answer, so it probes
// the endpoint only once per Client.
//
// Any other error propagates. A silently dropped prerequisite would let an
// Issue schedule as if it had none, so the adapter fails closed rather than
// guess.
func (c *Client) fetchBlockedBy(ctx context.Context, gl glIssue) (ids []string, ok bool, err error) {
	links, ok, err := c.fetchIssueLinks(ctx, gl)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	ids, err = blockedByIDs(gl, links)
	if err != nil {
		return nil, false, err
	}
	return ids, true, nil
}

func (c *Client) fetchIssueLinks(ctx context.Context, gl glIssue) ([]glIssueLink, bool, error) {
	if c.linksKnownUnavailable() {
		return nil, false, nil
	}

	var links []glIssueLink
	// per_page=100 keeps all but pathological link counts on one page.
	path := c.issuePath(gl.IID, "/links?per_page=100")
	if e := c.do(ctx, http.MethodGet, path, nil, &links); e != nil {
		var notFound *NotFoundError
		var forbidden *AuthorizationError
		if errors.As(e, &notFound) || errors.As(e, &forbidden) {
			c.recordLinksProbe(false)
			return nil, false, nil
		}
		return nil, false, e
	}
	c.recordLinksProbe(true)
	return links, true, nil
}

func blockedByIDs(gl glIssue, links []glIssueLink) ([]string, error) {
	ids := make([]string, 0, len(links))
	for _, link := range links {
		if link.LinkType != linkTypeIsBlockedBy {
			continue
		}
		// A prerequisite in another project cannot be named by a
		// project-scoped iid. Forge fails loudly instead of naming the wrong
		// Issue or dropping the prerequisite: cross-project links are out of
		// scope (ADR 0027).
		if link.ProjectID != 0 && gl.ProjectID != 0 && link.ProjectID != gl.ProjectID {
			return nil, fmt.Errorf(
				"gitlab: issue #%d: cross-project prerequisite (project %d, issue #%d) is not supported",
				gl.IID, link.ProjectID, link.IID)
		}
		ids = append(ids, strconv.Itoa(link.IID))
	}
	return ids, nil
}

// linksKnownUnavailable reports whether an earlier probe already found that
// this instance or project tier hides the issue-link endpoint. The tier does
// not change inside one run, so the adapter does not call the endpoint again.
func (c *Client) linksKnownUnavailable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.linksProbed && !c.linksAvailable
}

// recordLinksProbe stores what the probe found, for linksKnownUnavailable
// and for Capabilities.
func (c *Client) recordLinksProbe(available bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.linksProbed = true
	c.linksAvailable = available
}

// dependencyEdges resolves gl's final prerequisite IDs and maps them to
// neutral, provider-qualified DependencyEdges. It reads the native links
// when they name at least one prerequisite, and the `## Dependencies` body
// block otherwise. It applies the configured DependencyOverrides last (ADR
// 0003, ADR 0027).
//
// This is the DependencyStore capability's shared computation. Both
// GetDependencies and GetIssue's normalization resolve edges through it, so
// the two never drift onto separate encodings.
func (c *Client) dependencyEdges(gl glIssue, native []string, nativeOK bool) ([]tracker.DependencyEdge, error) {
	base := native
	if !nativeOK || len(native) == 0 {
		// There is no native prerequisite to read here. Fall back to the
		// body block. Its strict syntax still fails closed on freeform text
		// rather than guess (see tracker.ParseDependencyBlock).
		parsed, err := tracker.ParseDependencyBlock(gl.Description)
		if err != nil {
			return nil, fmt.Errorf("gitlab: issue #%d: %w", gl.IID, err)
		}
		base = parsed
	}

	issueID := strconv.Itoa(gl.IID)
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
// id's issue and its native links, then resolves and returns its prerequisite
// DependencyEdges (see dependencyEdges).
func (c *Client) GetDependencies(ctx context.Context, id string) ([]tracker.DependencyEdge, error) {
	gl, native, nativeOK, err := c.fetchIssueAndDeps(ctx, id)
	if err != nil {
		return nil, err
	}

	return c.dependencyEdges(gl, native, nativeOK)
}

// WriteDependencies implements the DependencyStore write capability. It
// fetches id's current description. It replaces the canonical `##
// Dependencies` block (ADR 0003) with dependsOn through
// tracker.ReplaceDependencyBlock, the same encoding dependencyEdges falls
// back to reading. It then writes the new description back with a PUT.
// Every other section of the description stays as it is.
//
// When GitLab exposes native issue links, WriteDependencies also syncs
// "is_blocked_by" links. This keeps the native-first read path consistent
// with the write. It still writes the body block for tiers that do not expose
// native links.
func (c *Client) WriteDependencies(ctx context.Context, id string, dependsOn []string) error {
	iid, err := parseIssueID(id)
	if err != nil {
		return err
	}

	var gl glIssue
	if err := c.do(ctx, http.MethodGet, c.issuePath(iid, ""), nil, &gl); err != nil {
		return fmt.Errorf("gitlab: fetch issue %s: %w", id, err)
	}

	links, linksOK, err := c.fetchIssueLinks(ctx, gl)
	if err != nil {
		return fmt.Errorf("gitlab: fetch dependencies for issue %s: %w", id, err)
	}
	if linksOK {
		if err := c.syncNativeDependencies(ctx, gl, links, dependsOn); err != nil {
			return fmt.Errorf("gitlab: sync native dependencies for issue %s: %w", id, err)
		}
	}

	newBody := tracker.ReplaceDependencyBlock(gl.Description, dependsOn)
	if err := c.UpdateIssue(ctx, id, tracker.UpdateIssueRequest{Body: newBody}); err != nil {
		return fmt.Errorf("gitlab: write dependencies for issue %s: %w", id, err)
	}
	return nil
}

func (c *Client) syncNativeDependencies(ctx context.Context, gl glIssue, links []glIssueLink, dependsOn []string) error {
	desired := make(map[int]struct{}, len(dependsOn))
	for _, id := range dependsOn {
		iid, err := parseIssueID(id)
		if err != nil {
			return err
		}
		desired[iid] = struct{}{}
	}

	present := make(map[int]struct{})
	for _, link := range links {
		if link.LinkType != linkTypeIsBlockedBy {
			continue
		}
		if link.ProjectID != 0 && gl.ProjectID != 0 && link.ProjectID != gl.ProjectID {
			return fmt.Errorf(
				"issue #%d: cross-project prerequisite (project %d, issue #%d) is not supported",
				gl.IID, link.ProjectID, link.IID)
		}
		if _, ok := desired[link.IID]; ok {
			present[link.IID] = struct{}{}
			continue
		}
		if err := c.deleteIssueLink(ctx, gl.IID, link.relationshipID()); err != nil {
			return err
		}
	}

	for _, id := range dependsOn {
		iid, err := parseIssueID(id)
		if err != nil {
			return err
		}
		if _, ok := present[iid]; ok {
			continue
		}
		if err := c.createBlockedByLink(ctx, gl.IID, iid); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) createBlockedByLink(ctx context.Context, issueIID, dependsOnIID int) error {
	q := url.Values{}
	q.Set("target_project_id", c.project)
	q.Set("target_issue_iid", strconv.Itoa(dependsOnIID))
	q.Set("link_type", linkTypeIsBlockedBy)
	path := c.issuePath(issueIID, "/links") + "?" + q.Encode()
	if err := c.do(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("create native prerequisite #%d: %w", dependsOnIID, err)
	}
	return nil
}

func (c *Client) deleteIssueLink(ctx context.Context, issueIID, linkID int) error {
	if linkID == 0 {
		return fmt.Errorf("delete native prerequisite: missing issue link id")
	}
	path := c.issuePath(issueIID, "/links/"+strconv.Itoa(linkID))
	if err := c.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("delete native prerequisite link %d: %w", linkID, err)
	}
	return nil
}
