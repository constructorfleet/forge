package gitlab_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

// GitLab gates a merge request on one pipeline, not on many named checks. The
// adapter therefore reports exactly one check named "pipeline".
func TestGetChecks_ReportsThePipelineAsOneCheck(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		wantState tracker.CheckState
	}{
		{name: "success", status: "success", wantState: tracker.CheckSuccess},
		{name: "failed", status: "failed", wantState: tracker.CheckFailure},
		{name: "canceled", status: "canceled", wantState: tracker.CheckFailure},
		{name: "running", status: "running", wantState: tracker.CheckPending},
		{name: "pending", status: "pending", wantState: tracker.CheckPending},
		{name: "created", status: "created", wantState: tracker.CheckPending},
		{name: "waiting for resource", status: "waiting_for_resource", wantState: tracker.CheckPending},
		{name: "preparing", status: "preparing", wantState: tracker.CheckPending},
		{name: "scheduled", status: "scheduled", wantState: tracker.CheckPending},
		{name: "manual", status: "manual", wantState: tracker.CheckPending},
		{name: "skipped", status: "skipped", wantState: tracker.CheckPending},
		{name: "unknown", status: "a_status_forge_does_not_know", wantState: tracker.CheckPending},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if got := r.URL.EscapedPath(); got != "/projects/"+escapedProject+"/merge_requests/7" {
					t.Fatalf("path = %q, want the merge request endpoint", got)
				}
				_, _ = w.Write([]byte(`{"iid":7,"pipeline":{"status":"` + tc.status + `"}}`))
			})

			checks, err := c.GetChecks(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7})
			if err != nil {
				t.Fatalf("GetChecks: %v", err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1: the merge request already carries its pipeline", requests)
			}
			if len(checks) != 1 {
				t.Fatalf("checks = %+v, want exactly one check", checks)
			}
			want := tracker.Check{Name: "pipeline", State: tc.wantState, Details: tc.status}
			if checks[0] != want {
				t.Fatalf("check = %+v, want %+v", checks[0], want)
			}
		})
	}
}

// A project that ran no pipeline yet reports none. That is not a failure, so
// the adapter reports no checks and no error.
func TestGetChecks_ReportsNoChecksWhenNoPipelineRan(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"pipeline":null}`))
	})

	checks, err := c.GetChecks(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7})
	if err != nil {
		t.Fatalf("GetChecks: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("checks = %+v, want none", checks)
	}
	if checks == nil {
		t.Fatal("checks = nil, want an empty slice")
	}
}
