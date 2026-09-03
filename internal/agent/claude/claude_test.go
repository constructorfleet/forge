package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestExecute_SchemaConformingResultCarriesFollowUps(t *testing.T) {
	var calls []recordedCall
	stdout := `{"status":"IMPLEMENTED","summary":"Added the feature.","follow_ups":[{"title":"Flaky test","body":"TestFoo occasionally times out under load."}]}`
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(result.FollowUps) != 1 {
		t.Fatalf("FollowUps = %+v, want 1 entry", result.FollowUps)
	}
	if result.FollowUps[0].Title != "Flaky test" || result.FollowUps[0].Body != "TestFoo occasionally times out under load." {
		t.Fatalf("FollowUps[0] = %+v, want title/body preserved", result.FollowUps[0])
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

// TestExecute_ReviewModeOmitsJSONSchema is the red test for issue #183:
// review mode is a raw-analysis task whose deliverable is the agent's final
// message itself (a review findings envelope), so the CLI must NOT be
// invoked with `--json-schema` — that enforcement forces the answer into
// {status, summary} and clobbers the envelope.
func TestExecute_ReviewModeOmitsJSONSchema(t *testing.T) {
	var calls []recordedCall
	stdout := `{"axis":"bugs","findings":[],"assurances":[]}`
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Mode = agent.ModeReview
	if _, err := a.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	for i, arg := range calls[0].args {
		if arg == "--json-schema" {
			t.Fatalf("review-mode args must omit --json-schema, got %v", calls[0].args)
		}
		_ = i
	}
}

// TestExecute_ReviewModeReturnsRawFinalTextAsSummary covers issue #183's
// core: in review mode the agent's reconstructed final message is returned
// verbatim as AgentResult.Summary (with Status IMPLEMENTED), so the reviewer
// can parse the findings envelope out of it — no structured {status,
// summary} decoding is applied.
func TestExecute_ReviewModeReturnsRawFinalTextAsSummary(t *testing.T) {
	var calls []recordedCall
	envelope := `{"axis":"bugs","findings":[{"severity":"HIGH","confidence":0.9,"file":"main.go","line":42,"message":"unhandled error"}],"assurances":["error paths are logged"]}`
	stdout := `{"type":"result","subtype":"success","is_error":false,"result":` + mustJSONString(t, envelope) + "}\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Mode = agent.ModeReview
	result, err := a.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED", result.Status)
	}
	if strings.TrimSpace(result.Summary) != envelope {
		t.Fatalf("Summary = %q, want the raw findings envelope %q", result.Summary, envelope)
	}
}

// TestExecute_ReviewModeFindingAboutRateLimitingIsNotMisclassified is the
// regression test for issue #416's review fix: a legitimate bugs-axis
// finding whose message happens to discuss rate limiting/quota concepts
// must not be misclassified as a provider limit and discarded — detection
// is scoped to stdout/stderr, and stdout here carries only the ordinary
// findings envelope, not a provider error.
func TestExecute_ReviewModeFindingAboutRateLimitingIsNotMisclassified(t *testing.T) {
	var calls []recordedCall
	envelope := `{"axis":"bugs","findings":[{"severity":"HIGH","confidence":0.9,"file":"main.go","line":42,"message":"Missing rate limit handling could allow too many requests"}],"assurances":[]}`
	stdout := `{"type":"result","subtype":"success","is_error":false,"result":` + mustJSONString(t, envelope) + "}\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Mode = agent.ModeReview
	result, err := a.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED for a legitimate finding that merely discusses rate limiting", result.Status)
	}
	if strings.TrimSpace(result.Summary) != envelope {
		t.Fatalf("Summary = %q, want the raw findings envelope %q preserved", result.Summary, envelope)
	}
}

// TestExecute_ReviewModeEmptyOutputFails covers the one failure review mode
// can detect on its own: an empty final message means the axis produced no
// reviewable output, which must surface as FAILED (so the reviewer's
// per-axis retry can react) rather than an empty, silently-parsed summary.
func TestExecute_ReviewModeEmptyOutputFails(t *testing.T) {
	var calls []recordedCall
	stdout := `{"type":"result","subtype":"success","is_error":false,"result":""}` + "\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Mode = agent.ModeReview
	result, err := a.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
}

// mustJSONString encodes s as a JSON string literal (with surrounding
// quotes and escaping), for embedding inside a stream-json "result" field.
func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal string: %v", err)
	}
	return string(b)
}

