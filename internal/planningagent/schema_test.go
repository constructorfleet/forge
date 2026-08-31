package planningagent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningreadiness"
	"github.com/Teagan42/forge/internal/planningsurvey"
	"github.com/Teagan42/forge/internal/specgeneration"
	"github.com/Teagan42/forge/internal/specreview"
	"github.com/Teagan42/forge/internal/ticketplan"
	"github.com/Teagan42/forge/internal/ticketplanreview"
)

// schemaGuardRoundTrip drives InvokeStructured for Res with want programmed
// as the backend's bare JSON output (no fenced block), asserting that:
//   - InvokeStructured (and therefore its schema derivation for Res) returns
//     no error,
//   - the InvokeRequest sent to the backend carries a non-empty, valid JSON
//     Schema document, and
//   - want round-trips through the strict-decode primary path unchanged.
func schemaGuardRoundTrip[Res any](t *testing.T, name string, want Res) {
	t.Helper()

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("%s: marshal representative value: %v", name, err)
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult(name, string(data))

	got, err := planningagent.InvokeStructured[struct{}, Res](
		context.Background(), backend, name, struct{}{},
		func(struct{}) string { return "prompt" }, nil,
	)
	if err != nil {
		t.Fatalf("%s: InvokeStructured: %v", name, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: round-trip mismatch:\n got  %+v\n want %+v", name, got, want)
	}

	invocations := backend.Invocations()
	last := invocations[len(invocations)-1]
	if len(last.Schema) == 0 {
		t.Fatalf("%s: InvokeRequest.Schema was not set", name)
	}
	var schemaDoc map[string]any
	if err := json.Unmarshal(last.Schema, &schemaDoc); err != nil {
		t.Fatalf("%s: InvokeRequest.Schema is not valid JSON: %v", name, err)
	}
}

func TestInvokeStructured_DerivesSchemaForLiveContractResultTypes(t *testing.T) {
	t.Run("decisionresolution.Result", func(t *testing.T) {
		schemaGuardRoundTrip(t, "decisionresolution.Result", decisionresolution.Result{
			Outcome:      "outcome",
			Rationale:    "rationale",
			Consequences: "consequences",
			Assumptions:  "assumptions",
			NewUnknowns: []planningsurvey.ProposedDecision{{
				TempKey:       "D-1",
				Title:         "title",
				Question:      "question",
				DependsOn:     []string{"D-0"},
				Consequential: true,
			}},
			NeedsHuman: &decisionresolution.NeedsHumanDetail{
				Question: "q",
				Context:  "c",
			},
		})
	})

	t.Run("planningreadiness.Result", func(t *testing.T) {
		schemaGuardRoundTrip(t, "planningreadiness.Result", planningreadiness.Result{
			Status: planningreadiness.StatusNotReady,
			Decisions: []planningsurvey.ProposedDecision{{
				TempKey:       "D-1",
				Title:         "title",
				Question:      "question",
				DependsOn:     nil,
				Consequential: false,
			}},
		})
	})

	t.Run("specgeneration.SpecGenerationResult", func(t *testing.T) {
		schemaGuardRoundTrip(t, "specgeneration.SpecGenerationResult", specgeneration.SpecGenerationResult{
			Summary: "summary",
			Requirements: []specgeneration.Requirement{{
				ID:          "REQ-1",
				Description: "description",
			}},
			NonGoals:     []string{"non-goal"},
			DecisionRefs: []string{"D-1"},
		})
	})

	t.Run("specreview.Result", func(t *testing.T) {
		schemaGuardRoundTrip(t, "specreview.Result", specreview.Result{
			Verdict: specreview.VerdictChangesRequired,
			Summary: "summary",
			Findings: []specreview.Finding{{
				Severity: specreview.SeverityWarning,
				File:     "SPEC.md",
				Line:     12,
				Message:  "message",
			}},
		})
	})

	t.Run("ticketplan.TicketPlanGenerationResult", func(t *testing.T) {
		schemaGuardRoundTrip(t, "ticketplan.TicketPlanGenerationResult", ticketplan.TicketPlanGenerationResult{
			Tickets: []ticketplan.TicketGenResult{{
				Key:                   "TKT-1",
				Objective:             "objective",
				Requirements:          []string{"REQ-1"},
				AcceptanceCriteria:    []string{"criterion"},
				Dependencies:          []string{"TKT-0"},
				ImplementationContext: []string{"context"},
				Estimate: &planning.TicketEstimate{
					Size: "M",
					Risk: "low",
				},
			}},
		})
	})

	t.Run("ticketplanreview.Result", func(t *testing.T) {
		schemaGuardRoundTrip(t, "ticketplanreview.Result", ticketplanreview.Result{
			Verdict: ticketplanreview.VerdictApproved,
			Summary: "summary",
			Findings: []ticketplanreview.Finding{{
				Severity:    ticketplanreview.SeverityError,
				TicketKey:   "TKT-1",
				Requirement: "REQ-1",
				Message:     "message",
			}},
		})
	})
}
