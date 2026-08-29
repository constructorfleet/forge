package clicommon

import (
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

func TestResolve_NoStructuredResultIsFailed(t *testing.T) {
	res := Resolve("codex", StructuredResult{}, false, 1, "raw stdout", "raw stderr")
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
	if res.Summary == "" {
		t.Fatalf("Summary is empty, want diagnostics")
	}
}

func TestResolve_ImplementedPassesThroughSummaryAndUsage(t *testing.T) {
	structured := StructuredResult{Status: "IMPLEMENTED", Summary: "done", Usage: &UsageFields{InputTokens: 1, OutputTokens: 2}}
	res := Resolve("codex", structured, true, 0, "", "")
	if res.Status != agent.StatusImplemented || res.Summary != "done" {
		t.Fatalf("res = %+v, want IMPLEMENTED/done", res)
	}
	if res.Usage == nil || res.Usage.InputTokens != 1 || res.Usage.OutputTokens != 2 {
		t.Fatalf("res.Usage = %+v, want input=1 output=2", res.Usage)
	}
}

func TestResolve_NeedsInfoRequiresQuestion(t *testing.T) {
	structured := StructuredResult{Status: "NEEDS_INFO", Summary: "stuck"}
	res := Resolve("codex", structured, true, 0, "stdout", "")
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED when needs_info.question is missing", res.Status)
	}
}

func TestResolve_NeedsInfoWithQuestionSucceeds(t *testing.T) {
	structured := StructuredResult{
		Status:    "NEEDS_INFO",
		Summary:   "stuck",
		NeedsInfo: &NeedsInfoFields{Question: "what now?", Context: "because"},
	}
	res := Resolve("codex", structured, true, 0, "", "")
	if res.Status != agent.StatusNeedsInfo {
		t.Fatalf("Status = %v, want NEEDS_INFO", res.Status)
	}
	if res.NeedsInfo == nil || res.NeedsInfo.Question != "what now?" || res.NeedsInfo.Context != "because" {
		t.Fatalf("NeedsInfo = %+v, want question/context passed through", res.NeedsInfo)
	}
}

func TestResolve_Failed(t *testing.T) {
	structured := StructuredResult{Status: "FAILED", Summary: "nope"}
	res := Resolve("codex", structured, true, 1, "", "")
	if res.Status != agent.StatusFailed || res.Summary != "nope" {
		t.Fatalf("res = %+v, want FAILED/nope", res)
	}
}
