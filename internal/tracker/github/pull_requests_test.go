package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestCreatePullRequest_CreatesWhenNoExistingPR(t *testing.T) {
	var sawPost bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/pulls":
			if got := r.URL.Query().Get("head"); got != "acme:forge/exec/22" {
				t.Fatalf("head query = %q", got)
			}
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/pulls":
			sawPost = true
			var req struct {
				Title string `json:"title"`
				Head  string `json:"head"`
				Base  string `json:"base"`
				Body  string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if req.Title != "Ship it" || req.Head != "forge/exec/22" || req.Base != "main" || req.Body != "body" {
				t.Fatalf("request = %+v", req)
			}
			_, _ = w.Write([]byte(`{"number":22,"html_url":"https://example.invalid/pr/22"}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	})

	pr, err := c.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base:  "main",
		Head:  "forge/exec/22",
		Title: "Ship it",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	if !sawPost {
		t.Fatal("expected POST /pulls")
	}
	if pr.Number != 22 || pr.URL != "https://example.invalid/pr/22" {
		t.Fatalf("pull request = %+v", pr)
	}
}

func TestCreatePullRequest_ReusesExistingOpenPR(t *testing.T) {
	var postCalls int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[{"number":7,"html_url":"https://example.invalid/pr/7"}]`))
		case http.MethodPost:
			postCalls++
			t.Fatal("did not expect POST when an open PR already exists")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})

	pr, err := c.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base: "main", Head: "forge/exec/22",
	})
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	if postCalls != 0 {
		t.Fatalf("postCalls = %d, want 0", postCalls)
	}
	if pr.Number != 7 || pr.URL != "https://example.invalid/pr/7" {
		t.Fatalf("pull request = %+v", pr)
	}
}

func TestCreatePullRequest_RecoversExistingPRAfterValidationRace(t *testing.T) {
	getCalls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/pulls":
			getCalls++
			if getCalls == 1 {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"number":9,"html_url":"https://example.invalid/pr/9"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/pulls":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"A pull request already exists"}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	})

	pr, err := c.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base: "main", Head: "forge/exec/22",
	})
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	if getCalls != 2 {
		t.Fatalf("GET calls = %d, want 2", getCalls)
	}
	if pr.Number != 9 || pr.URL != "https://example.invalid/pr/9" {
		t.Fatalf("pull request = %+v", pr)
	}
}

func TestGetPullRequestTargetBranch(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widgets/pulls/23" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{"base":{"ref":"main"}}`))
	})

	base, err := c.GetPullRequestTargetBranch(context.Background(), 23)
	if err != nil {
		t.Fatalf("GetPullRequestTargetBranch: %v", err)
	}
	if base != "main" {
		t.Fatalf("target branch = %q, want main", base)
	}
}
