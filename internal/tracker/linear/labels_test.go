package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddLabelCreatesLabelWhenMissingAndAttaches(t *testing.T) {
	var updateVars map[string]interface{}
	var labelCreated bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.Contains(body.Query, "teams(filter"):
			_, _ = w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"team-uuid","key":"FOR"}]}}}`))
		case strings.Contains(body.Query, "issues(filter"):
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`))
		case strings.Contains(body.Query, "issueLabels("):
			if body.Variables["teamId"] != "team-uuid" {
				t.Fatalf("issueLabels teamId = %v, want resolved team uuid", body.Variables["teamId"])
			}
			_, _ = w.Write([]byte(`{"data":{"issueLabels":{"nodes":[]}}}`))
		case strings.Contains(body.Query, "issueLabelCreate"):
			labelCreated = true
			if body.Variables["teamId"] != "team-uuid" {
				t.Fatalf("issueLabelCreate teamId = %v, want resolved team uuid", body.Variables["teamId"])
			}
			_, _ = w.Write([]byte(`{"data":{"issueLabelCreate":{"success":true,"issueLabel":{"id":"label-1"}}}}`))
		case strings.Contains(body.Query, "issue(id"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"labels":{"nodes":[]}}}}`))
		case strings.Contains(body.Query, "issueUpdate"):
			updateVars = body.Variables
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.AddLabel(context.Background(), "FOR-1", "needs-triage"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if !labelCreated {
		t.Fatalf("expected issueLabelCreate to be called for a missing label")
	}
	ids, ok := updateVars["labelIds"].([]interface{})
	if !ok || len(ids) != 1 || ids[0] != "label-1" {
		t.Fatalf("updateVars[labelIds] = %v, want [label-1]", updateVars["labelIds"])
	}
}

func TestAddLabelIsIdempotentWhenAlreadyPresent(t *testing.T) {
	var updateCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.Contains(body.Query, "teams(filter"):
			_, _ = w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"team-uuid","key":"FOR"}]}}}`))
		case strings.Contains(body.Query, "issues(filter"):
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`))
		case strings.Contains(body.Query, "issueLabels("):
			_, _ = w.Write([]byte(`{"data":{"issueLabels":{"nodes":[{"id":"label-1","name":"needs-triage"}]}}}`))
		case strings.Contains(body.Query, "issue(id"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"labels":{"nodes":[{"id":"label-1"}]}}}}`))
		default:
			updateCalled = true
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.AddLabel(context.Background(), "FOR-1", "needs-triage"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if updateCalled {
		t.Fatalf("AddLabel issued an update when the label was already present")
	}
}

func TestRemoveLabelIsIdempotentWhenAbsent(t *testing.T) {
	var updateCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.Contains(body.Query, "teams(filter"):
			_, _ = w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"team-uuid","key":"FOR"}]}}}`))
		case strings.Contains(body.Query, "issues(filter"):
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`))
		case strings.Contains(body.Query, "issueLabels("):
			_, _ = w.Write([]byte(`{"data":{"issueLabels":{"nodes":[{"id":"label-1","name":"needs-triage"}]}}}`))
		case strings.Contains(body.Query, "issue(id"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"labels":{"nodes":[]}}}}`))
		default:
			updateCalled = true
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.RemoveLabel(context.Background(), "FOR-1", "needs-triage"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	if updateCalled {
		t.Fatalf("RemoveLabel issued an update when the label was already absent")
	}
}

func TestRemoveLabelDetachesPresentLabel(t *testing.T) {
	var updateVars map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.Contains(body.Query, "teams(filter"):
			_, _ = w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"team-uuid","key":"FOR"}]}}}`))
		case strings.Contains(body.Query, "issues(filter"):
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`))
		case strings.Contains(body.Query, "issueLabels("):
			_, _ = w.Write([]byte(`{"data":{"issueLabels":{"nodes":[{"id":"label-1","name":"needs-triage"}]}}}`))
		case strings.Contains(body.Query, "issue(id"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"labels":{"nodes":[{"id":"label-1"},{"id":"label-2"}]}}}}`))
		case strings.Contains(body.Query, "issueUpdate"):
			updateVars = body.Variables
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.RemoveLabel(context.Background(), "FOR-1", "needs-triage"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	ids, ok := updateVars["labelIds"].([]interface{})
	if !ok || len(ids) != 1 || ids[0] != "label-2" {
		t.Fatalf("updateVars[labelIds] = %v, want [label-2]", updateVars["labelIds"])
	}
}
