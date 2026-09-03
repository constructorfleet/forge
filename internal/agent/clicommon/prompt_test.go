package clicommon

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

func TestModeResult_ReviewProviderLimitOutputIsProviderLimit(t *testing.T) {
	finalText := "Error: rate limit exceeded, please retry later"
	res, handled := ModeResult("codex", agent.ModeReview, finalText, finalText, "", 1)
	if !handled {
		t.Fatalf("ModeResult: handled = false, want true for ModeReview")
	}
	if res.Status != agent.StatusProviderLimit {
		t.Fatalf("Status = %v, want PROVIDER_LIMIT", res.Status)
	}
	if !strings.Contains(res.Summary, ProviderLimitReason) {
		t.Errorf("Summary = %q, want it to name %q", res.Summary, ProviderLimitReason)
	}
}

func TestModeResult_ReviewFindingAboutRateLimitingIsNotMisclassified(t *testing.T) {
	// A legitimate bugs-axis finding can discuss rate limiting as its
	// subject without that being the provider's own error (issue #416
	// review fix): detection must not scan finalText, only stdout/stderr.
	finalText := "```json\n{\"findings\":[{\"severity\":\"warning\",\"message\":\"Missing rate limit handling could allow too many requests\"}]}\n```"
	res, handled := ModeResult("codex", agent.ModeReview, finalText, "", "", 0)
	if !handled {
		t.Fatalf("ModeResult: handled = false, want true for ModeReview")
	}
	if res.Status != agent.StatusImplemented {
		t.Fatalf("Status = %v, want IMPLEMENTED for a legitimate finding that merely discusses rate limiting", res.Status)
	}
	if res.Summary != finalText {
		t.Errorf("Summary = %q, want the findings envelope preserved verbatim", res.Summary)
	}
}

func TestModeResult_ReviewOrdinaryOutputIsImplemented(t *testing.T) {
	res, handled := ModeResult("codex", agent.ModeReview, "```json\n{\"findings\":[]}\n```", "", "", 0)
	if !handled {
		t.Fatalf("ModeResult: handled = false, want true for ModeReview")
	}
	if res.Status != agent.StatusImplemented {
		t.Fatalf("Status = %v, want IMPLEMENTED for ordinary review output", res.Status)
	}
}

func TestBuildPrompt_IncludesIssueAndResultContract(t *testing.T) {
	req := agent.AgentRequest{
		Issue: domain.Issue{ID: "42", Title: "Do the thing", State: domain.StateReady, Body: "Body text"},
	}
	prompt := BuildPrompt("Codex", req)

	for _, want := range []string{"Forge Task: Issue 42", "Do the thing", "Body text", "Required output format", "IMPLEMENTED", "NEEDS_INFO", "FAILED"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("BuildPrompt() missing %q\ngot:\n%s", want, prompt)
		}
	}
}

func TestBuildPrompt_OmitsEmptyRepositoryContext(t *testing.T) {
	req := agent.AgentRequest{Issue: domain.Issue{ID: "1", State: domain.StateReady}}
	prompt := BuildPrompt("Codex", req)
	if strings.Contains(prompt, "Repository Context") {
		t.Errorf("BuildPrompt() should omit empty Repository Context section, got:\n%s", prompt)
	}
}

func TestBuildPrompt_IncludesFeedbackWhenPresent(t *testing.T) {
	req := agent.AgentRequest{
		Issue:    domain.Issue{ID: "1", State: domain.StateReady},
		Feedback: []agent.Feedback{{Source: agent.FeedbackSourceGate, Message: "lint failed"}},
	}
	prompt := BuildPrompt("Codex", req)
	if !strings.Contains(prompt, "lint failed") {
		t.Errorf("BuildPrompt() missing feedback, got:\n%s", prompt)
	}
}

func TestBuildPrompt_InstructsTestDrivenDevelopment(t *testing.T) {
	req := agent.AgentRequest{Issue: domain.Issue{ID: "1", State: domain.StateReady}}
	prompt := BuildPrompt("Codex", req)
	for _, want := range []string{"Test-Driven Development", "red", "green", "seam"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("BuildPrompt() missing TDD guidance %q, got:\n%s", want, prompt)
		}
	}
}

func TestBuildPrompt_ModeStructuredReturnsPromptVerbatim(t *testing.T) {
	got := BuildPrompt("codex", agent.AgentRequest{Mode: agent.ModeStructured, Prompt: "just-this"})
	if got != "just-this" {
		t.Fatalf("BuildPrompt = %q, want the verbatim req.Prompt", got)
	}
}

func TestBuildPrompt_ModeReviewOmitsImplementContract(t *testing.T) {
	req := agent.AgentRequest{
		Mode:   agent.ModeReview,
		Policy: agent.WorkflowPolicy{Notes: "RUBRIC: emit a findings envelope"},
	}
	got := BuildPrompt("codex", req)
	if strings.Contains(got, "Required output format") || strings.Contains(got, "Test-Driven Development") {
		t.Fatalf("review prompt must omit the implement-mode result contract / TDD guidance:\n%s", got)
	}
	if !strings.Contains(got, "REVIEWING") || !strings.Contains(got, "RUBRIC: emit a findings envelope") {
		t.Fatalf("review prompt must carry review framing + the rubric from Policy.Notes:\n%s", got)
	}
}
