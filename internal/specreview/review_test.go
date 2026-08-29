package specreview

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

func makeTestSpecPC() planningagent.PlanningContext {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	decisions := []*planning.Artifact{
		{
			Kind:     planning.KindDecision,
			Revision: "dec-rev",
			Sections: []planning.Section{{Heading: "Question", Body: "Which storage?"}, {Heading: "Outcome", Body: "SQLite"}},
		},
	}

	spec := &planning.Artifact{
		Kind:     planning.KindSpec,
		Revision: "spec-rev",
		Sections: []planning.Section{
			{Heading: "Context", Body: "A widget builder using SQLite"},
			{Heading: "Requirements", Body: "REQ-001: Widget must be buildable\nREQ-002: Widget must be testable"},
			{Heading: "Non-Goals", Body: "Not building a gadget"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
			{Kind: planning.KindDecision, ID: "001-storage", Revision: "dec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goal},
		{ID: "001-storage", Artifact: decisions[0]},
		{ID: "spec", Artifact: spec},
	}

	pc, err := planningagent.Compile(planningagent.PlanningContext{}.Repository, artifacts, nil)
	if err != nil {
		panic(err)
	}
	return pc
}

func TestSpecificationReview_Approved(t *testing.T) {
	pc := makeTestSpecPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "APPROVED",
		"summary": "Specification is clear, complete, and well-structured",
		"findings": []
	}`+"\n```\n")

	res, err := Review(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}

	if res.Verdict != VerdictApproved {
		t.Errorf("verdict = %q, want %q", res.Verdict, VerdictApproved)
	}
	if res.Summary != "Specification is clear, complete, and well-structured" {
		t.Errorf("summary = %q", res.Summary)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %v, want empty", res.Findings)
	}
}

func TestSpecificationReview_ChangesRequired(t *testing.T) {
	pc := makeTestSpecPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification has issues",
		"findings": [
			{"severity": "ERROR", "file": "", "line": 0, "message": "Requirements section lacks measurable acceptance criteria"},
			{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}
		]
	}`+"\n```\n")

	res, err := Review(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}

	if res.Verdict != VerdictChangesRequired {
		t.Errorf("verdict = %q, want %q", res.Verdict, VerdictChangesRequired)
	}
	if len(res.Findings) != 2 {
		t.Errorf("len(findings) = %d, want 2", len(res.Findings))
	}
	if res.Findings[0].Severity != SeverityError {
		t.Errorf("findings[0].severity = %q, want %q", res.Findings[0].Severity, SeverityError)
	}
	if res.Findings[0].Message != "Requirements section lacks measurable acceptance criteria" {
		t.Errorf("findings[0].message = %q", res.Findings[0].Message)
	}
	if res.Findings[1].Severity != SeverityWarning {
		t.Errorf("findings[1].severity = %q, want %q", res.Findings[1].Severity, SeverityWarning)
	}
}

func TestSpecificationReviewValidation(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "invalid_verdict",
			json:    `{"verdict":"INVALID","summary":"...","findings":[]}`,
			wantErr: true,
		},
		{
			name:    "blank_summary_on_approved",
			json:    `{"verdict":"APPROVED","summary":"","findings":[]}`,
			wantErr: true,
		},
		{
			name:    "blank_summary_on_changes",
			json:    `{"verdict":"CHANGES_REQUIRED","summary":"","findings":[]}`,
			wantErr: true,
		},
		{
			name:    "empty_findings_on_changes_required",
			json:    `{"verdict":"CHANGES_REQUIRED","summary":"has issues","findings":[]}`,
			wantErr: true,
		},
		{
			name:    "invalid_severity",
			json:    `{"verdict":"CHANGES_REQUIRED","summary":"has issues","findings":[{"severity":"INVALID","file":"","line":0,"message":"msg"}]}`,
			wantErr: true,
		},
		{
			name:    "valid_approved",
			json:    `{"verdict":"APPROVED","summary":"good spec","findings":[]}`,
			wantErr: false,
		},
		{
			name:    "valid_changes_required",
			json:    `{"verdict":"CHANGES_REQUIRED","summary":"has issues","findings":[{"severity":"ERROR","file":"","line":0,"message":"issue 1"},{"severity":"WARNING","file":"","line":0,"message":"issue 2"}]}`,
			wantErr: false,
		},
		{
			name:    "valid_with_info_findings",
			json:    `{"verdict":"APPROVED","summary":"good spec","findings":[{"severity":"INFO","file":"","line":0,"message":"minor note"}]}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := makeTestSpecPC()
			backend := planningagent.NewFakeBackend()
			backend.ProgramResult("specification-review", "```json\n"+tt.json+"\n```\n")

			_, err := Review(context.Background(), backend, pc)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
