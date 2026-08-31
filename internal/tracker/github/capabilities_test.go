package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/github"
)

var _ tracker.Tracker = (*github.Client)(nil)
var _ tracker.SCM = (*github.Client)(nil)
var _ tracker.CI = (*github.Client)(nil)
var _ tracker.ReviewGetter = (*github.Client)(nil)
var _ tracker.LegacyProvider = (*github.Client)(nil)
var _ tracker.DependencyStore = (*github.Client)(nil)

func TestGetDependenciesReturnsNeutralBlocksEdgesFromBodyBlock(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isBlockedByPath(r) {
			serveNoNativeDeps(w)
			return
		}
		_, _ = w.Write([]byte(`{"number":42,"body":"## Dependencies\n- #1\n- #2\n"}`))
	})

	edges, err := c.GetDependencies(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("got %d edges, want 2: %+v", len(edges), edges)
	}
	want := []tracker.DependencyEdge{
		{
			Issue:     domain.IssueRef{Provider: "github", ID: "42"},
			DependsOn: domain.IssueRef{Provider: "github", ID: "1"},
			Kind:      tracker.DependencyBlocks,
		},
		{
			Issue:     domain.IssueRef{Provider: "github", ID: "42"},
			DependsOn: domain.IssueRef{Provider: "github", ID: "2"},
			Kind:      tracker.DependencyBlocks,
		},
	}
	if edges[0] != want[0] || edges[1] != want[1] {
		t.Fatalf("edges = %+v, want %+v", edges, want)
	}
}

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

func TestGetChecksDelegatesToPullRequestChecksAndMapsNeutralType(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/pulls/293":
			_, _ = w.Write([]byte(`{"head":{"sha":"abc123"}}`))
		case "/repos/acme/widgets/commits/abc123/status":
			_, _ = w.Write([]byte(`{"statuses":[{"context":"build","state":"success","description":"ok"}]}`))
		case "/repos/acme/widgets/commits/abc123/check-runs":
			_, _ = w.Write([]byte(`{"check_runs":[]}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	})

	checks, err := c.GetChecks(context.Background(), tracker.ChangeRequestRef{Provider: "github", Number: 293})
	if err != nil {
		t.Fatalf("GetChecks: %v", err)
	}
	if reflect.TypeOf(checks[0]) != reflect.TypeOf(tracker.Check{}) {
		t.Fatalf("check type = %T, want tracker.Check", checks[0])
	}
	want := tracker.Check{Name: "build", State: tracker.CheckSuccess, Details: "ok"}
	if checks[0] != want {
		t.Fatalf("check = %+v, want %+v", checks[0], want)
	}
}

func TestGetReviewsDelegatesToPullRequestReviewsAndOmitsProviderID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widgets/pulls/293/reviews" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`[
			{"id":42,"user":{"login":"alice"},"state":"APPROVED","body":"ship it","submitted_at":"2026-01-01T00:00:00Z"}
		]`))
	})

	reviews, err := c.GetReviews(context.Background(), tracker.ChangeRequestRef{Provider: "github", Number: 293})
	if err != nil {
		t.Fatalf("GetReviews: %v", err)
	}
	if reflect.TypeOf(reviews[0]) != reflect.TypeOf(tracker.Review{}) {
		t.Fatalf("review type = %T, want tracker.Review", reviews[0])
	}
	if _, ok := reflect.TypeOf(reviews[0]).FieldByName("ID"); ok {
		t.Fatal("neutral Review must not expose GitHub review ID")
	}
	want := tracker.Review{Author: "alice", State: tracker.ReviewApproved, Body: "ship it"}
	if reviews[0].Author != want.Author || reviews[0].State != want.State || reviews[0].Body != want.Body {
		t.Fatalf("review = %+v, want %+v", reviews[0], want)
	}
	if reviews[0].RawDetail != "APPROVED" {
		t.Fatalf("review.RawDetail = %q, want raw provider state %q", reviews[0].RawDetail, "APPROVED")
	}
}

func TestGetChangeRequestMergeStatusPreservesRawDetail(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/pulls/293" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"mergeable_state":"dirty","merged":false}`))
	})

	status, err := c.GetChangeRequestMergeStatus(context.Background(), tracker.ChangeRequestRef{Provider: "github", Number: 293})
	if err != nil {
		t.Fatalf("GetChangeRequestMergeStatus: %v", err)
	}
	if !status.Conflicted {
		t.Fatal("status.Conflicted = false, want true")
	}
	if status.RawDetail != "dirty" {
		t.Fatalf("status.RawDetail = %q, want raw mergeable_state %q", status.RawDetail, "dirty")
	}
}
