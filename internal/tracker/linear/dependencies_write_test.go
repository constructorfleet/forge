package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteDependenciesCreatesAndDeletesRelations(t *testing.T) {
	var createVars, deleteVars map[string]interface{}
	var resolveCalls []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		switch {
		case strings.Contains(body.Query, "issues(filter"):
			identifier, _ := body.Variables["identifier"].(string)
			resolveCalls = append(resolveCalls, identifier)
			id := "uuid-" + identifier
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"` + id + `","identifier":"` + identifier + `"}]}}}`))
		case strings.Contains(body.Query, "issue(id"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"id":"uuid-FOR-1","identifier":"FOR-1","title":"T","description":"","url":"",
				"state":{"type":"started"},
				"inverseRelations":{"nodes":[
					{"id":"rel-old","type":"blocks","issue":{"identifier":"FOR-9"}},
					{"id":"rel-keep","type":"blocks","issue":{"identifier":"FOR-2"}}
				]}}}}`))
		case strings.Contains(body.Query, "issueRelationCreate"):
			createVars = body.Variables
			_, _ = w.Write([]byte(`{"data":{"issueRelationCreate":{"success":true}}}`))
		case strings.Contains(body.Query, "issueRelationDelete"):
			deleteVars = body.Variables
			_, _ = w.Write([]byte(`{"data":{"issueRelationDelete":{"success":true}}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	// FOR-1 currently blocked by FOR-9 and FOR-2; target set is FOR-2, FOR-3:
	// FOR-9's relation should be deleted, FOR-2 kept, FOR-3 created.
	if err := c.WriteDependencies(context.Background(), "FOR-1", []string{"FOR-2", "FOR-3"}); err != nil {
		t.Fatalf("WriteDependencies: %v", err)
	}

	if deleteVars["id"] != "rel-old" {
		t.Fatalf("deleteVars = %v, want id=rel-old (the FOR-9 relation)", deleteVars)
	}
	if createVars["issueId"] != "uuid-FOR-3" || createVars["relatedIssueId"] != "uuid-FOR-1" {
		t.Fatalf("createVars = %v, want issueId=uuid-FOR-3 relatedIssueId=uuid-FOR-1", createVars)
	}
}

func TestWriteDependenciesNoOpWhenAlreadyMatching(t *testing.T) {
	var mutationCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.Contains(body.Query, "issues(filter"):
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"uuid-FOR-1","identifier":"FOR-1"}]}}}`))
		case strings.Contains(body.Query, "issue(id"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"id":"uuid-FOR-1","identifier":"FOR-1","title":"T","description":"","url":"",
				"state":{"type":"started"},
				"inverseRelations":{"nodes":[{"id":"rel-1","type":"blocks","issue":{"identifier":"FOR-2"}}]}}}}`))
		default:
			mutationCalled = true
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.WriteDependencies(context.Background(), "FOR-1", []string{"FOR-2"}); err != nil {
		t.Fatalf("WriteDependencies: %v", err)
	}
	if mutationCalled {
		t.Fatalf("WriteDependencies issued a mutation when the relation set already matched")
	}
}
