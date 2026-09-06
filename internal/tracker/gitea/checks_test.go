package gitea_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetPullRequestChecks_NormalizesCommitStatuses(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/pulls/3":
			_, _ = w.Write([]byte(`{"number":3,"head":{"sha":"abc123"}}`))
		case "/repos/acme/widgets/commits/abc123/status":
			_, _ = w.Write([]byte(`{"statuses":[` +
				`{"context":"build","status":"success","description":"ok"},` +
				`{"context":"test","status":"failure","description":"boom"},` +
				`{"context":"lint","status":"pending","description":"running"}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})

	checks, err := c.GetPullRequestChecks(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := map[string]tracker.CheckState{}
	for _, chk := range checks {
		byName[chk.Name] = chk.State
	}
	if byName["build"] != tracker.CheckSuccess {
		t.Errorf("build = %q, want SUCCESS", byName["build"])
	}
	if byName["test"] != tracker.CheckFailure {
		t.Errorf("test = %q, want FAILURE", byName["test"])
	}
	if byName["lint"] != tracker.CheckPending {
		t.Errorf("lint = %q, want PENDING", byName["lint"])
	}
}

func TestGetChecks_AdaptsNeutralCapability(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/pulls/3":
			_, _ = w.Write([]byte(`{"number":3,"head":{"sha":"deadbeef"}}`))
		case "/repos/acme/widgets/commits/deadbeef/status":
			_, _ = w.Write([]byte(`{"statuses":[{"context":"ci","status":"success","description":""}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})

	checks, err := c.GetChecks(context.Background(), tracker.ChangeRequestRef{Provider: "gitea", Number: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checks) != 1 || checks[0].Name != "ci" || checks[0].State != tracker.CheckSuccess {
		t.Fatalf("unexpected checks: %+v", checks)
	}
}
