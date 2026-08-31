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
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"gopkg.in/yaml.v3"
)

// TrackerConfig identifies the issue tracker backing an Execution.
type TrackerConfig struct {
	Type string `yaml:"type"`

	// SkipAuthPreflight opts a context that legitimately needs no tracker
	// credential (e.g. an offline dry run against a tracker the Execution
	// never actually calls) out of `forge execute`/`forge resume`'s startup
	// authentication preflight (see cmd/forge's verifyTrackerAuth). Default
	// false: the preflight runs, and a missing/invalid credential aborts
	// before any side-effecting work begins.
	SkipAuthPreflight bool `yaml:"skip_auth_preflight"`
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

	// ReviewConfidenceFloor is the minimum Confidence (0.0-1.0) a
	// review axis's ERROR-severity Finding must carry to force
	// review.VerdictChangesRequired (issue #158). An ERROR finding below
	// this floor is advisory only: it is still surfaced in
	// review.Result.Findings, but does not by itself block APPROVED.
	ReviewConfidenceFloor float64 `yaml:"review_confidence_floor"`

	// ReviewRubrics optionally overrides one or more review axes' embedded
	// default rubric with a team's own rubric file (issue #162). A blank
	// path for an axis (the zero value) keeps that axis's embedded rubric
	// unchanged.
	ReviewRubrics ReviewRubricsConfig `yaml:"review_rubrics"`
}

// ReviewRubricsConfig names, per review axis, an optional file whose
// contents replace that axis's embedded default rubric text (issue #162:
// rubric.md, quality_rubric.md, docs_rubric.md in
// internal/review/agentreviewer). A blank field uses the embedded default.
type ReviewRubricsConfig struct {
	Bugs    string `yaml:"bugs"`
	Quality string `yaml:"quality"`
	Docs    string `yaml:"docs"`
}

// QualityGate is one deterministic command required to pass before
// publication. See CONTEXT.md "Quality Gate".
type QualityGate struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

// QualityConfig lists the ordered Quality Gates the Gate Runner executes
// and bounds how much of each gate's captured stdout/stderr is retained
// (IDEATION.md §23 "Output bounding"). MaxOutputBytes governs only Quality
// Gate output; it has no effect on other Agent-bound diagnostics (e.g. the
// Claude Code adapter's own captured subprocess output, which uses its own
// fixed bound) — a unified cross-source feedback budget is a later
// ticket's concern.
type QualityConfig struct {
	Gates          []QualityGate `yaml:"gates"`
	MaxOutputBytes int           `yaml:"max_output_bytes"`
}

// PullRequestsConfig configures pull-request publication behavior.
type PullRequestsConfig struct {
	Enabled bool `yaml:"enabled"`
	WatchCI bool `yaml:"watch_ci"`
	// CommitMessageTemplate templates the commit message the COMMITTING
	// stage's Publisher commits validated work with (ticket 22), using the
	// {type}, {title}, {body}, and {issue} placeholders (the inferred
	// Conventional Commits type, the Issue's Title, a wrapped change
	// description, and the Issue's ID). Default:
	// "{type}: {title}\n\n{body}\n\nRefs #{issue}".
	CommitMessageTemplate string `yaml:"commit_message_template"`
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
	PollInterval      time.Duration           `yaml:"poll_interval"`
	MaxOutputBytes    int                     `yaml:"max_output_bytes"`
}

// BlockedConfig configures behavior when an Issue needs human input.
type BlockedConfig struct {
	Label   string `yaml:"label"`
	Comment bool   `yaml:"comment"`
}

// AgentConfig selects the Agent Adapter backend.
type AgentConfig struct {
	Provider string `yaml:"provider"`

	// PermissionMode controls how the Claude Code CLI handles tool-use
	// permission prompts. Forge runs the Agent Adapter as an unattended
	// subprocess (ticket 30, "Agent runs need a non-interactive permission
	// mode") — there is no human present to answer an interactive prompt,
	// so a permission mode that would block on one leaves the run hung
	// until Execution's own timeout/cancellation kicks in. Empty defaults
	// to PermissionModeBypassPermissions, matching the Workspace isolation
	// boundary (CONTEXT.md "Workspace") the Agent already runs inside.
	PermissionMode AgentPermissionMode `yaml:"permission_mode"`

	// Timeout bounds one Agent invocation (issue 33, "Agent runs need a
	// timeout") so a wedged subprocess (observed: `claude -p` stalled at 0%
	// CPU for 14+ minutes) cannot block a Worker, and the whole `forge
	// execute` process, forever. It is a liveness timeout: the deadline
	// resets on every line of subprocess output, so a long-but-progressing
	// run is never killed by it — only a genuine stall trips it. Must be
	// positive; see Default for the shipped value.
	Timeout time.Duration `yaml:"timeout"`
}

