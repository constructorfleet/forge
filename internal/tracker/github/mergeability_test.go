package github_test

import (
	"context"
	"net/http"
	"testing"
)

func TestGetPullRequestMergeStatus(t *testing.T) {
	cases := []struct {
		mergeableState string
		wantConflicted bool
	}{
		{"clean", false},
		{"dirty", true},
		{"unknown", false},
		{"blocked", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.mergeableState, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/acme/widgets/pulls/23" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"mergeable_state":"` + tc.mergeableState + `"}`))
			})

			status, err := c.GetPullRequestMergeStatus(context.Background(), 23)
			if err != nil {
				t.Fatalf("GetPullRequestMergeStatus: %v", err)
			}
			if status.Conflicted != tc.wantConflicted {
				t.Fatalf("Conflicted = %v, want %v", status.Conflicted, tc.wantConflicted)
			}
		})
	}
}
