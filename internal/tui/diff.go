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

// resolveCommand resolves the environment variable envVar into the command
// and arguments that open path. env supplies the environment lookup, so the
// resolution is testable. A command with its own arguments keeps them, and
// an unset or blank environment variable falls back to fallback. Shared by
// PagerCommand and EditorCommand: the only difference between $PAGER and
// $EDITOR resolution is which variable and which default command.
func resolveCommand(env func(string) string, envVar string, fallback []string, path string) []string {
	words := strings.Fields(env(envVar))
	if len(words) == 0 {
		words = fallback
	}
	out := make([]string, 0, len(words)+1)
	out = append(out, words...)
	return append(out, path)
}

// PagerCommand resolves $PAGER into the command and arguments that open path.
// env supplies the environment lookup, so the resolution is testable. A pager
// with its own arguments keeps them, and an unset or blank $PAGER falls back to
// the default pager.
func PagerCommand(env func(string) string, path string) []string {
	return resolveCommand(env, "PAGER", defaultPager, path)
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

// openArtifactInProcess writes text under dir and opens it in the command
// resolveCmd names, delivering onClose's message when that process exits.
// tea.ExecProcess suspends the TUI while the process owns the terminal, and
// resumes on exit. The artifact file is removed after the process exits, but
// only after it is read back into onClose's text argument when readBack is
// set: an editor's whole point is to be written to, so its result is the
// operator's edits; a pager's is not, so it passes readBack false and an
// onClose that ignores the text. Shared by every control that defers a
// stored artifact to an external process ($PAGER for the diff and approve
// keys, $EDITOR for the answer key), so all three read their command,
// exec it, and clean up their temp file the same way.
func openArtifactInProcess(dir, text string, resolveCmd func(path string) []string, readBack bool, onClose func(text string, err error) tea.Msg) tea.Cmd {
	path, err := WriteDiffArtifact(dir, text)
	if err != nil {
		return func() tea.Msg { return onClose("", err) }
	}
	argv := resolveCmd(path)
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the command is the operator's own $PAGER/$EDITOR choice.
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		defer func() { _ = os.Remove(path) }()
		if runErr != nil {
			return onClose("", runErr)
		}
		if !readBack {
			return onClose("", nil)
		}
		content, readErr := os.ReadFile(path) //nolint:gosec // path is our own temp file, not user input.
		if readErr != nil {
			return onClose("", readErr)
		}
		return onClose(string(content), nil)
	})
}

// openArtifactInPager writes text under dir and opens it in $PAGER, delivering
// onClose's message when the pager exits. Shared by every control that defers
// a stored artifact to $PAGER (the diff key and the approve key), so both
// read $PAGER and clean up their temp file the same way.
func openArtifactInPager(dir, text string, onClose func(err error) tea.Msg) tea.Cmd {
	return openArtifactInProcess(dir, text, func(path string) []string { return PagerCommand(os.Getenv, path) }, false,
		func(_ string, err error) tea.Msg { return onClose(err) })
}

// OpenDiffInPager writes diff under dir and opens it in $PAGER.
// tea.ExecProcess suspends the TUI while the pager owns the terminal, and
// resumes on exit. The artifact file is removed after the pager exits.
func OpenDiffInPager(dir, diff string) tea.Cmd {
	return openArtifactInPager(dir, diff, func(err error) tea.Msg { return diffClosedMsg{err: err} })
}
