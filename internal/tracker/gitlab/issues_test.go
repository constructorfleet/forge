package gitlab_test

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
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		if r.URL.EscapedPath() != "/projects/"+escapedProject+"/issues/42" {
			t.Fatalf("unexpected path: %s", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9001,"iid":42,"title":"Do the thing","description":"desc\n\n## Dependencies\n- #1\n- #2\n","web_url":"https://gitlab.com/acme/widgets/-/issues/42"}`))
	})

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The project-scoped internal id (iid), not the global id, is the Forge
	// Issue ID.
	if issue.ID != "42" {
		t.Fatalf("got ID %q, want 42", issue.ID)
	}
	if issue.Provider != "gitlab" {
		t.Fatalf("got Provider %q, want gitlab", issue.Provider)
	}
	if issue.Title != "Do the thing" {
		t.Fatalf("got Title %q, want %q", issue.Title, "Do the thing")
	}
	if issue.Body != "desc\n\n## Dependencies\n- #1\n- #2\n" {
		t.Fatalf("got Body %q, want the raw issue description", issue.Body)
	}
	if len(issue.Dependencies) != 2 {
		t.Fatalf("got %d dependencies, want 2: %+v", len(issue.Dependencies), issue.Dependencies)
	}
	if issue.Dependencies[0].IssueID != "42" || issue.Dependencies[0].DependsOnID != "1" {
		t.Fatalf("unexpected dependency: %+v", issue.Dependencies[0])
	}
	if issue.Dependencies[0].IssueRef != (domain.IssueRef{Provider: "gitlab", ID: "42"}) {
		t.Fatalf("unexpected dependency issue ref: %+v", issue.Dependencies[0])
	}
	if issue.Dependencies[0].DependsOnRef != (domain.IssueRef{Provider: "gitlab", ID: "1"}) {
		t.Fatalf("unexpected dependency depends-on ref: %+v", issue.Dependencies[0])
	}
}

func TestGetIssue_StampsConfiguredProvider(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":1,"description":""}`))
	})
	c.Provider = "gitlab-self-managed"

	issue, err := c.GetIssue(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Provider != "gitlab-self-managed" {
		t.Fatalf("got Provider %q, want gitlab-self-managed", issue.Provider)
	}
}

func TestGetIssue_DoesNotAssignScope(t *testing.T) {
	// Managed-vs-External is execution-set membership, which the
	// scheduler/DAG owns — not the tracker adapter.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":1,"description":"## Dependencies: None"}`))
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
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		if r.URL.EscapedPath() != "/projects/"+escapedProject+"/issues/7" {
			t.Fatalf("unexpected path: %s", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"iid":7,"description":"## Dependencies: None"}`))
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

func TestGetIssue_RejectsNonNumericID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("expected no HTTP request for an invalid issue id")
	})

	if _, err := c.GetIssue(context.Background(), "not-a-number"); err == nil {
		t.Fatal("expected an error for a non-numeric issue id")
	}
}

func TestGetIssue_RejectsFreeformDependencyText(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":1,"description":"## Dependencies\nthis depends on stuff\n"}`))
	})

	if _, err := c.GetIssue(context.Background(), "1"); err == nil {
		t.Fatal("expected an error for freeform dependency text")
	}
}

func TestGetIssue_AppliesConfiguredOverrides(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"description":"## Dependencies\n- #1\n"}`))
	})
	c.DependencyOverrides = map[string][]string{"42": {"99"}}

	issue, err := c.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := dependsOnIDs(issue.Dependencies); len(got) != 1 || got[0] != "99" {
		t.Fatalf("expected override [99] to win, got %v", got)
	}
}

func TestGetIssues_FetchesMultiple(t *testing.T) {
	seen := map[string]bool{}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		seen[r.URL.EscapedPath()] = true
		switch r.URL.EscapedPath() {
		case "/projects/" + escapedProject + "/issues/1":
			_, _ = w.Write([]byte(`{"iid":1,"description":""}`))
		case "/projects/" + escapedProject + "/issues/2":
			_, _ = w.Write([]byte(`{"iid":2,"description":""}`))
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
	if issues[0].ID != "1" || issues[1].ID != "2" {
		t.Fatalf("unexpected issue order: %+v", issues)
	}
	if !seen["/projects/"+escapedProject+"/issues/1"] || !seen["/projects/"+escapedProject+"/issues/2"] {
		t.Fatalf("did not fetch both issues: %v", seen)
	}
}

func TestCreateIssue_PostsAndNormalizesResponse(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9002,"iid":99,"web_url":"https://gitlab.com/acme/widgets/-/issues/99"}`))
	})

	created, err := c.CreateIssue(context.Background(), tracker.IssueRequest{Title: "New thing", Body: "desc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("got method %q, want POST", gotMethod)
	}
	if gotPath != "/projects/"+escapedProject+"/issues" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	// GitLab names the issue body "description", not "body".
	if gotBody["title"] != "New thing" || gotBody["description"] != "desc" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	if created.ID != "99" {
		t.Fatalf("got ID %q, want the iid 99", created.ID)
	}
	if created.URL != "https://gitlab.com/acme/widgets/-/issues/99" {
		t.Fatalf("got URL %q", created.URL)
	}
}

func TestCreateIssue_ThenGetIssue_FetchesTheCreatedIssue(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/projects/"+escapedProject+"/issues":
			_, _ = w.Write([]byte(`{"iid":5,"title":"Created","web_url":"https://gitlab.com/acme/widgets/-/issues/5"}`))
		case isLinksPath(r):
			serveNoNativeLinks(w)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/projects/"+escapedProject+"/issues/5":
			_, _ = w.Write([]byte(`{"iid":5,"title":"Created","description":"## Dependencies: None"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
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

func TestUpdateIssue_PutsDescription(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	err := c.UpdateIssue(context.Background(), "42", tracker.UpdateIssueRequest{Body: "new body"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("got method %q, want PUT", gotMethod)
	}
	if gotPath != "/projects/"+escapedProject+"/issues/42" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody["description"] != "new body" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
}
