package agentreviewer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
)

func TestDetectParentRef_SpecPointerWithOwnerRepo(t *testing.T) {
	issue := domain.Issue{Body: "## Parent — Spec: constructorfleet/forge#284\n\nSome body."}
	id, ok := detectParentRef(issue)
	if !ok || id != "284" {
		t.Fatalf("detectParentRef = (%q, %v), want (\"284\", true)", id, ok)
	}
}

func TestDetectParentRef_BareSpecPointer(t *testing.T) {
	issue := domain.Issue{Body: "Spec: #99\n"}
	id, ok := detectParentRef(issue)
	if !ok || id != "99" {
		t.Fatalf("detectParentRef = (%q, %v), want (\"99\", true)", id, ok)
	}
}

func TestDetectParentRef_ParentHeadingWithIssueRefBelow(t *testing.T) {
	issue := domain.Issue{Body: "## Parent\n\nSee #123 for the full spec.\n\n## Description\n\nmore text"}
	id, ok := detectParentRef(issue)
	if !ok || id != "123" {
		t.Fatalf("detectParentRef = (%q, %v), want (\"123\", true)", id, ok)
	}
}

func TestDetectParentRef_FallsBackToDependenciesBlock(t *testing.T) {
	issue := domain.Issue{
		Body:         "No parent pointer here.",
		Dependencies: []domain.Dependency{{IssueID: "42", DependsOnID: "7"}},
	}
	id, ok := detectParentRef(issue)
	if !ok || id != "7" {
		t.Fatalf("detectParentRef = (%q, %v), want (\"7\", true)", id, ok)
	}
}

func TestDetectParentRef_NoReferenceFound(t *testing.T) {
	issue := domain.Issue{Body: "Just a normal issue body, no cross-ticket pointer."}
	if _, ok := detectParentRef(issue); ok {
		t.Fatalf("detectParentRef found a reference in a body with none")
	}
}

func TestResolveParentSpec_FetchesAndFormatsReferencedParent(t *testing.T) {
	req := review.Request{
		Issue: domain.Issue{ID: "296", Body: "## Parent — Spec: constructorfleet/forge#284"},
		ParentFetcher: func(_ context.Context, id string) (domain.Issue, error) {
			if id != "284" {
				t.Fatalf("ParentFetcher called with %q, want %q", id, "284")
			}
			return domain.Issue{ID: "284", Title: "Provider split", Body: "US10: compose the merge verdict."}, nil
		},
	}

	got := resolveParentSpec(context.Background(), req)
	if !strings.Contains(got, "Provider split") || !strings.Contains(got, "US10") {
		t.Errorf("resolveParentSpec = %q, want it to contain the parent's title and body", got)
	}
}

func TestResolveParentSpec_EmptyWhenNoFetcherWired(t *testing.T) {
	req := review.Request{
		Issue: domain.Issue{ID: "296", Body: "## Parent — Spec: constructorfleet/forge#284"},
	}
	if got := resolveParentSpec(context.Background(), req); got != "" {
		t.Errorf("resolveParentSpec = %q, want empty when ParentFetcher is nil", got)
	}
}

func TestResolveParentSpec_EmptyWhenNoReferenceInBody(t *testing.T) {
	called := false
	req := review.Request{
		Issue: domain.Issue{ID: "42", Body: "No cross-ticket pointer."},
		ParentFetcher: func(_ context.Context, id string) (domain.Issue, error) {
			called = true
			return domain.Issue{}, nil
		},
	}
	if got := resolveParentSpec(context.Background(), req); got != "" {
		t.Errorf("resolveParentSpec = %q, want empty when body references no parent", got)
	}
	if called {
		t.Error("ParentFetcher was called despite no reference in the body")
	}
}

func TestResolveParentSpec_EmptyWhenFetchFails(t *testing.T) {
	req := review.Request{
		Issue: domain.Issue{ID: "296", Body: "## Parent — Spec: constructorfleet/forge#284"},
		ParentFetcher: func(_ context.Context, id string) (domain.Issue, error) {
			return domain.Issue{}, errors.New("boom")
		},
	}
	if got := resolveParentSpec(context.Background(), req); got != "" {
		t.Errorf("resolveParentSpec = %q, want empty when the fetch errors", got)
	}
}

func TestResolveParentSpec_TruncatesOversizedParentBody(t *testing.T) {
	huge := strings.Repeat("x", parentSpecMaxRunes+500)
	req := review.Request{
		Issue: domain.Issue{ID: "296", Body: "## Parent — Spec: constructorfleet/forge#284"},
		ParentFetcher: func(_ context.Context, id string) (domain.Issue, error) {
			return domain.Issue{ID: "284", Title: "Big spec", Body: huge}, nil
		},
	}
	got := resolveParentSpec(context.Background(), req)
	if len(got) >= len(huge) {
		t.Errorf("resolveParentSpec did not bound the parent body: got %d runes, source was %d runes", len(got), len(huge))
	}
}
