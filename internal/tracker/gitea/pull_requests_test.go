package gitea_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestCreatePullRequest_CreatesWhenNoneExists(t *testing.T) {
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/pulls":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/pulls":
			_ = decodeJSON(t, r, &gotBody)
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://gitea.example.com/acme/widgets/pulls/7"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	pr, err := c.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base: "main", Head: "forge/x/1", Title: "Do", Body: "desc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["head"] != "forge/x/1" || gotBody["base"] != "main" {
		t.Fatalf("unexpected create body: %+v", gotBody)
	}
	if pr.Number != 7 || pr.URL != "https://gitea.example.com/acme/widgets/pulls/7" {
		t.Fatalf("unexpected pull request: %+v", pr)
	}
}

func TestCreatePullRequest_RecoversExistingByHead(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatal("must not create a pull request when one already exists")
		}
		_, _ = w.Write([]byte(`[{"number":9,"html_url":"https://gitea.example.com/acme/widgets/pulls/9","head":{"ref":"forge/x/1"}}]`))
	})

	pr, err := c.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base: "main", Head: "forge/x/1", Title: "Do", Body: "desc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr.Number != 9 {
		t.Fatalf("expected to recover PR 9, got %+v", pr)
	}
}

func TestCreatePullRequest_ConflictTriggersRecovery(t *testing.T) {
	listCalls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listCalls++
			if listCalls == 1 {
				// First lookup finds nothing, so create is attempted.
				_, _ = w.Write([]byte(`[]`))
				return
			}
			// Recovery lookup after the 409 finds the racing PR.
			_, _ = w.Write([]byte(`[{"number":11,"html_url":"u","head":{"ref":"forge/x/1"}}]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"pull request already exists"}`))
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	})

	pr, err := c.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base: "main", Head: "forge/x/1", Title: "Do", Body: "desc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr.Number != 11 {
		t.Fatalf("expected to recover PR 11 after conflict, got %+v", pr)
	}
}

func TestGetPullRequestTargetBranch(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/pulls/4" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"number":4,"base":{"ref":"release/2.0"}}`))
	})

	branch, err := c.GetPullRequestTargetBranch(context.Background(), 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "release/2.0" {
		t.Fatalf("got %q, want release/2.0", branch)
	}
}
