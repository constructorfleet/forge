package initdiscovery

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ciKeywords maps gate kinds to keywords looked for in CI workflow "run:"
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

// ciKeywordPatterns compiles each ciKeywords entry into a \b-bounded
// regexp so a keyword only matches as a whole word/token, not as a
// substring of an unrelated one — e.g. "test" must not match inside
// "latest", and "build" must not match inside "rebuild".
var ciKeywordPatterns = buildCIKeywordPatterns()

func buildCIKeywordPatterns() map[string][]*regexp.Regexp {
	patterns := make(map[string][]*regexp.Regexp, len(ciKeywords))
	for kind, keywords := range ciKeywords {
		for _, kw := range keywords {
			patterns[kind] = append(patterns[kind], regexp.MustCompile(`\b`+regexp.QuoteMeta(kw)+`\b`))
		}
	}
	return patterns
}

// runLineRE matches a workflow step's "run:" key, whether written as its
// own line ("run: cmd") or as the value of a YAML sequence item ("- run:
// cmd").
var runLineRE = regexp.MustCompile(`^\s*-?\s*run:\s*(.*)$`)

// detectCIHints scans .github/workflows/*.{yml,yaml} for "run:" command
// lines — both single-line ("run: cmd") and block scalar ("run: |" /
// "run: >", followed by one or more indented command lines) — and
// classifies each physical command line by keyword into a gate kind. Only
// used to fill gate kinds no explicit config format resolved; see Detect.
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
			val := strings.TrimSpace(m[1])
			if val == "" || strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">") {
				// Block scalar: every indented line following "run:"
				// belongs to the command body, until indentation returns
				// to the "run:" line's level or less.
				runIndent := leadingSpaces(lines[i])
				j := i + 1
				for j < len(lines) {
					if strings.TrimSpace(lines[j]) == "" {
						j++
						continue
					}
					if leadingSpaces(lines[j]) <= runIndent {
						break
					}
					classifyRunLine(strings.TrimSpace(lines[j]), hints)
					j++
				}
				i = j - 1
				continue
			}
			classifyRunLine(val, hints)
		}
	}
	return hints
}

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

// classifyRunLine assigns cmdLine to at most one gate kind: the
// first-checked kind (in ciKeywordOrder) not already resolved whose
// keyword matches. A single command line never populates more than one
// gate kind.
func classifyRunLine(cmdLine string, hints map[string]string) {
	if cmdLine == "" {
		return
	}
	lower := strings.ToLower(cmdLine)
	for _, kind := range ciKeywordOrder {
		if _, already := hints[kind]; already {
			continue
		}
		for _, re := range ciKeywordPatterns[kind] {
			if re.MatchString(lower) {
				hints[kind] = cmdLine
				return
			}
		}
	}
}
