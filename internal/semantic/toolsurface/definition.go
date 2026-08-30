package toolsurface

import (
	"context"
	"strings"
)

// snippetContextLines is how many lines of source find_definition inlines
// on each side of the definition line, giving the ~5-line snippet ADR-0014
// calls for (2 before + the line itself + 2 after).
const snippetContextLines = 2

// FindDefinition resolves the symbol at (file, line[, symbol]) and returns
// its definition as a single Source Location with an inlined ~5-line
// snippet — the one asymmetric case among the location tools, since a
// definition is where an agent reads code next (see ADR-0014).
func (t *Toolset) FindDefinition(ctx context.Context, file string, line int, symbol string) (SourceLocation, error) {
	pos, err := resolvePosition(ctx, t.driver, file, line, symbol)
	if err != nil {
		return SourceLocation{}, err
	}

	locs, err := t.driver.FindDefinition(ctx, file, pos)
	if err != nil {
		return SourceLocation{}, err
	}
	if len(locs) == 0 {
		return SourceLocation{}, nil
	}

	loc := sourceLocationFromGoplsLocation(locs[0])
	loc.Snippet = t.snippet(loc.File, loc.Line)
	return loc, nil
}

// snippet reads file and returns the ~5 lines of source centered on line
// (1-based), clamped to the file's bounds. Read failures degrade to an
// empty snippet rather than failing the whole tool call — the location is
// still useful without it.
func (t *Toolset) snippet(file string, line int) string {
	content, err := t.readFile(file)
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if line < 1 || line > len(lines) {
		return ""
	}

	start := line - 1 - snippetContextLines
	if start < 0 {
		start = 0
	}
	end := line - 1 + snippetContextLines
	if end > len(lines)-1 {
		end = len(lines) - 1
	}

	return strings.Join(lines[start:end+1], "\n")
}
