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
