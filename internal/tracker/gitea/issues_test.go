package gitea_test

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
	if issue.Provider != "gitea" {
		t.Fatalf("got Provider %q, want gitea", issue.Provider)
	}
	if issue.Title != "Do the thing" {
		t.Fatalf("got Title %q, want %q", issue.Title, "Do the thing")
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("got dependencies %v, want [1 2]", got)
	}
	if issue.Dependencies[0].IssueRef != (domain.IssueRef{Provider: "gitea", ID: "42"}) {
		t.Fatalf("unexpected dependency issue ref: %+v", issue.Dependencies[0])
	}
	if issue.Dependencies[0].DependsOnRef != (domain.IssueRef{Provider: "gitea", ID: "1"}) {
		t.Fatalf("unexpected dependency depends-on ref: %+v", issue.Dependencies[0])
	}
}

func TestGetIssue_DoesNotAssignScope(t *testing.T) {
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

	if _, err := c.GetIssue(context.Background(), "1"); err == nil {
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
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "99" {
		t.Fatalf("expected override to win, got %v", got)
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
		_, _ = w.Write([]byte(`{"number":99,"html_url":"https://gitea.example.com/acme/widgets/issues/99"}`))
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
	if created.URL != "https://gitea.example.com/acme/widgets/issues/99" {
		t.Fatalf("got URL %q", created.URL)
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
	if caps.NativeDependencyLinks {
		t.Fatal("expected NativeDependencyLinks to be false for a body-block adapter")
	}
}
