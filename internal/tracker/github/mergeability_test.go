package github_test

import (
	"context"
	"net/http"
	"testing"
)

func TestGetPullRequestMergeStatus(t *testing.T) {
	cases := []struct {
		mergeableState string
		merged         bool
		wantConflicted bool
		wantBehind     bool
		wantMerged     bool
	}{
		{"clean", false, false, false, false},
		{"dirty", false, true, false, false},
		{"behind", false, false, true, false},
		{"unknown", false, false, false, false},
		{"blocked", false, false, false, false},
		{"clean", true, false, false, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.mergeableState, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/acme/widgets/pulls/23" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				merged := "false"
				if tc.merged {
					merged = "true"
				}
				_, _ = w.Write([]byte(`{"mergeable_state":"` + tc.mergeableState + `","merged":` + merged + `}`))
			})

			status, err := c.GetPullRequestMergeStatus(context.Background(), 23)
			if err != nil {
				t.Fatalf("GetPullRequestMergeStatus: %v", err)
			}
			if status.Conflicted != tc.wantConflicted {
				t.Fatalf("Conflicted = %v, want %v", status.Conflicted, tc.wantConflicted)
			}
			if status.Behind != tc.wantBehind {
				t.Fatalf("Behind = %v, want %v", status.Behind, tc.wantBehind)
			}
			if status.Merged != tc.wantMerged {
				t.Fatalf("Merged = %v, want %v", status.Merged, tc.wantMerged)
			}
			if status.RawDetail != tc.mergeableState {
				t.Fatalf("RawDetail = %q, want raw mergeable_state %q", status.RawDetail, tc.mergeableState)
			}
		})
	}
}
