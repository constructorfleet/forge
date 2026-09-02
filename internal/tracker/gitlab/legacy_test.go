package gitlab_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestLegacyPullRequestMethodsAdaptToGitLabMergeRequests(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == mergeRequestsPath:
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.EscapedPath() == mergeRequestsPath:
			_, _ = w.Write([]byte(`{"iid":7,"web_url":"https://gitlab.example.com/mr/7"}`))
		case r.Method == http.MethodGet && r.URL.EscapedPath() == mergeRequestsPath+"/7":
			_, _ = w.Write([]byte(`{
				"iid":7,
				"state":"opened",
				"target_branch":"main",
				"detailed_merge_status":"need_rebase",
				"pipeline":{"status":"failed"}
			}`))
		case r.Method == http.MethodGet && r.URL.EscapedPath() == mergeRequestsPath+"/7/approvals":
			_, _ = w.Write([]byte(`{"approved":true,"approved_by":[{"user":{"username":"alice"}}]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	})

	pr, err := c.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base: "main", Head: "forge/exec/290", Title: "Ship it", Body: "body",
	})
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	if pr.Number != 7 || pr.URL != "https://gitlab.example.com/mr/7" {
		t.Fatalf("pull request = %+v, want GitLab merge request identity", pr)
	}

	checks, err := c.GetPullRequestChecks(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetPullRequestChecks: %v", err)
	}
	if len(checks) != 1 || checks[0] != (tracker.PullRequestCheck{Name: "pipeline", State: tracker.CheckFailure, Details: "failed"}) {
		t.Fatalf("checks = %+v, want the failed pipeline check", checks)
	}

	status, err := c.GetPullRequestMergeStatus(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetPullRequestMergeStatus: %v", err)
	}
	if status != (tracker.PullRequestMergeStatus{Behind: true, RawDetail: "need_rebase"}) {
		t.Fatalf("status = %+v, want the normalized stale state", status)
	}

	base, err := c.GetPullRequestTargetBranch(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetPullRequestTargetBranch: %v", err)
	}
	if base != "main" {
		t.Fatalf("target branch = %q, want main", base)
	}

	reviews, err := c.GetPullRequestReviews(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetPullRequestReviews: %v", err)
	}
	if len(reviews) != 1 || reviews[0].Author != "alice" || reviews[0].State != tracker.ReviewApproved {
		t.Fatalf("reviews = %+v, want Alice approval", reviews)
	}
}
