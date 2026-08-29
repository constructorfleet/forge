package tracker_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestParseForgeProvenance_RoundTrip(t *testing.T) {
	p := tracker.ForgeProvenance{
		Status:       tracker.ProvenanceReady,
		TempKey:      "TKT-003",
		Project:      "my-feature",
		SpecRevision: "abc123",
		PlanRevision: "def456",
		Requirements: []string{"REQ-001", "REQ-004"},
		Decisions:    []string{"0007-use-postgres"},
	}
	rendered := tracker.RenderForgeProvenance(p)

	body := "### Objective\nDo the thing.\n\n" + rendered

	got, err := tracker.ParseForgeProvenance(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a provenance block, got nil")
	}
	if !reflect.DeepEqual(*got, p) {
		t.Fatalf("got %+v, want %+v", *got, p)
	}
}

func TestParseForgeProvenance_NoBlockPresent(t *testing.T) {
	got, err := tracker.ParseForgeProvenance("Some description.\n\n## Dependencies: None\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestParseForgeProvenance_NearMissHeading(t *testing.T) {
	_, err := tracker.ParseForgeProvenance("## Forge Provenance: v2\n<!-- forge\nstatus: ready\n-->\n")
	if err == nil {
		t.Fatal("expected an error for a near-miss heading")
	}
}

func TestParseForgeProvenance_MissingBlock(t *testing.T) {
	_, err := tracker.ParseForgeProvenance("## Forge Provenance\nsome freeform text\n")
	if err == nil {
		t.Fatal("expected an error when the metadata comment is missing")
	}
}

func TestParseForgeProvenance_UnclosedBlock(t *testing.T) {
	_, err := tracker.ParseForgeProvenance("## Forge Provenance\n<!-- forge\nstatus: ready\n")
	if err == nil {
		t.Fatal("expected an error for an unclosed block")
	}
}

func TestParseForgeProvenance_InvalidStatus(t *testing.T) {
	_, err := tracker.ParseForgeProvenance("## Forge Provenance\n<!-- forge\nstatus: bogus\n-->\n")
	if err == nil {
		t.Fatal("expected an error for an invalid status")
	}
}

func TestParseForgeProvenance_MaterializingStatus(t *testing.T) {
	got, err := tracker.ParseForgeProvenance("## Forge Provenance\n<!-- forge\nstatus: materializing\nproject: p\n-->\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.Status != tracker.ProvenanceMaterializing {
		t.Fatalf("got %+v", got)
	}
}

func TestRenderForgeProvenance_ContainsHeadingAndBlock(t *testing.T) {
	rendered := tracker.RenderForgeProvenance(tracker.ForgeProvenance{
		Status:  tracker.ProvenanceMaterializing,
		Project: "p",
	})
	if !strings.Contains(rendered, "## Forge Provenance") {
		t.Fatalf("rendered output missing heading: %q", rendered)
	}
	if !strings.Contains(rendered, "<!-- forge") || !strings.Contains(rendered, "-->") {
		t.Fatalf("rendered output missing comment delimiters: %q", rendered)
	}
}
