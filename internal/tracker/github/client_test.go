package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/github"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*github.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := github.NewClient(srv.Client(), srv.URL, "acme", "widgets")
	return c, srv
}

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
