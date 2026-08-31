package clicommon

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

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
