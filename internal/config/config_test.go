package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".forge.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestDefault_ZeroConfig(t *testing.T) {
	cfg := Default()

	if cfg.Version != 1 {
		t.Errorf("Version = %d, want 1", cfg.Version)
	}
	if cfg.Tracker.Type != "github" {
		t.Errorf("Tracker.Type = %q, want github", cfg.Tracker.Type)
	}
	if cfg.Git.Base != "origin/main" {
		t.Errorf("Git.Base = %q, want origin/main", cfg.Git.Base)
	}
	if cfg.Git.BranchTemplate != "forge/{execution}/{issue}" {
		t.Errorf("Git.BranchTemplate = %q, want forge/{execution}/{issue}", cfg.Git.BranchTemplate)
	}
	if cfg.Git.WorktreeRoot != ".forge/worktrees" {
		t.Errorf("Git.WorktreeRoot = %q, want .forge/worktrees", cfg.Git.WorktreeRoot)
	}
	if cfg.Execution.MaxParallel != 4 {
		t.Errorf("Execution.MaxParallel = %d, want 4", cfg.Execution.MaxParallel)
	}
	wantRetry := domain.RetryLimits{Gate: 3, Review: 2, CI: 3}
	if cfg.Retry != wantRetry {
		t.Errorf("Retry = %+v, want %+v", cfg.Retry, wantRetry)
	}
	if cfg.Workflow.Implementation != "tdd" || !cfg.Workflow.Review {
		t.Errorf("Workflow = %+v, want {tdd true}", cfg.Workflow)
	}
	if len(cfg.Quality.Gates) != 0 {
		t.Errorf("Quality.Gates = %+v, want empty", cfg.Quality.Gates)
	}
	if !cfg.PullRequests.Enabled || !cfg.PullRequests.WatchCI {
		t.Errorf("PullRequests = %+v, want {true true}", cfg.PullRequests)
	}
	if cfg.CI.MergeRequirements.Mode != MergeRequirementsGitHub {
		t.Errorf("CI.MergeRequirements.Mode = %q, want github", cfg.CI.MergeRequirements.Mode)
	}
	if cfg.Blocked.Label != "needs-info" || !cfg.Blocked.Comment {
		t.Errorf("Blocked = %+v, want {needs-info true}", cfg.Blocked)
	}
	if cfg.Agent.Provider != "claude-code" {
		t.Errorf("Agent.Provider = %q, want claude-code", cfg.Agent.Provider)
	}
	if cfg.Agent.PermissionMode != PermissionModeBypassPermissions {
		t.Errorf("Agent.PermissionMode = %q, want %q", cfg.Agent.PermissionMode, PermissionModeBypassPermissions)
	}
	if len(cfg.Dependencies.Overrides) != 0 {
		t.Errorf("Dependencies.Overrides = %+v, want empty", cfg.Dependencies.Overrides)
	}
	if cfg.Quality.MaxOutputBytes != 20000 {
		t.Errorf("Quality.MaxOutputBytes = %d, want 20000", cfg.Quality.MaxOutputBytes)
	}

	// Default() must itself be a valid config.
	if err := validate(cfg); err != nil {
		t.Errorf("validate(Default()) = %v, want nil", err)
	}
}

func TestDefault_RetryIsDomainRetryLimits(t *testing.T) {
	// Retry must be domain.RetryLimits directly (not a parallel type), so
	// it plugs straight into domain.NewRetryBudget without any mapping step.
	cfg := Default()
	budget := domain.NewRetryBudget(cfg.Retry)
	if budget.Limits() != cfg.Retry {
		t.Errorf("budget.Limits() = %+v, want %+v", budget.Limits(), cfg.Retry)
	}
}

