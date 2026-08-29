package github_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker/github"
)

func TestClient_VerifyAuth_MissingToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("expected no HTTP request when GITHUB_TOKEN is unset")
	})

	err := c.VerifyAuth(context.Background())
	if !errors.Is(err, github.ErrMissingToken) {
		t.Fatalf("expected github.ErrMissingToken, got %v", err)
	}
}

func TestClient_VerifyAuth_ValidToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "valid-token")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer valid-token" {
			t.Fatalf("expected Authorization header, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	if err := c.VerifyAuth(context.Background()); err != nil {
		t.Fatalf("expected VerifyAuth to pass, got %v", err)
	}
}

func TestClient_VerifyAuth_InvalidTokenIsUnauthenticated(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "invalid-token")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})

	err := c.VerifyAuth(context.Background())
	var authErr *github.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *github.AuthenticationError, got %T: %v", err, err)
	}

	var authzErr *github.AuthorizationError
	if errors.As(err, &authzErr) {
		t.Fatal("did not expect a 401 to be classified as AuthorizationError")
	}
}

func TestClient_VerifyAuth_ForbiddenIsAuthorizationError(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "valid-token")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"insufficient permissions"}`))
	})

	err := c.VerifyAuth(context.Background())
	var authzErr *github.AuthorizationError
	if !errors.As(err, &authzErr) {
		t.Fatalf("expected *github.AuthorizationError, got %T: %v", err, err)
	}

	var authErr *github.AuthenticationError
	if errors.As(err, &authErr) {
		t.Fatal("did not expect a 403 to be classified as AuthenticationError")
	}
}

func TestClient_VerifyAuth_NotFoundIsDistinctFromUnauthenticated(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "valid-token")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})

	err := c.VerifyAuth(context.Background())
	var notFoundErr *github.NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected *github.NotFoundError, got %T: %v", err, err)
	}

	var authErr *github.AuthenticationError
	if errors.As(err, &authErr) {
		t.Fatal("did not expect a 404 to be classified as AuthenticationError")
	}
}
