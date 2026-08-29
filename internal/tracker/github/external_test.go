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

// graphQLHandler builds an httptest handler that answers the single GraphQL
// query CheckExternal issues (closedByPullRequestsReferences) with a fixed
// data payload. The payload is the JSON that would nest under GraphQL's
// top-level "data" key. Any other path fails the test, so an accidental
// REST fallthrough is caught.
func graphQLHandler(t *testing.T, dataJSON string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = fmt.Fprintf(w, `{"data":%s}`, dataJSON)
	}
}

// issueData renders the GraphQL data payload for an issue with the given
// state and closing-PR nodes. nodesJSON is the JSON array literal for
// closedByPullRequestsReferences.nodes.
func issueData(state, nodesJSON string) string {
	return fmt.Sprintf(
		`{"repository":{"issue":{"state":%q,"closedByPullRequestsReferences":{"nodes":%s}}}}`,
		state, nodesJSON)
}

func TestCheckExternal_MergedAndReachable_ReturnsSatisfied(t *testing.T) {
	c, _ := newTestClient(t, graphQLHandler(t,
		issueData("CLOSED", `[{"merged":true,"mergeCommit":{"oid":"abc123"}}]`)))
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

// TestCheckExternal_SquashMergedCloser_ReturnsSatisfied is the regression
// test for the squash-merge bug: a PR merged via squash (or rebase) closes
// the issue but leaves the REST timeline's "closed" event commit_id null, so
// the old timeline-commit_id association reported "no merged PR" and the
// dependency became permanently unsatisfiable. closedByPullRequestsReferences
// still names the merged closer and its merge commit, so once that commit is
// reachable the dependency is satisfied.
func TestCheckExternal_SquashMergedCloser_ReturnsSatisfied(t *testing.T) {
	c, _ := newTestClient(t, graphQLHandler(t,
		issueData("CLOSED", `[{"merged":true,"mergeCommit":{"oid":"squash-sha"}}]`)))
	reach := &fakeReachability{ancestor: map[string]bool{"squash-sha": true}}
	c.Reachability = reach

	state, err := c.CheckExternal(context.Background(), "96", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalSatisfied {
		t.Fatalf("state = %s, want EXTERNAL_SATISFIED (squash-merged closing PR)", state)
	}
}

func TestCheckExternal_MergedButNotReachable_ReturnsPending(t *testing.T) {
	c, _ := newTestClient(t, graphQLHandler(t,
		issueData("CLOSED", `[{"merged":true,"mergeCommit":{"oid":"abc123"}}]`)))
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
	c, _ := newTestClient(t, graphQLHandler(t, issueData("CLOSED", `[]`)))
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
	c, _ := newTestClient(t, graphQLHandler(t,
		issueData("CLOSED", `[{"merged":false,"mergeCommit":null}]`)))
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
	c, _ := newTestClient(t, graphQLHandler(t, issueData("OPEN", `[]`)))
	c.Reachability = &fakeReachability{}

	state, err := c.CheckExternal(context.Background(), "53", "origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != tracker.ExternalPending {
		t.Fatalf("state = %s, want EXTERNAL_PENDING", state)
	}
}

// TestCheckExternal_UnrelatedMergedPRMention_NotSatisfied confirms an
// unrelated merged PR that merely mentions the issue (e.g. "related to #62")
// does not satisfy it: closedByPullRequestsReferences names only PRs that
// close the issue, so a bare mention never appears there. The still-open
// issue yields EXTERNAL_PENDING and reachability is never consulted.
func TestCheckExternal_UnrelatedMergedPRMention_NotSatisfied(t *testing.T) {
	c, _ := newTestClient(t, graphQLHandler(t, issueData("OPEN", `[]`)))
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
		t.Fatalf("reachability calls = %v, want none (an unrelated PR must never even be checked)", reach.calls)
	}
}

// TestCheckExternal_GenuineClosingPR_Satisfied confirms that among several
// closing-PR references, the first merged one with a recorded merge commit
// is used and satisfies the issue once reachable.
func TestCheckExternal_GenuineClosingPR_Satisfied(t *testing.T) {
	c, _ := newTestClient(t, graphQLHandler(t, issueData("CLOSED", `[
		{"merged":false,"mergeCommit":null},
		{"merged":true,"mergeCommit":{"oid":"real-close-sha"}}
	]`)))
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

// TestCheckExternal_ClosedByDirectCommit_NoMergedPR_ReturnsInvalid covers an
// issue closed by a direct push (not a PR): no closing PR reference exists,
// so there is no merge commit to satisfy the dependency -> EXTERNAL_INVALID.
func TestCheckExternal_ClosedByDirectCommit_NoMergedPR_ReturnsInvalid(t *testing.T) {
	c, _ := newTestClient(t, graphQLHandler(t, issueData("CLOSED", `[]`)))
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
	c, _ := newTestClient(t, graphQLHandler(t,
		issueData("CLOSED", `[{"merged":true,"mergeCommit":{"oid":"def456"}}]`)))
	// c.Reachability left nil.

	if _, err := c.CheckExternal(context.Background(), "54", "origin/main"); err == nil {
		t.Fatal("expected an error when no GitReachabilityChecker is configured, got nil")
	}
}

func TestCheckExternal_ReachabilityError_Propagates(t *testing.T) {
	c, _ := newTestClient(t, graphQLHandler(t,
		issueData("CLOSED", `[{"merged":true,"mergeCommit":{"oid":"ghi789"}}]`)))
	c.Reachability = &fakeReachability{err: fmt.Errorf("git: boom")}

	if _, err := c.CheckExternal(context.Background(), "58", "origin/main"); err == nil {
		t.Fatal("expected the reachability error to propagate, got nil")
	}
}

// TestCheckExternal_GraphQLError_Propagates confirms a GraphQL-level error
// (200 carrying an "errors" array, e.g. the issue does not exist) surfaces
// as an error rather than being decoded as an absent/open issue.
func TestCheckExternal_GraphQLError_Propagates(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Could not resolve to an Issue"}]}`))
	})
	c.Reachability = &fakeReachability{}

	if _, err := c.CheckExternal(context.Background(), "999", "origin/main"); err == nil {
		t.Fatal("expected a GraphQL error to propagate, got nil")
	}
}

var _ tracker.ExternalChecker = (*github.Client)(nil)
