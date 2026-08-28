package tracker_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestApplyOverrides_NoOverridePassesThroughParsed(t *testing.T) {
	got := tracker.ApplyOverrides("42", []string{"1", "2"}, map[string][]string{
		"99": {"3"},
	})
	if !equalSlices(got, []string{"1", "2"}) {
		t.Fatalf("got %v", got)
	}
}

func TestApplyOverrides_OverridePrecedesParsed(t *testing.T) {
	got := tracker.ApplyOverrides("42", []string{"1", "2"}, map[string][]string{
		"42": {"9"},
	})
	if !equalSlices(got, []string{"9"}) {
		t.Fatalf("got %v", got)
	}
}

func TestApplyOverrides_EmptyOverrideListWins(t *testing.T) {
	// An explicit empty override list means "no dependencies", which must
	// still take precedence over a parsed body block.
	got := tracker.ApplyOverrides("42", []string{"1", "2"}, map[string][]string{
		"42": {},
	})
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestApplyOverrides_NilOverridesMapIsNoop(t *testing.T) {
	got := tracker.ApplyOverrides("42", []string{"1"}, nil)
	if !equalSlices(got, []string{"1"}) {
		t.Fatalf("got %v", got)
	}
}
