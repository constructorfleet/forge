package linear

import (
	"context"
	"testing"
)

func TestGetCommentsNormalizesOldestFirst(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`,
		"comments": `{"data":{"issue":{"comments":{"nodes":[
			{"body":"first","createdAt":"2024-01-01T00:00:00Z","user":{"name":"alice"}},
			{"body":"second","createdAt":"2024-01-02T00:00:00Z","user":{"name":"bob"}}
		]}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	comments, err := c.GetComments(context.Background(), "FOR-1")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("comments = %+v, want 2", comments)
	}
	if comments[0].Author != "alice" || comments[0].Body != "first" {
		t.Fatalf("comments[0] = %+v", comments[0])
	}
	if comments[1].Author != "bob" || comments[1].Body != "second" {
		t.Fatalf("comments[1] = %+v", comments[1])
	}
}

func TestAddCommentReturnsNormalizedComment(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`,
		"commentCreate": `{"data":{"commentCreate":{"success":true,"comment":{"body":"hello","createdAt":"2024-01-03T00:00:00Z","user":{"name":"forge-bot"}}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	comment, err := c.AddComment(context.Background(), "FOR-1", "hello")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if comment.Author != "forge-bot" || comment.Body != "hello" {
		t.Fatalf("comment = %+v", comment)
	}
}
