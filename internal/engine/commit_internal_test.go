package engine

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
)

// TestConventionalCommitType_InfersFromTitleAndBody covers ticket 78's
// "commit messages/PR titles must follow conventional commit format"
// acceptance criterion: the type prefix is inferred from keywords in the
// Issue's Title/Body, defaulting to "feat" when none match.
func TestConventionalCommitType_InfersFromTitleAndBody(t *testing.T) {
	cases := []struct {
		name  string
		issue domain.Issue
		want  string
	}{
		{"fix keyword in title", domain.Issue{Title: "Fix nil panic on empty diff"}, "fix"},
		{"bug keyword in body", domain.Issue{Title: "Widget breaks", Body: "This is a bug in the widget."}, "fix"},
		{"docs keyword", domain.Issue{Title: "Update documentation"}, "docs"},
		{"refactor keyword", domain.Issue{Title: "Refactor the publisher"}, "refactor"},
		{"test keyword", domain.Issue{Title: "Add test coverage"}, "test"},
		{"chore keyword", domain.Issue{Title: "Repo cleanup"}, "chore"},
		{"no keyword defaults to feat", domain.Issue{Title: "Add widget support"}, "feat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conventionalCommitType(tc.issue); got != tc.want {
				t.Errorf("conventionalCommitType(%+v) = %q, want %q", tc.issue, got, tc.want)
			}
		})
	}
}

// TestPrTitle_PrefixesWithInferredType covers the Conventional Commits
// prefixing prTitle applies: an Issue Title with no prefix gets the
// inferred type prepended; a Title already carrying a *valid* Conventional
// Commits type (including a scope and/or breaking-change marker) is left
// unchanged (not double-prefixed); and a Title carrying a prefix that looks
// conventional but isn't an allowed type (e.g. "review:", an area prefix
// this repo's Issue titles use) has that invalid prefix replaced with the
// inferred type rather than nested underneath it (issue 187).
func TestPrTitle_PrefixesWithInferredType(t *testing.T) {
	cases := []struct {
		name  string
		issue domain.Issue
		want  string
	}{
		{
			name:  "no prefix gets inferred type prepended",
			issue: domain.Issue{ID: "1", Title: "Add widget support"},
			want:  "feat: Add widget support",
		},
		{
			name:  "valid prefix left unchanged",
			issue: domain.Issue{ID: "2", Title: "fix: nil panic on empty diff"},
			want:  "fix: nil panic on empty diff",
		},
		{
			name:  "valid scoped prefix left unchanged",
			issue: domain.Issue{ID: "3", Title: "fix(engine): nil panic on empty diff"},
			want:  "fix(engine): nil panic on empty diff",
		},
		{
			name:  "valid breaking-change marker left unchanged",
			issue: domain.Issue{ID: "4", Title: "feat!: drop the legacy config format"},
			want:  "feat!: drop the legacy config format",
		},
		{
			name:  "invalid area prefix is replaced, not nested",
			issue: domain.Issue{ID: "5", Title: "review: persist per-axis assurances in the audit trail"},
			want:  "feat: persist per-axis assurances in the audit trail",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prTitle(tc.issue); got != tc.want {
				t.Errorf("prTitle(%+v) = %q, want %q", tc.issue, got, tc.want)
			}
		})
	}
}

// TestCommitMessage_DefaultTemplate_EndsWithIssueID covers ticket 78's
// "commit message bodies must include the issue id being addressed at the
// end" acceptance criterion, and that the header follows Conventional
// Commits format with a separate body.
func TestCommitMessage_DefaultTemplate_EndsWithIssueID(t *testing.T) {
	e := &Engine{}
	issue := domain.Issue{ID: "78", Title: "Commits and pull requests need more details"}
	msg := e.commitMessage(issue, "Added Conventional Commits formatting to commit/PR generation.")

	lines := strings.Split(msg, "\n")
	if !strings.HasPrefix(lines[0], "feat: ") {
		t.Errorf("commit message header = %q, want it to start with a Conventional Commits type", lines[0])
	}
	if !strings.HasSuffix(strings.TrimRight(msg, "\n"), "Refs #78") {
		t.Errorf("commit message = %q, want it to end with the issue id", msg)
	}
	if !strings.Contains(msg, "Added Conventional Commits formatting") {
		t.Errorf("commit message = %q, want it to contain a body describing the change", msg)
	}
}

// TestCommitMessage_HeaderConsistentWithPRTitle covers issue 187's
// requirement that the commit message header ({type}: {title}) stay
// consistent with prTitle: an Issue Title carrying an invalid area prefix
// (e.g. "review:") must not leave the commit header double-prefixed
// ("feat: review: …") or the invalid type kept as the header's type.
func TestCommitMessage_HeaderConsistentWithPRTitle(t *testing.T) {
	e := &Engine{}
	issue := domain.Issue{ID: "187", Title: "review: persist per-axis assurances in the audit trail"}
	msg := e.commitMessage(issue, "Stripped invalid area prefixes from generated titles.")

	header := strings.SplitN(msg, "\n", 2)[0]
	wantHeader := "feat: persist per-axis assurances in the audit trail"
	if header != wantHeader {
		t.Errorf("commit message header = %q, want %q", header, wantHeader)
	}
	if wantPR := prTitle(issue); header != wantPR {
		t.Errorf("commit message header = %q, want it to match prTitle(issue) = %q", header, wantPR)
	}
}

// TestWrapText_WrapsAt80Columns covers ticket 78's "commit messages should
// wrap at 80 characters" acceptance criterion.
func TestWrapText_WrapsAt80Columns(t *testing.T) {
	long := strings.Repeat("word ", 40)
	wrapped := wrapText(long, commitMessageWrapWidth)
	for _, line := range strings.Split(wrapped, "\n") {
		if len(line) > commitMessageWrapWidth {
			t.Errorf("wrapped line %q exceeds %d columns (%d)", line, commitMessageWrapWidth, len(line))
		}
	}
}

// TestPrBody_ContainsRequiredSections covers ticket 78's PR description
// acceptance criterion: Summary, Why, What Was Changed, How it Was Tested,
// and a Closes/Ref issue reference.
func TestPrBody_ContainsRequiredSections(t *testing.T) {
	body := prBody(domain.Issue{ID: "78", Title: "More detail in commits/PRs"}, "Implemented the new templates.")
	for _, want := range []string{"## Summary", "## Why", "## What Was Changed", "## How it Was Tested", "Closes #78"} {
		if !strings.Contains(body, want) {
			t.Errorf("prBody = %q, want it to contain %q", body, want)
		}
	}
}
