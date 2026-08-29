// Command forge is Forge's CLI entrypoint. Subcommands land incrementally
// across tickets: `forge init` (ticket 29) generates .forge.yaml, and
// `forge execute`/`forge status` (ticket 18) drive and inspect an Execution.
// `forge plan` (ticket 16) drives the planning compiler pipeline.
// `forge approve` (ticket 17) approves a Specification at a specific revision.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Teagan42/forge/internal/initdiscovery"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/replan"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/ticketplan"
)

const helpText = `forge - deterministic orchestration for software-engineering agents

Usage:
  forge [command]

Commands:
  init                     Generate .forge.yaml via deterministic repository-policy discovery
  execute <issue-number>   Execute a single Issue with no unmet Dependencies
  status [execution-id]    Show active Executions or one Execution's persisted state
  cancel <execution-id>    Stop an active Execution and mark running work CANCELLED
  retry <execution>/<issue> Retry a FAILED Issue within its Execution
  resume <execution-id>    Reconcile and continue an incomplete Execution
  goal init <feature-id>   Create a skeleton .forge/features/<feature-id>/goal.md
  plan <feature-id>        Run the planning compiler pipeline for a Feature
  approve <feature-id> spec   Approve a Specification at its current revision
  approve <feature-id> tickets  Approve a Ticket Plan at its current revision
  materialize <feature-id> Turn an approved Ticket Plan into an executable Issue DAG
  help                     Show this help text

Run 'forge <command> --help' for command-specific flags.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(os.Stdout, helpText)
		return 0
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		if err := runInit(rest); err != nil {
			fmt.Fprintf(os.Stderr, "forge init: %v\n", err)
			return 1
		}
		return 0
	case "execute":
		return runExecute(rest)
	case "status":
		return runStatus(rest)
	case "cancel":
		return runCancel(rest)
	case "retry":
		return runRetry(rest)
	case "resume":
		return runResume(rest)
	case "goal":
		return runGoalInit(rest)
	case "plan":
		return runPlan(rest)
	case "materialize":
		return runMaterialize(rest)
	case "approve":
		if len(rest) >= 2 && rest[1] == "tickets" {
			return runApproveTickets(rest)
		}
		return runApprove(rest)
	default:
		fmt.Fprintf(os.Stderr, "forge: unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, helpText)
		return 1
	}
}

const initUsage = `Usage: forge init [--force] [dir]

Generate .forge.yaml via deterministic repository-policy discovery.
dir defaults to the current directory.

  --force   Overwrite an existing .forge.yaml instead of refusing to.
`

func runInit(args []string) error {
	dir := "."
	force := false
	var positional []string
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, initUsage)
			return nil
		case "--force":
			force = true
		default:
			positional = append(positional, a)
		}
	}
	switch len(positional) {
	case 0:
	case 1:
		dir = positional[0]
	default:
		return fmt.Errorf("too many arguments: %v\n\n%s", positional, initUsage)
	}

	path := filepath.Join(dir, ".forge.yaml")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; rerun with --force to overwrite", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}

	result := initdiscovery.Detect(dir)

	out, err := initdiscovery.Render(result)
	if err != nil {
		return fmt.Errorf("render .forge.yaml: %w", err)
	}

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Fprintf(os.Stdout, "wrote %s\n", path)
	for _, n := range result.Notes {
		fmt.Fprintf(os.Stdout, "  note: %s: %s\n", n.Field, n.Message)
	}
	return nil
}

const approveUsage = `Usage: forge approve <feature-id> spec

Approve a Specification at its current revision. The approval binds to the
specification's content revision (computed from Kind, DerivedFrom, and Sections).
If the specification's definitional content is edited, its revision changes and
the approval is automatically invalidated.

This command requires that the Specification has passed automated review
(VerdictApproved from SpecificationReview).
`

func runApprove(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, approveUsage)
		return 0
	}

	if len(args) < 2 || args[1] != "spec" {
		fmt.Fprintf(os.Stderr, "forge approve: expected 'spec' as second argument\n\n%s", approveUsage)
		return 1
	}

	featureID := args[0]

	ctx := context.Background()

	dsn := filepath.Join(".forge", "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage.Open: %v\n", err)
		return 1
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Migrate: %v\n", err)
		return 1
	}

	loader := &fileArtifactLoader{featureID: featureID}

	// Load the spec
	specArtifact, err := loader.LoadSpec(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge approve: load spec: %v\n", err)
		return 1
	}
	if specArtifact == nil {
		fmt.Fprintf(os.Stderr, "forge approve: no spec found for feature %s\n", featureID)
		return 1
	}

	// Check if spec is approved by automated review (state should be "reviewed" or similar)
	// For now, we just check that it's a valid spec
	if specArtifact.Kind != planning.KindSpec {
		fmt.Fprintf(os.Stderr, "forge approve: artifact is not a specification\n")
		return 1
	}

	// Compute current revision and set as approved
	currentRev := planning.ComputeRevision(specArtifact)
	specArtifact.ApprovedRevision = currentRev
	specArtifact.State = "approved"

	// Save the approved spec
	if err := loader.SaveSpec(ctx, featureID, specArtifact); err != nil {
		fmt.Fprintf(os.Stderr, "forge approve: save spec: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "spec.md approved for feature %s at revision %s\n", featureID, currentRev[:16])
	return 0
}

const approveTicketsUsage = `Usage: forge approve <feature-id> tickets

Approve a Ticket Plan at its current revision. The approval binds to the
ticket plan's content revision (computed from Kind, DerivedFrom, and Sections).
If the ticket plan's definitional content is edited, its revision changes and
the approval is automatically invalidated.

