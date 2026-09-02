package gitlab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// GitLab reports each link type from the point of view of the issue in the
// request path. For issue 290, "is_blocked_by" names a prerequisite of 290,
// "blocks" names an issue that 290 blocks, and "relates_to" names an
// unordered relation. Only "is_blocked_by" becomes a Forge prerequisite
// edge. Getting this backwards would invert the whole DAG, so the table
// pins each link type and the order of a mixed set.
func TestGetIssue_MapsOnlyIsBlockedByLinksToPrerequisites(t *testing.T) {
	cases := []struct {
		name  string
		links string
		// body is what the issue description holds. Each case that expects
		// the body-block fallback uses "#7" so a fallback is visible in the
		// result.
		body string
		want []string
	}{
		{
			name:  "is_blocked_by names a prerequisite",
			links: `[{"iid":284,"project_id":4,"link_type":"is_blocked_by"}]`,
			body:  "## Dependencies\n- #7\n",
			want:  []string{"284"},
		},
		{
			name:  "blocks points the other way and is ignored",
			links: `[{"iid":901,"project_id":4,"link_type":"blocks"}]`,
			body:  "## Dependencies\n- #7\n",
			want:  []string{"7"},
		},
		{
			name:  "relates_to carries no order and is ignored",
			links: `[{"iid":902,"project_id":4,"link_type":"relates_to"}]`,
			body:  "## Dependencies\n- #7\n",
			want:  []string{"7"},
		},
		{
			name: "a mixed set keeps only the is_blocked_by entries, in order",
			links: `[
				{"iid":284,"project_id":4,"link_type":"is_blocked_by"},
				{"iid":901,"project_id":4,"link_type":"blocks"},
				{"iid":902,"project_id":4,"link_type":"relates_to"},
				{"iid":289,"project_id":4,"link_type":"is_blocked_by"}
			]`,
			body: "## Dependencies\n- #7\n",
			want: []string{"284", "289"},
		},
		{
			name:  "an unknown link type is ignored",
			links: `[{"iid":903,"project_id":4,"link_type":"duplicates"}]`,
			body:  "## Dependencies\n- #7\n",
			want:  []string{"7"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if isLinksPath(r) {
					_, _ = w.Write([]byte(tc.links))
					return
				}
				// The links and the body block disagree on purpose, so the
				// result names which source the adapter used.
				body, _ := json.Marshal(tc.body)
				_, _ = w.Write([]byte(`{"iid":290,"project_id":4,"description":` + string(body) + `}`))
			})

			issue, err := c.GetIssue(context.Background(), "290")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := dependsOnIDs(issue.Dependencies)
			if len(got) != len(tc.want) {
				t.Fatalf("prerequisites = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("prerequisites = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// A malformed body block does not matter when native prerequisites are
// present: the native links win and the body block is never parsed.
func TestGetIssue_NativeLinksIgnoreFreeformBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			_, _ = w.Write([]byte(`[{"iid":1,"project_id":4,"link_type":"is_blocked_by"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"iid":2,"project_id":4,"description":"## Dependencies\nfreeform nonsense\n"}`))
	})

	issue, err := c.GetIssue(context.Background(), "2")
	if err != nil {
		t.Fatalf("expected native links to bypass body parsing, got error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "1" {
		t.Fatalf("expected native prerequisite [1], got %v", got)
	}
}

// A GitLab tier that does not include typed issue links answers 404 or 403
// on the links endpoint. The adapter degrades to the body block instead of
// failing.
func TestGetIssue_FallsBackToBodyWhenLinksUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{name: "the endpoint is absent on this host", status: http.StatusNotFound},
		{name: "the project tier hides typed links", status: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if isLinksPath(r) {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies\n- #7\n"}`))
			})

			issue, err := c.GetIssue(context.Background(), "42")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "7" {
				t.Fatalf("expected body-block fallback [7], got %v", got)
			}
		})
	}
}

// Once the links endpoint reports the feature is unavailable, the adapter
// stops calling it: the tier does not change inside one run, so every later
// Issue goes straight to the body block.
func TestGetIssue_ProbesTheLinksEndpointOnlyOnceWhenUnavailable(t *testing.T) {
	linkCalls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			linkCalls++
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies: None"}`))
	})

	if _, err := c.GetIssues(context.Background(), []string{"42", "43", "44"}); err != nil {
		t.Fatalf("GetIssues: %v", err)
	}
	if linkCalls != 1 {
		t.Fatalf("probed the links endpoint %d times, want 1", linkCalls)
	}
}

// A native link set that names no prerequisite still falls back to the body
// block, so a project does not have to backfill links before Forge stops
// reading body blocks.
func TestGetIssue_EmptyNativeLinksFallBackToBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies\n- #7\n"}`))
	})

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "7" {
		t.Fatalf("expected body-block fallback [7], got %v", got)
	}
}

// Configured overrides still take precedence over native links.
func TestGetIssue_OverridesBeatNativeLinks(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			_, _ = w.Write([]byte(`[{"iid":1,"project_id":4,"link_type":"is_blocked_by"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":""}`))
	})
	c.DependencyOverrides = map[string][]string{"42": {"99"}}

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "99" {
		t.Fatalf("expected override [99] to beat the native links, got %v", got)
	}
}

