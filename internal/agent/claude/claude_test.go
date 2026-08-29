package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
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
	return func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(string)) (string, string, int, error) {
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

// TestExecute_ArgsIncludeJSONSchema is the red test for issue 20/ticket 32:
// the CLI invocation must carry `--json-schema <schema>`, where <schema> is
// a JSON Schema for the {status, summary, needs_info, usage} envelope, so
// the CLI enforces the result's shape itself instead of Forge inferring it
// from free-form output after the fact.
func TestExecute_ArgsIncludeJSONSchema(t *testing.T) {
	var calls []recordedCall
	stdout := `{"status":"IMPLEMENTED","summary":"done"}`
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	if _, err := a.Execute(context.Background(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}

	schema := findFlagValue(t, calls[0].args, "--json-schema")

	var decoded struct {
		Type       string `json:"type"`
		Properties struct {
			Status struct {
				Enum []string `json:"enum"`
			} `json:"status"`
			Summary struct {
				Type string `json:"type"`
			} `json:"summary"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(schema), &decoded); err != nil {
		t.Fatalf("--json-schema value is not valid JSON: %v\nschema: %s", err, schema)
	}
	if decoded.Type != "object" {
		t.Fatalf("schema type = %q, want object", decoded.Type)
	}
	wantEnum := []string{"IMPLEMENTED", "NEEDS_INFO", "FAILED"}
	if !reflect.DeepEqual(decoded.Properties.Status.Enum, wantEnum) {
		t.Fatalf("schema status enum = %v, want %v", decoded.Properties.Status.Enum, wantEnum)
	}
	if decoded.Properties.Summary.Type != "string" {
		t.Fatalf("schema summary type = %q, want string", decoded.Properties.Summary.Type)
	}
	if !reflect.DeepEqual(decoded.Required, []string{"status", "summary"}) {
		t.Fatalf("schema required = %v, want [status summary]", decoded.Required)
	}
}

// findFlagValue fails t unless args contains flag immediately followed by a
// value, and returns that value.
func findFlagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, a := range args {
		if a == flag {
			if i+1 >= len(args) {
				t.Fatalf("args %v: %s has no value", args, flag)
			}
			return args[i+1]
		}
	}
	t.Fatalf("args %v: missing %s", args, flag)
	return ""
}

// TestExecute_SchemaConformingResultDecodesDirectly covers the case where
// the CLI's `--json-schema` enforcement means the final text is already a
// bare JSON object (no fenced ```json block, no surrounding prose) — the
// shape parseStructuredResult was never designed to require.
func TestExecute_SchemaConformingResultDecodesDirectly(t *testing.T) {
	var calls []recordedCall
	stdout := `{"status":"IMPLEMENTED","summary":"Added the feature.","usage":{"input_tokens":10,"output_tokens":5}}`
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
	if result.Usage == nil || result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 5 {
		t.Fatalf("Usage = %+v, want input=10 output=5", result.Usage)
	}
}

// TestExecute_SchemaConformingResultViaStreamJSON covers the composed path
// (streamingArgs + jsonSchemaArgs): the terminal stream-json "result" line
// carries the schema-conforming JSON directly in its "result" field, with
// no fenced block wrapping it.
func TestExecute_SchemaConformingResultViaStreamJSON(t *testing.T) {
	var calls []recordedCall
	stdout := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Working on it."}]}}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"{\"status\":\"NEEDS_INFO\",\"summary\":\"Blocked.\",\"needs_info\":{\"question\":\"Which backend?\",\"context\":\"Two candidates.\"}}"}` + "\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusNeedsInfo {
		t.Fatalf("Status = %q, want NEEDS_INFO", result.Status)
	}
	if result.NeedsInfo == nil || result.NeedsInfo.Question != "Which backend?" {
		t.Fatalf("NeedsInfo = %+v, want question preserved", result.NeedsInfo)
	}
}

// TestExecute_ErrorResultLineFailsDistinctly covers a CLI-level error
// result (e.g. a permission-request the unattended run couldn't satisfy, or
// any other error subtype) — it must be diagnosed distinctly from "no
// structured result found", since there was never a result to decode.
func TestExecute_ErrorResultLineFailsDistinctly(t *testing.T) {
	var calls []recordedCall
	stdout := `{"type":"result","subtype":"error_during_execution","is_error":true,"result":""}` + "\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
	if !strings.Contains(result.Summary, "CLI reported an error result") {
		t.Fatalf("Summary = %q, want a distinct CLI-error diagnosis", result.Summary)
	}
	if !strings.Contains(result.Summary, "error_during_execution") {
		t.Fatalf("Summary = %q, want the error subtype captured", result.Summary)
	}
	if strings.Contains(result.Summary, "no structured result found") {
		t.Fatalf("Summary = %q, want the CLI-error path distinct from the generic no-result diagnosis", result.Summary)
	}
}

func TestExecute_DefaultPermissionModeIsBypassPermissions(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	if _, err := a.Execute(context.Background(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	assertContainsPermissionMode(t, calls[0].args, "bypassPermissions")
}

func TestExecute_ExplicitPermissionModeIsPassedThrough(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil), PermissionMode: "plan"}

	if _, err := a.Execute(context.Background(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	assertContainsPermissionMode(t, calls[0].args, "plan")
}

// assertContainsPermissionMode fails t unless args contains
// "--permission-mode" immediately followed by want.
func assertContainsPermissionMode(t *testing.T, args []string, want string) {
	t.Helper()
	for i, a := range args {
		if a == "--permission-mode" {
			if i+1 >= len(args) {
				t.Fatalf("args %v: --permission-mode has no value", args)
			}
			if args[i+1] != want {
				t.Fatalf("--permission-mode = %q, want %q", args[i+1], want)
			}
			return
		}
	}
	t.Fatalf("args %v: missing --permission-mode", args)
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
	// A runner error unrelated to cancellation (e.g. the binary crashed, or
	// couldn't be found) must surface as a FAILED AgentResult, not a Go
	// error, so callers get uniform outcome handling for ordinary failures.
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, "", "", -1, errors.New("exec: binary not found"))}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
	if !strings.Contains(result.Summary, "exec: binary not found") {
		t.Fatalf("Summary = %q, want runner error captured", result.Summary)
	}
}

func TestExecute_CancellationSurfacesDistinctError(t *testing.T) {
	// Unlike an ordinary runner error, cancellation must be distinguishable
	// via a returned Go error wrapping ctx.Err(), so a retry loop (tickets
	// 21/24) can tell "the caller gave up on this attempt" apart from "the
	// agent genuinely failed" and avoid miscounting it against the retry
	// budget.
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, "", "", -1, errors.New("signal: killed"))}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := a.Execute(ctx, baseRequest())
	if err == nil {
		t.Fatalf("Execute returned nil error, want wrapped ctx.Err()")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
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

func TestExecute_DefaultAuthEnvVarsPassThrough(t *testing.T) {
	// The standard Claude auth vars must reach the subprocess by default so
	// the common headless (API-key) case works out of the box, without
	// requiring an operator to opt in explicitly.
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-key")
	t.Setenv("FORGE_SECRET", "super-secret-value")

	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	if _, err := a.Execute(context.Background(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	env := calls[0].env
	if !envContainsKey(env, "ANTHROPIC_API_KEY", "sk-test-key") {
		t.Fatalf("env missing default auth var ANTHROPIC_API_KEY: %v", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "FORGE_SECRET=") {
			t.Fatalf("env leaked FORGE_SECRET: %v", env)
		}
	}
}

func TestExecute_ExtraEnvPassthroughIsOptIn(t *testing.T) {
	// Bedrock/Vertex/AWS/GOOGLE credentials aren't part of the default
	// allowlist; an operator must explicitly opt in via
	// ExtraEnvPassthrough.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA-test")
	t.Setenv("FORGE_SECRET", "super-secret-value")

	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{
		Runner:              newFakeRunner(&calls, stdout, "", 0, nil),
		ExtraEnvPassthrough: []string{"AWS_ACCESS_KEY_ID"},
	}

	if _, err := a.Execute(context.Background(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	env := calls[0].env
	if !envContainsKey(env, "AWS_ACCESS_KEY_ID", "AKIA-test") {
		t.Fatalf("env missing opted-in AWS_ACCESS_KEY_ID: %v", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "FORGE_SECRET=") {
			t.Fatalf("env leaked FORGE_SECRET: %v", env)
		}
	}
}

func TestExecute_ExtraEnvPassthroughUnsetByDefault(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA-test")

	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	if _, err := a.Execute(context.Background(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, kv := range calls[0].env {
		if strings.HasPrefix(kv, "AWS_ACCESS_KEY_ID=") {
			t.Fatalf("env leaked AWS_ACCESS_KEY_ID without opt-in: %v", calls[0].env)
		}
	}
}

func envContainsKey(env []string, key, value string) bool {
	for _, kv := range env {
		if kv == key+"="+value {
			return true
		}
	}
	return false
}

func TestExecute_NeedsInfoWithEmptyQuestionIsFailed(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"NEEDS_INFO","summary":"Blocked.","needs_info":{}}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED for empty needs_info question", result.Status)
	}
}

func TestExecute_LastValidStructuredBlockWins(t *testing.T) {
	// Claude may emit an earlier fenced JSON block as part of its
	// explanation (e.g. an example) before the real, final result block;
	// only the last well-formed one is authoritative.
	var calls []recordedCall
	stdout := "Here's an example of the format:\n\n```json\n" +
		`{"status":"IMPLEMENTED","summary":"example, not the real result"}` +
		"\n```\n\nNow the actual result:\n\n```json\n" +
		`{"status":"FAILED","summary":"the real result"}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED (from the last block)", result.Status)
	}
	if result.Summary != "the real result" {
		t.Fatalf("Summary = %q, want the last block's summary", result.Summary)
	}
}

func TestExecute_ResultBlockSummaryContainingTripleBacktickIsNotTruncated(t *testing.T) {
	// The closing fence must be anchored to the start of a line so a
	// result whose summary contains a literal ``` sequence isn't parsed as
	// the block's end, discarding the rest of the JSON.
	stdout := "```json\n" +
		`{"status":"IMPLEMENTED","summary":"see the ` + "```go\\ncode\\n```" + ` snippet above"}` +
		"\n```\n"
	res, ok := parseStructuredResult(stdout)
	if !ok {
		t.Fatalf("parseStructuredResult failed to parse block containing an inline backtick fence")
	}
	if res.Status != string(agent.StatusImplemented) {
		t.Fatalf("Status = %q, want IMPLEMENTED", res.Status)
	}
}

func TestExecute_TrailingCommaBeforeClosingBraceIsRepaired(t *testing.T) {
	// Regression test for the exact envelope from Phase-2 dogfood issue #5
	// (ticket 12): a fully implemented, tests-passing outcome was discarded
	// as FAILED because the JSON ended with a trailing comma before `}`.
	var calls []recordedCall
	stdout := "```json\n" +
		`{"status": "IMPLEMENTED", "summary": "Structured-invocation core landed.",}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED (trailing comma should be repaired)", result.Status)
	}
	if result.Summary != "Structured-invocation core landed." {
		t.Fatalf("Summary = %q, unexpected", result.Summary)
	}
}

func TestExecute_TrailingCommaInNestedNeedsInfoIsRepaired(t *testing.T) {
	// Trailing commas can appear at any nesting depth, not just the
	// outermost object.
	var calls []recordedCall
	stdout := "```json\n" +
		`{"status": "NEEDS_INFO", "summary": "Blocked.", "needs_info": {"question": "Which backend?", "context": "Two candidates.",},}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusNeedsInfo {
		t.Fatalf("Status = %q, want NEEDS_INFO", result.Status)
	}
	if result.NeedsInfo == nil || result.NeedsInfo.Question != "Which backend?" {
		t.Fatalf("NeedsInfo = %+v, want question preserved", result.NeedsInfo)
	}
}

func TestExecute_TrailingCommaWithProseWrappingIsRepaired(t *testing.T) {
	// The malformed block may be surrounded by free-form prose; the repair
	// must still apply to the fenced block content, not the whole output.
	var calls []recordedCall
	stdout := "I finished the implementation. Here is the result:\n\n```json\n" +
		`{"status": "IMPLEMENTED", "summary": "Done.",}` +
		"\n```\n\nLet me know if you need anything else.\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED", result.Status)
	}
}

func TestExecute_TrailingCommaInLastOfMultipleBlocksIsRepaired(t *testing.T) {
	// An earlier, well-formed example block must not shadow a later,
	// malformed-but-repairable real result block.
	var calls []recordedCall
	stdout := "Here's an example of the format:\n\n```json\n" +
		`{"status":"IMPLEMENTED","summary":"example, not the real result"}` +
		"\n```\n\nNow the actual result:\n\n```json\n" +
		`{"status": "FAILED", "summary": "the real result",}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED (from the last, repaired block)", result.Status)
	}
	if result.Summary != "the real result" {
		t.Fatalf("Summary = %q, want the last block's summary", result.Summary)
	}
}

func TestExecute_TokenUsageSurvivesTrailingCommaRepair(t *testing.T) {
	// The repair must apply uniformly, including to usage decoding.
	var calls []recordedCall
	stdout := "```json\n" +
		`{"status": "IMPLEMENTED", "summary": "Done.", "usage": {"input_tokens": 100, "output_tokens": 50,},}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Usage == nil || result.Usage.InputTokens != 100 || result.Usage.OutputTokens != 50 {
		t.Fatalf("Usage = %+v, want tokens preserved through repair", result.Usage)
	}
}

func TestExecute_UnrecoverableMalformedBlockStillFails(t *testing.T) {
	// Genuinely truncated/ambiguous output must still fail loudly rather
	// than being silently accepted; the repair is bounded to known
	// cosmetic issues, not a general JSON-repair pass.
	var calls []recordedCall
	stdout := "```json\n" +
		`{"status": "IMPLEMENTED", "summary": "Truncated mid-str` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED for unrecoverable malformed output", result.Status)
	}
	if !strings.Contains(result.Summary, "no structured result found") {
		t.Fatalf("Summary = %q, want diagnostic about missing structured result", result.Summary)
	}
}

func TestTruncate_MarksTruncatedOutput(t *testing.T) {
	s := strings.Repeat("x", 100)
	got := truncate(s, 10)
	if !strings.HasPrefix(got, s[:10]) {
		t.Fatalf("truncate did not preserve the first n bytes: %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("truncate did not mark output as truncated: %q", got)
	}
}

func TestTruncate_LeavesShortStringUnchanged(t *testing.T) {
	s := "short"
	if got := truncate(s, 10); got != s {
		t.Fatalf("truncate(%q, 10) = %q, want unchanged", s, got)
	}
}

func TestBuildPrompt_IncludesIssueTitleAndBody(t *testing.T) {
	req := baseRequest()
	req.Issue.Title = "The agent prompt must carry the Issue's title and body"
	req.Issue.Body = "Acceptance: the prompt renders the Issue's full description, not just its ID."

	prompt := buildPrompt(req)

	if !strings.Contains(prompt, req.Issue.Title) {
		t.Fatalf("prompt missing Issue Title:\n%s", prompt)
	}
	if !strings.Contains(prompt, req.Issue.Body) {
		t.Fatalf("prompt missing Issue Body:\n%s", prompt)
	}
}

func TestBuildPrompt_OmitsRepositoryContextHeaderWhenEmpty(t *testing.T) {
	req := baseRequest()
	req.Repository = agent.RepositoryContext{}
	req.Policy = agent.WorkflowPolicy{}

	prompt := buildPrompt(req)
	if strings.Contains(prompt, "Repository Context") {
		t.Fatalf("prompt included empty Repository Context section:\n%s", prompt)
	}
}

func TestDefaultRunner_BoundsUnboundedOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if _, err := exec.LookPath("yes"); err != nil {
		t.Skip("yes not available")
	}

	a := &Adapter{Executable: "sh"}
	runner := a.defaultRunner()

	stdout, _, _, err := runner(
		context.Background(),
		t.TempDir(),
		[]string{"-c", "yes x | head -c 5000000"},
		"",
		nil,
		func(string) {},
	)
	if err != nil {
		t.Fatalf("runner returned error: %v", err)
	}
	if len(stdout) > maxCapturedOutputLen*2 {
		t.Fatalf("captured stdout len = %d, want bounded near maxCapturedOutputLen (%d)", len(stdout), maxCapturedOutputLen)
	}
}

var _ agent.Agent = (*Adapter)(nil)
