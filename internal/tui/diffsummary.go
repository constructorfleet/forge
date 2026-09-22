package tui

// diffsummary.go reduces a stored unified diff to the file list the diff pane
// shows: one entry per changed file with its additions and deletions. The
// diff body itself still defers to $PAGER.

import "strings"

// DiffFile is one changed file in a Review diff.
type DiffFile struct {
	Path      string
	Additions int
	Deletions int
}

// DiffSummary is the diff pane's view data: the changed files in diff order
// and the totals across them.
type DiffSummary struct {
	Files     []DiffFile
	Additions int
	Deletions int
}

// SummarizeDiff parses a unified diff into per-file addition and deletion
// counts. A file opens at its "diff --git" line, or at its "+++ b/" header
// when the diff carries no git header. The +++ and --- headers never count
// as changed lines. An empty diff yields no file.
func SummarizeDiff(diff string) DiffSummary {
	var sum DiffSummary
	var cur *DiffFile
	open := func(path string) {
		sum.Files = append(sum.Files, DiffFile{Path: path})
		cur = &sum.Files[len(sum.Files)-1]
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			open(gitDiffPath(line))
		case strings.HasPrefix(line, "+++ "):
			path := headerPath(line[4:])
			switch {
			case cur == nil:
				open(path)
			case cur.Path == "" && path != "":
				cur.Path = path
			}
		case strings.HasPrefix(line, "--- "):
			// The old-file header names no change.
		case strings.HasPrefix(line, "+"):
			if cur == nil {
				open("")
			}
			cur.Additions++
			sum.Additions++
		case strings.HasPrefix(line, "-"):
			if cur == nil {
				open("")
			}
			cur.Deletions++
			sum.Deletions++
		}
	}
	return sum
}

// gitDiffPath reads the new-side path from a "diff --git a/x b/x" line.
func gitDiffPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimPrefix(fields[len(fields)-1], "b/")
}

// headerPath reads the path from a +++ header operand, dropping the b/ prefix
// and any trailing tab-separated timestamp. /dev/null names no path.
func headerPath(operand string) string {
	if i := strings.IndexByte(operand, '\t'); i >= 0 {
		operand = operand[:i]
	}
	if operand == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(operand, "b/")
}
