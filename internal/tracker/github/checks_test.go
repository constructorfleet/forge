package github_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetPullRequestChecks_MergesStatusesAndCheckRuns(t *testing.T) {
	var pullCalls int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/pulls/23":
			pullCalls++
			_, _ = w.Write([]byte(`{"head":{"sha":"abc123"}}`))
		case "/repos/acme/widgets/commits/abc123/status":
			_, _ = w.Write([]byte(`{"statuses":[{"context":"build","state":"success","description":"ok"},{"context":"lint","state":"failure","description":"bad"}]}`))
		case "/repos/acme/widgets/commits/abc123/check-runs":
			_, _ = w.Write([]byte(`{"check_runs":[{"name":"test","status":"completed","conclusion":"success","output":{"title":"passed","summary":"all good"}},{"name":"scan","status":"in_progress","conclusion":null}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})

	checks, err := c.GetPullRequestChecks(context.Background(), 23)
	if err != nil {
		t.Fatalf("GetPullRequestChecks: %v", err)
	}
	if pullCalls != 1 {
		t.Fatalf("pull calls = %d, want 1", pullCalls)
	}

	want := map[string]tracker.CheckState{
		"build": tracker.CheckSuccess,
		"lint":  tracker.CheckFailure,
		"test":  tracker.CheckSuccess,
		"scan":  tracker.CheckPending,
	}
	if len(checks) != len(want) {
		t.Fatalf("got %d checks, want %d: %+v", len(checks), len(want), checks)
	}
	for _, check := range checks {
		if want[check.Name] != check.State {
			t.Fatalf("check %s state = %s, want %s", check.Name, check.State, want[check.Name])
		}
	}
}
