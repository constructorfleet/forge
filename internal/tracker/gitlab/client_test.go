package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/gitlab"
)

// testProject is the project path every test client targets. It contains a
// "/" on purpose: GitLab identifies a project by its URL-encoded path, so
// each request path must carry "acme%2Fwidgets".
const testProject = "acme/widgets"

// escapedProject is testProject in the form that must appear in a request
// path.
const escapedProject = "acme%2Fwidgets"

// dependsOnIDs extracts the prerequisite Issue IDs from a normalized Issue's
// dependency edges, in order, for concise assertions.
func dependsOnIDs(deps []domain.Dependency) []string {
	ids := make([]string, len(deps))
	for i, d := range deps {
		ids[i] = d.DependsOnID
	}
	return ids
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*gitlab.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := gitlab.NewClient(srv.Client(), srv.URL, testProject)
	return c, srv
}

// isLinksPath reports whether r targets an issue's native issue-link
// subresource (…/issues/N/links). The adapter probes it before it reads the
// body block.
func isLinksPath(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/links")
}

// serveNoNativeLinks answers the native links probe with an empty list, so a
// test that exercises body-block parsing gets the body-block fallback.
func serveNoNativeLinks(w http.ResponseWriter) { _, _ = w.Write([]byte("[]")) }

func TestClient_SendsPrivateTokenHeader(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "secret-token")

	var gotPrivateToken, gotAuthorization string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPrivateToken = r.Header.Get("PRIVATE-TOKEN")
		gotAuthorization = r.Header.Get("Authorization")
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":1,"description":""}`))
	})

	if _, err := c.GetIssue(context.Background(), "1"); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotPrivateToken != "secret-token" {
		t.Fatalf("PRIVATE-TOKEN = %q, want %q", gotPrivateToken, "secret-token")
	}
	if gotAuthorization != "" {
		t.Fatalf("Authorization = %q, want no Authorization header", gotAuthorization)
	}
}

func TestClient_EncodesProjectPathInRequestPath(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"iid":42,"description":""}`))
	})

	if _, err := c.GetIssue(context.Background(), "42"); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotPath != "/projects/"+escapedProject+"/issues/42" {
		t.Fatalf("path = %q, want %q", gotPath, "/projects/"+escapedProject+"/issues/42")
	}
}

func TestClient_NotFoundIsTyped(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var notFound *gitlab.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected *gitlab.NotFoundError, got %T: %v", err, err)
	}
}

func TestClient_ValidationErrorIsTyped(t *testing.T) {
	// GitLab answers 400 where GitHub answers 422 for a malformed request
	// body, so both statuses map to one type.
	cases := []struct {
		name   string
		status int
	}{
		{name: "bad request", status: http.StatusBadRequest},
		{name: "unprocessable entity", status: http.StatusUnprocessableEntity},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"title is missing"}`))
			})

			_, err := c.CreateIssue(context.Background(), tracker.IssueRequest{})
			var validation *gitlab.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected *gitlab.ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestClient_RateLimit429(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"Retry later"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *tracker.RateLimitError, got %T: %v", err, err)
	}
	if rlErr.ResetAt.IsZero() {
		t.Fatal("expected ResetAt to be populated from RateLimit-Reset")
	}
}

func TestClient_RateLimit429WithRetryAfter(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"Retry later"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *tracker.RateLimitError, got %T: %v", err, err)
	}
	if rlErr.ResetAt.IsZero() {
		t.Fatal("expected ResetAt to be populated from Retry-After")
	}
}

func TestClient_PlainForbiddenIsNotRateLimited(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"403 Forbidden"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	var rlErr *tracker.RateLimitError
	if errors.As(err, &rlErr) {
		t.Fatal("did not expect a RateLimitError for a plain 403")
	}
	var authzErr *gitlab.AuthorizationError
	if !errors.As(err, &authzErr) {
		t.Fatalf("expected *gitlab.AuthorizationError, got %T: %v", err, err)
	}
}

func TestClient_ServerErrorIsPlainError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})

	_, err := c.GetIssue(context.Background(), "1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var notFound *gitlab.NotFoundError
	if errors.As(err, &notFound) {
		t.Fatal("did not expect a 500 to be classified as NotFoundError")
	}
}

func TestClient_EmptyProviderFallsBackToGitLab(t *testing.T) {
	// A Config built in code can leave the sidecar provider tag empty. An
	// Issue must still carry a provider, so the client fills in its own
	// name.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			serveNoNativeLinks(w)
			return
		}
		_, _ = w.Write([]byte(`{"iid":1,"description":""}`))
	})
	c.Provider = ""

	issue, err := c.GetIssue(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Provider != "gitlab" {
		t.Fatalf("Provider = %q, want gitlab", issue.Provider)
	}
}

func TestTypedErrors_NameTheProblemAndTheTokenVariable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "authentication error names the token variable",
			err:  &gitlab.AuthenticationError{Path: "https://gitlab.com/api/v4/projects/acme%2Fwidgets"},
			want: []string{"unauthenticated", "GITLAB_TOKEN"},
		},
		{
			name: "authorization error says the credential is authenticated",
			err:  &gitlab.AuthorizationError{Path: "/projects/acme%2Fwidgets"},
			want: []string{"forbidden", "not authorized"},
		},
		{
			name: "not found error names the path",
			err:  &gitlab.NotFoundError{Path: "/projects/acme%2Fwidgets/issues/42"},
			want: []string{"not found", "/issues/42"},
		},
		{
			name: "validation error carries the response body",
			err:  &gitlab.ValidationError{Path: "/projects/acme%2Fwidgets/issues", Body: `{"message":"title is missing"}`},
			want: []string{"validation failed", "title is missing"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.Error()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("error %q does not contain %q", got, want)
				}
			}
		})
	}
}

func TestNewClient_DefaultsToGitLabDotCom(t *testing.T) {
	c := gitlab.NewClient(nil, "", testProject)
	if got := c.BaseURL(); got != "https://gitlab.com/api/v4" {
		t.Fatalf("BaseURL = %q, want the gitlab.com API root", got)
	}
	if c.Provider != "gitlab" {
		t.Fatalf("Provider = %q, want gitlab", c.Provider)
	}
}
