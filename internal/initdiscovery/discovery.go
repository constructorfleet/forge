// Package initdiscovery implements the deterministic repository-policy
// discovery behind `forge init` (ticket 29). It inspects a repository for
// its base branch, package manager, quality-gate commands, CI workflow
// hints, agent-instruction files, and issue tracker, then produces a
// config.Config ready to be rendered as .forge.yaml.
//
// Detection is strictly deterministic: no LLM is invoked. Every value comes
// from an explicit known config format (package.json, go.mod, pyproject.toml,
// Cargo.toml, Makefile, Taskfile, justfile), a CI workflow file, or a
// conventional default tied to a detected toolchain. Anything that cannot
// be resolved by one of those tiers is left with a clear Note rather than
// silently defaulted to a possibly-wrong value; see Render.
package initdiscovery

import (
	"fmt"

	"github.com/Teagan42/forge/internal/config"
)

// gateKinds is the fixed, ordered set of Quality Gate kinds forge init
// knows how to detect commands for.
var gateKinds = []string{"test", "lint", "format-check", "typecheck", "build"}

// Note flags a piece of information surfaced to the operator in the
// generated .forge.yaml as a comment: either a field forge init could not
// confidently resolve, or a purely informational detection (e.g. presence
// of AGENTS.md) that has no corresponding Config field.
type Note struct {
	// Field is a dotted path into Config (e.g. "git.base",
	// "quality.gates.lint") identifying what the note is about, or a
	// synthetic name (e.g. "agent_instructions") for informational notes
	// with no Config field.
	Field   string
	Message string
}

// Result is the outcome of Detect: a fully-defaulted, valid config.Config
// plus any Notes about fields that could not be confidently resolved.
type Result struct {
	Config config.Config
	Notes  []Note
}

// Detect inspects the repository rooted at dir and returns a Result. The
// returned Config is always valid and loadable via config.Load (it starts
// from config.Default() and only overwrites fields Detect can confidently
// resolve); anything left at its default because it could not be resolved
// is recorded in Result.Notes so Render can mark it clearly.
func Detect(dir string) (Result, error) {
	cfg := config.Default()
	var notes []Note

	base, baseNote := detectBaseBranch(dir)
	cfg.Git.Base = base
	if baseNote != nil {
		notes = append(notes, *baseNote)
	}

	if trackerNote := detectTracker(dir, &cfg); trackerNote != nil {
		notes = append(notes, *trackerNote)
	}

	explicit := map[string]string{}
	convention := map[string]string{}
	for _, detect := range []languageDetector{
		detectGo,
		detectNode,
		detectPython,
		detectRust,
		detectMake,
		detectTaskfile,
		detectJustfile,
	} {
		expl, conv := detect(dir)
		for k, v := range expl {
			if _, ok := explicit[k]; !ok {
				explicit[k] = v
			}
		}
		for k, v := range conv {
			if _, ok := convention[k]; !ok {
				convention[k] = v
			}
		}
	}

	ci := detectCIHints(dir)

	var gates []config.QualityGate
	var unresolvedGates []string
	for _, kind := range gateKinds {
		if cmd, ok := explicit[kind]; ok {
			gates = append(gates, config.QualityGate{Name: kind, Command: cmd})
			continue
		}
		if cmd, ok := ci[kind]; ok {
			gates = append(gates, config.QualityGate{Name: kind, Command: cmd})
			continue
		}
		if cmd, ok := convention[kind]; ok {
			gates = append(gates, config.QualityGate{Name: kind, Command: cmd})
			continue
		}
		unresolvedGates = append(unresolvedGates, kind)
	}
	cfg.Quality.Gates = gates
	if len(unresolvedGates) > 0 {
		notes = append(notes, Note{
			Field:   "quality.gates",
			Message: fmt.Sprintf("could not determine a command for: %v; add manually", unresolvedGates),
		})
	}

	notes = append(notes, detectAgentDocs(dir)...)

	return Result{Config: cfg, Notes: notes}, nil
}
