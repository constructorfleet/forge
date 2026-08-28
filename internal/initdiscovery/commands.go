package initdiscovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// languageDetector inspects dir for one toolchain's known config formats
// and returns commands it found, split into two priority tiers: explicit
// (read directly from a config format — package.json scripts, presence of
// go.mod, a Makefile target, etc.) and convention (a well-known default
// tied to a detected toolchain, used only when nothing more specific is
// found). Both maps are keyed by gate kind ("test", "lint",
// "format-check", "typecheck", "build"); a kind absent from both means this
// detector found nothing for it.
type languageDetector func(dir string) (explicit, convention map[string]string)

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// detectGo detects a Go module via go.mod. Build, test, and vet (mapped to
// the "typecheck" gate kind, since Go has no separate typecheck step — the
// compiler and `go vet` cover it) are explicit: go.mod alone is sufficient
// signal for the standard toolchain commands. gofmt is the one true Go
// formatter, so format-check is explicit too. Lint is only set when a
// golangci-lint config is present, since no lint tool ships with the
// toolchain.
func detectGo(dir string) (explicit, convention map[string]string) {
	explicit = map[string]string{}
	if !fileExists(filepath.Join(dir, "go.mod")) {
		return explicit, nil
	}
	explicit["build"] = "go build ./..."
	explicit["test"] = "go test ./..."
	explicit["typecheck"] = "go vet ./..."
	explicit["format-check"] = "gofmt -l ."
	for _, name := range []string{".golangci.yml", ".golangci.yaml"} {
		if fileExists(filepath.Join(dir, name)) {
			explicit["lint"] = "golangci-lint run"
			break
		}
	}
	return explicit, nil
}

// packageJSON is the subset of package.json fields detectNode reads.
type packageJSON struct {
	Scripts map[string]string `json:"scripts"`
}

// detectNode detects a Node project via package.json, picks the package
// manager from whichever lockfile is present (pnpm > yarn > npm, npm being
// the fallback), and maps gate kinds to package.json scripts by
// conventional script name. A script that exists in package.json is an
// explicit signal (the maintainers defined it); there is no npm-specific
// convention tier since script names vary too much to guess safely.
func detectNode(dir string) (explicit, convention map[string]string) {
	explicit = map[string]string{}
	raw, ok := readFile(filepath.Join(dir, "package.json"))
	if !ok {
		return explicit, nil
	}

	var pkg packageJSON
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		return explicit, nil
	}

	pm := "npm"
	switch {
	case fileExists(filepath.Join(dir, "pnpm-lock.yaml")):
		pm = "pnpm"
	case fileExists(filepath.Join(dir, "yarn.lock")):
		pm = "yarn"
	case fileExists(filepath.Join(dir, "package-lock.json")):
		pm = "npm"
	}

	scriptNames := map[string][]string{
		"test":         {"test"},
		"lint":         {"lint"},
		"format-check": {"format:check", "format-check", "fmt:check"},
		"typecheck":    {"typecheck", "type-check"},
		"build":        {"build"},
	}
	for kind, candidates := range scriptNames {
		for _, name := range candidates {
			if _, ok := pkg.Scripts[name]; !ok {
				continue
			}
			if name == "test" {
				explicit[kind] = pm + " test"
			} else {
				explicit[kind] = pm + " run " + name
			}
			break
		}
	}
	return explicit, nil
}

// pyprojectSections is a rough (non-TOML-parsing) detector for the
// [tool.X] section headers forge cares about; a full TOML parser is not
// worth a new dependency for presence checks.
func pyprojectSections(content string) map[string]bool {
	sections := map[string]bool{}
	for _, section := range []string{"[tool.pytest", "[tool.ruff", "[tool.black", "[tool.mypy", "[build-system"} {
		sections[section] = strings.Contains(content, section)
	}
	return sections
}

// detectPython detects a Python project via pyproject.toml. Commands tied
// to a detected [tool.X] section are explicit; pytest is otherwise assumed
// as a convention default whenever pyproject.toml exists at all, since it
// is the de facto standard Python test runner.
func detectPython(dir string) (explicit, convention map[string]string) {
	explicit = map[string]string{}
	convention = map[string]string{}
	content, ok := readFile(filepath.Join(dir, "pyproject.toml"))
	if !ok {
		return explicit, convention
	}

	sections := pyprojectSections(content)
	if sections["[tool.pytest"] {
		explicit["test"] = "pytest"
	} else {
		convention["test"] = "pytest"
	}
	if sections["[tool.ruff"] {
		explicit["lint"] = "ruff check ."
	}
	if sections["[tool.black"] {
		explicit["format-check"] = "black --check ."
	}
	if sections["[tool.mypy"] {
		explicit["typecheck"] = "mypy ."
	}
	if sections["[build-system"] {
		explicit["build"] = "python -m build"
	}
	return explicit, convention
}

