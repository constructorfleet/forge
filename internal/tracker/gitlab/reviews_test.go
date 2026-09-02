package gitlab_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetReviews_ReportsEachApprover(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/projects/"+escapedProject+"/merge_requests/7/approvals" {
			t.Fatalf("path = %q, want the approvals endpoint", got)
		}
		_, _ = w.Write([]byte(`{
			"approved": true,
			"approved_by": [{"user":{"username":"alice"}},{"user":{"username":"bob"}}]
		}`))
	})

	reviews, err := c.GetReviews(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7})
	if err != nil {
		t.Fatalf("GetReviews: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("reviews = %+v, want two", reviews)
	}
	want := []tracker.Review{
		{Author: "alice", State: tracker.ReviewApproved, RawDetail: "APPROVED"},
		{Author: "bob", State: tracker.ReviewApproved, RawDetail: "APPROVED"},
	}
	for i, got := range reviews {
		if got != want[i] {
			t.Fatalf("review %d = %+v, want %+v", i, got, want[i])
		}
	}
}

// A project tier without approval rules answers 403 or 404. Forge must not
// gate on approvals there, so the adapter reports no reviews and no error.
func TestGetReviews_DegradesWhenTheTierHidesApprovals(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})

			reviews, err := c.GetReviews(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7})
			if err != nil {
				t.Fatalf("GetReviews: %v", err)
			}
			if len(reviews) != 0 {
				t.Fatalf("reviews = %+v, want none", reviews)
			}
			if reviews == nil {
				t.Fatal("reviews = nil, want an empty slice")
			}
		})
	}
}

// A failure the adapter cannot interpret must stay a failure.
func TestGetReviews_ReportsAnUnexpectedFailure(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})

	if _, err := c.GetReviews(context.Background(), tracker.ChangeRequestRef{Provider: "gitlab", Number: 7}); err == nil {
		t.Fatal("expected an error, got nil")
	}
}
