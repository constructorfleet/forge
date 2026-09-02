package gitlab_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

// GitLab reports one merge obstacle at a time, so the verdict carries at most
// one blocker. The raw GitLab string must survive on the blocker.
func TestEvaluateMergeEligibility_ReportsGitLabsSingleReason(t *testing.T) {
	cases := []struct {
		name        string
		reason      string
		wantBlocker *tracker.MergeBlocker
	}{
		{name: "mergeable", reason: "mergeable"},
		{
			name:        "conflict",
			reason:      "conflict",
			wantBlocker: &tracker.MergeBlocker{Reason: tracker.Conflict, Source: tracker.CapabilitySCM, RawDetail: "conflict"},
		},
		{
			name:        "ci must pass",
			reason:      "ci_must_pass",
			wantBlocker: &tracker.MergeBlocker{Reason: tracker.ChecksFailing, Source: tracker.CapabilityCI, RawDetail: "ci_must_pass"},
		},
		{
			name:        "not approved",
			reason:      "not_approved",
			wantBlocker: &tracker.MergeBlocker{Reason: tracker.NotApproved, Source: tracker.CapabilitySCM, RawDetail: "not_approved"},
		},
		{
			name:        "need rebase",
			reason:      "need_rebase",
			wantBlocker: &tracker.MergeBlocker{Reason: tracker.Behind, Source: tracker.CapabilitySCM, RawDetail: "need_rebase"},
		},
		{
			name:        "another reason",
			reason:      "discussions_not_resolved",
			wantBlocker: &tracker.MergeBlocker{Reason: tracker.Blocked, Source: tracker.CapabilitySCM, RawDetail: "discussions_not_resolved"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if got := r.URL.EscapedPath(); got != "/projects/"+escapedProject+"/merge_requests/7" {
					t.Fatalf("path = %q, want the merge request endpoint", got)
				}
				_, _ = w.Write([]byte(`{"iid":7,"state":"opened","detailed_merge_status":"` + tc.reason + `"}`))
			})

			verdict, err := c.EvaluateMergeEligibility(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7})
			if err != nil {
				t.Fatalf("EvaluateMergeEligibility: %v", err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1: one merge request read answers the whole verdict", requests)
			}
			if tc.wantBlocker == nil {
				if !verdict.Mergeable {
					t.Fatalf("Mergeable = false, want true: %+v", verdict.Blockers)
				}
				if len(verdict.Blockers) != 0 {
					t.Fatalf("Blockers = %+v, want none", verdict.Blockers)
				}
				return
			}
			if verdict.Mergeable {
				t.Fatal("Mergeable = true, want false")
			}
			if len(verdict.Blockers) != 1 {
				t.Fatalf("Blockers = %+v, want exactly one", verdict.Blockers)
			}
			if verdict.Blockers[0] != *tc.wantBlocker {
				t.Fatalf("blocker = %+v, want %+v", verdict.Blockers[0], *tc.wantBlocker)
			}
		})
	}
}

func TestEvaluateMergeEligibility_ReportsAReadFailure(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})

	if _, err := c.EvaluateMergeEligibility(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7}); err == nil {
		t.Fatal("expected an error, got nil")
	}
}
