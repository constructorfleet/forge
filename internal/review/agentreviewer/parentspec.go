package agentreviewer

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
)

// parentSpecMaxRunes bounds how much of a fetched parent Issue's body is
// injected into a review prompt (issue #319's acceptance criterion that
// parent content cannot blow the prompt budget). It is generous enough to
// carry a spec's intent while staying well short of typical axis-rubric
// prompt budgets.
const parentSpecMaxRunes = 4000

var (
	// reSpecRef matches a "Spec:" pointer to another Issue, with or without
	// an owner/repo prefix: "Spec: #284" or "Spec: constructorfleet/forge#284".
	reSpecRef = regexp.MustCompile(`(?i)\bSpec:\s*(?:[\w.-]+/[\w.-]+)?#(\d+)`)

	// reParentHeading matches a "## Parent" Markdown heading.
	reParentHeading = regexp.MustCompile(`(?m)^##\s*Parent\b`)

	// reAnyHeading matches any Markdown heading, used to bound the "##
	// Parent" section when it has no inline Spec: pointer.
	reAnyHeading = regexp.MustCompile(`(?m)^#{1,6}\s`)

	// reIssueRef matches a bare "#NNN" issue reference.
	reIssueRef = regexp.MustCompile(`#(\d+)`)
)

// detectParentRef scans issue for a reference to a parent/spec Issue (issue
// #319): a "Spec: #NNN" or "Spec: owner/repo#NNN" pointer anywhere in the
// body, a bare "#NNN" reference inside a "## Parent" section, or — failing
// both — the first entry in the Issue's Dependencies (the "## Dependencies"
// block is the repo's canonical Dependency Source per CONTEXT.md, and
// domain.Issue.Dependencies already holds it parsed). It returns the
// referenced Issue's ID and whether a reference was found.
func detectParentRef(issue domain.Issue) (string, bool) {
	if m := reSpecRef.FindStringSubmatch(issue.Body); m != nil {
		return m[1], true
	}

	if loc := reParentHeading.FindStringIndex(issue.Body); loc != nil {
		section := issue.Body[loc[1]:]
		if next := reAnyHeading.FindStringIndex(section); next != nil {
			section = section[:next[0]]
		}
		if m := reIssueRef.FindStringSubmatch(section); m != nil {
			return m[1], true
		}
	}

	if len(issue.Dependencies) > 0 {
		return issue.Dependencies[0].DependsOnID, true
	}

	return "", false
}

// resolveParentSpec resolves req's parent-spec injection for buildPolicyNotes:
// it detects a parent/spec reference in req.Issue's body, fetches that
// Issue through req.ParentFetcher, and formats it, size-bounded, for
// injection under buildPolicyNotes' "## Parent spec" heading. It returns ""
// whenever there is nothing to inject — no fetcher wired, no reference
// detected, or the fetch failed — so a Review call degrades to today's
// behavior (no parent context) rather than blocking on this optional
// enrichment.
func resolveParentSpec(ctx context.Context, req review.Request) string {
	if req.ParentFetcher == nil {
		return ""
	}

	id, ok := detectParentRef(req.Issue)
	if !ok || id == "" || id == req.Issue.ID {
		return ""
	}

	parent, err := req.ParentFetcher(ctx, id)
	if err != nil {
		return ""
	}

	return formatParentSpec(parent)
}

// formatParentSpec renders parent's title and body as the "## Parent spec"
// section content, truncating the body to parentSpecMaxRunes.
func formatParentSpec(parent domain.Issue) string {
	body := strings.TrimSpace(parent.Body)
	if runes := []rune(body); len(runes) > parentSpecMaxRunes {
		body = string(runes[:parentSpecMaxRunes]) + "\n\n[truncated]"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n\n", parent.Title)
	b.WriteString(body)
	return b.String()
}
