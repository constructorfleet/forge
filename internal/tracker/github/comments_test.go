package github_test

import (
	"context"
	"encoding/json"
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

func TestAddComment_PostsBody(t *testing.T) {
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
	})

	if err := c.AddComment(context.Background(), "5", "hi there"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured["body"] != "hi there" {
		t.Fatalf("got body %q", captured["body"])
	}
}
