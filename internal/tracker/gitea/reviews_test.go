package gitea_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetPullRequestReviews_NormalizesGiteaStates(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/pulls/3/reviews" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[` +
			`{"id":1,"state":"APPROVED","user":{"login":"alice"}},` +
			`{"id":2,"state":"REQUEST_CHANGES","user":{"login":"bob"}},` +
			`{"id":3,"state":"COMMENT","user":{"login":"carol"}},` +
			`{"id":4,"state":"APPROVED","dismissed":true,"user":{"login":"dave"}}]`))
	})

	reviews, err := c.GetPullRequestReviews(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 4 {
		t.Fatalf("got %d reviews, want 4", len(reviews))
	}
	if reviews[0].State != tracker.ReviewApproved {
		t.Errorf("review 0 = %q, want APPROVED", reviews[0].State)
	}
	if reviews[1].State != tracker.ReviewChangesRequested {
		t.Errorf("review 1 = %q, want CHANGES_REQUESTED", reviews[1].State)
	}
	if reviews[2].State != tracker.ReviewCommented {
		t.Errorf("review 2 = %q, want COMMENTED", reviews[2].State)
	}
	if reviews[3].State != tracker.ReviewDismissed {
		t.Errorf("review 3 = %q, want DISMISSED for a dismissed review", reviews[3].State)
	}
}

func TestGetReviews_AdaptsNeutralCapability(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"state":"APPROVED","body":"lgtm","user":{"login":"alice"}}]`))
	})

	reviews, err := c.GetReviews(context.Background(), tracker.ChangeRequestRef{Number: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 1 || reviews[0].Author != "alice" || reviews[0].State != tracker.ReviewApproved {
		t.Fatalf("unexpected reviews: %+v", reviews)
	}
}