// AgentPermissionMode selects the Claude Code CLI's `--permission-mode`
// value.
type AgentPermissionMode string

const (
	// PermissionModeDefault leaves Claude Code's own interactive
	// permission prompting in place. Unsuitable for unattended Execution
	// runs; supported only for operators invoking the Adapter in a context
	// where a human is actually present to answer prompts.
	PermissionModeDefault AgentPermissionMode = "default"
	// PermissionModeAcceptEdits auto-approves file edits but still prompts
	// for other tool categories (e.g. arbitrary shell commands).
	PermissionModeAcceptEdits AgentPermissionMode = "acceptEdits"
	// PermissionModeBypassPermissions auto-approves every tool call,
	// relying on Workspace isolation (the Issue's Git worktree) as the
	// safety boundary instead of per-call prompts. Default for unattended
	// Execution runs.
	PermissionModeBypassPermissions AgentPermissionMode = "bypassPermissions"
	// PermissionModePlan restricts the Agent to read-only planning,
	// prompting before any mutating tool call.
	PermissionModePlan AgentPermissionMode = "plan"
)

// StatusReflectionConfig configures the tracker-side in-progress signal
// Forge applies as an Issue moves through active work (ticket 24,
// "Execution status reflection to the tracker") — see
// internal/statusreflect. Disabled by default: today's behavior (no
// tracker-side signal until a pull request appears) is unchanged unless an
// operator opts in.
type StatusReflectionConfig struct {
	Enabled bool `yaml:"enabled"`
	// InProgressLabel is applied for every state from CLAIMED through
	// PR_CREATING (Forge actively working the Issue) and removed once the
	// Issue leaves that range for any reason. Required (non-empty) when
	// Enabled is true.
	InProgressLabel string `yaml:"in_progress_label"`
	// InReviewLabel replaces InProgressLabel once a pull request exists
	// (CI_PENDING/CI_FAILED). Empty disables this label — the in-progress
	// label is simply removed with nothing to replace it.
	InReviewLabel string `yaml:"in_review_label"`
	// FailedLabel replaces InProgressLabel when an Issue reaches FAILED.
	// Empty disables this label.
	FailedLabel string `yaml:"failed_label"`
	// Comment, if true, posts a one-line comment the first time the
	// in-progress signal is applied (the READY -> CLAIMED transition).
	Comment bool `yaml:"comment"`
}

