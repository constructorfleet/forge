// Package repocontext compiles the Repository Context (see CONTEXT.md
// "Repository Context") shared across all Workers in an Execution. It is
// compiled exactly once, from configuration and a small set of repository
// files, and handed to Workers as an immutable value — Workers never
// independently rediscover quality gate commands or project structure.
package repocontext

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
)

// instructionFiles lists the agent-instruction files read and merged, in
// order. AGENTS.md is the general entry point; CLAUDE.md carries
// Claude-Code-specific guidance layered on top of it, so it is merged
// second.
var instructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

// manifest associates a project manifest file with the language and package
// manager it indicates. Detection here is informational only: it never
// determines Quality Gate commands, which come exclusively from cfg.Quality
// (see CONTEXT.md "Quality Gate" — gates are configured, not discovered).
type manifest struct {
	file           string
	language       string
	packageManager string
}

var manifests = []manifest{
	{file: "go.mod", language: "Go", packageManager: "Go Modules"},
	{file: "Cargo.toml", language: "Rust", packageManager: "Cargo"},
	{file: "Gemfile", language: "Ruby", packageManager: "Bundler"},
	{file: "pom.xml", language: "Java", packageManager: "Maven"},
	{file: "build.gradle", language: "Java", packageManager: "Gradle"},
	{file: "build.gradle.kts", language: "Kotlin", packageManager: "Gradle"},
	{file: "requirements.txt", language: "Python", packageManager: "pip"},
	{file: "pyproject.toml", language: "Python", packageManager: "Poetry"},
}

// jsPackageManagers maps a JavaScript/TypeScript lock file to the package
// manager it indicates, checked in order. package.json alone (no lock file)
// falls back to npm, the default that ships with Node.
var jsPackageManagers = []manifest{
	{file: "pnpm-lock.yaml", packageManager: "pnpm"},
	{file: "yarn.lock", packageManager: "Yarn"},
	{file: "package-lock.json", packageManager: "npm"},
}

// Compile builds the immutable Repository Context for one Execution from
// cfg, the repository at repoRoot, and the Execution's baseRevision.
//
// Compile is the single entrypoint for producing a Repository Context: it
// returns a value with no exported mutator methods, and every reference-
// typed field (QualityGates, Languages, PackageManagers) is a fresh slice
// independent of cfg's backing arrays and of any other Compile call, so
// mutating one result cannot affect cfg or a subsequently compiled result.
func Compile(cfg config.Config, repoRoot, baseRevision string) (agent.RepositoryContext, error) {
	languages, packageManagers, err := detectManifests(repoRoot)
	if err != nil {
		return agent.RepositoryContext{}, fmt.Errorf("repocontext: detect manifests: %w", err)
	}

	structure, err := describeStructure(repoRoot)
	if err != nil {
		return agent.RepositoryContext{}, fmt.Errorf("repocontext: describe structure: %w", err)
	}

	instructions, err := normalizeInstructions(repoRoot)
	if err != nil {
		return agent.RepositoryContext{}, fmt.Errorf("repocontext: read agent instructions: %w", err)
	}

	return agent.RepositoryContext{
		BaseRevision:      baseRevision,
		ProjectStructure:  structure,
		AgentInstructions: instructions,
		QualityGates:      gateCommands(cfg),
		Languages:         languages,
		PackageManagers:   packageManagers,
	}, nil
}

// gateCommands returns the Quality Gate commands from cfg, in configured
// order, as a fresh slice. This is the only source of QualityGates — the
// compiler never scans the repository for build/test scripts.
func gateCommands(cfg config.Config) []string {
	gates := make([]string, len(cfg.Quality.Gates))
	for i, g := range cfg.Quality.Gates {
		gates[i] = g.Command
	}
	return gates
}

// normalizeInstructions reads and merges instructionFiles found at
// repoRoot, trimming each and joining present ones with a blank line. A
// genuinely absent file is skipped silently (fs.ErrNotExist); any other
// read error (permission denied, is-a-directory, I/O error) is propagated
// rather than silently dropping authoritative instructions. If none are
// present the result is the empty string.
func normalizeInstructions(repoRoot string) (string, error) {
	var parts []string
	for _, name := range instructionFiles {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n"), nil
}

// detectManifests inspects repoRoot for known project manifests and returns
// the sorted, deduplicated languages and package managers they indicate.
// This is purely informational context for the Agent; it has no bearing on
// which Quality Gates run.
func detectManifests(repoRoot string) (languages, packageManagers []string, err error) {
	var langs, pkgMgrs []string

	for _, m := range manifests {
		ok, err := fileExists(filepath.Join(repoRoot, m.file))
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		langs = append(langs, m.language)
		pkgMgrs = append(pkgMgrs, m.packageManager)
	}

	hasPackageJSON, err := fileExists(filepath.Join(repoRoot, "package.json"))
	if err != nil {
		return nil, nil, err
	}
	if hasPackageJSON {
		langs = append(langs, "JavaScript")
		pm := "npm"
		for _, jm := range jsPackageManagers {
			ok, err := fileExists(filepath.Join(repoRoot, jm.file))
			if err != nil {
				return nil, nil, err
			}
			if ok {
				pm = jm.packageManager
				break
			}
		}
		pkgMgrs = append(pkgMgrs, pm)
	}

	return dedupeSorted(langs), dedupeSorted(pkgMgrs), nil
}

// describeStructure returns a deterministic, one-line-per-entry listing of
// repoRoot's top-level contents (directories suffixed with "/"), sorted by
// name. Dotfiles/dot-directories (e.g. .git, .env) are VCS or local
// configuration noise, not project structure, and are excluded.
func describeStructure(repoRoot string) (string, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, "\n"), nil
}

// fileExists reports whether path exists and is a regular file (or at
// least stat-able); errors other than "not exist" are propagated.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// dedupeSorted returns the sorted, deduplicated contents of vs.
func dedupeSorted(vs []string) []string {
	if len(vs) == 0 {
		return nil
	}
	out := slices.Clone(vs)
	slices.Sort(out)
	out = slices.Compact(out)
	return out
}
