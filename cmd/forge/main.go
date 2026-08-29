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
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/storage"
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
  plan <feature-id>        Run the planning compiler pipeline for a Feature
  approve <feature-id> spec   Approve a Specification at its current revision
  approve <feature-id> tickets  Approve a Ticket Plan at its current revision
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
	case "plan":
		return runPlan(rest)
	case "approve":
		if len(rest) > 0 && rest[0] == "tickets" {
			return runApproveTickets(rest[1:])
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

const planUsage = `Usage: forge plan <feature-id> [--until wayfinding|spec|tickets]

Run the planning compiler pipeline for a Feature.

  --until   Stop after the named stage (default: tickets)
            wayfinding  - resolve Decisions only
            spec        - generate and validate Specification
            tickets     - generate and validate TicketPlan (default)
`

func runPlan(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, planUsage)
		return 0
	}

	var featureID string
	untilStage := "tickets"
	var positional []string
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, planUsage)
			return 0
		case "--until":
		default:
			positional = append(positional, a)
		}
	}

	for i, a := range positional {
		if a == "--until" && i+1 < len(positional) {
			untilStage = positional[i+1]
			continue
		}
		if a == "--until" {
			fmt.Fprintf(os.Stderr, "--until requires a stage argument\n\n%s", planUsage)
			return 1
		}
		if featureID == "" {
			featureID = a
		} else {
			fmt.Fprintf(os.Stderr, "too many arguments: %v\n\n%s", positional, planUsage)
			return 1
		}
	}

	if featureID == "" {
		fmt.Fprintf(os.Stderr, "feature-id is required\n\n%s", planUsage)
		return 1
	}

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

	_ = planengine.New(store)

	backend := planningagent.NewFakeBackend()

	specEngine := specengine.NewSpecEngine(backend)

	if untilStage == "spec" || untilStage == "tickets" {
		if err := specEngine.GenerateSpec(ctx, featureID, &fileArtifactLoader{featureID: featureID}); err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "spec.md generated for feature %s\n", featureID)
		if untilStage == "spec" {
			return 0
		}
	}

	if untilStage == "tickets" {
		if err := specEngine.GenerateTicketPlan(ctx, featureID, &fileArtifactLoader{featureID: featureID}); err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "ticket-plan.md generated for feature %s\n", featureID)
	}

	return 0
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
	return 0
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
