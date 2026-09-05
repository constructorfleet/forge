package tui

// diff.go defers the one heavy artifact, a Review diff, out of the frame to
// $PAGER. Forge stores exactly one copy of a diff, review_runs.diff (migration
// 0004), and the TUI reads it from there: the live diff producer is a git
// operation, which the store-only read path forbids. The frame holds no diff
// text, so it needs no lexing, no pagination, and no navigation mode.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ErrNoDiff reports that the store holds no diff for the Issue: no Review has
// run yet, or the recorded run stored an empty diff.
var ErrNoDiff = errors.New("tui: no stored diff")

// defaultPager is the pager used when $PAGER is unset. -R keeps a coloured
// diff readable rather than showing escape sequences.
var defaultPager = []string{"less", "-R"}

// LatestDiff returns the current Review's stored diff for one Issue. It returns
// ErrNoDiff when the store holds none. The store reads the one diff column, so
// the pager path loads no finding and no axis envelope.
func LatestDiff(ctx context.Context, store RosterStore, executionID, issueID string) (string, error) {
	diff, err := store.LatestReviewDiff(ctx, executionID, issueID)
	if err != nil {
		return "", fmt.Errorf("tui: read review diff for issue %s: %w", issueID, err)
	}
	if diff == "" {
		return "", ErrNoDiff
	}
	return diff, nil
}

// PagerCommand resolves $PAGER into the command and arguments that open path.
// env supplies the environment lookup, so the resolution is testable. A pager
// with its own arguments keeps them, and an unset or blank $PAGER falls back to
// the default pager.
func PagerCommand(env func(string) string, path string) []string {
	words := strings.Fields(env("PAGER"))
	if len(words) == 0 {
		words = defaultPager
	}
	out := make([]string, 0, len(words)+1)
	out = append(out, words...)
	return append(out, path)
}

// WriteDiffArtifact writes diff to a new file under dir and returns its path.
// The pager receives a file argument, not the diff on stdin, so it keeps the
// terminal for its own key input.
func WriteDiffArtifact(dir, diff string) (string, error) {
	f, err := os.CreateTemp(dir, "forge-*.diff")
	if err != nil {
		return "", fmt.Errorf("tui: write diff artifact: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(diff); err != nil {
		return "", fmt.Errorf("tui: write diff artifact: %w", err)
	}
	return filepath.Clean(f.Name()), nil
}

// diffClosedMsg reports that the pager exited. A failure rides the frame's
// notice: an observer never aborts on a failed artifact view.
type diffClosedMsg struct{ err error }

// openArtifactInPager writes text under dir and opens it in $PAGER, delivering
// onClose's message when the pager exits. tea.ExecProcess suspends the TUI
// while the pager owns the terminal, and resumes on exit. The artifact file is
// removed after the pager exits. Shared by every control that defers a stored
// artifact to $PAGER (the diff key and the approve key), so both read $PAGER
// and clean up their temp file the same way.
func openArtifactInPager(dir, text string, onClose func(err error) tea.Msg) tea.Cmd {
	path, err := WriteDiffArtifact(dir, text)
	if err != nil {
		return func() tea.Msg { return onClose(err) }
	}
	argv := PagerCommand(os.Getenv, path)
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // $PAGER is the operator's own choice.
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		_ = os.Remove(path)
		return onClose(runErr)
	})
}

// OpenDiffInPager writes diff under dir and opens it in $PAGER.
// tea.ExecProcess suspends the TUI while the pager owns the terminal, and
// resumes on exit. The artifact file is removed after the pager exits.
func OpenDiffInPager(dir, diff string) tea.Cmd {
	return openArtifactInPager(dir, diff, func(err error) tea.Msg { return diffClosedMsg{err: err} })
}