// detectRust detects a Rust crate via Cargo.toml. Build, test, format
// (rustfmt), and check (mapped to "typecheck") ship with the standard
// toolchain and are explicit. Clippy is a convention default: extremely
// common but not guaranteed installed, so it is lower priority than an
// explicit or CI-derived command.
func detectRust(dir string) (explicit, convention map[string]string) {
	explicit = map[string]string{}
	convention = map[string]string{}
	if !fileExists(filepath.Join(dir, "Cargo.toml")) {
		return explicit, convention
	}
	explicit["build"] = "cargo build"
	explicit["test"] = "cargo test"
	explicit["format-check"] = "cargo fmt -- --check"
	explicit["typecheck"] = "cargo check"
	convention["lint"] = "cargo clippy -- -D warnings"
	return explicit, convention
}

// gateAliases maps each gate kind to the target/task/recipe names in
// Makefile/Taskfile/justfile that conventionally correspond to it.
var gateAliases = map[string][]string{
	"test":         {"test"},
	"lint":         {"lint"},
	"format-check": {"fmt-check", "format-check", "fmt", "format"},
	"typecheck":    {"typecheck", "type-check", "vet", "check"},
	"build":        {"build"},
}

func matchAlias(name string) (kind string, ok bool) {
	for kind, aliases := range gateAliases {
		for _, alias := range aliases {
			if alias == name {
				return kind, true
			}
		}
	}
	return "", false
}

// makeTargetRE matches a Makefile target header ("name:" or "name: deps",
// at the start of a line) while excluding "name := value" / "name ::=
// value" variable assignments, which are followed by "=" rather than
// end-of-line or a dependency list.
var makeTargetRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+):($|[^=])`)

// detectMake reads Makefile target names (lines of the form "name:" at the
// start of a line, ignoring "name := value" variable assignments) and
// treats a target matching a known gate alias as an explicit command.
func detectMake(dir string) (explicit, convention map[string]string) {
	explicit = map[string]string{}
	content, ok := readFile(filepath.Join(dir, "Makefile"))
	if !ok {
		return explicit, nil
	}
	for _, line := range strings.Split(content, "\n") {
		m := makeTargetRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if kind, ok := matchAlias(m[1]); ok {
			if _, exists := explicit[kind]; !exists {
				explicit[kind] = "make " + m[1]
			}
		}
	}
	return explicit, nil
}

// detectTaskfile reads Taskfile.yml/Taskfile.yaml's top-level "tasks" map
// and treats a task matching a known gate alias as an explicit command.
func detectTaskfile(dir string) (explicit, convention map[string]string) {
	explicit = map[string]string{}
	var content string
	var ok bool
	for _, name := range []string{"Taskfile.yml", "Taskfile.yaml"} {
		if content, ok = readFile(filepath.Join(dir, name)); ok {
			break
		}
	}
	if !ok {
		return explicit, nil
	}

	var doc struct {
		Tasks map[string]any `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return explicit, nil
	}
	for name := range doc.Tasks {
		if kind, ok := matchAlias(name); ok {
			if _, exists := explicit[kind]; !exists {
				explicit[kind] = "task " + name
			}
		}
	}
	return explicit, nil
}

var justRecipeRE = regexp.MustCompile(`^([A-Za-z0-9_-]+)[^:=\n]*:\s*$`)

// detectJustfile reads justfile recipe headers (lines of the form "name:"
// or "name arg1 arg2:", ending the line — distinguishing them from "name
// := value" variable assignments) and treats a recipe matching a known
// gate alias as an explicit command.
func detectJustfile(dir string) (explicit, convention map[string]string) {
	explicit = map[string]string{}
	var content string
	var ok bool
	for _, name := range []string{"justfile", ".justfile"} {
		if content, ok = readFile(filepath.Join(dir, name)); ok {
			break
		}
	}
	if !ok {
		return explicit, nil
	}
	for _, line := range strings.Split(content, "\n") {
		m := justRecipeRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if kind, ok := matchAlias(m[1]); ok {
			if _, exists := explicit[kind]; !exists {
				explicit[kind] = "just " + m[1]
			}
		}
	}
	return explicit, nil
}

// detectAgentDocs checks for the presence of AGENTS.md and CLAUDE.md.
// Neither has a corresponding Config field (agent instructions are read at
// runtime, not recorded in .forge.yaml), so this only produces
// informational Notes rendered as header comments.
func detectAgentDocs(dir string) []Note {
	var found []string
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if fileExists(filepath.Join(dir, name)) {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		return []Note{{
			Field:   "agent_instructions",
			Message: "no AGENTS.md or CLAUDE.md found; consider adding agent instructions",
		}}
	}
	return []Note{{
		Field:   "agent_instructions",
		Message: "found agent instructions: " + strings.Join(found, ", "),
	}}
}
