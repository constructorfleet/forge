package gitlab_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/gitlab"
)

// GitLab reports one merge obstacle at a time in detailed_merge_status. Each
// obstacle must map to one neutral reason, and the raw GitLab string must
// survive on RawDetail for a human reader.
func TestMapDetailedMergeStatus_MapsEveryReasonToTheNeutralEnum(t *testing.T) {
	cases := []struct {
		reason      string
		wantBlocked bool
		wantReason  tracker.MergeBlockerReason
		wantSource  tracker.Capability
	}{
		{reason: "mergeable", wantBlocked: false},
		// An absent field means GitLab did not compute the status yet. An
		// unset field must not report a blocker.
		{reason: "", wantBlocked: false},
		{reason: "ci_must_pass", wantBlocked: true, wantReason: tracker.ChecksFailing, wantSource: tracker.CapabilityCI},
		{reason: "ci_still_running", wantBlocked: true, wantReason: tracker.ChecksPending, wantSource: tracker.CapabilityCI},
		{reason: "not_approved", wantBlocked: true, wantReason: tracker.NotApproved, wantSource: tracker.CapabilitySCM},
		{reason: "conflict", wantBlocked: true, wantReason: tracker.Conflict, wantSource: tracker.CapabilitySCM},
		{reason: "need_rebase", wantBlocked: true, wantReason: tracker.Behind, wantSource: tracker.CapabilitySCM},
		{reason: "draft_status", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "discussions_not_resolved", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "merge_request_blocked", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "checking", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "unchecked", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "broken_status", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "policies_denied", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "jira_association_missing", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "requested_changes", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "external_status_checks", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "preparing", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
		{reason: "a_reason_forge_does_not_know", wantBlocked: true, wantReason: tracker.Blocked, wantSource: tracker.CapabilitySCM},
	}

	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			got := gitlab.MapDetailedMergeStatus(tc.reason)
			if !tc.wantBlocked {
				if got != nil {
					t.Fatalf("blocker = %+v, want nil for %q", *got, tc.reason)
				}
				return
			}
			if got == nil {
				t.Fatalf("blocker = nil, want reason %q for %q", tc.wantReason, tc.reason)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Source != tc.wantSource {
				t.Fatalf("Source = %q, want %q", got.Source, tc.wantSource)
			}
			if got.RawDetail != tc.reason {
				t.Fatalf("RawDetail = %q, want the raw GitLab string %q", got.RawDetail, tc.reason)
			}
		})
	}
}

func TestGetChangeRequestMergeStatus_NormalizesTheMergeRequestState(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		wantMerged    bool
		wantConflict  bool
		wantBehind    bool
		wantRawDetail string
	}{
		{
			name:          "mergeable",
			body:          `{"iid":7,"state":"opened","detailed_merge_status":"mergeable","has_conflicts":false,"diverged_commits_count":0}`,
			wantRawDetail: "mergeable",
		},
		{
			name:          "conflict",
			body:          `{"iid":7,"state":"opened","detailed_merge_status":"conflict","has_conflicts":true}`,
			wantConflict:  true,
			wantRawDetail: "conflict",
		},
		{
			name:          "need rebase",
			body:          `{"iid":7,"state":"opened","detailed_merge_status":"need_rebase","diverged_commits_count":3}`,
			wantBehind:    true,
			wantRawDetail: "need_rebase",
		},
		{
			name:          "merged",
			body:          `{"iid":7,"state":"merged","detailed_merge_status":"mergeable"}`,
			wantMerged:    true,
			wantRawDetail: "mergeable",
		},
		{
			// An older instance reports merge_status and has_conflicts only.
			name:          "older instance falls back to the coarse fields",
			body:          `{"iid":7,"state":"opened","merge_status":"cannot_be_merged","has_conflicts":true,"diverged_commits_count":2}`,
			wantConflict:  true,
			wantBehind:    true,
			wantRawDetail: "cannot_be_merged",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.EscapedPath(); got != "/projects/"+escapedProject+"/merge_requests/7" {
					t.Fatalf("path = %q, want the merge request endpoint", got)
				}
				_, _ = w.Write([]byte(tc.body))
			})

			status, err := c.GetChangeRequestMergeStatus(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7})
			if err != nil {
				t.Fatalf("GetChangeRequestMergeStatus: %v", err)
			}
			want := tracker.ChangeRequestMergeStatus{
				Merged:     tc.wantMerged,
				Conflicted: tc.wantConflict,
				Behind:     tc.wantBehind,
				RawDetail:  tc.wantRawDetail,
			}
			if status != want {
				t.Fatalf("status = %+v, want %+v", status, want)
			}
		})
	}
}