// LSPConfig configures Forge's semantic-navigation (LSP) tooling surface
// (issue #79 and the LSP Semantic Tooling map). It is a new top-level
// section rather than nested under AgentConfig because it is cross-cutting
// over both the execution and planning paths, not tied to one backend.
//
// The zero value (Enabled false, every map empty) must be fully safe and
// inert: the capability-model, detection, and SemanticProvider tickets this
// section feeds (#82-#84) are independent, still-evolving components, so
// merely adding this config surface must not itself turn on any behavior.
type LSPConfig struct {
	// Enabled turns Forge's semantic-navigation tooling on. Opt-in
	// (defaults false) rather than on-by-default, so "no lsp: section"
	// degrades to exactly today's behavior.
	Enabled bool `yaml:"enabled"`

	// Servers maps a language identifier (as reported by
	// RepositoryContext.Languages, see the Language & server detection
	// ticket) to the Forge-managed language server command to run for it,
	// consulted when capability/provider selection lands on
	// forge-managed. Empty by default: v1 ships no server definitions
	// pre-populated here — that population lives in the detection
	// ticket's registry, not in this config's defaults.
	Servers map[string]LSPServerConfig `yaml:"servers"`

	// Extensions overrides the file-extension -> language mapping the
	// detection ticket otherwise derives from repository manifests. Keys
	// are extensions including the leading dot (e.g. ".mjs"); values are
	// language identifiers matching Servers' keys.
	Extensions map[string]string `yaml:"extensions"`

	// Capabilities force-overrides individual semantic operations for one
	// agent backend, keyed by backend/provider name (matching
	// AgentConfig.Provider, e.g. "claude-code"), overriding that backend's
	// static capability declaration (see the Capability model & backend
	// declaration ticket). A field left nil within an entry means
	// "unmodified" — only explicitly set fields override the backend's
	// declaration.
	Capabilities map[string]LSPCapabilityOverride `yaml:"capabilities"`

	// Providers is the operator escape hatch selecting, per semantic
	// capability (see LSPCapabilityOverride's field names), which provider
	// fulfills it — "harness-native", "forge-managed", or "off". A
	// capability absent from this map falls back to the SemanticProvider
	// seam's own three-state selection policy; this map only overrides
	// that default when an operator needs to force a specific choice.
	Providers map[string]LSPProviderPreference `yaml:"providers"`

	// ReadinessTimeout bounds how long the Forge-managed LSP driver
	// (internal/semantic/lspdriver) blocks waiting for a spawned language
	// server's initialize/initialized handshake to complete before
	// degrading that server to inert rather than surfacing an error to
	// callers. See issue #123.
	ReadinessTimeout time.Duration `yaml:"readiness_timeout"`

	// RestartLimit bounds how many times the Forge-managed LSP driver
	// restarts a language server subprocess that crashed after a
	// successful handshake before giving up and going permanently inert.
	// See issue #123.
	RestartLimit int `yaml:"restart_limit"`

	// MaxResults caps how many results the Forge-managed tool surface
	// (internal/semantic/toolsurface) returns from any list-returning tool
	// (find_references, find_implementations, search_symbols, and the
	// flattened call/type hierarchy tools). A truncated result reports
	// truncated=true and the untruncated total rather than paginating (see
	// ADR-0014 and issue #125).
	MaxResults int `yaml:"max_results"`
}

// LSPServerConfig is one Forge-managed language server definition.
type LSPServerConfig struct {
	Command []string `yaml:"command"`
}

// LSPCapabilityOverride force-sets individual semantic operations for one
// agent backend. Field names mirror the capability model's fine-grained,
// one-flag-per-operation shape. A nil pointer means "leave the backend's
// static declaration unmodified"; only non-nil fields override it.
type LSPCapabilityOverride struct {
	Definition      *bool `yaml:"definition"`
	References      *bool `yaml:"references"`
	Implementations *bool `yaml:"implementations"`
	Hover           *bool `yaml:"hover"`
	DocumentSymbol  *bool `yaml:"document_symbol"`
	WorkspaceSymbol *bool `yaml:"workspace_symbol"`
	CallHierarchy   *bool `yaml:"call_hierarchy"`
	TypeHierarchy   *bool `yaml:"type_hierarchy"`
}

// LSPProviderPreference selects which provider fulfills a semantic
// capability, overriding the SemanticProvider seam's own selection policy.
type LSPProviderPreference string

const (
	// LSPProviderForgeManaged forces Forge to start/own a language server
	// for this capability rather than relying on harness-native tooling.
	LSPProviderForgeManaged LSPProviderPreference = "forge-managed"
	// LSPProviderHarnessNative forces use of the agent backend's own
	// native tool for this capability, even if Forge could also manage a
	// server for it.
	LSPProviderHarnessNative LSPProviderPreference = "harness-native"
	// LSPProviderOff disables this capability entirely, regardless of
	// what the backend declares or what servers are detected.
	LSPProviderOff LSPProviderPreference = "off"
)

