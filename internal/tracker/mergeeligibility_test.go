package tracker_test

import (
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestEvaluateMergeEligibility_AllClearIsMergeable(t *testing.T) {
	got := tracker.EvaluateMergeEligibility(tracker.MergeEligibilityInput{
		MergeStatus: tracker.ChangeRequestMergeStatus{Merged: true},
	})
	if !got.Mergeable || len(got.Blockers) != 0 {
		t.Fatalf("got = %+v, want Mergeable with no blockers", got)
	}
}

func TestEvaluateMergeEligibility_ChecksBlockerIsCarriedThrough(t *testing.T) {
	blocker := tracker.MergeBlocker{Reason: tracker.ChecksFailing, Source: tracker.CapabilityCI, RawDetail: "build: unit tests failed"}
	got := tracker.EvaluateMergeEligibility(tracker.MergeEligibilityInput{
		ChecksBlocker: &blocker,
		MergeStatus:   tracker.ChangeRequestMergeStatus{Merged: true},
	})
	if got.Mergeable {
		t.Fatalf("got.Mergeable = true, want false")
	}
	if len(got.Blockers) != 1 || got.Blockers[0] != blocker {
		t.Fatalf("got.Blockers = %+v, want [%+v]", got.Blockers, blocker)
	}
}

func TestEvaluateMergeEligibility_ConflictBlocksAndPreservesRawDetail(t *testing.T) {
	got := tracker.EvaluateMergeEligibility(tracker.MergeEligibilityInput{
		MergeStatus: tracker.ChangeRequestMergeStatus{Conflicted: true, RawDetail: "dirty"},
	})
	if got.Mergeable {
		t.Fatalf("got.Mergeable = true, want false")
	}
	want := tracker.MergeBlocker{Reason: tracker.Conflict, Source: tracker.CapabilitySCM, RawDetail: "dirty"}
	if len(got.Blockers) != 1 || got.Blockers[0] != want {
		t.Fatalf("got.Blockers = %+v, want [%+v]", got.Blockers, want)
	}
}

func TestEvaluateMergeEligibility_BehindBlocksAndPreservesRawDetail(t *testing.T) {
	got := tracker.EvaluateMergeEligibility(tracker.MergeEligibilityInput{
		MergeStatus: tracker.ChangeRequestMergeStatus{Behind: true, RawDetail: "behind"},
	})
	if got.Mergeable {
		t.Fatalf("got.Mergeable = true, want false")
	}
	want := tracker.MergeBlocker{Reason: tracker.Behind, Source: tracker.CapabilitySCM, RawDetail: "behind"}
	if len(got.Blockers) != 1 || got.Blockers[0] != want {
		t.Fatalf("got.Blockers = %+v, want [%+v]", got.Blockers, want)
	}
}

func TestEvaluateMergeEligibility_CombinesBlockersFromMultipleCapabilities(t *testing.T) {
	blocker := tracker.MergeBlocker{Reason: tracker.ChecksPending, Source: tracker.CapabilityCI}
	got := tracker.EvaluateMergeEligibility(tracker.MergeEligibilityInput{
		ChecksBlocker: &blocker,
		MergeStatus:   tracker.ChangeRequestMergeStatus{Behind: true, RawDetail: "behind"},
	})
	if got.Mergeable {
		t.Fatalf("got.Mergeable = true, want false")
	}
	wantReasons := []tracker.MergeBlockerReason{tracker.ChecksPending, tracker.Behind}
	gotReasons := make([]tracker.MergeBlockerReason, len(got.Blockers))
	for i, b := range got.Blockers {
		gotReasons[i] = b.Reason
	}
	if !reflect.DeepEqual(gotReasons, wantReasons) {
		t.Fatalf("got.Blockers reasons = %v, want %v", gotReasons, wantReasons)
	}
}
