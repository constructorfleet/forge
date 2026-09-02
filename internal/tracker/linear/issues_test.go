package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

// fakeLinearServer dispatches a fixed GraphQL response by matching a
// substring of the incoming query, so a test can stub out the sequence of
// queries one adapter call issues without a real Linear workspace.
func fakeLinearServer(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for match, resp := range responses {
			if strings.Contains(body.Query, match) {
				_, _ = w.Write([]byte(resp))
				return
			}
		}
		t.Fatalf("no fake response configured for query: %s", body.Query)
	}))
}

func TestGetIssueResolvesIdentifierAndNormalizes(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-345"}]}}}`,
		"issue(id":      `{"data":{"issue":{"id":"uuid-1","identifier":"FOR-345","title":"Do the thing","description":"body text","url":"https://linear.app/x/issue/FOR-345","state":{"type":"started"},"inverseRelations":{"nodes":[]}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	issue, err := c.GetIssue(context.Background(), "FOR-345")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.ID != "FOR-345" {
		t.Fatalf("ID = %q, want FOR-345", issue.ID)
	}
	if issue.Provider != "linear" {
		t.Fatalf("Provider = %q, want linear", issue.Provider)
	}
	if issue.Title != "Do the thing" {
		t.Fatalf("Title = %q", issue.Title)
	}
	if issue.Body != "body text" {
		t.Fatalf("Body = %q", issue.Body)
	}
	if len(issue.Dependencies) != 0 {
		t.Fatalf("Dependencies = %v, want none", issue.Dependencies)
	}
}

func TestGetIssueUnknownIdentifierReturnsNotFound(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[]}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	_, err := c.GetIssue(context.Background(), "FOR-999")
	if err == nil {
		t.Fatalf("GetIssue() = nil error, want not-found")
	}
}

func TestGetIssuesFetchesEachSequentially(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`,
		"issue(id":      `{"data":{"issue":{"id":"uuid-1","identifier":"FOR-1","title":"T","description":"","url":"","state":{"type":"backlog"},"inverseRelations":{"nodes":[]}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	issues, err := c.GetIssues(context.Background(), []string{"FOR-1"})
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "FOR-1" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestCreateIssueReturnsIdentityFromResponse(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"teams(filter": `{"data":{"teams":{"nodes":[{"id":"team-uuid","key":"FOR"}]}}}`,
		"issueCreate":  `{"data":{"issueCreate":{"success":true,"issue":{"id":"uuid-2","identifier":"FOR-2","url":"https://linear.app/x/issue/FOR-2"}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	created, err := c.CreateIssue(context.Background(), tracker.IssueRequest{Title: "Title", Body: "Body"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.ID != "FOR-2" {
		t.Fatalf("ID = %q, want FOR-2", created.ID)
	}
	if created.URL != "https://linear.app/x/issue/FOR-2" {
		t.Fatalf("URL = %q", created.URL)
	}
}

func TestCreateIssueUsesResolvedTeamID(t *testing.T) {
	var createVars map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.Contains(body.Query, "teams(filter"):
			if body.Variables["key"] != "FOR" {
				t.Fatalf("team lookup variables = %v, want key=FOR", body.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"team-uuid","key":"FOR"}]}}}`))
		case strings.Contains(body.Query, "issueCreate"):
			createVars = body.Variables
			_, _ = w.Write([]byte(`{"data":{"issueCreate":{"success":true,"issue":{"id":"uuid-2","identifier":"FOR-2","url":"https://linear.app/x/issue/FOR-2"}}}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	if _, err := c.CreateIssue(context.Background(), tracker.IssueRequest{Title: "Title", Body: "Body"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if createVars["teamId"] != "team-uuid" {
		t.Fatalf("CreateIssue teamId = %v, want resolved team uuid", createVars["teamId"])
	}
}

func TestUpdateIssueResolvesIdentifierThenUpdates(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`,
		"issueUpdate":   `{"data":{"issueUpdate":{"success":true}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.UpdateIssue(context.Background(), "FOR-1", tracker.UpdateIssueRequest{Body: "new body"}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
}

func TestNormalizeIssueMapsWorkflowStateType(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`,
		"issue(id":      `{"data":{"issue":{"id":"uuid-1","identifier":"FOR-1","title":"T","description":"","url":"","state":{"type":"completed"},"inverseRelations":{"nodes":[]}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	issue, err := c.GetIssue(context.Background(), "FOR-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	// domain.Issue.State is Forge's own orchestration lifecycle, not a
	// mirror of Linear's workflow state (see the GitHub/GitLab adapters'
	// normalizeIssue): it stays at its zero value here.
	if issue.State != "" {
		t.Fatalf("State = %q, want zero value", issue.State)
	}
}
