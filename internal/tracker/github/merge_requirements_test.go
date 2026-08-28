package github_test

import (
	"context"
	"net/http"
	"sort"
	"testing"
)

func TestGetMergeRequirements_FromBranchProtection(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/branches/main/protection" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"required_status_checks":{"contexts":["build","test"]}}`))
	})

	mr, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := append([]string{}, mr.RequiredChecks...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "build" || got[1] != "test" {
		t.Fatalf("got %v", got)
	}
}

func TestGetMergeRequirements_FallsBackToRulesetsWhenNoProtection(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/branches/main/protection":
			w.WriteHeader(http.StatusNotFound)
		case "/repos/acme/widgets/rules/branches/main":
			_, _ = w.Write([]byte(`[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"lint"}]}}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})

	mr, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mr.RequiredChecks) != 1 || mr.RequiredChecks[0] != "lint" {
		t.Fatalf("got %v", mr.RequiredChecks)
	}
}

func TestGetMergeRequirements_EmptyWhenNeitherConfigured(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mr, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mr.RequiredChecks) != 0 {
		t.Fatalf("expected no required checks, got %v", mr.RequiredChecks)
	}
}