// lspCapabilityFields is the fixed set of capability names
// LSPCapabilityOverride and LSPConfig.Providers recognize, mirroring the
// capability model's one-flag-per-operation shape (see LSPCapabilityOverride).
var lspCapabilityFields = map[string]bool{
	"definition":       true,
	"references":       true,
	"implementations":  true,
	"hover":            true,
	"document_symbol":  true,
	"workspace_symbol": true,
	"call_hierarchy":   true,
	"type_hierarchy":   true,
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
	Version          int                    `yaml:"version"`
	Tracker          TrackerConfig          `yaml:"tracker"`
	Git              GitConfig              `yaml:"git"`
	Execution        ExecutionConfig        `yaml:"execution"`
	Retry            domain.RetryLimits     `yaml:"retry"`
	Workflow         WorkflowConfig         `yaml:"workflow"`
	Quality          QualityConfig          `yaml:"quality"`
	PullRequests     PullRequestsConfig     `yaml:"pull_requests"`
	CI               CIConfig               `yaml:"ci"`
	Blocked          BlockedConfig          `yaml:"blocked"`
	Agent            AgentConfig            `yaml:"agent"`
	Dependencies     DependenciesConfig     `yaml:"dependencies"`
	StatusReflection StatusReflectionConfig `yaml:"status_reflection"`
	LSP              LSPConfig              `yaml:"lsp"`
}

// defaultCommitMessageTemplate is PullRequestsConfig.CommitMessageTemplate's
// default value. See internal/engine's runCommitAndPR (ticket 22) and
// commitMessage (ticket 78) for the {type}/{title}/{body}/{issue}
// placeholder rendering.
const defaultCommitMessageTemplate = "{type}: {title}\n\n{body}\n\nRefs #{issue}"

// defaultAgentTimeout is AgentConfig.Timeout's default: generous enough for
// a genuinely long-running (but progressing) agent turn, since Timeout is a
// liveness bound reset by every line of output rather than a flat cap on
// total run length, while still bounding how long a truly wedged subprocess
// (issue 33 was discovered from one stalled 14+ minutes with no output) can
// block a Worker before Forge kills it.
const defaultAgentTimeout = 20 * time.Minute

// defaultLSPReadinessTimeout is LSPConfig.ReadinessTimeout's default: long
// enough for a cold gopls (package load + type-check on first request) to
// complete its initialize/initialized handshake on a typical repository,
// while still bounding how long a wedged or misconfigured server blocks
// before the driver degrades it to inert (issue #123).
const defaultLSPReadinessTimeout = 30 * time.Second

// defaultLSPRestartLimit is LSPConfig.RestartLimit's default: one retry
// tolerates a single transient crash without masking a persistently broken
// server behind repeated restart attempts.
const defaultLSPRestartLimit = 1

// defaultLSPMaxResults is LSPConfig.MaxResults's default: the Forge-managed
// tool surface's per-call cap from ADR-0014, chosen because agents
// consuming these tools act on the first few highest-relevance results in
// practice rather than needing exhaustive enumeration.
const defaultLSPMaxResults = 50

// defaultReviewConfidenceFloor is WorkflowConfig.ReviewConfidenceFloor's
// default (issue #158's acceptance criteria): a review axis's ERROR finding
// must carry at least this much Confidence to force VerdictChangesRequired,
// chosen so only findings the axis itself is fairly sure about can block an
// Issue, while still-plausible-but-uncertain findings route back only as
// advisory signal.
const defaultReviewConfidenceFloor = 0.7

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
			Implementation:        "tdd",
			Review:                true,
			ReviewConfidenceFloor: defaultReviewConfidenceFloor,
		},
		Quality: QualityConfig{MaxOutputBytes: 20000},
		PullRequests: PullRequestsConfig{
			Enabled:               true,
			WatchCI:               true,
			CommitMessageTemplate: defaultCommitMessageTemplate,
		},
		CI: CIConfig{
			MergeRequirements: MergeRequirementsConfig{Mode: MergeRequirementsGitHub},
			PollInterval:      30 * time.Second,
			MaxOutputBytes:    4000,
		},
		Blocked: BlockedConfig{Label: "needs-info", Comment: true},
		Agent: AgentConfig{
			Provider:       "claude-code",
			PermissionMode: PermissionModeBypassPermissions,
			Timeout:        defaultAgentTimeout,
		},
		StatusReflection: StatusReflectionConfig{
			Enabled:         false,
			InProgressLabel: "in-progress",
			InReviewLabel:   "in-review",
			FailedLabel:     "failed",
			Comment:         false,
		},
		LSP: LSPConfig{
			ReadinessTimeout: defaultLSPReadinessTimeout,
			RestartLimit:     defaultLSPRestartLimit,
			MaxResults:       defaultLSPMaxResults,
		},
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
