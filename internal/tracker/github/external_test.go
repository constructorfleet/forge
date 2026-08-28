package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":55,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}},{"event":"closed","commit_id":"abc123"}]`))
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
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":55,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}},{"event":"closed","commit_id":"abc123"}]`))
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

// TestCheckExternal_MergedPRCrossReferenceOnPage2_StillFound is the P2
// pagination fix's regression test: a long-lived issue's timeline can span
// multiple pages (labels, comments, and references all count as timeline
// events), so the genuine closing PR's cross-reference may land beyond
// page 1. findMergedPRCommit must follow the Link rel="next" header (the
// same pagination GetComments already uses) rather than reading only the
// first page.
func TestCheckExternal_MergedPRCrossReferenceOnPage2_StillFound(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := github.NewClient(srv.Client(), srv.URL, "acme", "widgets")
	c.Reachability = &fakeReachability{ancestor: map[string]bool{"page2sha": true}}

	mux.HandleFunc("/repos/acme/widgets/issues/60", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":60,"state":"closed"}`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues/60/timeline", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "2" {
			w.Header().Set("Link", `<`+srv.URL+`/repos/acme/widgets/issues/60/timeline?page=2>; rel="next"`)
			_, _ = w.Write([]byte(`[{"event":"labeled"}]`))
			return
		}
		_, _ = w.Write([]byte(`[
			{"event":"cross-referenced","source":{"issue":{"number":61,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}},
			{"event":"closed","commit_id":"page2sha"}
		]`))
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/61", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"page2sha"}`))
	})

	state, err := c.CheckExternal(context.Background(), "60", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalSatisfied {
		t.Fatalf("state = %s, want EXTERNAL_SATISFIED (merged PR cross-reference was on timeline page 2)", state)
	}
}

// TestCheckExternal_UnrelatedMergedPRMention_NotSatisfied is the P2
// over-linkage fix's regression test: a GitHub cross-reference fires for
// any mention of the issue, not just its closing PR. An unrelated merged
// PR that merely mentions the external issue (e.g. "related to #62") must
// not satisfy it — only the PR that is authoritatively the issue's closer
// (its merge commit matches the timeline "closed" event's commit_id) may.
func TestCheckExternal_UnrelatedMergedPRMention_NotSatisfied(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/62":
			_, _ = w.Write([]byte(`{"number":62,"state":"open"}`))
		case "/repos/acme/widgets/issues/62/timeline":
			// PR #70 merged and mentions #62, but never closed it (no
			// "closed" event at all -- #62 is still open).
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":70,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}}]`))
		case "/repos/acme/widgets/pulls/70":
			_, _ = w.Write([]byte(`{"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"unrelated-sha"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	reach := &fakeReachability{ancestor: map[string]bool{"unrelated-sha": true}}
	c.Reachability = reach

	state, err := c.CheckExternal(context.Background(), "62", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalPending {
		t.Fatalf("state = %s, want EXTERNAL_PENDING (an unrelated merged PR mention must not satisfy the issue)", state)
	}
	if len(reach.calls) != 0 {
		t.Fatalf("reachability calls = %v, want none (the unrelated PR must never even be checked)", reach.calls)
	}
}

// TestCheckExternal_GenuineClosingPR_Satisfied confirms the fixed matching
// logic still recognizes the real case: a "closed" timeline event whose
// commit_id matches a cross-referenced merged PR's merge_commit_sha is
// that PR's authoritative closing, and satisfies the issue once reachable.
func TestCheckExternal_GenuineClosingPR_Satisfied(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/63":
			_, _ = w.Write([]byte(`{"number":63,"state":"closed"}`))
		case "/repos/acme/widgets/issues/63/timeline":
			_, _ = w.Write([]byte(`[
				{"event":"cross-referenced","source":{"issue":{"number":70,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}},
				{"event":"cross-referenced","source":{"issue":{"number":71,"pull_request":{"merged_at":"2026-01-02T00:00:00Z"}}}},
				{"event":"closed","commit_id":"real-close-sha"}
			]`))
		case "/repos/acme/widgets/pulls/70":
			_, _ = w.Write([]byte(`{"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"unrelated-sha"}`))
		case "/repos/acme/widgets/pulls/71":
			_, _ = w.Write([]byte(`{"merged_at":"2026-01-02T00:00:00Z","merge_commit_sha":"real-close-sha"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	reach := &fakeReachability{ancestor: map[string]bool{"real-close-sha": true}}
	c.Reachability = reach

	state, err := c.CheckExternal(context.Background(), "63", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalSatisfied {
		t.Fatalf("state = %s, want EXTERNAL_SATISFIED", state)
	}
	if len(reach.calls) != 1 || reach.calls[0] != "real-close-sha" {
		t.Fatalf("reachability calls = %v, want [real-close-sha]", reach.calls)
	}
}

// TestCheckExternal_ClosedByDirectCommit_NoMatchingMergedPR_ReturnsInvalid
// covers an issue closed by a commit_id that doesn't correspond to any
// discovered merged PR (e.g. closed by a direct push, not a PR): still no
// "associated merged PR", so EXTERNAL_INVALID.
func TestCheckExternal_ClosedByDirectCommit_NoMatchingMergedPR_ReturnsInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/64":
			_, _ = w.Write([]byte(`{"number":64,"state":"closed"}`))
		case "/repos/acme/widgets/issues/64/timeline":
			_, _ = w.Write([]byte(`[{"event":"closed","commit_id":"direct-push-sha"}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	c.Reachability = &fakeReachability{}

	state, err := c.CheckExternal(context.Background(), "64", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalInvalid {
		t.Fatalf("state = %s, want EXTERNAL_INVALID", state)
	}
}

func TestCheckExternal_NoReachabilityConfigured_ErrorsRatherThanGuessing(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/54":
			_, _ = w.Write([]byte(`{"number":54,"state":"closed"}`))
		case "/repos/acme/widgets/issues/54/timeline":
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":57,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}},{"event":"closed","commit_id":"def456"}]`))
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
			_, _ = w.Write([]byte(`[{"event":"cross-referenced","source":{"issue":{"number":59,"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}}},{"event":"closed","commit_id":"ghi789"}]`))
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
