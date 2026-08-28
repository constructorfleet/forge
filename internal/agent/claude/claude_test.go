package claude

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

// recordedCall captures the arguments one fakeRunner invocation received, so
// tests can assert on prompt construction and environment sanitization.
type recordedCall struct {
	dir   string
	args  []string
	stdin string
	env   []string
}

// newFakeRunner returns a Runner that records every call it receives and
// returns the canned stdout/stderr/exitCode/err on each invocation.
func newFakeRunner(calls *[]recordedCall, stdout, stderr string, exitCode int, err error) Runner {
	return func(ctx context.Context, dir string, args []string, stdin string, env []string) (string, string, int, error) {
		*calls = append(*calls, recordedCall{dir: dir, args: append([]string(nil), args...), stdin: stdin, env: append([]string(nil), env...)})
		return stdout, stderr, exitCode, err
	}
}

func baseRequest() agent.AgentRequest {
	return agent.AgentRequest{
		WorkspacePath: "/workspace/issue-1",
		Issue: domain.Issue{
			ID:    "ISSUE-1",
			State: domain.StatePreparing,
			Scope: domain.ScopeManaged,
			Dependencies: []domain.Dependency{
				{IssueID: "ISSUE-1", DependsOnID: "ISSUE-0"},
			},
		},
		Repository: agent.RepositoryContext{
			BaseRevision:      "abc123",
			ProjectStructure:  "cmd/\ninternal/",
			AgentInstructions: "Follow CLAUDE.md.",
			QualityGates:      []string{"go build ./...", "go test ./..."},
		},
		Policy: agent.WorkflowPolicy{
			Notes: "Squash commits before publication.",
		},
	}
}

func TestExecute_ImplementedFromStructuredOutput(t *testing.T) {
	var calls []recordedCall
	stdout := "I implemented the change.\n\n```json\n" +
		`{"status":"IMPLEMENTED","summary":"Added the feature."}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED", result.Status)
	}
	if result.Summary != "Added the feature." {
		t.Fatalf("Summary = %q", result.Summary)
	}
	if result.NeedsInfo != nil {
		t.Fatalf("NeedsInfo = %+v, want nil", result.NeedsInfo)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].dir != "/workspace/issue-1" {
		t.Fatalf("dir = %q", calls[0].dir)
	}
}

func TestExecute_NeedsInfoWithReasonAndQuestions(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" +
		`{"status":"NEEDS_INFO","summary":"Blocked.","needs_info":{"question":"Which auth provider?","context":"Two providers are configured."}}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusNeedsInfo {
		t.Fatalf("Status = %q, want NEEDS_INFO", result.Status)
	}
	if result.NeedsInfo == nil {
		t.Fatalf("NeedsInfo = nil, want populated")
	}
	if result.NeedsInfo.Question != "Which auth provider?" {
		t.Fatalf("Question = %q", result.NeedsInfo.Question)
	}
	if result.NeedsInfo.Context != "Two providers are configured." {
		t.Fatalf("Context = %q", result.NeedsInfo.Context)
	}
}

func TestExecute_ExplicitFailedStatus(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"FAILED","summary":"Could not build."}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
	if result.Summary != "Could not build." {
		t.Fatalf("Summary = %q", result.Summary)
	}
}

func TestExecute_NonZeroExitWithoutStructuredResultIsFailed(t *testing.T) {
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, "some partial output", "panic: oops", 1, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
	if !strings.Contains(result.Summary, "panic: oops") {
		t.Fatalf("Summary = %q, want stderr captured", result.Summary)
	}
	if !strings.Contains(result.Summary, "some partial output") {
		t.Fatalf("Summary = %q, want stdout captured", result.Summary)
	}
}

func TestExecute_RunnerErrorSurfacesAsFailed(t *testing.T) {
	// A runner error (e.g. context cancellation propagated up from
	// exec.CommandContext killing the subprocess) must surface as a FAILED
	// AgentResult, not a transport-level Go error, so callers get uniform
	// outcome handling.
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, "", "", -1, errors.New("signal: killed"))}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := a.Execute(ctx, baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
	if !strings.Contains(result.Summary, "signal: killed") {
		t.Fatalf("Summary = %q, want runner error captured", result.Summary)
	}
}

func TestExecute_StdoutAndStderrCapturedForDiagnostics(t *testing.T) {
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, "unstructured chatter", "warning: deprecated flag", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
	if !strings.Contains(result.Summary, "unstructured chatter") {
		t.Fatalf("Summary missing captured stdout: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "warning: deprecated flag") {
		t.Fatalf("Summary missing captured stderr: %q", result.Summary)
	}
}

func TestExecute_PromptIncludesIssueRulesAndContext(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	if _, err := a.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	prompt := calls[0].stdin

	for _, want := range []string{
		"ISSUE-1",                            // issue identity
		"ISSUE-0",                            // dependency context
		"go build ./...",                     // quality gates
		"go test ./...",                      // quality gates
		"cmd/\ninternal/",                    // project structure
		"Follow CLAUDE.md.",                  // agent instructions
		"abc123",                             // base revision
		"Squash commits before publication.", // workflow policy notes
		"do not create pull requests",
		"do not manage labels",
		"do not decide workflow state",
	} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(want)) {
			t.Fatalf("prompt missing %q\nprompt:\n%s", want, prompt)
		}
	}
}

func TestExecute_PromptIncludesFeedbackOnRetries(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Feedback = []agent.Feedback{
		{Source: agent.FeedbackSourceGate, Message: "go vet failed: unused variable x"},
		{Source: agent.FeedbackSourceReview, Message: "missing test for edge case"},
		{Source: agent.FeedbackSourceCI, Message: "integration suite timed out"},
	}

	if _, err := a.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	prompt := calls[0].stdin
	for _, want := range []string{
		"go vet failed: unused variable x",
		"missing test for edge case",
		"integration suite timed out",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing feedback %q\nprompt:\n%s", want, prompt)
		}
	}
}

func TestExecute_EnvironmentIsSanitized(t *testing.T) {
	t.Setenv("FORGE_SECRET", "super-secret-value")
	t.Setenv("PATH", os.Getenv("PATH"))

	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	if _, err := a.Execute(context.Background(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	env := calls[0].env
	for _, kv := range env {
		if strings.HasPrefix(kv, "FORGE_SECRET=") {
			t.Fatalf("env leaked FORGE_SECRET: %v", env)
		}
	}
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			found = true
		}
	}
	if !found {
		t.Fatalf("env missing allowlisted PATH: %v", env)
	}
}

func TestExecute_UsesWorkspaceAsWorkingDirectory(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.WorkspacePath = "/some/other/workspace"
	if _, err := a.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if calls[0].dir != "/some/other/workspace" {
		t.Fatalf("dir = %q, want workspace path", calls[0].dir)
	}
}

func TestExecute_DefaultRunnerUsedWhenUnset(t *testing.T) {
	a := &Adapter{Executable: "definitely-not-a-real-claude-binary-xyz"}
	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED for missing binary", result.Status)
	}
}

var _ agent.Agent = (*Adapter)(nil)
