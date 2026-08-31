package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/github"
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

func newTestClient(t *testing.T, handler http.HandlerFunc) (*github.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := github.NewClient(srv.Client(), srv.URL, "acme", "widgets")
	return c, srv
}

// isBlockedByPath reports whether r targets an issue's native "blocked by"
// dependency subresource (…/issues/N/dependencies/blocked_by). GetIssue
// probes it before consulting the body block.
func isBlockedByPath(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/dependencies/blocked_by")
}

// serveNoNativeDeps answers the native blocked_by probe with an empty list,
// so a test exercising body-block parsing gets the body-block fallback.
func serveNoNativeDeps(w http.ResponseWriter) { _, _ = w.Write([]byte("[]")) }

func TestClient_RateLimit_403ZeroRemaining(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var rlErr *tracker.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *tracker.RateLimitError, got %T: %v", err, err)
	}
	if rlErr.ResetAt.IsZero() {
		t.Fatal("expected ResetAt to be populated from X-RateLimit-Reset")
	}
}

func TestClient_RateLimit_429(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"secondary rate limit"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *tracker.RateLimitError, got %T: %v", err, err)
	}
}

func TestClient_RateLimit_403WithRetryAfter(t *testing.T) {
	// GitHub's secondary/abuse rate limit is often a 403 with a
	// Retry-After header and non-zero X-RateLimit-Remaining, distinct from
	// the primary-limit 403+Remaining:0 case.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "42")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *tracker.RateLimitError, got %T: %v", err, err)
	}
	if rlErr.ResetAt.IsZero() {
		t.Fatal("expected ResetAt to be populated from Retry-After")
	}
}

func TestClient_PlainForbiddenIsNotTyped(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"insufficient permissions"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if errors.As(err, &rlErr) {
		t.Fatal("did not expect a RateLimitError for a plain 403 with no rate-limit signals")
	}
}

func TestClient_NonRateLimitErrorIsNotTyped(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var rlErr *tracker.RateLimitError
	if errors.As(err, &rlErr) {
		t.Fatal("did not expect a RateLimitError for a plain 500")
	}
}
