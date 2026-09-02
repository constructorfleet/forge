package linear

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyAuthFailsWithoutNetworkWhenKeyMissing(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"}}}`))
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.VerifyAuth(context.Background()); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("VerifyAuth() = %v, want ErrMissingToken", err)
	}
	if called {
		t.Fatalf("VerifyAuth made a network request with no key set")
	}
}

func TestVerifyAuthSucceedsWithValidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"}}}`))
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "good-key")
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.VerifyAuth(context.Background()); err != nil {
		t.Fatalf("VerifyAuth() = %v, want nil", err)
	}
}

func TestVerifyAuthFailsOnRejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "bad-key")
	c := NewClient(nil, srv.URL, "FOR")

	var authErr *AuthenticationError
	err := c.VerifyAuth(context.Background())
	if err == nil || !errors.As(err, &authErr) {
		t.Fatalf("VerifyAuth() = %v (%T), want *AuthenticationError", err, err)
	}
}
