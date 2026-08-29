package github_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetPullRequestReviews_Normalizes(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/pulls/23/reviews" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[
			{"id":1,"user":{"login":"alice"},"state":"APPROVED","body":"lgtm","submitted_at":"2026-01-01T00:00:00Z"},
			{"id":2,"user":{"login":"bob"},"state":"CHANGES_REQUESTED","body":"fix the thing","submitted_at":"2026-01-02T00:00:00Z"},
			{"id":3,"user":{"login":"carol"},"state":"COMMENTED","body":"","submitted_at":"2026-01-03T00:00:00Z"},
			{"id":4,"user":{"login":"dave"},"state":"DISMISSED","body":"","submitted_at":"2026-01-04T00:00:00Z"}
		]`))
	})

	reviews, err := c.GetPullRequestReviews(context.Background(), 23)
	if err != nil {
		t.Fatalf("GetPullRequestReviews: %v", err)
	}
	if len(reviews) != 4 {
		t.Fatalf("got %d reviews, want 4", len(reviews))
	}

	want := []tracker.PullRequestReview{
		{ID: 1, Author: "alice", State: tracker.ReviewApproved, Body: "lgtm"},
		{ID: 2, Author: "bob", State: tracker.ReviewChangesRequested, Body: "fix the thing"},
		{ID: 3, Author: "carol", State: tracker.ReviewCommented, Body: ""},
		{ID: 4, Author: "dave", State: tracker.ReviewDismissed, Body: ""},
	}
	for i, w := range want {
		got := reviews[i]
		if got.ID != w.ID || got.Author != w.Author || got.State != w.State || got.Body != w.Body {
			t.Fatalf("review[%d] = %+v, want %+v", i, got, w)
		}
	}
}
