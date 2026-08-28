package planning_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

func sampleArtifact() *planning.Artifact {
	a := &planning.Artifact{
		Kind:  planning.KindDecision,
		State: "draft",
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindDecision, ID: "001-foo", Revision: "bbb"},
			{Kind: planning.KindGoal, ID: "goal", Revision: "aaa"},
		},
		Sections: []planning.Section{
			{Heading: "Question", Body: "Where does Dependency metadata live?"},
			{Heading: "Answer", Body: "Tracker-local by default."},
		},
	}
	a.Revision = planning.ComputeRevision(a)
	return a
}

func TestParseRenderRoundTrip(t *testing.T) {
	a := sampleArtifact()
	rendered := planning.Render(a)

	parsed, err := planning.Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(a, parsed) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", parsed, a)
	}

	rendered2 := planning.Render(parsed)
	if string(rendered) != string(rendered2) {
		t.Fatalf("render is not idempotent:\nfirst:\n%s\nsecond:\n%s", rendered, rendered2)
	}
}

func TestRenderCanonicalizesHandAlignedBlock(t *testing.T) {
	a := sampleArtifact()
	canonical := planning.Render(a)

	// Hand-written variant: different YAML key order, extra whitespace,
	// derived_from listed in reverse order, trailing whitespace on a
	// section body line. All content is the same.
	handAligned := "<!-- forge\n" +
		"kind:    decision\n" +
		"revision: " + a.Revision + "\n" +
		"derived_from:\n" +
		"  - id: 001-foo\n" +
		"    kind: decision\n" +
		"    revision: bbb\n" +
		"  - id: goal\n" +
		"    kind: goal\n" +
		"    revision: aaa\n" +
		"state: draft\n" +
		"approved_revision: \"\"\n" +
		"approved_by: \"\"\n" +
		"approved_at: \"\"\n" +
		"-->\n" +
		"\n" +
		"## Question   \n" +
		"\n" +
		"Where does Dependency metadata live?\n" +
		"\n" +
		"## Answer\n" +
		"\n" +
		"Tracker-local by default.\n"

	parsed, err := planning.Parse([]byte(handAligned))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := planning.Render(parsed)
	if string(got) != string(canonical) {
		t.Fatalf("hand-aligned block did not canonicalize:\n got:\n%s\nwant:\n%s", got, canonical)
	}
}

func TestComputeRevisionStableUnderReformatting(t *testing.T) {
	base := sampleArtifact()
	base.Revision = ""
	want := planning.ComputeRevision(base)

	reordered := &planning.Artifact{
		Kind: base.Kind,
		DerivedFrom: []planning.DerivedFromEntry{
			base.DerivedFrom[1],
			base.DerivedFrom[0],
		},
		Sections: []planning.Section{
			{Heading: "Question", Body: "Where does Dependency metadata live?\n\n"},
			{Heading: "Answer", Body: "Tracker-local by default.   "},
		},
	}
	got := planning.ComputeRevision(reordered)

	if got != want {
		t.Fatalf("revision changed under reformatting: got %s want %s", got, want)
	}
}

func TestComputeRevisionIgnoresWorkflowFields(t *testing.T) {
	a := sampleArtifact()
	base := planning.ComputeRevision(a)

	variant := *a
	variant.Revision = "something-else"
	variant.State = "approved"
	variant.ApprovedRevision = base
	variant.ApprovedBy = "alice"
	variant.ApprovedAt = "2026-08-28T00:00:00Z"

	if got := planning.ComputeRevision(&variant); got != base {
		t.Fatalf("revision changed with only workflow fields touched: got %s want %s", got, base)
	}
}

func TestComputeRevisionChangesWithDefinitionalContent(t *testing.T) {
	a := sampleArtifact()
	base := planning.ComputeRevision(a)

	changedBody := *a
	changedBody.Sections = []planning.Section{
		{Heading: "Question", Body: "Where does Dependency metadata live?"},
		{Heading: "Answer", Body: "Somewhere else entirely."},
	}
	if got := planning.ComputeRevision(&changedBody); got == base {
		t.Fatalf("revision did not change when section body changed")
	}

	changedDerived := *a
	changedDerived.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: "different"},
	}
	if got := planning.ComputeRevision(&changedDerived); got == base {
		t.Fatalf("revision did not change when derived_from changed")
	}

	changedKind := *a
	changedKind.Kind = planning.KindSpec
	if got := planning.ComputeRevision(&changedKind); got == base {
		t.Fatalf("revision did not change when kind changed")
	}
}

func TestStale(t *testing.T) {
	a := sampleArtifact()
	if planning.Stale(a) {
		t.Fatalf("freshly computed artifact should not be stale")
	}

	a.Sections[1].Body = "Something new, without recomputing the revision."
	if !planning.Stale(a) {
		t.Fatalf("artifact with unrecorded content change should be stale")
	}
}

func TestApprovedAndReady(t *testing.T) {
	a := sampleArtifact()
	if planning.Approved(a) {
		t.Fatalf("artifact with no ApprovedRevision should not be approved")
	}
	if planning.Ready(a) {
		t.Fatalf("unapproved decision should not be ready")
	}

	a.ApprovedRevision = a.Revision
	a.ApprovedBy = "alice"
	if !planning.Approved(a) {
		t.Fatalf("artifact approved at its current revision should be approved")
	}
	if !planning.Ready(a) {
		t.Fatalf("approved decision should be ready")
	}

	nonDecision := sampleArtifact()
	nonDecision.Kind = planning.KindSpec
	nonDecision.ApprovedRevision = nonDecision.Revision
	if planning.Ready(nonDecision) {
		t.Fatalf("Ready should only ever be true for decisions")
	}

	// Editing content after approval un-approves it, with no stored bit.
	a.Sections[1].Body = "Edited after approval."
	if planning.Approved(a) {
		t.Fatalf("editing content after approval should un-approve it")
	}
	if planning.Ready(a) {
		t.Fatalf("editing content after approval should make it not ready")
	}
}

func TestParseMissingBlock(t *testing.T) {
	if _, err := planning.Parse([]byte("## Question\n\nNo metadata block.\n")); err == nil {
		t.Fatalf("expected error for missing metadata block")
	}
}

func TestParseUnterminatedBlock(t *testing.T) {
	if _, err := planning.Parse([]byte("<!-- forge\nrevision: abc\n")); err == nil {
		t.Fatalf("expected error for unterminated metadata block")
	}
}

func TestParsePreservesSectionOrderAndCRLF(t *testing.T) {
	a := sampleArtifact()
	rendered := planning.Render(a)
	crlf := strings.ReplaceAll(string(rendered), "\n", "\r\n")

	parsed, err := planning.Parse([]byte(crlf))
	if err != nil {
		t.Fatalf("Parse CRLF: %v", err)
	}
	if len(parsed.Sections) != 2 || parsed.Sections[0].Heading != "Question" || parsed.Sections[1].Heading != "Answer" {
		t.Fatalf("unexpected sections after CRLF parse: %+v", parsed.Sections)
	}
}
