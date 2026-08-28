// Command forge is Forge's CLI entrypoint. Subcommands land incrementally
// across tickets; `forge init` (ticket 29) is the first one wired up here.
package main

import (
	"fmt"
	"os"

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

// runInit implements `forge init`: deterministic repository-policy
// discovery that writes .forge.yaml at the repository root. It never
// invokes an LLM and never touches issue bodies, labels, or branch
// protection — see internal/initdiscovery.
func runInit(args []string) error {
	dir := "."
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprint(os.Stdout, "Usage: forge init [dir]\n\nGenerate .forge.yaml via deterministic repository-policy discovery.\ndir defaults to the current directory.\n")
			return nil
		}
		dir = a
	}

	result, err := initdiscovery.Detect(dir)
	if err != nil {
		return fmt.Errorf("discover repository policy: %w", err)
	}

	out, err := initdiscovery.Render(result)
	if err != nil {
		return fmt.Errorf("render .forge.yaml: %w", err)
	}

	path := dir + "/.forge.yaml"
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Fprintf(os.Stdout, "wrote %s\n", path)
	for _, n := range result.Notes {
		fmt.Fprintf(os.Stdout, "  note: %s: %s\n", n.Field, n.Message)
	}
	return nil
}
