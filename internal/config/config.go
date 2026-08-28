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
	"bytes"
	"fmt"
	"os"

	"github.com/Teagan42/forge/internal/domain"
	"gopkg.in/yaml.v3"
)

// TrackerConfig identifies the issue tracker backing an Execution.
type TrackerConfig struct {
	Type string `yaml:"type"`
}

// GitConfig configures the base revision, branch naming, and Workspace
// location for Workers.
type GitConfig struct {
	Base           string `yaml:"base"`
	BranchTemplate string `yaml:"branch_template"`
	WorktreeRoot   string `yaml:"worktree_root"`
}

// ExecutionConfig bounds how many Workers may run concurrently.
type ExecutionConfig struct {
	MaxParallel int `yaml:"max_parallel"`
}

// WorkflowConfig configures the implementation loop and whether a Review
// stage runs after Quality Gates pass.
type WorkflowConfig struct {
	Implementation string `yaml:"implementation"`
	Review         bool   `yaml:"review"`
}

// QualityGate is one deterministic command required to pass before
// publication. See CONTEXT.md "Quality Gate".
type QualityGate struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

// QualityConfig lists the ordered Quality Gates the Gate Runner executes.
type QualityConfig struct {
	Gates []QualityGate `yaml:"gates"`
}

// PullRequestsConfig configures pull-request publication behavior.
type PullRequestsConfig struct {
	Enabled bool `yaml:"enabled"`
	WatchCI bool `yaml:"watch_ci"`
}

// MergeRequirementsMode selects where the CI Supervisor sources Merge
// Requirements from. See CONTEXT.md "Merge Requirements".
type MergeRequirementsMode string

const (
	// MergeRequirementsGitHub sources Merge Requirements from the
	// tracker's native branch protection/rulesets. Authoritative by
	// default. Wire value: "github".
	MergeRequirementsGitHub MergeRequirementsMode = "github"
	// MergeRequirementsExplicit overrides the tracker with a fixed check
	// list, for repositories without branch protection configured. Wire
	// value: "explicit".
	MergeRequirementsExplicit MergeRequirementsMode = "explicit"
)

// MergeRequirementsConfig configures how the CI Supervisor determines Merge
// Requirements. The YAML key stays `required_checks` for backward/forward
// wire compatibility even though the Go identifiers use the domain term
// Merge Requirements (CONTEXT.md lists "required checks" as a term to avoid
// for this concept).
type MergeRequirementsConfig struct {
	Mode   MergeRequirementsMode `yaml:"mode"`
	Checks []string              `yaml:"checks"`
}

// CIConfig configures CI Supervisor behavior.
type CIConfig struct {
	MergeRequirements MergeRequirementsConfig `yaml:"required_checks"`
}

// BlockedConfig configures behavior when an Issue needs human input.
type BlockedConfig struct {
	Label   string `yaml:"label"`
	Comment bool   `yaml:"comment"`
}

// AgentConfig selects the Agent Adapter backend.
type AgentConfig struct {
	Provider string `yaml:"provider"`
}

// DependenciesConfig configures the escape-hatch Dependency Source. The
// canonical source is the issue body's `## Dependencies` block; entries here
// override it. See CONTEXT.md "Dependency Source" and ADR 0003. Keys and
// values are Issue IDs; Overrides[issueID] is the full replacement list of
// IDs issueID depends on.
type DependenciesConfig struct {
	Overrides map[string][]string `yaml:"overrides"`
}

// Config is Forge's fully-resolved, defaulted, and validated repository
// configuration, loaded from .forge.yaml. It never contains secrets.
//
// Retry reuses domain.RetryLimits directly rather than a parallel
// config-only type, per CONTEXT.md's Retry Budget vocabulary — the ceilings
// configured here are exactly what an Issue's RetryBudget is constructed
// from.
type Config struct {
	Version      int                `yaml:"version"`
	Tracker      TrackerConfig      `yaml:"tracker"`
	Git          GitConfig          `yaml:"git"`
	Execution    ExecutionConfig    `yaml:"execution"`
	Retry        domain.RetryLimits `yaml:"retry"`
	Workflow     WorkflowConfig     `yaml:"workflow"`
	Quality      QualityConfig      `yaml:"quality"`
	PullRequests PullRequestsConfig `yaml:"pull_requests"`
	CI           CIConfig           `yaml:"ci"`
	Blocked      BlockedConfig      `yaml:"blocked"`
	Agent        AgentConfig        `yaml:"agent"`
	Dependencies DependenciesConfig `yaml:"dependencies"`
}

// Default returns the fully-defaulted Config used when no .forge.yaml is
// present — the zero-config case. It is also the single source of truth for
// every deterministic default: Load starts from this literal and lets YAML
// decoding overwrite only the fields the file actually sets.
func Default() Config {
	return Config{
		Version: 1,
		Tracker: TrackerConfig{Type: "github"},
		Git: GitConfig{
			Base:           "origin/main",
			BranchTemplate: "forge/{execution}/{issue}",
			WorktreeRoot:   ".forge/worktrees",
		},
		Execution: ExecutionConfig{MaxParallel: 4},
		Retry:     domain.RetryLimits{Gate: 3, Review: 2, CI: 3},
		Workflow: WorkflowConfig{
			Implementation: "tdd",
			Review:         true,
		},
		PullRequests: PullRequestsConfig{Enabled: true, WatchCI: true},
		CI: CIConfig{
			MergeRequirements: MergeRequirementsConfig{Mode: MergeRequirementsGitHub},
		},
		Blocked: BlockedConfig{Label: "needs-info", Comment: true},
		Agent:   AgentConfig{Provider: "claude-code"},
	}
}

// Load reads, parses, defaults, and validates the .forge.yaml file at path.
//
// Decoding starts from Default() and unmarshals onto it, so any field
// absent from the file keeps its default and only fields actually present
// are overwritten — including an explicit false or 0, which correctly
// resets a field rather than being indistinguishable from "absent". An
// explicit YAML null is the one exception: yaml.v3 treats it as "no value
// supplied" and leaves the pre-populated default in place rather than
// zeroing the field (see TestLoad_ExplicitNullLeavesDefaultInPlace).
// Unknown keys (e.g. a typo like `gats` for `gate`) are rejected rather
// than silently ignored, since a deterministic orchestrator must not
// tolerate an operator's misspelled config being treated as absent.
//
// Malformed YAML and validation failures are both returned as errors
// identifying the offending field where possible; see FieldError.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := Default()
	if len(bytes.TrimSpace(data)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}

	if err := validate(cfg); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}
