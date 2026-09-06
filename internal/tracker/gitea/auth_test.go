package gitea_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker/gitea"
)

func TestVerifyAuth_MissingTokenFailsWithoutRequest(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "")
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("VerifyAuth must not send a request when the token is missing")
	})

	err := c.VerifyAuth(context.Background())
	if !errors.Is(err, gitea.ErrMissingToken) {
		t.Fatalf("expected ErrMissingToken, got %v", err)
	}
}

func TestVerifyAuth_GetsRepository(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "token")
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	if err := c.VerifyAuth(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/repos/acme/widgets" {
		t.Fatalf("VerifyAuth queried %q, want /repos/acme/widgets", gotPath)
	}
}

func TestVerifyAuth_RejectedTokenIsAuthenticationError(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "bad")
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	err := c.VerifyAuth(context.Background())
	var authErr *gitea.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *gitea.AuthenticationError, got %T: %v", err, err)
	}
}