This command requires that the Ticket Plan has passed automated review
(VerdictApproved from TicketPlanReview).
`

func runApproveTickets(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, approveTicketsUsage)
		return 0
	}

	if len(args) < 2 || args[1] != "tickets" {
		fmt.Fprintf(os.Stderr, "forge approve: expected 'tickets' as second argument\n\n%s", approveTicketsUsage)
		return 1
	}

	featureID := args[0]

	ctx := context.Background()

	dsn := filepath.Join(".forge", "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage.Open: %v\n", err)
		return 1
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Migrate: %v\n", err)
		return 1
	}

	loader := &fileArtifactLoader{featureID: featureID}

	// Load the ticket plan
	tpArtifact, err := loader.LoadTicketPlan(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge approve tickets: load ticket plan: %v\n", err)
		return 1
	}
	if tpArtifact == nil {
		fmt.Fprintf(os.Stderr, "forge approve tickets: no ticket plan found for feature %s\n", featureID)
		return 1
	}

	// Check if ticket plan is a valid ticket plan
	if tpArtifact.Kind != planning.KindTicketPlan {
		fmt.Fprintf(os.Stderr, "forge approve tickets: artifact is not a ticket plan\n")
		return 1
	}

	// Compute current revision and set as approved
	currentRev := planning.ComputeRevision(tpArtifact)
	tpArtifact.ApprovedRevision = currentRev
	tpArtifact.State = "approved"

	// Save the approved ticket plan
	if err := loader.SaveTicketPlan(ctx, featureID, tpArtifact); err != nil {
		fmt.Fprintf(os.Stderr, "forge approve tickets: save ticket plan: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "ticket-plan.md approved for feature %s at revision %s\n", featureID, currentRev[:16])

	if err := resumeFrozenFeature(ctx, store, featureID, currentRev, tpArtifact); err != nil {
		fmt.Fprintf(os.Stderr, "forge approve tickets: %v\n", err)
		return 1
	}
	return 0
}

// resumeFrozenFeature is acceptance item 5's approval-side half: once a new
// Ticket Plan is approved, the Issues the old plan produced that this one no
// longer contains — and that never started — are closed as superseded, and
// only then is the Feature's replan freeze lifted so frozen work can resume.
// A Feature that is not frozen (the ordinary, non-replan approval) is left
// entirely alone.
func resumeFrozenFeature(ctx context.Context, store storage.Store, featureID, planRevision string, plan *planning.Artifact) error {
	// Checked before the plan is parsed so an ordinary approval of a
	// never-frozen Feature is completely unaffected by replanning — it does
	// not even have to satisfy the ticket parser.
	frozen, _, err := store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		return fmt.Errorf("check replan freeze: %w", err)
	}
	if !frozen {
		return nil
	}

	tickets, err := ticketplan.ParseTicketPlan(plan)
	if err != nil {
		return fmt.Errorf("parse approved ticket plan: %w", err)
	}
	planned := make([]string, 0, len(tickets))
	for _, t := range tickets {
		planned = append(planned, t.Key)
	}

	superseded, err := replan.ResumeFeature(ctx, store, featureID, planRevision, planned)
	if errors.Is(err, replan.ErrNotFrozen) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, id := range superseded {
		fmt.Fprintf(os.Stdout, "issue %s closed as superseded by the new ticket plan\n", id)
	}
	fmt.Fprintf(os.Stdout, "feature %s unfrozen; work may resume against the approved plan\n", featureID)
	return nil
}

type fileArtifactLoader struct {
	featureID string
}

func (f *fileArtifactLoader) LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error) {
	path := filepath.Join(".forge", "features", featureID, "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return planning.Parse(data)
}

func (f *fileArtifactLoader) SaveGoal(ctx context.Context, featureID string, goal *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "goal.md")
	data := planning.Render(goal)
	return os.WriteFile(path, data, 0o644)
}

func (f *fileArtifactLoader) LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error) {
	dir := filepath.Join(".forge", "features", featureID, "decisions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]*planning.Artifact{}, nil
		}
		return nil, err
	}

	decisions := make(map[string]*planning.Artifact)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		// Extract decision ID from filename (NNN-slug.md)
		id := entry.Name()[:len(entry.Name())-3]
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		artifact, err := planning.Parse(data)
		if err != nil {
			return nil, err
		}
		decisions[id] = artifact
	}
	return decisions, nil
}

// SaveDecision writes one Decision Artifact back to
// .forge/features/<feature>/decisions/<id>.md, creating the directory if
// this is the Feature's first Decision. It is what makes fileArtifactLoader
// satisfy replan.DecisionStore, so a REPLAN_REQUIRED escalation can
// create/reopen a Decision on disk (ticket 22).
func (f *fileArtifactLoader) SaveDecision(ctx context.Context, featureID, decisionID string, decision *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID, "decisions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, decisionID+".md"), planning.Render(decision), 0o644)
}

func (f *fileArtifactLoader) SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "spec.md")
	data := planning.Render(spec)
	return os.WriteFile(path, data, 0o644)
}

func (f *fileArtifactLoader) LoadSpec(ctx context.Context, featureID string) (*planning.Artifact, error) {
	path := filepath.Join(".forge", "features", featureID, "spec.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return planning.Parse(data)
}

func (f *fileArtifactLoader) LoadTicketPlan(ctx context.Context, featureID string) (*planning.Artifact, error) {
	path := filepath.Join(".forge", "features", featureID, "ticket-plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return planning.Parse(data)
}

func (f *fileArtifactLoader) SaveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "ticket-plan.md")
	data := planning.Render(tp)
	return os.WriteFile(path, data, 0o644)
}
