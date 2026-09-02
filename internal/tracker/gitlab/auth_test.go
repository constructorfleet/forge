package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/gitlab"
)

func TestClient_VerifyAuth_MissingToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("expected no HTTP request when GITLAB_TOKEN is unset")
	})

	err := c.VerifyAuth(context.Background())
	if !errors.Is(err, gitlab.ErrMissingToken) {
		t.Fatalf("expected gitlab.ErrMissingToken, got %v", err)
	}
}

func TestClient_VerifyAuth_ValidTokenGetsTheProject(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "valid-token")

	var gotMethod, gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "valid-token" {
			t.Fatalf("PRIVATE-TOKEN = %q, want valid-token", got)
		}
		_, _ = w.Write([]byte(`{"id":7,"path_with_namespace":"acme/widgets"}`))
	})

	if err := c.VerifyAuth(context.Background()); err != nil {
		t.Fatalf("expected VerifyAuth to pass, got %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/projects/"+escapedProject {
		t.Fatalf("path = %q, want the project endpoint", gotPath)
	}
}

func TestClient_VerifyAuth_InvalidTokenIsUnauthenticated(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "invalid-token")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	})

	err := c.VerifyAuth(context.Background())
	var authErr *gitlab.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *gitlab.AuthenticationError, got %T: %v", err, err)
	}

	var authzErr *gitlab.AuthorizationError
	if errors.As(err, &authzErr) {
		t.Fatal("did not expect a 401 to be classified as AuthorizationError")
	}
}

func TestClient_VerifyAuth_ForbiddenIsAuthorizationError(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "valid-token")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"403 Forbidden"}`))
	})

	err := c.VerifyAuth(context.Background())
	var authzErr *gitlab.AuthorizationError
	if !errors.As(err, &authzErr) {
		t.Fatalf("expected *gitlab.AuthorizationError, got %T: %v", err, err)
	}

	var authErr *gitlab.AuthenticationError
	if errors.As(err, &authErr) {
		t.Fatal("did not expect a 403 to be classified as AuthenticationError")
	}
}

func TestClient_VerifyAuth_NotFoundIsDistinctFromUnauthenticated(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "valid-token")

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
	})

	err := c.VerifyAuth(context.Background())
	var notFoundErr *gitlab.NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected *gitlab.NotFoundError, got %T: %v", err, err)
	}

	var authErr *gitlab.AuthenticationError
	if errors.As(err, &authErr) {
		t.Fatal("did not expect a 404 to be classified as AuthenticationError")
	}
}

func TestClient_ImplementsAuthPreflighter(t *testing.T) {
	var _ tracker.AuthPreflighter = (*gitlab.Client)(nil)
}
