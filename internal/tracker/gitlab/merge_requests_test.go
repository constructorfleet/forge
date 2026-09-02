package gitlab_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/gitlab"
)

// mergeRequestsPath is the collection endpoint every merge-request test
// targets. It carries the URL-encoded project path.
const mergeRequestsPath = "/projects/" + escapedProject + "/merge_requests"

func TestCreateChangeRequest_CreatesTheMergeRequest(t *testing.T) {
	var sawPost bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != mergeRequestsPath {
			t.Fatalf("unexpected path %q", r.URL.EscapedPath())
		}
		switch r.Method {
		case http.MethodGet:
			// The adapter looks for an existing open merge request first.
			if got := r.URL.Query().Get("source_branch"); got != "forge/exec/290" {
				t.Fatalf("source_branch = %q, want the head branch", got)
			}
			if got := r.URL.Query().Get("target_branch"); got != "main" {
				t.Fatalf("target_branch = %q, want the base branch", got)
			}
			if got := r.URL.Query().Get("state"); got != "opened" {
				t.Fatalf("state = %q, want opened", got)
			}
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			sawPost = true
			var body struct {
				SourceBranch string `json:"source_branch"`
				TargetBranch string `json:"target_branch"`
				Title        string `json:"title"`
				Description  string `json:"description"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if body.SourceBranch != "forge/exec/290" || body.TargetBranch != "main" {
				t.Fatalf("body = %+v, want the head and base branches", body)
			}
			if body.Title != "Ship it" || body.Description != "body" {
				t.Fatalf("body = %+v, want the title and the description", body)
			}
			_, _ = w.Write([]byte(`{"iid":7,"web_url":"https://gitlab.example.com/acme/widgets/-/merge_requests/7"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})

	cr, err := c.CreateChangeRequest(context.Background(), tracker.ChangeRequestRequest{
		Base:  "main",
		Head:  "forge/exec/290",
		Title: "Ship it",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("CreateChangeRequest: %v", err)
	}
	if !sawPost {
		t.Fatal("expected a POST to the merge requests endpoint")
	}
	want := tracker.ChangeRequest{
		Ref: tracker.ChangeRequestRef{Provider: "gitlab", Number: 7},
		URL: "https://gitlab.example.com/acme/widgets/-/merge_requests/7",
	}
	if cr != want {
		t.Fatalf("change request = %+v, want %+v", cr, want)
	}
}

func TestCreateChangeRequest_RecoversAnExistingOpenMergeRequest(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatal("expected no POST: an open merge request already exists")
		}
		_, _ = w.Write([]byte(`[{"iid":7,"web_url":"https://gitlab.example.com/mr/7"}]`))
	})

	cr, err := c.CreateChangeRequest(context.Background(), tracker.ChangeRequestRequest{
		Base:  "main",
		Head:  "forge/exec/290",
		Title: "Ship it",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("CreateChangeRequest: %v", err)
	}
	want := tracker.ChangeRequest{
		Ref: tracker.ChangeRequestRef{Provider: "gitlab", Number: 7},
		URL: "https://gitlab.example.com/mr/7",
	}
	if cr != want {
		t.Fatalf("change request = %+v, want the recovered merge request %+v", cr, want)
	}
}

// Two forge processes can race between the lookup and the create call. GitLab
// then rejects the second create. The adapter must look the merge request up
// once more and recover it, rather than fail the Execution.
func TestCreateChangeRequest_RecoversAfterTheCreateCallIsRejected(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{name: "conflict", status: http.StatusConflict},
		{name: "bad request", status: http.StatusBadRequest},
		{name: "unprocessable entity", status: http.StatusUnprocessableEntity},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lookups int
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"message":["Another open merge request already exists for this source branch"]}`))
					return
				}
				lookups++
				if lookups == 1 {
					// The first lookup runs before the race is lost.
					_, _ = w.Write([]byte(`[]`))
					return
				}
				_, _ = w.Write([]byte(`[{"iid":7,"web_url":"https://gitlab.example.com/mr/7"}]`))
			})

			cr, err := c.CreateChangeRequest(context.Background(), tracker.ChangeRequestRequest{
				Base: "main", Head: "forge/exec/290", Title: "Ship it", Body: "body",
			})
			if err != nil {
				t.Fatalf("CreateChangeRequest: %v", err)
			}
			if lookups != 2 {
				t.Fatalf("lookups = %d, want 2 (one before the create call, one to recover)", lookups)
			}
			if cr.Ref.Number != 7 || cr.URL != "https://gitlab.example.com/mr/7" {
				t.Fatalf("change request = %+v, want the recovered merge request", cr)
			}
		})
	}
}

// A rejected create with no recoverable merge request must report the
// original failure, not a silent success.
func TestCreateChangeRequest_ReportsTheCreateFailureWhenNothingIsRecoverable(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"source_branch does not exist"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})

	_, err := c.CreateChangeRequest(context.Background(), tracker.ChangeRequestRequest{
		Base: "main", Head: "forge/exec/290", Title: "Ship it", Body: "body",
	})
	var validation *gitlab.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected *gitlab.ValidationError, got %T: %v", err, err)
	}
}
