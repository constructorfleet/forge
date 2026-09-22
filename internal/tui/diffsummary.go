package tui

// diffsummary.go reduces a stored unified diff to the file list and hunk lines
// the bounded in-frame diff pane shows.

import "strings"

// BoundDiff limits the body shown in the TUI. It keeps complete lines until
// either positive limit is reached. A non-positive limit disables that bound.
// It omits a line that exceeds the byte limit.
func BoundDiff(diff string, maxBytes, maxLines int) (string, bool) {
	if maxBytes <= 0 && maxLines <= 0 {
		return diff, false
	}
	var b strings.Builder
	truncated := false
	lines := 0
	for start := 0; start < len(diff); {
		end := strings.IndexByte(diff[start:], '\n')
		if end >= 0 {
			end += start + 1
		} else {
			end = len(diff)
		}
		line := diff[start:end]
		if maxLines > 0 && lines >= maxLines {
			truncated = true
			break
		}
		if maxBytes > 0 && b.Len()+len(line) > maxBytes {
			truncated = true
			break
		}
		b.WriteString(line)
		lines++
		start = end
	}
	if b.Len() < len(diff) {
		truncated = true
	}
	return b.String(), truncated
}

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
	Lines     []DiffLine
	Truncated bool
}

// DiffLine is one display line from a unified diff.
type DiffLine struct {
	Kind byte
	Text string
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
		if line != "" {
			kind := byte(' ')
			if line[0] == '+' || line[0] == '-' || line[0] == '@' {
				kind = line[0]
			}
			sum.Lines = append(sum.Lines, DiffLine{Kind: kind, Text: line})
		}
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