// An error other than "feature unavailable" fails closed. A dropped
// dependency would let an Issue schedule as if it had no prerequisites.
func TestGetIssue_LinksServerErrorFailsClosed(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies: None"}`))
	})

	if _, err := c.GetIssue(context.Background(), "42"); err == nil {
		t.Fatal("expected GetIssue to fail when the links probe errors")
	}
}

// A prerequisite in another project cannot be named by a project-scoped iid,
// so the adapter fails loudly instead of naming the wrong Issue or dropping
// the prerequisite. Cross-project links are out of scope.
func TestGetIssue_CrossProjectBlockerFailsClosed(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			_, _ = w.Write([]byte(`[{"iid":3,"project_id":77,"link_type":"is_blocked_by"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies: None"}`))
	})

	_, err := c.GetIssue(context.Background(), "42")
	if err == nil {
		t.Fatal("expected GetIssue to fail on a cross-project prerequisite")
	}
	if !strings.Contains(err.Error(), "cross-project") {
		t.Fatalf("expected a cross-project error, got %v", err)
	}
}

// GetDependencies, the DependencyStore capability's own entry point, uses the
// same native-first-then-body-block precedence as GetIssue rather than a
// separate code path.
func TestGetDependencies_PrefersNativeLinksOverBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			_, _ = w.Write([]byte(`[{"iid":284,"project_id":4,"link_type":"is_blocked_by"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies\n- #7\n"}`))
	})

	edges, err := c.GetDependencies(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	want := tracker.DependencyEdge{
		Issue:     domain.IssueRef{Provider: "gitlab", ID: "42"},
		DependsOn: domain.IssueRef{Provider: "gitlab", ID: "284"},
		Kind:      tracker.DependencyBlocks,
	}
	if len(edges) != 1 || edges[0] != want {
		t.Fatalf("edges = %+v, want [%+v]", edges, want)
	}
}

// GetDependencies rejects freeform body text with the same fail-closed
// grammar the body-block parser applies for GetIssue.
func TestGetDependencies_RejectsFreeformDependencyText(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies\nsomething vague\n"}`))
	})

	if _, err := c.GetDependencies(context.Background(), "42"); err == nil {
		t.Fatal("expected GetDependencies to reject freeform dependency text")
	}
}

// WriteDependencies writes the canonical `## Dependencies` body block with a
// PUT, and keeps the rest of the description.
func TestWriteDependencies_PutsDependenciesBlockPreservingRestOfBody(t *testing.T) {
	var putBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isLinksPath(r):
			serveNoNativeLinks(w)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"### Objective\nDo the thing.\n\n## Dependencies\n- #1\n"}`))
		case r.Method == http.MethodPut:
			var req struct {
				Description string `json:"description"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			putBody = req.Description
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	})

	if err := c.WriteDependencies(context.Background(), "42", []string{"7", "8"}); err != nil {
		t.Fatalf("WriteDependencies: %v", err)
	}
	if !strings.Contains(putBody, "### Objective\nDo the thing.") {
		t.Fatalf("expected the rest of the description preserved, got %q", putBody)
	}
	deps, err := tracker.ParseDependencyBlock(putBody)
	if err != nil {
		t.Fatalf("parse written description: %v", err)
	}
	if len(deps) != 2 || deps[0] != "7" || deps[1] != "8" {
		t.Fatalf("deps = %v, want [7 8]", deps)
	}
}

// GetDependencies falls back to the body block, so a full round trip (write,
// then read) proves the write path and the read path share one encoding
// inside this adapter.
func TestDependencyEdges_RoundTripThroughWriteThenRead(t *testing.T) {
	var storedBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isLinksPath(r):
			serveNoNativeLinks(w)
		case r.Method == http.MethodGet:
			body, _ := json.Marshal(storedBody)
			_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":` + string(body) + `}`))
		case r.Method == http.MethodPut:
			var req struct {
				Description string `json:"description"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			storedBody = req.Description
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	})

	want := []tracker.DependencyEdge{
		{
			Issue:     domain.IssueRef{Provider: "gitlab", ID: "42"},
			DependsOn: domain.IssueRef{Provider: "gitlab", ID: "3"},
			Kind:      tracker.DependencyBlocks,
		},
		{
			Issue:     domain.IssueRef{Provider: "gitlab", ID: "42"},
			DependsOn: domain.IssueRef{Provider: "gitlab", ID: "4"},
			Kind:      tracker.DependencyBlocks,
		},
	}

	if err := c.WriteDependencies(context.Background(), "42", []string{"3", "4"}); err != nil {
		t.Fatalf("WriteDependencies: %v", err)
	}
	got, err := c.GetDependencies(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
}

// Overrides take precedence over the body block through GetDependencies too.
func TestGetDependencies_OverridesBeatBodyBlock(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies\n- #7\n"}`))
	})
	c.DependencyOverrides = map[string][]string{"42": {"99"}}

	edges, err := c.GetDependencies(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(edges) != 1 || edges[0].DependsOn.ID != "99" {
		t.Fatalf("edges = %+v, want the override [99]", edges)
	}
}
