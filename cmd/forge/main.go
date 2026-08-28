// Command forge is Forge's CLI entrypoint. Subcommands land incrementally
// across tickets; `forge init` (ticket 29) is the first one wired up here.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Teagan42/forge/internal/initdiscovery"
)

const helpText = `forge - deterministic orchestration for software-engineering agents

Usage:
  forge [command]

Commands:
  init    Generate .forge.yaml via deterministic repository-policy discovery

Run 'forge help' with no other commands implemented yet; this is a project
skeleton beyond init.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(os.Stdout, helpText)
		return
	}

	switch args[0] {
	case "init":
		if err := runInit(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "forge init: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Fprintf(os.Stderr, "forge: unknown command %q\n\n", args[0])
	fmt.Fprint(os.Stderr, helpText)
	os.Exit(1)
}

const initUsage = `Usage: forge init [--force] [dir]

Generate .forge.yaml via deterministic repository-policy discovery.
dir defaults to the current directory.

  --force   Overwrite an existing .forge.yaml instead of refusing to.
`

// runInit implements `forge init`: deterministic repository-policy
// discovery that writes .forge.yaml at the repository root. It never
// invokes an LLM and never touches issue bodies, labels, or branch
// protection — see internal/initdiscovery.
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
		// dir stays "."
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
