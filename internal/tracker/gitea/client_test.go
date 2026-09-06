package gitea_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/gitea"
)

// dependsOnIDs extracts the prerequisite Issue IDs from a normalized Issue's
// dependency edges, in order, for concise assertions.
func dependsOnIDs(deps []domain.Dependency) []string {
	ids := make([]string, len(deps))
	for i, d := range deps {
		ids[i] = d.DependsOnID
	}
	return ids
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*gitea.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := gitea.NewClient(srv.Client(), srv.URL, "acme", "widgets")
	return c, srv
}

// decodeJSON decodes r's JSON request body into out, failing the test on
// error.
func decodeJSON(t *testing.T, r *http.Request, out interface{}) error {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return nil
}

func TestClient_RateLimit_429(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limit exceeded"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *tracker.RateLimitError, got %T: %v", err, err)
	}
	if rlErr.ResetAt.IsZero() {
		t.Fatal("expected ResetAt to be populated from X-RateLimit-Reset")
	}
}

func TestClient_PlainForbiddenIsNotRateLimit(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"insufficient permissions"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if errors.As(err, &rlErr) {
		t.Fatal("did not expect a RateLimitError for a plain 403")
	}
	var authzErr *gitea.AuthorizationError
	if !errors.As(err, &authzErr) {
		t.Fatalf("expected *gitea.AuthorizationError, got %T: %v", err, err)
	}
}

func TestClient_SendsTokenHeader(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "secret-token")
	var gotAuth string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"number":1,"body":""}`))
	})

	if _, err := c.GetIssue(context.Background(), "1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "token secret-token" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "token secret-token")
	}
}

func TestClient_Unauthorized(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := c.GetIssue(context.Background(), "1")
	var authErr *gitea.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *gitea.AuthenticationError, got %T: %v", err, err)
	}
}
