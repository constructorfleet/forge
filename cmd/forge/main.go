// Command forge is Forge's CLI entrypoint. Subcommands land in later
// tickets; this scaffold only wires up help output.
package main

import (
	"fmt"
	"os"
)

const helpText = `forge - deterministic orchestration for software-engineering agents

Usage:
  forge [command]

No commands are implemented yet. This is a project skeleton.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(os.Stdout, helpText)
		return
	}

	fmt.Fprintf(os.Stderr, "forge: unknown command %q\n\n", args[0])
	fmt.Fprint(os.Stderr, helpText)
	os.Exit(1)
}
