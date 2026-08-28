package github_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/github"
)

// fakeReachability is a github.GitReachabilityChecker double so
// CheckExternal's GitHub-API behavior can be tested independently of a real
// git checkout.
type fakeReachability struct {
	ancestor map[string]bool // commit -> IsAncestor result
	err      error
	calls    []string // commits queried, in order
}

func (f *fakeReachability) IsAncestor(_ context.Context, commit, _ string) (bool, error) {
	f.calls = append(f.calls, commit)
	if f.err != nil {
		return false, f.err
	}
	return f.ancestor[commit], nil
}

func TestCheckExternal_MergedAndReachable_ReturnsSatisfied(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/50":
			_, _ = w.Write([]byte(`{"number":50,"state":"closed"}`))
		case "/repos/acme/widgets/issues/50/timeline":
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":55,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}}]`))
		case "/repos/acme/widgets/pulls/55":
			_, _ = w.Write([]byte(`{"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"abc123"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	reach := &fakeReachability{ancestor: map[string]bool{"abc123": true}}
	c.Reachability = reach

	state, err := c.CheckExternal(context.Background(), "50", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalSatisfied {
		t.Fatalf("state = %s, want EXTERNAL_SATISFIED", state)
	}
	if len(reach.calls) != 1 || reach.calls[0] != "abc123" {
		t.Fatalf("reachability calls = %v, want [abc123]", reach.calls)
	}
}

func TestCheckExternal_MergedButNotReachable_ReturnsPending(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/50":
			_, _ = w.Write([]byte(`{"number":50,"state":"closed"}`))
		case "/repos/acme/widgets/issues/50/timeline":
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":55,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}}]`))
		case "/repos/acme/widgets/pulls/55":
			_, _ = w.Write([]byte(`{"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"abc123"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	c.Reachability = &fakeReachability{ancestor: map[string]bool{"abc123": false}}

	state, err := c.CheckExternal(context.Background(), "50", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalPending {
		t.Fatalf("state = %s, want EXTERNAL_PENDING (merged but not yet reachable)", state)
	}
}

func TestCheckExternal_ClosedWithoutMergedPR_ReturnsInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/51":
			_, _ = w.Write([]byte(`{"number":51,"state":"closed"}`))
		case "/repos/acme/widgets/issues/51/timeline":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	c.Reachability = &fakeReachability{}

	state, err := c.CheckExternal(context.Background(), "51", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalInvalid {
		t.Fatalf("state = %s, want EXTERNAL_INVALID (closed without merge)", state)
	}
}

func TestCheckExternal_ClosedWithOnlyUnmergedPR_ReturnsInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/52":
			_, _ = w.Write([]byte(`{"number":52,"state":"closed"}`))
		case "/repos/acme/widgets/issues/52/timeline":
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":56,"pull_request":{"merged_at":null}}}}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	c.Reachability = &fakeReachability{}

	state, err := c.CheckExternal(context.Background(), "52", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalInvalid {
		t.Fatalf("state = %s, want EXTERNAL_INVALID (closed, PR referenced but never merged)", state)
	}
}

func TestCheckExternal_OpenWithoutPR_ReturnsPending(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/53":
			_, _ = w.Write([]byte(`{"number":53,"state":"open"}`))
		case "/repos/acme/widgets/issues/53/timeline":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	c.Reachability = &fakeReachability{}

	state, err := c.CheckExternal(context.Background(), "53", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalPending {
		t.Fatalf("state = %s, want EXTERNAL_PENDING", state)
	}
}

func TestCheckExternal_NoReachabilityConfigured_ErrorsRatherThanGuessing(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/54":
			_, _ = w.Write([]byte(`{"number":54,"state":"closed"}`))
		case "/repos/acme/widgets/issues/54/timeline":
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":57,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}}]`))
		case "/repos/acme/widgets/pulls/57":
			_, _ = w.Write([]byte(`{"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"def456"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	// c.Reachability left nil.

	if _, err := c.CheckExternal(context.Background(), "54", "origin/main"); err == nil {
		t.Fatal("expected an error when no GitReachabilityChecker is configured, got nil")
	}
}

func TestCheckExternal_ReachabilityError_Propagates(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/58":
			_, _ = w.Write([]byte(`{"number":58,"state":"closed"}`))
		case "/repos/acme/widgets/issues/58/timeline":
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":59,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}}]`))
		case "/repos/acme/widgets/pulls/59":
			_, _ = w.Write([]byte(`{"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"ghi789"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	c.Reachability = &fakeReachability{err: fmt.Errorf("git: boom")}

	if _, err := c.CheckExternal(context.Background(), "58", "origin/main"); err == nil {
		t.Fatal("expected the reachability error to propagate, got nil")
	}
}

var _ tracker.ExternalChecker = (*github.Client)(nil)