// TestExecute_StructuredModeUsesPromptVerbatim covers issue 200: a
// ModeStructured invocation runs the CLI with req.Prompt used verbatim as
// stdin — no implement/review prompt scaffolding (Issue/Repository/Policy
// framing) built around it.
func TestExecute_StructuredModeUsesPromptVerbatim(t *testing.T) {
	var calls []recordedCall
	stdout := `{"answer":"42"}`
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Mode = agent.ModeStructured
	req.Prompt = "STRUCTURED-PROMPT-MARKER: answer with a number."
	req.Schema = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`

	if _, err := a.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].stdin != req.Prompt {
		t.Fatalf("stdin = %q, want req.Prompt %q verbatim", calls[0].stdin, req.Prompt)
	}
}

// TestExecute_StructuredModeUsesCallerSchema covers issue 200: the
// `--json-schema` argument must carry req.Schema, not the fixed
// implementation-result envelope schema.
func TestExecute_StructuredModeUsesCallerSchema(t *testing.T) {
	var calls []recordedCall
	stdout := `{"answer":"42"}`
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Mode = agent.ModeStructured
	req.Prompt = "STRUCTURED-PROMPT-MARKER"
	req.Schema = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`

	if _, err := a.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}

	schema := findFlagValue(t, calls[0].args, "--json-schema")
	if schema != req.Schema {
		t.Fatalf("--json-schema = %q, want caller schema %q", schema, req.Schema)
	}
}

// TestExecute_StructuredModeReturnsResultAsSummary covers issue 200: the
// schema-conforming result text is returned verbatim as AgentResult.Summary;
// the run must not attempt to decode Phase 1's {status,summary} envelope out
// of it.
func TestExecute_StructuredModeReturnsResultAsSummary(t *testing.T) {
	var calls []recordedCall
	result := `{"answer":"42"}`
	stdout := `{"type":"result","subtype":"success","is_error":false,"result":` + mustJSONString(t, result) + "}\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Mode = agent.ModeStructured
	req.Prompt = "STRUCTURED-PROMPT-MARKER"
	req.Schema = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`

	got, err := a.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED", got.Status)
	}
	if strings.TrimSpace(got.Summary) != result {
		t.Fatalf("Summary = %q, want the raw schema-conforming result %q", got.Summary, result)
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

func TestExecute_ProviderLimitStderrIsProviderLimit(t *testing.T) {
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, "", "Error: rate limit exceeded, please retry later", 1, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusProviderLimit {
		t.Fatalf("Status = %q, want PROVIDER_LIMIT", result.Status)
	}
	if !strings.Contains(result.Summary, "provider limit reached") {
		t.Fatalf("Summary = %q, want it to name the provider limit", result.Summary)
	}
}

func TestExecute_ImplementedSummaryAboutRateLimitingIsNotMisclassified(t *testing.T) {
	// A completed implementation can legitimately describe rate-limiting
	// work in its own summary without that being the provider's own error
	// (issue #416 review fix): detection must not scan finalText when a
	// valid {status, summary} result already parsed.
	var calls []recordedCall
	stdout := "```json\n" +
		`{"status": "IMPLEMENTED", "summary": "Implemented a 429 too many requests response for the rate limiter."}` +
		"\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED for a legitimate summary that merely discusses rate limiting", result.Status)
	}
	if result.Summary != "Implemented a 429 too many requests response for the rate limiter." {
		t.Fatalf("Summary = %q, unexpected", result.Summary)
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

func TestExecute_TimeoutKillsWedgedRunner(t *testing.T) {
	// A Runner that never returns on its own (the "wedged" case ticket 33
	// was written for) must still be bounded by Adapter.Timeout: Execute
	// should return promptly, reporting a distinct FAILED outcome rather
	// than hanging forever or surfacing the generic cancellation error.
	blocked := make(chan struct{})
	runner := Runner(func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(string)) (string, string, int, error) {
		<-ctx.Done()
		close(blocked)
		return "", "", -1, ctx.Err()
	})
	a := &Adapter{Runner: runner, Timeout: 20 * time.Millisecond}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v, want nil (timeout is a diagnosable FAILED outcome, not a generic error)", err)
	}
	if result.Status != agent.StatusFailed {
		t.Fatalf("Status = %q, want FAILED", result.Status)
	}
	if !strings.Contains(result.Summary, "timed out") {
		t.Fatalf("Summary = %q, want it to mention the timeout", result.Summary)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("runner's ctx was never canceled: the subprocess would be left running")
	}
}

func TestExecute_TimeoutResetsOnOutput(t *testing.T) {
	// A long-but-progressing run must not be killed: each line of output
	// resets the idle deadline, so a run whose total duration exceeds
	// Timeout, but never goes Timeout without producing a line, succeeds.
	runner := Runner(func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(string)) (string, string, int, error) {
		for i := 0; i < 5; i++ {
			select {
			case <-ctx.Done():
				return "", "", -1, ctx.Err()
			case <-time.After(15 * time.Millisecond):
			}
			onLine(fmt.Sprintf("progress %d", i))
		}
		stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
		return stdout, "", 0, nil
	})
	a := &Adapter{Runner: runner, Timeout: 40 * time.Millisecond}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED (heartbeat should have prevented the timeout)", result.Status)
	}
}

