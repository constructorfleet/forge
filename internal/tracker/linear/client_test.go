package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestGraphQLSendsQueryAndDecodesData(t *testing.T) {
	var gotAuth string
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"}}}`))
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "test-key")
	c := NewClient(nil, srv.URL, "FOR")

	var out struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
	}
	if err := c.graphQL(context.Background(), "query { viewer { id } }", nil, &out); err != nil {
		t.Fatalf("graphQL: %v", err)
	}
	if out.Viewer.ID != "u1" {
		t.Fatalf("Viewer.ID = %q, want u1", out.Viewer.ID)
	}
	if gotAuth != "test-key" {
		t.Fatalf("Authorization header = %q, want test-key", gotAuth)
	}
	if gotBody["query"] == nil {
		t.Fatalf("request body missing query field: %v", gotBody)
	}
}

func TestGraphQLSurfacesGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"entity not found"}]}`))
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "test-key")
	c := NewClient(nil, srv.URL, "FOR")

	err := c.graphQL(context.Background(), "query { viewer { id } }", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "entity not found") {
		t.Fatalf("err = %v, want it to mention 'entity not found'", err)
	}
}

func TestGraphQLMapsUnauthorizedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	t.Setenv(tokenEnvVar, "bad-key")
	c := NewClient(nil, srv.URL, "FOR")

	err := c.graphQL(context.Background(), "query { viewer { id } }", nil, nil)
	var authErr *AuthenticationError
	if err == nil || !errors.As(err, &authErr) {
		t.Fatalf("err = %v (%T), want *AuthenticationError", err, err)
	}
}

func TestGraphQLOmitsAuthorizationHeaderWhenTokenUnset(t *testing.T) {
	var gotAuth string
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header["Authorization"]
		sawHeader = len(values) > 0
		if sawHeader {
			gotAuth = values[0]
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	_ = os.Unsetenv(tokenEnvVar)
	c := NewClient(nil, srv.URL, "FOR")

	if err := c.graphQL(context.Background(), "query {}", nil, nil); err != nil {
		t.Fatalf("graphQL: %v", err)
	}
	if sawHeader && gotAuth != "" {
		t.Fatalf("Authorization header = %q, want empty/absent when token unset", gotAuth)
	}
}

func TestProviderIDDefaultsToLinear(t *testing.T) {
	c := NewClient(nil, "", "FOR")
	if got := c.providerID(); got != "linear" {
		t.Fatalf("providerID() = %q, want linear", got)
	}
	c.Provider = "custom"
	if got := c.providerID(); got != "custom" {
		t.Fatalf("providerID() = %q, want custom", got)
	}
}
