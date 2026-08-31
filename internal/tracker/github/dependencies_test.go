package github_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// When GitHub exposes native "blocked by" relationships, they are the
// canonical Dependency Source (ADR 0003): GetIssue uses them and does not
// consult the body block.
func TestGetIssue_UsesNativeBlockedByRelationships(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			// Native relationships disagree with the body block on purpose;
			// native must win.
			_, _ = w.Write([]byte(`[{"number":284},{"number":289}]`))
			return
		}
		_, _ = w.Write([]byte(`{"number":290,"body":"## Dependencies\n- #7\n"}`))
	})

	issue, err := c.GetIssue(context.Background(), "290")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := dependsOnIDs(issue.Dependencies)
	if len(got) != 2 || got[0] != "284" || got[1] != "289" {
		t.Fatalf("expected native deps [284 289], got %v", got)
	}
}

// A malformed body block is irrelevant when native relationships are
// present — native wins and the body block is never parsed.
func TestGetIssue_NativeDepsIgnoreFreeformBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			_, _ = w.Write([]byte(`[{"number":1}]`))
			return
		}
		_, _ = w.Write([]byte(`{"number":2,"body":"## Dependencies\nfreeform nonsense\n"}`))
	})

	issue, err := c.GetIssue(context.Background(), "2")
	if err != nil {
		t.Fatalf("expected native deps to bypass body parsing, got error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "1" {
		t.Fatalf("expected native dep [1], got %v", got)
	}
}

// A repository/host that does not expose the dependencies subresource
// answers 404; GetIssue degrades to the body block rather than failing.
func TestGetIssue_FallsBackToBodyWhenNativeUnavailable(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies\n- #7\n"}`))
	})

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "7" {
		t.Fatalf("expected body-block fallback [7], got %v", got)
	}
}

// When native relationships are available but empty, GetIssue still falls
// back to the body block (migration-friendly: relationships need not be set
// before body blocks stop being honored).
func TestGetIssue_EmptyNativeFallsBackToBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			serveNoNativeDeps(w)
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies\n- #7\n"}`))
	})

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "7" {
		t.Fatalf("expected body-block fallback [7], got %v", got)
	}
}

// Configured overrides still take precedence over native relationships.
func TestGetIssue_OverridesBeatNativeDeps(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			_, _ = w.Write([]byte(`[{"number":1},{"number":2}]`))
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":""}`))
	})
	c.DependencyOverrides = map[string][]string{"42": {"99"}}

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "99" {
		t.Fatalf("expected override [99] to beat native deps, got %v", got)
	}
}

// A non-404 error from the dependencies subresource fails closed rather
// than silently degrading — a dropped dependency would let an Issue
// schedule as if it had no prerequisites.
func TestGetIssue_NativeServerErrorFailsClosed(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies: None"}`))
	})

	if _, err := c.GetIssue(context.Background(), "42"); err == nil {
		t.Fatal("expected GetIssue to fail when the native deps probe errors")
	}
}

// GetDependencies, the DependencyStore capability's own entry point,
// resolves through the same native-first-then-body-block precedence as
// GetIssue rather than a separate code path.
func TestGetDependencies_PrefersNativeOverBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			_, _ = w.Write([]byte(`[{"number":284}]`))
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies\n- #7\n"}`))
	})

	edges, err := c.GetDependencies(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	want := tracker.DependencyEdge{
		Issue:     domain.IssueRef{Provider: "github", ID: "42"},
		DependsOn: domain.IssueRef{Provider: "github", ID: "284"},
		Kind:      tracker.DependencyBlocks,
	}
	if len(edges) != 1 || edges[0] != want {
		t.Fatalf("edges = %+v, want [%+v]", edges, want)
	}
}

// GetDependencies rejects freeform body text with the same fail-closed
// grammar as the body-block parser used by GetIssue.
func TestGetDependencies_RejectsFreeformDependencyText(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			serveNoNativeDeps(w)
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies\nsomething vague\n"}`))
	})

	if _, err := c.GetDependencies(context.Background(), "42"); err == nil {
		t.Fatal("expected GetDependencies to reject freeform dependency text")
	}
}

// Overrides take precedence over the body block through GetDependencies too.
func TestGetDependencies_OverridesBeatBodyBlock(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			serveNoNativeDeps(w)
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies\n- #7\n"}`))
	})
	c.DependencyOverrides = map[string][]string{"42": {"99"}}

	edges, err := c.GetDependencies(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(edges) != 1 || edges[0].DependsOn.ID != "99" {
		t.Fatalf("edges = %+v, want override [99]", edges)
	}
}
