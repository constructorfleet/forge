package review_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
)

func TestFakeReviewer_ReturnsProgrammedApprovedResult(t *testing.T) {
	fake := review.NewFakeReviewer()
	fake.ProgramResult("issue-1", review.Result{Verdict: review.VerdictApproved, Summary: "looks good"})

	req := review.Request{Issue: domain.Issue{ID: "issue-1"}, Diff: "diff --git a b"}
	result, err := fake.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review returned unexpected error: %v", err)
	}
	if result.Verdict != review.VerdictApproved {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
}

func TestFakeReviewer_ReturnsProgrammedChangesRequiredResultWithFindings(t *testing.T) {
	fake := review.NewFakeReviewer()
	fake.ProgramResult("issue-2", review.Result{
		Verdict: review.VerdictChangesRequired,
		Summary: "one issue found",
		Findings: []review.Finding{
			{Severity: review.SeverityError, File: "main.go", Line: 42, Message: "unhandled error"},
		},
	})

	result, err := fake.Review(context.Background(), review.Request{Issue: domain.Issue{ID: "issue-2"}})
	if err != nil {
		t.Fatalf("Review returned unexpected error: %v", err)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictChangesRequired)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(result.Findings))
	}
	f := result.Findings[0]
	if f.Severity != review.SeverityError || f.File != "main.go" || f.Line != 42 || f.Message != "unhandled error" {
		t.Errorf("Findings[0] = %+v, want Severity ERROR File main.go Line 42 Message %q", f, "unhandled error")
	}
}

func TestFakeReviewer_RecordsEachInvocationWithItsRequest(t *testing.T) {
	fake := review.NewFakeReviewer()
	fake.ProgramResult("issue-3", review.Result{Verdict: review.VerdictApproved})

	req := review.Request{Issue: domain.Issue{ID: "issue-3"}, Diff: "some diff"}
	if _, err := fake.Review(context.Background(), req); err != nil {
		t.Fatalf("Review returned unexpected error: %v", err)
	}

	got := fake.Invocations()
	if len(got) != 1 {
		t.Fatalf("Invocations() len = %d, want 1", len(got))
	}
	if got[0].Diff != "some diff" || got[0].Issue.ID != "issue-3" {
		t.Errorf("Invocations()[0] = %+v, want Diff %q Issue.ID issue-3", got[0], "some diff")
	}
}

func TestFakeReviewer_ExecuteErrorsWhenScenarioNotProgrammed(t *testing.T) {
	fake := review.NewFakeReviewer()
	_, err := fake.Review(context.Background(), review.Request{Issue: domain.Issue{ID: "unprogrammed"}})
	if err == nil {
		t.Fatal("Review() error = nil, want an error for an unprogrammed scenario")
	}
}

func TestFakeReviewer_RepeatsFinalQueuedOutcomeOnLaterCalls(t *testing.T) {
	fake := review.NewFakeReviewer()
	fake.ProgramResult("issue-repeat", review.Result{Verdict: review.VerdictChangesRequired})
	fake.ProgramResult("issue-repeat", review.Result{Verdict: review.VerdictApproved})

	req := review.Request{Issue: domain.Issue{ID: "issue-repeat"}}
	first, err := fake.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review(first): %v", err)
	}
	second, err := fake.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review(second): %v", err)
	}
	third, err := fake.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review(third): %v", err)
	}

	if first.Verdict != review.VerdictChangesRequired {
		t.Errorf("first Verdict = %q, want %q", first.Verdict, review.VerdictChangesRequired)
	}
	if second.Verdict != review.VerdictApproved || third.Verdict != review.VerdictApproved {
		t.Errorf("second/third Verdict = %q/%q, want %q repeated", second.Verdict, third.Verdict, review.VerdictApproved)
	}
}

func TestFakeReviewer_ProgramErrorReturnsExactErrorWithoutFallingThroughToDefault(t *testing.T) {
	fake := review.NewFakeReviewer()
	sentinel := errors.New("boom")
	fake.ProgramError("issue-err", sentinel)
	fake.ProgramDefault(review.Result{Verdict: review.VerdictApproved})

	result, err := fake.Review(context.Background(), review.Request{Issue: domain.Issue{ID: "issue-err"}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Review() error = %v, want %v", err, sentinel)
	}
	if result.Verdict != "" || result.Summary != "" || len(result.Findings) != 0 {
		t.Errorf("Review() result = %+v, want zero value alongside a programmed error", result)
	}
}

func TestFakeReviewer_ProgramDefaultAppliesOnlyToUnprogrammedScenarios(t *testing.T) {
	fake := review.NewFakeReviewer()
	fake.ProgramDefault(review.Result{Verdict: review.VerdictChangesRequired, Summary: "default rejection"})
	fake.ProgramResult("issue-specific", review.Result{Verdict: review.VerdictApproved, Summary: "specific approval"})

	defaultResult, err := fake.Review(context.Background(), review.Request{Issue: domain.Issue{ID: "issue-unprogrammed"}})
	if err != nil {
		t.Fatalf("Review(unprogrammed): %v", err)
	}
	if defaultResult.Verdict != review.VerdictChangesRequired {
		t.Errorf("unprogrammed scenario Verdict = %q, want default %q", defaultResult.Verdict, review.VerdictChangesRequired)
	}

	specificResult, err := fake.Review(context.Background(), review.Request{Issue: domain.Issue{ID: "issue-specific"}})
	if err != nil {
		t.Fatalf("Review(issue-specific): %v", err)
	}
	if specificResult.Verdict != review.VerdictApproved {
		t.Errorf("issue-specific Verdict = %q, want its programmed %q, not the default", specificResult.Verdict, review.VerdictApproved)
	}
}

func TestFakeReviewer_InvocationsReturnsIndependentCopyAcrossCalls(t *testing.T) {
	fake := review.NewFakeReviewer()
	fake.ProgramResult("issue-copy", review.Result{Verdict: review.VerdictApproved})
	req := review.Request{Diff: "original diff", Issue: domain.Issue{ID: "issue-copy"}}
	if _, err := fake.Review(context.Background(), req); err != nil {
		t.Fatalf("Review returned unexpected error: %v", err)
	}

	got := fake.Invocations()
	got[0].Diff = "mutated diff"

	again := fake.Invocations()
	if again[0].Diff != "original diff" {
		t.Errorf("Invocations() = %q after mutating a prior copy, want %q unaffected", again[0].Diff, "original diff")
	}
}
