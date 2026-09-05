package tui

// editor.go defers the answer control's composition to $EDITOR: the same
// suspend-and-return mechanic diff.go's openArtifactInProcess uses for
// $PAGER, except the editor's whole point is to be written to, so the
// artifact is read back once the process exits and the file is stable.

import (
	"os"

	tea "charm.land/bubbletea/v2"
)

// defaultEditor is the editor used when $EDITOR is unset.
var defaultEditor = []string{"vi"}

// EditorCommand resolves $EDITOR into the command and arguments that open
// path. env supplies the environment lookup, so the resolution is testable.
// An editor with its own arguments keeps them, and an unset or blank
// $EDITOR falls back to the default editor.
func EditorCommand(env func(string) string, path string) []string {
	return resolveCommand(env, "EDITOR", defaultEditor, path)
}

// openArtifactInEditor writes text under dir and opens it in $EDITOR,
// delivering onClose's message when the editor exits. Unlike
// openArtifactInPager, the artifact is read back after the editor exits (the
// operator's edits are the whole point), and only then removed.
func openArtifactInEditor(dir, text string, onClose func(text string, err error) tea.Msg) tea.Cmd {
	return openArtifactInProcess(dir, text, func(path string) []string { return EditorCommand(os.Getenv, path) }, true, onClose)
}
