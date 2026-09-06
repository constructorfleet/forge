package gitea_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestGetComments_FollowsPagination(t *testing.T) {
	var srvURL string
	c, srv := newTestClient(t, nil)
	srvURL = srv.URL
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/widgets/issues/5/comments?page=2>; rel="next"`, srvURL))
			_, _ = w.Write([]byte(`[{"body":"first","user":{"login":"alice"}}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"body":"second","user":{"login":"bob"}}]`))
		default:
			t.Fatalf("unexpected page: %s", r.URL.Query().Get("page"))
		}
	})

	comments, err := c.GetComments(context.Background(), "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2: %+v", len(comments), comments)
	}
	if comments[0].Author != "alice" || comments[0].Body != "first" {
		t.Fatalf("unexpected first comment: %+v", comments[0])
	}
	if comments[1].Author != "bob" || comments[1].Body != "second" {
		t.Fatalf("unexpected second comment: %+v", comments[1])
	}
}

func TestAddComment_PostsAndNormalizes(t *testing.T) {
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/repos/acme/widgets/issues/5/comments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = decodeJSON(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"body":"hello","user":{"login":"forge-bot"},"created_at":"2024-01-02T03:04:05Z"}`))
	})

	comment, err := c.AddComment(context.Background(), "5", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "hello" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	if comment.Author != "forge-bot" || comment.Body != "hello" {
		t.Fatalf("unexpected normalized comment: %+v", comment)
	}
	if comment.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be populated from the server response")
	}
}
