// Package config loads and validates Forge's repository configuration file,
// .forge.yaml. See CONTEXT.md for the domain vocabulary the sections below
// correspond to (Retry Budget, Quality Gate, Merge Requirements, Dependency
// Source, etc).
//
// Config never expects or stores secrets. Anything that requires
// authentication (tracker tokens, agent API keys) is sourced from the
// environment at the point of use, not from this file.
package config

import (
	"fmt"
	"os"

	"github.com/Teagan42/forge/internal/domain"
	"gopkg.in/yaml.v3"
)

// Deterministic defaults applied to any field omitted from .forge.yaml. See
// Default() for the fully-defaulted zero-config Config these compose into.
const (
	defaultVersion        = 1
	defaultTrackerType    = "github"
	defaultGitBase        = "origin/main"
	defaultBranchTemplate = "agent/{issue}"
	defaultWorktreeRoot   = ".forge/worktrees"
	defaultMaxParallel    = 4
	defaultRetryGate      = 3
	defaultRetryReview    = 2
	defaultRetryCI        = 3
	defaultImplementation = "tdd"
	defaultWorkflowReview = true
	defaultPREnabled      = true
	defaultPRWatchCI      = true
	defaultCIMode         = "github"
	defaultBlockedLabel   = "needs-info"
	defaultBlockedComment = true
	defaultAgentProvider  = "claude-code"
)

// TrackerConfig identifies the issue tracker backing an Execution.
type TrackerConfig struct {
	Type string
}

// GitConfig configures the base revision, branch naming, and Workspace
// location for Workers.
type GitConfig struct {
	Base           string
	BranchTemplate string
	WorktreeRoot   string
}

// ExecutionConfig bounds how many Workers may run concurrently.
type ExecutionConfig struct {
	MaxParallel int
}

// RetryConfig configures the ceiling for each independent Retry Budget
// counter. ToDomain maps it onto domain.RetryLimits, which is what the
// Issue's RetryBudget is actually constructed from — this type exists only
// to carry YAML structure, not to duplicate domain semantics.
type RetryConfig struct {
	Gate   int
	Review int
	CI     int
}

// ToDomain maps the parsed retry ceilings onto domain.RetryLimits.
func (r RetryConfig) ToDomain() domain.RetryLimits {
	return domain.RetryLimits{Gate: r.Gate, Review: r.Review, CI: r.CI}
}

// WorkflowConfig configures the implementation loop and whether a Review
// stage runs after Quality Gates pass.
type WorkflowConfig struct {
	Implementation string
	Review         bool
}

// QualityGate is one deterministic command required to pass before
// publication. See CONTEXT.md "Quality Gate".
type QualityGate struct {
	Name    string
	Command string
}

// QualityConfig lists the ordered Quality Gates the Gate Runner executes.
type QualityConfig struct {
	Gates []QualityGate
}

// PullRequestsConfig configures pull-request publication behavior.
type PullRequestsConfig struct {
	Enabled bool
	WatchCI bool
}

// RequiredChecksMode selects where the CI Supervisor sources Merge
// Requirements from. See CONTEXT.md "Merge Requirements".
type RequiredChecksMode string

const (
	// RequiredChecksGitHub sources required checks from the tracker's
	// native branch protection/rulesets. Authoritative by default.
	RequiredChecksGitHub RequiredChecksMode = "github"
	// RequiredChecksExplicit overrides the tracker with a fixed check
	// list, for repositories without branch protection configured.
	RequiredChecksExplicit RequiredChecksMode = "explicit"
)

// RequiredChecksConfig configures how the CI Supervisor determines Merge
// Requirements.
type RequiredChecksConfig struct {
	Mode   RequiredChecksMode
	Checks []string
}

// CIConfig configures CI Supervisor behavior.
type CIConfig struct {
	RequiredChecks RequiredChecksConfig
}

// BlockedConfig configures behavior when an Issue needs human input.
type BlockedConfig struct {
	Label   string
	Comment bool
}

// AgentConfig selects the Agent Adapter backend.
type AgentConfig struct {
	Provider string
}

// DependenciesConfig configures the escape-hatch Dependency Source. The
// canonical source is the issue body's `## Dependencies` block; entries here
// override it. See CONTEXT.md "Dependency Source" and ADR 0003. Keys and
// values are Issue IDs; Overrides[issueID] is the full replacement list of
// IDs issueID depends on.
type DependenciesConfig struct {
	Overrides map[string][]string
}

// Config is Forge's fully-resolved, defaulted, and validated repository
// configuration, loaded from .forge.yaml. It never contains secrets.
type Config struct {
	Version      int
	Tracker      TrackerConfig
	Git          GitConfig
	Execution    ExecutionConfig
	Retry        RetryConfig
	Workflow     WorkflowConfig
	Quality      QualityConfig
	PullRequests PullRequestsConfig
	CI           CIConfig
	Blocked      BlockedConfig
	Agent        AgentConfig
	Dependencies DependenciesConfig
}

// Default returns the fully-defaulted Config used when no .forge.yaml is
// present — the zero-config case.
func Default() Config {
	return resolve(rawConfig{})
}

// Load reads, parses, defaults, and validates the .forge.yaml file at path.
// Malformed YAML and validation failures are both returned as errors
// identifying the offending field where possible; see ValidationError.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}

	cfg := resolve(raw)
	if err := validate(cfg); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// resolve merges raw (possibly partial) parsed YAML onto the deterministic
