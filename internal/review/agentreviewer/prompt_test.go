package agentreviewer

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
)

func TestBuildPolicyNotes_IncludesParentSpecWhenProvided(t *testing.T) {
	req := review.Request{
		Issue: domain.Issue{ID: "296", Title: "Sub-issue", Body: "## Parent — Spec: constructorfleet/forge#284"},
		Diff:  "diff --git a b",
	}
	notes := buildPolicyNotes(req, "rubric text", "Title: Provider split\n\nUS10: compose the merge verdict from each capability's slice.")

	if !strings.Contains(notes, "## Parent spec") {
		t.Fatalf("notes missing %q heading: %q", "## Parent spec", notes)
	}
	if !strings.Contains(notes, "US10: compose the merge verdict") {
		t.Fatalf("notes missing parent spec content: %q", notes)
	}
	// The parent spec section must come before the diff so a reader (and
	// the reviewing agent) sees cross-ticket intent ahead of the change
	// under review.
	if strings.Index(notes, "## Parent spec") > strings.Index(notes, "## Diff under review") {
		t.Errorf("expected \"## Parent spec\" before \"## Diff under review\", got: %q", notes)
	}
}

func TestBuildPolicyNotes_OmitsParentSpecSectionWhenEmpty(t *testing.T) {
	req := review.Request{
		Issue: domain.Issue{ID: "42", Title: "Fix the thing", Body: "Do the thing correctly."},
		Diff:  "diff --git a b",
	}
	notes := buildPolicyNotes(req, "rubric text", "")

	if strings.Contains(notes, "## Parent spec") {
		t.Errorf("notes should not contain %q when parentSpec is empty: %q", "## Parent spec", notes)
	}
}
