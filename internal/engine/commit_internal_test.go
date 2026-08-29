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
// prefixing prTitle applies, and that a Title already carrying its own
// prefix (e.g. authored by a human/Agent that already follows the
// convention) is not double-prefixed.
func TestPrTitle_PrefixesWithInferredType(t *testing.T) {
	got := prTitle(domain.Issue{ID: "1", Title: "Add widget support"})
	if want := "feat: Add widget support"; got != want {
		t.Errorf("prTitle = %q, want %q", got, want)
	}

	got = prTitle(domain.Issue{ID: "2", Title: "fix(engine): nil panic on empty diff"})
	if want := "fix(engine): nil panic on empty diff"; got != want {
		t.Errorf("prTitle = %q, want %q (should not double-prefix)", got, want)
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