// defaults, section by section and field by field. A nil section pointer
// takes the entire default section; a present section with nil leaf fields
// takes the default for just those fields.
func resolve(raw rawConfig) Config {
	return Config{
		Version:      intOr(raw.Version, defaultVersion),
		Tracker:      resolveTracker(raw.Tracker),
		Git:          resolveGit(raw.Git),
		Execution:    resolveExecution(raw.Execution),
		Retry:        resolveRetry(raw.Retry),
		Workflow:     resolveWorkflow(raw.Workflow),
		Quality:      resolveQuality(raw.Quality),
		PullRequests: resolvePullRequests(raw.PullRequests),
		CI:           resolveCI(raw.CI),
		Blocked:      resolveBlocked(raw.Blocked),
		Agent:        resolveAgent(raw.Agent),
		Dependencies: resolveDependencies(raw.Dependencies),
	}
}

func resolveTracker(r *rawTracker) TrackerConfig {
	cfg := TrackerConfig{Type: defaultTrackerType}
	if r == nil {
		return cfg
	}
	cfg.Type = strOr(r.Type, defaultTrackerType)
	return cfg
}

func resolveGit(r *rawGit) GitConfig {
	cfg := GitConfig{
		Base:           defaultGitBase,
		BranchTemplate: defaultBranchTemplate,
		WorktreeRoot:   defaultWorktreeRoot,
	}
	if r == nil {
		return cfg
	}
	cfg.Base = strOr(r.Base, defaultGitBase)
	cfg.BranchTemplate = strOr(r.BranchTemplate, defaultBranchTemplate)
	cfg.WorktreeRoot = strOr(r.WorktreeRoot, defaultWorktreeRoot)
	return cfg
}

func resolveExecution(r *rawExecution) ExecutionConfig {
	cfg := ExecutionConfig{MaxParallel: defaultMaxParallel}
	if r == nil {
		return cfg
	}
	cfg.MaxParallel = intOr(r.MaxParallel, defaultMaxParallel)
	return cfg
}

func resolveRetry(r *rawRetry) RetryConfig {
	cfg := RetryConfig{Gate: defaultRetryGate, Review: defaultRetryReview, CI: defaultRetryCI}
	if r == nil {
		return cfg
	}
	cfg.Gate = intOr(r.Gate, defaultRetryGate)
	cfg.Review = intOr(r.Review, defaultRetryReview)
	cfg.CI = intOr(r.CI, defaultRetryCI)
	return cfg
}

func resolveWorkflow(r *rawWorkflow) WorkflowConfig {
	cfg := WorkflowConfig{Implementation: defaultImplementation, Review: defaultWorkflowReview}
	if r == nil {
		return cfg
	}
	cfg.Implementation = strOr(r.Implementation, defaultImplementation)
	cfg.Review = boolOr(r.Review, defaultWorkflowReview)
	return cfg
}

func resolveQuality(r *rawQuality) QualityConfig {
	if r == nil || len(r.Gates) == 0 {
		return QualityConfig{Gates: nil}
	}
	gates := make([]QualityGate, len(r.Gates))
	for i, g := range r.Gates {
		gates[i] = QualityGate{Name: g.Name, Command: g.Command}
	}
	return QualityConfig{Gates: gates}
}

func resolvePullRequests(r *rawPullRequests) PullRequestsConfig {
	cfg := PullRequestsConfig{Enabled: defaultPREnabled, WatchCI: defaultPRWatchCI}
	if r == nil {
		return cfg
	}
	cfg.Enabled = boolOr(r.Enabled, defaultPREnabled)
	cfg.WatchCI = boolOr(r.WatchCI, defaultPRWatchCI)
	return cfg
}

func resolveCI(r *rawCI) CIConfig {
	cfg := CIConfig{RequiredChecks: RequiredChecksConfig{Mode: RequiredChecksMode(defaultCIMode)}}
	if r == nil || r.RequiredChecks == nil {
		return cfg
	}
	rc := r.RequiredChecks
	cfg.RequiredChecks.Mode = RequiredChecksMode(strOr(rc.Mode, defaultCIMode))
	cfg.RequiredChecks.Checks = append([]string(nil), rc.Checks...)
	return cfg
}

func resolveBlocked(r *rawBlocked) BlockedConfig {
	cfg := BlockedConfig{Label: defaultBlockedLabel, Comment: defaultBlockedComment}
	if r == nil {
		return cfg
	}
	cfg.Label = strOr(r.Label, defaultBlockedLabel)
	cfg.Comment = boolOr(r.Comment, defaultBlockedComment)
	return cfg
}

func resolveAgent(r *rawAgent) AgentConfig {
	cfg := AgentConfig{Provider: defaultAgentProvider}
	if r == nil {
		return cfg
	}
	cfg.Provider = strOr(r.Provider, defaultAgentProvider)
	return cfg
}

func resolveDependencies(r *rawDependencies) DependenciesConfig {
	if r == nil || len(r.Overrides) == 0 {
		return DependenciesConfig{Overrides: nil}
	}
	overrides := make(map[string][]string, len(r.Overrides))
	for k, v := range r.Overrides {
		overrides[k] = append([]string(nil), v...)
	}
	return DependenciesConfig{Overrides: overrides}
}

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
