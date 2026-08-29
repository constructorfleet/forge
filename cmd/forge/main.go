// Command forge is Forge's CLI entrypoint. Subcommands land incrementally
// across tickets: `forge init` (ticket 29) generates .forge.yaml, and
// `forge execute`/`forge status` (ticket 18) drive and inspect an Execution.
// `forge plan` (ticket 16) drives the planning compiler pipeline.
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

	return 0
}

type fileArtifactLoader struct {
	featureID string
}

func (f *fileArtifactLoader) LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error) {
	return nil, fmt.Errorf("not implemented: file-based artifact loading")
}

func (f *fileArtifactLoader) LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error) {
	return nil, fmt.Errorf("not implemented: file-based artifact loading")
}

func (f *fileArtifactLoader) SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error {
	return fmt.Errorf("not implemented: file-based artifact saving")
}
