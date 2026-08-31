package ticketplanreview

import (
	"context"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

func makeTestTicketPlanPC() planningagent.PlanningContext {
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

const sampleTicketPlan = `Ticket: TKT-001
### Objective
Implement widget builder core

### Requirements
REQ-001

### Acceptance Criteria
- Widget builds successfully
- All unit tests pass

### Dependencies
None

Ticket: TKT-002
### Objective
Add widget integration tests

### Requirements
REQ-002

### Acceptance Criteria
- Integration tests pass
- Coverage > 80%

### Dependencies
TKT-001
`

func TestTicketPlanReview_Approved(t *testing.T) {
	pc := makeTestTicketPlanPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("ticket-plan-review", `{
		"verdict": "APPROVED",
		"summary": "Ticket plan is well-structured with clear boundaries and full coverage",
		"findings": []
	}`)

	res, err := Review(context.Background(), backend, pc, sampleTicketPlan, []string{"REQ-001", "REQ-002"}, "spec-rev")
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}

	if res.Verdict != VerdictApproved {
		t.Errorf("verdict = %q, want %q", res.Verdict, VerdictApproved)
	}
	if res.Summary != "Ticket plan is well-structured with clear boundaries and full coverage" {
		t.Errorf("summary = %q", res.Summary)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %v, want empty", res.Findings)
	}
}

func TestTicketPlanReview_ChangesRequired(t *testing.T) {
	pc := makeTestTicketPlanPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("ticket-plan-review", `{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Ticket plan has issues with sizing and coupling",
		"findings": [
			{"severity": "ERROR", "ticket_key": "TKT-001", "requirement": "REQ-001", "message": "Ticket TKT-001 is too large, covers multiple unrelated responsibilities"},
			{"severity": "WARNING", "ticket_key": "TKT-002", "requirement": "", "message": "Ticket TKT-002 has vague acceptance criteria: 'Coverage > 80%' is not measurable"}
		]
	}`)

	res, err := Review(context.Background(), backend, pc, sampleTicketPlan, []string{"REQ-001", "REQ-002"}, "spec-rev")
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
	if res.Findings[0].Message != "Ticket TKT-001 is too large, covers multiple unrelated responsibilities" {
		t.Errorf("findings[0].message = %q", res.Findings[0].Message)
	}
	if res.Findings[0].TicketKey != "TKT-001" {
		t.Errorf("findings[0].ticket_key = %q, want TKT-001", res.Findings[0].TicketKey)
	}
	if res.Findings[1].Severity != SeverityWarning {
		t.Errorf("findings[1].severity = %q, want %q", res.Findings[1].Severity, SeverityWarning)
	}
	if res.Findings[1].Message != "Ticket TKT-002 has vague acceptance criteria: 'Coverage > 80%' is not measurable" {
		t.Errorf("findings[1].message = %q", res.Findings[1].Message)
	}
}

func TestTicketPlanReviewValidation(t *testing.T) {
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
			json:    `{"verdict":"APPROVED","summary":"good plan","findings":[]}`,
			wantErr: false,
		},
		{
			name:    "valid_changes_required",
			json:    `{"verdict":"CHANGES_REQUIRED","summary":"has issues","findings":[{"severity":"ERROR","ticket_key":"TKT-001","requirement":"REQ-001","message":"issue 1"},{"severity":"WARNING","ticket_key":"TKT-002","requirement":"","message":"issue 2"}]}`,
			wantErr: false,
		},
		{
			name:    "valid_with_info_findings",
			json:    `{"verdict":"APPROVED","summary":"good plan","findings":[{"severity":"INFO","ticket_key":"","requirement":"","message":"minor note"}]}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := makeTestTicketPlanPC()
			backend := planningagent.NewFakeBackend()
			backend.ProgramResult("ticket-plan-review", tt.json)

			_, err := Review(context.Background(), backend, pc, sampleTicketPlan, []string{"REQ-001", "REQ-002"}, "spec-rev")
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

func TestBuildTicketPlanReviewPromptFlagsNoCodeDeliverables(t *testing.T) {
	pc := makeTestTicketPlanPC()

	prompt := buildTicketPlanReviewPrompt(ticketPlanReviewRequest{
		Context:      pc,
		TicketPlan:   sampleTicketPlan,
		SpecReqIDs:   []string{"REQ-001", "REQ-002"},
		SpecRevision: "spec-rev",
	})

	for _, want := range []string{
		"Flag executable tickets whose only deliverable is verification-only",
		"tracker-only",
		"cannot produce a git diff",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q: %q", want, prompt)
		}
	}
}
