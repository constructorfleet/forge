package gitlab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestGetComments_NormalizesNotesOldestFirst(t *testing.T) {
	var gotPath, gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.Query().Get("sort")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"body":"first","created_at":"2024-01-01T10:00:00.000Z","author":{"username":"alice"}},
			{"body":"second","created_at":"2024-01-02T11:30:00.000Z","author":{"username":"bob"}}
		]`))
	})

	comments, err := c.GetComments(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if gotPath != "/projects/"+escapedProject+"/issues/42/notes" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	// GitLab sorts notes newest first by default; the Tracker contract is
	// oldest first, so the adapter must ask for ascending order.
	if gotQuery != "asc" {
		t.Fatalf("sort = %q, want asc", gotQuery)
	}
	want := []tracker.Comment{
		{Author: "alice", Body: "first", CreatedAt: time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)},
		{Author: "bob", Body: "second", CreatedAt: time.Date(2024, 1, 2, 11, 30, 0, 0, time.UTC)},
	}
	if len(comments) != len(want) {
		t.Fatalf("got %d comments, want %d: %+v", len(comments), len(want), comments)
	}
	for i := range want {
		if comments[i].Author != want[i].Author || comments[i].Body != want[i].Body {
			t.Fatalf("comment %d = %+v, want %+v", i, comments[i], want[i])
		}
		if !comments[i].CreatedAt.Equal(want[i].CreatedAt) {
			t.Fatalf("comment %d CreatedAt = %v, want %v", i, comments[i].CreatedAt, want[i].CreatedAt)
		}
	}
}

func TestGetComments_DropsSystemNotes(t *testing.T) {
	// A GitLab system note records an activity event ("changed the
	// description"), not a human comment. The needs-info resume flow
	// compares comments to find a human reply, so a system note must not
	// appear as one.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"body":"changed the description","created_at":"2024-01-01T10:00:00Z","system":true,"author":{"username":"alice"}},
			{"body":"a real reply","created_at":"2024-01-02T10:00:00Z","system":false,"author":{"username":"bob"}}
		]`))
	})

	comments, err := c.GetComments(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "a real reply" {
		t.Fatalf("comments = %+v, want only the human reply", comments)
	}
}

func TestGetComments_FollowsPagination(t *testing.T) {
	var srvURL string
	pages := 0
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[{"body":"second page","created_at":"2024-01-03T10:00:00Z","author":{"username":"carol"}}]`))
			return
		}
		w.Header().Set("Link", `<`+srvURL+`/projects/`+escapedProject+`/issues/42/notes?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[{"body":"first page","created_at":"2024-01-01T10:00:00Z","author":{"username":"alice"}}]`))
	})
	srvURL = srv.URL

	comments, err := c.GetComments(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if pages != 2 {
		t.Fatalf("fetched %d pages, want 2", pages)
	}
	if len(comments) != 2 || comments[0].Body != "first page" || comments[1].Body != "second page" {
		t.Fatalf("comments = %+v, want both pages in order", comments)
	}
}

func TestAddComment_PostsNoteAndReturnsTrackerReportedIdentity(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"body":"hello","created_at":"2024-05-06T07:08:09Z","author":{"username":"forge-bot"}}`))
	})

	comment, err := c.AddComment(context.Background(), "42", "hello")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/projects/"+escapedProject+"/issues/42/notes" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody["body"] != "hello" {
		t.Fatalf("request body = %+v", gotBody)
	}
	// The caller needs the tracker's own author and clock, not a locally
	// captured one.
	if comment.Author != "forge-bot" {
		t.Fatalf("Author = %q, want forge-bot", comment.Author)
	}
	if !comment.CreatedAt.Equal(time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)) {
		t.Fatalf("CreatedAt = %v", comment.CreatedAt)
	}
}
