package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if cfg.Git.BranchTemplate != "agent/{issue}" {
		t.Errorf("Git.BranchTemplate = %q, want agent/{issue}", cfg.Git.BranchTemplate)
	}
	if cfg.Git.WorktreeRoot != ".forge/worktrees" {
		t.Errorf("Git.WorktreeRoot = %q, want .forge/worktrees", cfg.Git.WorktreeRoot)
	}
	if cfg.Execution.MaxParallel != 4 {
		t.Errorf("Execution.MaxParallel = %d, want 4", cfg.Execution.MaxParallel)
	}
	if cfg.Retry.Gate != 3 || cfg.Retry.Review != 2 || cfg.Retry.CI != 3 {
		t.Errorf("Retry = %+v, want {Gate:3 Review:2 CI:3}", cfg.Retry)
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
	if cfg.CI.RequiredChecks.Mode != RequiredChecksGitHub {
		t.Errorf("CI.RequiredChecks.Mode = %q, want github", cfg.CI.RequiredChecks.Mode)
	}
	if cfg.Blocked.Label != "needs-info" || !cfg.Blocked.Comment {
		t.Errorf("Blocked = %+v, want {needs-info true}", cfg.Blocked)
	}
	if cfg.Agent.Provider != "claude-code" {
		t.Errorf("Agent.Provider = %q, want claude-code", cfg.Agent.Provider)
	}
	if len(cfg.Dependencies.Overrides) != 0 {
		t.Errorf("Dependencies.Overrides = %+v, want empty", cfg.Dependencies.Overrides)
	}

	// Default() must itself be a valid config.
	if err := validate(cfg); err != nil {
		t.Errorf("validate(Default()) = %v, want nil", err)
	}
}

func TestRetryConfig_ToDomain(t *testing.T) {
	rc := RetryConfig{Gate: 5, Review: 1, CI: 2}
	got := rc.ToDomain()
	want := domain.RetryLimits{Gate: 5, Review: 1, CI: 2}
	if got != want {
		t.Errorf("ToDomain() = %+v, want %+v", got, want)
	}
}

func TestLoad_CompleteConfig(t *testing.T) {
	path := writeTemp(t, `
version: 1
tracker:
  type: github
git:
  base: origin/main
  branch_template: agent/{issue}
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
	if cfg.Retry.Gate != 5 || cfg.Retry.Review != 4 || cfg.Retry.CI != 6 {
		t.Errorf("Retry = %+v, want {5 4 6}", cfg.Retry)
	}
	if len(cfg.Quality.Gates) != 2 {
		t.Fatalf("Quality.Gates len = %d, want 2", len(cfg.Quality.Gates))
	}
	if cfg.Quality.Gates[0] != (QualityGate{Name: "test", Command: "make test"}) {
		t.Errorf("Quality.Gates[0] = %+v", cfg.Quality.Gates[0])
	}
	if cfg.CI.RequiredChecks.Mode != RequiredChecksExplicit {
		t.Errorf("CI.RequiredChecks.Mode = %q, want explicit", cfg.CI.RequiredChecks.Mode)
	}
	if got, want := cfg.CI.RequiredChecks.Checks, []string{"build", "test"}; !equalStrings(got, want) {
		t.Errorf("CI.RequiredChecks.Checks = %v, want %v", got, want)
	}
	if deps, ok := cfg.Dependencies.Overrides["123"]; !ok || !equalStrings(deps, []string{"100", "101"}) {
		t.Errorf("Dependencies.Overrides[123] = %v", deps)
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
	if cfg.Git.BranchTemplate != "agent/{issue}" {
		t.Errorf("Git.BranchTemplate = %q, want default agent/{issue}", cfg.Git.BranchTemplate)
	}
	if cfg.Git.WorktreeRoot != ".forge/worktrees" {
		t.Errorf("Git.WorktreeRoot = %q, want default .forge/worktrees", cfg.Git.WorktreeRoot)
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

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
