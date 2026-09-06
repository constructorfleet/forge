package gitea_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetPullRequestMergeStatus_Conflicted(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":3,"mergeable":false,"merged":false}`))
	})

	status, err := c.GetPullRequestMergeStatus(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Conflicted {
		t.Fatal("expected Conflicted for an open, non-mergeable pull request")
	}
	if status.Merged || status.Behind {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestGetPullRequestMergeStatus_Mergeable(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":3,"mergeable":true,"merged":false}`))
	})

	status, err := c.GetPullRequestMergeStatus(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Conflicted {
		t.Fatal("did not expect Conflicted for a mergeable pull request")
	}
}

func TestGetPullRequestMergeStatus_Merged(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":3,"mergeable":false,"merged":true}`))
	})

	status, err := c.GetPullRequestMergeStatus(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Merged {
		t.Fatal("expected Merged")
	}
	if status.Conflicted {
		t.Fatal("a merged pull request must not be reported as Conflicted")
	}
}

func TestGetChangeRequestMergeStatus_Adapts(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":3,"mergeable":true,"merged":false}`))
	})

	status, err := c.GetChangeRequestMergeStatus(context.Background(), tracker.ChangeRequestRef{Number: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Conflicted || status.Merged {
		t.Fatalf("unexpected status: %+v", status)
	}
}
