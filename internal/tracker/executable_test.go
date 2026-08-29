package tracker_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestValidateExecutable_NoProvenanceBlock_IsExecutable(t *testing.T) {
	if err := tracker.ValidateExecutable("42", "plain issue body\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExecutable_Ready_IsExecutable(t *testing.T) {
	body := tracker.RenderForgeProvenance(tracker.ForgeProvenance{Status: tracker.ProvenanceReady, Project: "p"})
	if err := tracker.ValidateExecutable("42", body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExecutable_Materializing_IsRejected(t *testing.T) {
	body := tracker.RenderForgeProvenance(tracker.ForgeProvenance{Status: tracker.ProvenanceMaterializing, Project: "p"})
	err := tracker.ValidateExecutable("42", body)
	if err == nil {
		t.Fatal("expected an error for a materializing issue")
	}
	var notExecutable *tracker.NotExecutableError
	if !isNotExecutableError(err, &notExecutable) {
		t.Fatalf("got %T, want *tracker.NotExecutableError", err)
	}
}

func TestValidateExecutable_MalformedProvenance_IsRejected(t *testing.T) {
	err := tracker.ValidateExecutable("42", "## Forge Provenance\nfreeform text\n")
	if err == nil {
		t.Fatal("expected an error for a malformed provenance block")
	}
}

func isNotExecutableError(err error, target **tracker.NotExecutableError) bool {
	e, ok := err.(*tracker.NotExecutableError)
	if ok {
		*target = e
	}
	return ok
}
