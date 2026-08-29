package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetIssue_NormalizesToDomainIssueWithParsedDependencies(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/issues/42" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":42,"title":"Do the thing","body":"desc\n\n## Dependencies\n- #1\n- #2\n"}`))
	})

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "42" {
		t.Fatalf("got ID %q, want 42", issue.ID)
	}
	if issue.Title != "Do the thing" {
		t.Fatalf("got Title %q, want %q", issue.Title, "Do the thing")
	}
	if issue.Body != "desc\n\n## Dependencies\n- #1\n- #2\n" {
		t.Fatalf("got Body %q, want the raw issue body", issue.Body)
	}
	if len(issue.Dependencies) != 2 {
		t.Fatalf("got %d dependencies, want 2: %+v", len(issue.Dependencies), issue.Dependencies)
	}
	if issue.Dependencies[0].IssueID != "42" || issue.Dependencies[0].DependsOnID != "1" {
		t.Fatalf("unexpected dependency: %+v", issue.Dependencies[0])
	}
}

func TestGetIssue_DoesNotAssignScope(t *testing.T) {
	// Managed-vs-External is execution-set membership, which the
	// scheduler/DAG owns (tickets 26/27) — not the tracker adapter. A
	// directly-fetched Issue must not be mislabeled Managed here.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":1,"body":"## Dependencies: None"}`))
	})

	issue, err := c.GetIssue(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Scope != domain.IssueScope("") {
		t.Fatalf("expected zero-value Scope, got %q", issue.Scope)
	}
}

func TestGetIssue_AcceptsHashPrefixedID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/issues/7" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"number":7,"body":"## Dependencies: None"}`))
	})

	issue, err := c.GetIssue(context.Background(), "#7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "7" {
		t.Fatalf("got %q", issue.ID)
	}
	if len(issue.Dependencies) != 0 {
		t.Fatalf("expected no dependencies, got %+v", issue.Dependencies)
	}
}

func TestGetIssue_RejectsFreeformDependencyText(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":1,"body":"## Dependencies\nthis depends on stuff\n"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	if err == nil {
		t.Fatal("expected an error for freeform dependency text")
	}
}

func TestGetIssue_AppliesConfiguredOverrides(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies\n- #1\n"}`))
	})
	c.DependencyOverrides = map[string][]string{"42": {"99"}}

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issue.Dependencies) != 1 || issue.Dependencies[0].DependsOnID != "99" {
		t.Fatalf("expected override to win, got %+v", issue.Dependencies)
	}
}

func TestGetIssues_FetchesMultiple(t *testing.T) {
	seen := map[string]bool{}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/1":
			_, _ = w.Write([]byte(`{"number":1,"body":""}`))
		case "/repos/acme/widgets/issues/2":
			_, _ = w.Write([]byte(`{"number":2,"body":""}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	issues, err := c.GetIssues(context.Background(), []string{"1", "2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if !seen["/repos/acme/widgets/issues/1"] || !seen["/repos/acme/widgets/issues/2"] {
		t.Fatalf("did not fetch both issues: %v", seen)
	}
}

func TestCreateIssue_PostsAndNormalizesResponse(t *testing.T) {
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/repos/acme/widgets/issues" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":99,"html_url":"https://github.com/acme/widgets/issues/99"}`))
	})

	created, err := c.CreateIssue(context.Background(), tracker.IssueRequest{Title: "New thing", Body: "desc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["title"] != "New thing" || gotBody["body"] != "desc" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	if created.ID != "99" {
		t.Fatalf("got ID %q, want 99", created.ID)
	}
	if created.URL != "https://github.com/acme/widgets/issues/99" {
		t.Fatalf("got URL %q", created.URL)
	}
}

func TestCreateIssue_ThenGetIssue_FetchesTheCreatedIssue(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/issues":
			_, _ = w.Write([]byte(`{"number":5,"title":"Created","html_url":"https://github.com/acme/widgets/issues/5"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues/5":
			_, _ = w.Write([]byte(`{"number":5,"title":"Created","body":"## Dependencies: None"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	created, err := c.CreateIssue(context.Background(), tracker.IssueRequest{Title: "Created"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	fetched, err := c.GetIssue(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if fetched.ID != created.ID || fetched.Title != "Created" {
		t.Fatalf("fetched issue %+v does not match created issue %+v", fetched, created)
	}
}

func TestUpdateIssue_PatchesBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	err := c.UpdateIssue(context.Background(), "42", tracker.UpdateIssueRequest{Body: "new body"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("got method %q, want PATCH", gotMethod)
	}
	if gotPath != "/repos/acme/widgets/issues/42" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody["body"] != "new body" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
}

func TestCapabilities_PlanningMirrorIsFalse(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Capabilities should not make a network request")
	})

	caps := c.Capabilities()
	if caps.PlanningMirror {
		t.Fatal("expected PlanningMirror to be false for MVP")
	}
}
