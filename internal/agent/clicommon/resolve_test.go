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

func TestResolve_ImplementedCarriesFollowUps(t *testing.T) {
	structured := StructuredResult{
		Status:  "IMPLEMENTED",
		Summary: "done",
		FollowUps: []FollowUpFields{
			{Title: "flaky test", Body: "TestFoo occasionally times out"},
		},
	}
	res := Resolve("codex", structured, true, 0, "", "")
	if len(res.FollowUps) != 1 {
		t.Fatalf("res.FollowUps = %+v, want 1 entry", res.FollowUps)
	}
	if res.FollowUps[0].Title != "flaky test" || res.FollowUps[0].Body != "TestFoo occasionally times out" {
		t.Errorf("res.FollowUps[0] = %+v, want title/body passed through", res.FollowUps[0])
	}
}

func TestResolve_NoFollowUpsLeavesFieldNil(t *testing.T) {
	structured := StructuredResult{Status: "IMPLEMENTED", Summary: "done"}
	res := Resolve("codex", structured, true, 0, "", "")
	if res.FollowUps != nil {
		t.Errorf("res.FollowUps = %+v, want nil when the backend reported none", res.FollowUps)
	}
}