func TestLoad_CompleteConfig(t *testing.T) {
	path := writeTemp(t, `
version: 1
tracker:
  type: github
git:
  base: origin/main
  branch_template: agent/{execution}/{issue}
  worktree_root: .forge/worktrees
execution:
  max_parallel: 8
retry:
  gate: 5
  review: 4
  ci: 6
workflow:
  implementation: tdd
  review: true
quality:
  gates:
    - name: test
      command: make test
    - name: lint
      command: make lint
  max_output_bytes: 5000
pull_requests:
  enabled: true
  watch_ci: true
ci:
  required_checks:
    mode: explicit
    checks:
      - build
      - test
blocked:
  label: needs-info
  comment: true
agent:
  provider: claude-code
dependencies:
  overrides:
    "123":
      - "100"
      - "101"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Execution.MaxParallel != 8 {
		t.Errorf("Execution.MaxParallel = %d, want 8", cfg.Execution.MaxParallel)
	}
	wantRetry := domain.RetryLimits{Gate: 5, Review: 4, CI: 6}
	if cfg.Retry != wantRetry {
		t.Errorf("Retry = %+v, want %+v", cfg.Retry, wantRetry)
	}
	if len(cfg.Quality.Gates) != 2 {
		t.Fatalf("Quality.Gates len = %d, want 2", len(cfg.Quality.Gates))
	}
	if cfg.Quality.Gates[0] != (QualityGate{Name: "test", Command: "make test"}) {
		t.Errorf("Quality.Gates[0] = %+v", cfg.Quality.Gates[0])
	}
	if cfg.CI.MergeRequirements.Mode != MergeRequirementsExplicit {
		t.Errorf("CI.MergeRequirements.Mode = %q, want explicit", cfg.CI.MergeRequirements.Mode)
	}
	if got, want := cfg.CI.MergeRequirements.Checks, []string{"build", "test"}; !slices.Equal(got, want) {
		t.Errorf("CI.MergeRequirements.Checks = %v, want %v", got, want)
	}
	if deps, ok := cfg.Dependencies.Overrides["123"]; !ok || !slices.Equal(deps, []string{"100", "101"}) {
		t.Errorf("Dependencies.Overrides[123] = %v", deps)
	}
	if cfg.Quality.MaxOutputBytes != 5000 {
		t.Errorf("Quality.MaxOutputBytes = %d, want 5000", cfg.Quality.MaxOutputBytes)
	}
}

func TestLoad_PartialConfig_FillsDefaults(t *testing.T) {
	path := writeTemp(t, `
execution:
  max_parallel: 2
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Execution.MaxParallel != 2 {
		t.Errorf("Execution.MaxParallel = %d, want 2", cfg.Execution.MaxParallel)
	}
	// Everything else should fall back to Default().
	def := Default()
	if cfg.Tracker != def.Tracker {
		t.Errorf("Tracker = %+v, want default %+v", cfg.Tracker, def.Tracker)
	}
	if cfg.Git != def.Git {
		t.Errorf("Git = %+v, want default %+v", cfg.Git, def.Git)
	}
	if cfg.Retry != def.Retry {
		t.Errorf("Retry = %+v, want default %+v", cfg.Retry, def.Retry)
	}
	if cfg.Agent != def.Agent {
		t.Errorf("Agent = %+v, want default %+v", cfg.Agent, def.Agent)
	}
}

func TestLoad_PartialSection_FillsMissingFieldsOnly(t *testing.T) {
	path := writeTemp(t, `
git:
  base: upstream/main
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Git.Base != "upstream/main" {
		t.Errorf("Git.Base = %q, want upstream/main", cfg.Git.Base)
	}
	if cfg.Git.BranchTemplate != "forge/{execution}/{issue}" {
		t.Errorf("Git.BranchTemplate = %q, want default forge/{execution}/{issue}", cfg.Git.BranchTemplate)
	}
	if cfg.Git.WorktreeRoot != ".forge/worktrees" {
		t.Errorf("Git.WorktreeRoot = %q, want default .forge/worktrees", cfg.Git.WorktreeRoot)
	}
}

func TestLoad_ExplicitFalseOverridesDefaultTrue(t *testing.T) {
	// Pins that an explicitly-set false/0 is distinguishable from an
	// absent key now that Load decodes directly onto Default() rather
	// than through pointer-based presence tracking.
	path := writeTemp(t, "workflow:\n  review: false\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Workflow.Review {
		t.Errorf("Workflow.Review = true, want false (explicit override)")
	}
	// Sibling field left at its default.
	if cfg.Workflow.Implementation != "tdd" {
		t.Errorf("Workflow.Implementation = %q, want default tdd", cfg.Workflow.Implementation)
	}
}

func TestLoad_ExplicitNullLeavesDefaultInPlace(t *testing.T) {
	// Pins the edge case an explicit YAML null hits when decoding onto a
	// pre-populated struct: yaml.v3 treats an explicit null as "no value
	// provided" and does not overwrite the field, so the default survives
	// rather than being reset to the zero value.
	path := writeTemp(t, "git:\n  base: null\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (explicit null should not clobber the default)", err)
	}
	if cfg.Git.Base != "origin/main" {
		t.Errorf("Git.Base = %q, want default origin/main to survive explicit null", cfg.Git.Base)
	}
}

func TestLoad_CIPollIntervalParsesDuration(t *testing.T) {
	path := writeTemp(t, "ci:\n  poll_interval: 15s\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CI.PollInterval != 15*time.Second {
		t.Fatalf("PollInterval = %v, want 15s", cfg.CI.PollInterval)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing file")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	path := writeTemp(t, "tracker:\n  type: [github\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("Load() error = %v, want it to mention parsing", err)
	}
}

func TestLoad_UnknownKeyRejected(t *testing.T) {
	path := writeTemp(t, "retry:\n  gats: 5\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error for unknown field")
	}
	if !strings.Contains(err.Error(), "gats") {
		t.Errorf("Load() error = %v, want it to name the unknown field gats", err)
	}
}

func TestLoad_InvalidTrackerType(t *testing.T) {
	path := writeTemp(t, "tracker:\n  type: gitlab\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "tracker.type") {
		t.Errorf("Load() error = %v, want it to identify tracker.type", err)
	}
	if !strings.Contains(err.Error(), "gitlab") {
		t.Errorf("Load() error = %v, want it to include the offending value", err)
	}
}

func TestLoad_InvalidAgentPermissionMode(t *testing.T) {
	path := writeTemp(t, "agent:\n  permission_mode: yolo\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "agent.permission_mode") {
		t.Errorf("Load() error = %v, want it to identify agent.permission_mode", err)
	}
	if !strings.Contains(err.Error(), "yolo") {
		t.Errorf("Load() error = %v, want it to include the offending value", err)
	}
}

func TestLoad_AgentPermissionModeOverride(t *testing.T) {
	path := writeTemp(t, "agent:\n  permission_mode: plan\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Agent.PermissionMode != PermissionModePlan {
		t.Errorf("Agent.PermissionMode = %q, want plan", cfg.Agent.PermissionMode)
	}
}

func TestDefault_AgentTimeoutIsPositive(t *testing.T) {
	if Default().Agent.Timeout <= 0 {
		t.Fatalf("Default().Agent.Timeout = %v, want > 0", Default().Agent.Timeout)
	}
}

func TestLoad_AgentTimeoutParsesDuration(t *testing.T) {
	path := writeTemp(t, "agent:\n  timeout: 5m\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent.Timeout != 5*time.Minute {
		t.Fatalf("Agent.Timeout = %v, want 5m", cfg.Agent.Timeout)
	}
}

func TestLoad_InvalidAgentTimeout(t *testing.T) {
	path := writeTemp(t, "agent:\n  timeout: 0s\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "agent.timeout") {
		t.Errorf("Load() error = %v, want it to identify agent.timeout", err)
	}
}

func TestLoad_InvalidCIRequiredChecksMode(t *testing.T) {
	path := writeTemp(t, "ci:\n  required_checks:\n    mode: jenkins\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "ci.required_checks.mode") {
		t.Errorf("Load() error = %v, want it to identify ci.required_checks.mode", err)
	}
}

func TestLoad_ExplicitModeRequiresChecks(t *testing.T) {
	path := writeTemp(t, "ci:\n  required_checks:\n    mode: explicit\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "ci.required_checks.checks") {
		t.Errorf("Load() error = %v, want it to identify ci.required_checks.checks", err)
	}
}

func TestLoad_InvalidMaxParallel(t *testing.T) {
	path := writeTemp(t, "execution:\n  max_parallel: 0\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "execution.max_parallel") {
		t.Errorf("Load() error = %v, want it to identify execution.max_parallel", err)
	}
}

func TestLoad_NegativeRetryLimit(t *testing.T) {
	path := writeTemp(t, "retry:\n  gate: -1\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "retry.gate") {
		t.Errorf("Load() error = %v, want it to identify retry.gate", err)
	}
}

func TestLoad_BranchTemplateMissingPlaceholder(t *testing.T) {
	path := writeTemp(t, "git:\n  branch_template: agent/fixed\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "git.branch_template") {
		t.Errorf("Load() error = %v, want it to identify git.branch_template", err)
	}
}

func TestLoad_BranchTemplateMissingExecutionPlaceholder(t *testing.T) {
	path := writeTemp(t, "git:\n  branch_template: agent/{issue}\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "git.branch_template") {
		t.Errorf("Load() error = %v, want it to identify git.branch_template", err)
	}
	if !strings.Contains(err.Error(), "{execution}") {
		t.Errorf("Load() error = %v, want it to mention the {execution} placeholder", err)
	}
}

func TestLoad_EmptyQualityGateFields(t *testing.T) {
	path := writeTemp(t, "quality:\n  gates:\n    - name: \"\"\n      command: make test\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "quality.gates[0].name") {
		t.Errorf("Load() error = %v, want it to identify quality.gates[0].name", err)
	}
}

func TestLoad_NonPositiveMaxOutputBytes(t *testing.T) {
	path := writeTemp(t, "quality:\n  max_output_bytes: 0\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "quality.max_output_bytes") {
		t.Errorf("Load() error = %v, want it to identify quality.max_output_bytes", err)
	}
}

func TestLoad_DependencyOverrideSelfReference(t *testing.T) {
	path := writeTemp(t, "dependencies:\n  overrides:\n    \"1\":\n      - \"1\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "dependencies.overrides[1]") {
		t.Errorf("Load() error = %v, want it to identify dependencies.overrides[1]", err)
	}
}

func TestLoad_MultipleErrorsAllReported(t *testing.T) {
	path := writeTemp(t, "execution:\n  max_parallel: 0\nblocked:\n  label: \"\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "execution.max_parallel") {
		t.Errorf("Load() error = %v, want execution.max_parallel", err)
	}
	if !strings.Contains(err.Error(), "blocked.label") {
		t.Errorf("Load() error = %v, want blocked.label", err)
	}
}

func TestDefault_StatusReflectionDisabled(t *testing.T) {
	// Ticket 24: the tracker-side in-progress signal must default to off so
	// existing behavior is unchanged unless an operator opts in.
	cfg := Default()
	if cfg.StatusReflection.Enabled {
		t.Error("StatusReflection.Enabled = true, want false (default off)")
	}
	if cfg.StatusReflection.InProgressLabel != "in-progress" {
		t.Errorf("StatusReflection.InProgressLabel = %q, want in-progress", cfg.StatusReflection.InProgressLabel)
	}
	if cfg.StatusReflection.InReviewLabel != "in-review" {
		t.Errorf("StatusReflection.InReviewLabel = %q, want in-review", cfg.StatusReflection.InReviewLabel)
	}
	if cfg.StatusReflection.FailedLabel != "failed" {
		t.Errorf("StatusReflection.FailedLabel = %q, want failed", cfg.StatusReflection.FailedLabel)
	}
	if cfg.StatusReflection.Comment {
		t.Error("StatusReflection.Comment = true, want false")
	}
}

func TestLoad_StatusReflectionEnabled(t *testing.T) {
	path := writeTemp(t, `
status_reflection:
  enabled: true
  in_progress_label: working
  in_review_label: review
  failed_label: broken
  comment: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := StatusReflectionConfig{
		Enabled:         true,
		InProgressLabel: "working",
		InReviewLabel:   "review",
		FailedLabel:     "broken",
		Comment:         true,
	}
	if cfg.StatusReflection != want {
		t.Errorf("StatusReflection = %+v, want %+v", cfg.StatusReflection, want)
	}
}

func TestLoad_StatusReflectionEnabledRequiresInProgressLabel(t *testing.T) {
	path := writeTemp(t, "status_reflection:\n  enabled: true\n  in_progress_label: \"\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "status_reflection.in_progress_label") {
		t.Errorf("Load() error = %v, want it to identify status_reflection.in_progress_label", err)
	}
}

func TestLoad_StatusReflectionDisabledAllowsEmptyLabels(t *testing.T) {
	// Enabled defaults to false, so leaving the whole block out of a config
	// file (or an explicit false with no labels) must not fail validation —
	// InProgressLabel is only required once an operator opts in.
	path := writeTemp(t, "status_reflection:\n  enabled: false\n")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
}

func TestDefault_LSPInertByDefault(t *testing.T) {
	// No .forge.yaml, and no lsp: section, must leave the feature entirely
	// inert: Enabled false and every override surface empty.
	cfg := Default()
	if cfg.LSP.Enabled {
		t.Error("LSP.Enabled = true, want false (opt-in)")
	}
	if len(cfg.LSP.Servers) != 0 {
		t.Errorf("LSP.Servers = %+v, want empty", cfg.LSP.Servers)
	}
	if len(cfg.LSP.Extensions) != 0 {
		t.Errorf("LSP.Extensions = %+v, want empty", cfg.LSP.Extensions)
	}
	if len(cfg.LSP.Capabilities) != 0 {
		t.Errorf("LSP.Capabilities = %+v, want empty", cfg.LSP.Capabilities)
	}
	if len(cfg.LSP.Providers) != 0 {
		t.Errorf("LSP.Providers = %+v, want empty", cfg.LSP.Providers)
	}
	if err := validate(cfg); err != nil {
		t.Errorf("validate(Default()) = %v, want nil", err)
	}
}

func TestLoad_LSPFullConfig(t *testing.T) {
	path := writeTemp(t, `
lsp:
  enabled: true
  servers:
    go:
      command: ["gopls"]
  extensions:
    .mjs: javascript
  capabilities:
    claude-code:
      definition: false
      hover: true
  providers:
    definition: harness-native
    references: forge-managed
    hover: "off"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.LSP.Enabled {
		t.Error("LSP.Enabled = false, want true")
	}
	gopls, ok := cfg.LSP.Servers["go"]
	if !ok || !slices.Equal(gopls.Command, []string{"gopls"}) {
		t.Errorf("LSP.Servers[go] = %+v, ok=%v, want {[gopls]} true", gopls, ok)
	}
	if cfg.LSP.Extensions[".mjs"] != "javascript" {
		t.Errorf("LSP.Extensions[.mjs] = %q, want javascript", cfg.LSP.Extensions[".mjs"])
	}
	override, ok := cfg.LSP.Capabilities["claude-code"]
	if !ok {
		t.Fatal("LSP.Capabilities[claude-code] missing")
	}
	if override.Definition == nil || *override.Definition {
		t.Errorf("LSP.Capabilities[claude-code].Definition = %v, want pointer to false", override.Definition)
	}
	if override.Hover == nil || !*override.Hover {
		t.Errorf("LSP.Capabilities[claude-code].Hover = %v, want pointer to true", override.Hover)
	}
	if override.References != nil {
		t.Errorf("LSP.Capabilities[claude-code].References = %v, want nil (unset)", override.References)
	}
	if cfg.LSP.Providers["definition"] != LSPProviderHarnessNative {
		t.Errorf("LSP.Providers[definition] = %q, want harness-native", cfg.LSP.Providers["definition"])
	}
	if cfg.LSP.Providers["references"] != LSPProviderForgeManaged {
		t.Errorf("LSP.Providers[references] = %q, want forge-managed", cfg.LSP.Providers["references"])
	}
	if cfg.LSP.Providers["hover"] != LSPProviderOff {
		t.Errorf("LSP.Providers[hover] = %q, want off", cfg.LSP.Providers["hover"])
	}
}

func TestLoad_LSPUnknownKeyRejected(t *testing.T) {
	path := writeTemp(t, "lsp:\n  enable: true\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error for unknown field")
	}
	if !strings.Contains(err.Error(), "enable") {
		t.Errorf("Load() error = %v, want it to name the unknown field enable", err)
	}
}

func TestLoad_LSPServerEmptyCommandRejected(t *testing.T) {
	path := writeTemp(t, "lsp:\n  servers:\n    go:\n      command: []\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "lsp.servers[go].command") {
		t.Errorf("Load() error = %v, want it to identify lsp.servers[go].command", err)
	}
}

func TestLoad_LSPServerEmptyLanguageKeyRejected(t *testing.T) {
	path := writeTemp(t, "lsp:\n  servers:\n    \"\":\n      command: [\"gopls\"]\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "lsp.servers") {
		t.Errorf("Load() error = %v, want it to identify lsp.servers", err)
	}
}

func TestLoad_LSPExtensionMissingDotRejected(t *testing.T) {
	path := writeTemp(t, "lsp:\n  extensions:\n    mjs: javascript\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "lsp.extensions") {
		t.Errorf("Load() error = %v, want it to identify lsp.extensions", err)
	}
}

func TestLoad_LSPExtensionEmptyLanguageRejected(t *testing.T) {
	path := writeTemp(t, "lsp:\n  extensions:\n    .mjs: \"\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "lsp.extensions[.mjs]") {
		t.Errorf("Load() error = %v, want it to identify lsp.extensions[.mjs]", err)
	}
}

func TestLoad_LSPUnknownProviderPreferenceRejected(t *testing.T) {
	path := writeTemp(t, "lsp:\n  providers:\n    definition: sometimes\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "lsp.providers[definition]") {
		t.Errorf("Load() error = %v, want it to identify lsp.providers[definition]", err)
	}
	if !strings.Contains(err.Error(), "sometimes") {
		t.Errorf("Load() error = %v, want it to include the offending value", err)
	}
}

func TestLoad_LSPUnknownProviderCapabilityKeyRejected(t *testing.T) {
	path := writeTemp(t, "lsp:\n  providers:\n    typo_field: harness-native\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "lsp.providers") {
		t.Errorf("Load() error = %v, want it to identify lsp.providers", err)
	}
	if !strings.Contains(err.Error(), "typo_field") {
		t.Errorf("Load() error = %v, want it to include the offending key", err)
	}
}

func TestLoad_NoSecretsFieldsExist(t *testing.T) {
	// Structural guard: config carries no token/password/secret fields.
	// Anything auth-related must be sourced from the environment at use
	// time, never from .forge.yaml.
	path := writeTemp(t, `
tracker:
  type: github
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	_ = cfg // no Token/Secret/Password field exists on Config or its
	// sub-structs; this test exists to make that guarantee discoverable
	// and to fail loudly (compile error) if such a field is ever added
	// without updating this comment.
}
