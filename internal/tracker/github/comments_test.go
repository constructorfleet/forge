package github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestGetComments_Normalizes(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/issues/5/comments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"body":"hello","created_at":"2024-01-02T03:04:05Z","user":{"login":"alice"}}]`))
	})

	comments, err := c.GetComments(context.Background(), "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	if comments[0].Author != "alice" || comments[0].Body != "hello" {
		t.Fatalf("unexpected comment: %+v", comments[0])
	}
}

func TestGetComments_RequestsMaxPageSize(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("expected per_page=100, got %q", got)
		}
		_, _ = w.Write([]byte(`[]`))
	})
	_ = srv

	if _, err := c.GetComments(context.Background(), "5"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetComments_FollowsLinkPaginationAcrossPages(t *testing.T) {
	var srvURL string
	calls := 0
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/widgets/issues/5/comments?per_page=100&page=2>; rel="next"`, srvURL))
			_, _ = w.Write([]byte(`[{"body":"oldest","user":{"login":"alice"}}]`))
		case "2":
			// Newest comments live on later pages — these must not be
			// dropped by stopping at page 1.
			_, _ = w.Write([]byte(`[{"body":"newest","user":{"login":"bob"}}]`))
		default:
			t.Fatalf("unexpected page: %s", r.URL.Query().Get("page"))
		}
	})
	srvURL = srv.URL

	comments, err := c.GetComments(context.Background(), "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 requests (one per page), got %d", calls)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2 (accumulated across pages): %+v", len(comments), comments)
	}
	if comments[0].Body != "oldest" || comments[1].Body != "newest" {
		t.Fatalf("unexpected comment order/content: %+v", comments)
	}
}

func TestAddComment_PostsBodyAndReturnsNormalizedComment(t *testing.T) {
	var captured map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/repos/acme/widgets/issues/5/comments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"body":"hi there","created_at":"2024-01-02T03:04:05Z","user":{"login":"forge-bot"}}`))
	})

	got, err := c.AddComment(context.Background(), "5", "hi there")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured["body"] != "hi there" {
		t.Fatalf("got posted body %q", captured["body"])
	}
	if got.Author != "forge-bot" || got.Body != "hi there" {
		t.Fatalf("unexpected returned comment: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("returned comment CreatedAt is zero, want the server-assigned timestamp")
	}
}
