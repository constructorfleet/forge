package gitea_test

import (
	"context"
	"net/http"
	"testing"
)

func TestGetMergeRequirements_FromBranchProtection(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/branch_protections/main" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"enable_status_check":true,"status_check_contexts":["build","test","build"]}`))
	})

	reqs, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reqs.RequiredChecks) != 2 {
		t.Fatalf("got %v, want deduped [build test]", reqs.RequiredChecks)
	}
	if reqs.RequiredChecks[0] != "build" || reqs.RequiredChecks[1] != "test" {
		t.Fatalf("unexpected required checks: %v", reqs.RequiredChecks)
	}
}

func TestGetMergeRequirements_NoProtectionIsEmpty(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	reqs, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("expected no error for an unprotected branch, got: %v", err)
	}
	if len(reqs.RequiredChecks) != 0 {
		t.Fatalf("expected no required checks, got %v", reqs.RequiredChecks)
	}
}

func TestGetMergeRequirements_StatusCheckDisabledIsEmpty(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"enable_status_check":false,"status_check_contexts":["build"]}`))
	})

	reqs, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reqs.RequiredChecks) != 0 {
		t.Fatalf("expected no required checks when status checks are disabled, got %v", reqs.RequiredChecks)
	}
}
