package initdiscovery

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ciKeywords maps gate kinds to substrings looked for in CI workflow "run:"
// command lines. Checked in the order listed here so a line matching an
// earlier, more specific keyword (e.g. "typecheck" before the more generic
// "build") isn't mis-classified.
var ciKeywordOrder = []string{"lint", "typecheck", "format-check", "test", "build"}

var ciKeywords = map[string][]string{
	"lint":         {"lint", "clippy", "ruff", "flake8", "eslint"},
	"typecheck":    {"typecheck", "type-check", "tsc", "mypy", "go vet", "cargo check"},
	"format-check": {"fmt --check", "format --check", "gofmt -l", "black --check", "prettier --check"},
	"test":         {"test"},
	"build":        {"build"},
}

// runLineRE matches a workflow step's "run:" key, whether written as its
// own line ("run: cmd") or as the value of a YAML sequence item ("- run:
// cmd").
var runLineRE = regexp.MustCompile(`^\s*-?\s*run:\s*(.*)$`)

// detectCIHints scans .github/workflows/*.{yml,yaml} for "run:" command
// lines (both single-line "run: cmd" and the first line of a "run: |"
// block) and classifies each by keyword into a gate kind. Only used to
// fill gate kinds no explicit config format resolved; see Detect.
func detectCIHints(dir string) map[string]string {
	hints := map[string]string{}

	var files []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", pattern))
		files = append(files, matches...)
	}

	for _, path := range files {
		content, ok := readFile(path)
		if !ok {
			continue
		}
		lines := strings.Split(content, "\n")
		for i := 0; i < len(lines); i++ {
			m := runLineRE.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			cmdLine := strings.TrimSpace(m[1])
			if cmdLine == "|" || cmdLine == ">" || cmdLine == "" {
				// Block scalar: the actual command is the next non-blank
				// line.
				if i+1 < len(lines) {
					cmdLine = strings.TrimSpace(lines[i+1])
				}
			}
			if cmdLine == "" {
				continue
			}
			classifyRunLine(cmdLine, hints)
		}
	}
	return hints
}

func classifyRunLine(cmdLine string, hints map[string]string) {
	lower := strings.ToLower(cmdLine)
	for _, kind := range ciKeywordOrder {
		if _, already := hints[kind]; already {
			continue
		}
		for _, kw := range ciKeywords[kind] {
			if strings.Contains(lower, kw) {
				hints[kind] = cmdLine
				break
			}
		}
	}
}
