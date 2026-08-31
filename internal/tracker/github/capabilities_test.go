package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/github"
)

var _ tracker.Tracker = (*github.Client)(nil)
var _ tracker.SCM = (*github.Client)(nil)
var _ tracker.CI = (*github.Client)(nil)
var _ tracker.ReviewGetter = (*github.Client)(nil)
var _ tracker.LegacyProvider = (*github.Client)(nil)

func TestCreateChangeRequestDelegatesToPullRequestCreation(t *testing.T) {
	var sawPost bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/pulls":
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
			if req.Title != "Ship it" || req.Head != "forge/exec/293" || req.Base != "main" || req.Body != "body" {
				t.Fatalf("request = %+v", req)
			}
			_, _ = w.Write([]byte(`{"number":293,"html_url":"https://example.invalid/pr/293"}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	})

	cr, err := c.CreateChangeRequest(context.Background(), tracker.ChangeRequestRequest{
		Base:  "main",
		Head:  "forge/exec/293",
		Title: "Ship it",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("CreateChangeRequest: %v", err)
	}
	if !sawPost {
		t.Fatal("expected POST /pulls")
	}
	if cr.Ref.Provider != "github" || cr.Ref.Number != 293 || cr.URL != "https://example.invalid/pr/293" {
		t.Fatalf("change request = %+v", cr)
	}
}