func TestExecute_FinishesJustUnderDeadlineSucceeds(t *testing.T) {
	runner := Runner(func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(string)) (string, string, int, error) {
		time.Sleep(10 * time.Millisecond)
		stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
		return stdout, "", 0, nil
	})
	a := &Adapter{Runner: runner, Timeout: 200 * time.Millisecond}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED", result.Status)
	}
}

func TestExecute_ZeroTimeoutDisablesIt(t *testing.T) {
	var calls []recordedCall
	stdout := "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	result, err := a.Execute(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED", result.Status)
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

func TestBuildPrompt_InstructsTestDrivenDevelopment(t *testing.T) {
	req := baseRequest()

	prompt := buildPrompt(req)

	for _, want := range []string{"Test-Driven Development", "red", "green", "seam"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing TDD guidance %q:\n%s", want, prompt)
		}
	}
}

// TestBuildPrompt_ReviewModeIsReadOnlyAnalysis covers issue #183: a review
// invocation must NOT carry implement-mode framing — no "Implement the
// Issue's requirements", no TDD guidance, no {status,…} result contract —
// since those steer the agent toward writing a prose summary instead of the
// findings envelope its rubric (carried in Policy.Notes) asks for. It must
// instead carry read-only review rules and the Policy.Notes rubric verbatim.
func TestBuildPrompt_ReviewModeIsReadOnlyAnalysis(t *testing.T) {
	req := baseRequest()
	req.Mode = agent.ModeReview
	req.Policy.Notes = "REVIEW-RUBRIC-MARKER: emit only the findings JSON object."

	prompt := buildPrompt(req)

	if !strings.Contains(prompt, "REVIEW-RUBRIC-MARKER") {
		t.Fatalf("review prompt missing Policy.Notes rubric:\n%s", prompt)
	}
	for _, forbidden := range []string{
		"Implement the Issue's requirements",
		"Test-Driven Development",
		`"status": "IMPLEMENTED"`,
		"Do NOT create pull requests.",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("review prompt must not contain implement-mode framing %q:\n%s", forbidden, prompt)
		}
	}
	if !strings.Contains(prompt, "REVIEWING") && !strings.Contains(prompt, "reviewing") {
		t.Fatalf("review prompt should frame the task as a review:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Do NOT modify") {
		t.Fatalf("review prompt should be read-only (no file modification):\n%s", prompt)
	}
}

// TestBuildPrompt_ImplementModeUnchanged guards that the default (implement)
// mode still carries its full framing after issue #183's review-mode split.
func TestBuildPrompt_ImplementModeUnchanged(t *testing.T) {
	req := baseRequest()

	prompt := buildPrompt(req)

	for _, want := range []string{
		"Implement the Issue's requirements",
		"Test-Driven Development",
		"Report your outcome as",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implement prompt missing %q:\n%s", want, prompt)
		}
	}
}

// TestBuildPrompt_StructuredModeReturnsPromptVerbatim covers issue 200:
// ModeStructured returns req.Prompt verbatim, with none of the
// Issue/Repository/Policy scaffolding buildPrompt otherwise assembles.
func TestBuildPrompt_StructuredModeReturnsPromptVerbatim(t *testing.T) {
	req := baseRequest()
	req.Mode = agent.ModeStructured
	req.Prompt = "STRUCTURED-PROMPT-MARKER: answer with a number."

	prompt := buildPrompt(req)

	if prompt != req.Prompt {
		t.Fatalf("prompt = %q, want req.Prompt verbatim %q", prompt, req.Prompt)
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

func TestAdapter_SemanticProfileDeclaresAllButTypeHierarchyViaLSPPlugin(t *testing.T) {
	a := &Adapter{}

	var profiler agent.SemanticProfiler = a
	got := profiler.SemanticProfile()

	want := agent.SemanticProfile{
		Capabilities: agent.SemanticCapabilities{
			Definition:      true,
			References:      true,
			Implementations: true,
			Hover:           true,
			DocumentSymbol:  true,
			WorkspaceSymbol: true,
			CallHierarchy:   true,
			TypeHierarchy:   false,
		},
		Channel: agent.InjectionChannelLSPPlugin,
	}
	if got != want {
		t.Fatalf("SemanticProfile() = %+v, want %+v", got, want)
	}
}

// A failure before the stream yields anything (subprocess never produced
// stdout, only an error/stderr) must still persist a diagnostic transcript
// event — no run is ever a blank transcript (issue #257).
func TestExecute_SubprocessErrorNeverBlankTranscript(t *testing.T) {
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, "", "boom: could not start claude", -1, errors.New("exec: not found"))}
	sink := agent.NewTranscriptRecorder()
	req := baseRequest()
	req.Transcript = sink

	res, err := a.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (subprocess errors surface via Status)", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
	if len(sink.Events()) == 0 {
		t.Fatalf("Events() = 0, want a non-blank fallback transcript on early failure")
	}
}
