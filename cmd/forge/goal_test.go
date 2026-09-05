package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

// writeFakeEditor writes a shell script that appends a line to the Goal
// section's body and returns its path, suitable for $EDITOR/$VISUAL in
// tests exercising `forge goal init --edit`.
func writeFakeEditor(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake editor script requires a POSIX shell")
	}
	script := filepath.Join(dir, "fake-editor.sh")
	body := "#!/bin/sh\nprintf '\\n' >> \"$1\"\nprintf 'Edited in-editor.\\n' >> \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return script
}

func writeFakeGH(t *testing.T, dir string, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh script requires a POSIX shell")
	}
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return script
}

// TestRunGoalInit_CreatesValidSkeleton is the core acceptance criterion:
// `forge goal init foo` on a clean repo must produce a goal.md that is
// kind: goal, state: draft, has a non-empty revision, carries the four
// required sections, and is not Stale (revision matches a fresh
// recomputation of its own content).
func TestRunGoalInit_CreatesValidSkeleton(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runGoalInit([]string{"init", "foo"}); code != 0 {
		t.Fatalf("runGoalInit = %d, want 0", code)
	}

	path := filepath.Join(dir, ".forge", "features", "foo", "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.Kind != planning.KindGoal {
		t.Fatalf("Kind = %q, want %q", a.Kind, planning.KindGoal)
	}
	if a.State != "draft" {
		t.Fatalf("State = %q, want draft", a.State)
	}
	if a.Revision == "" {
		t.Fatal("Revision is empty, want non-empty")
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("artifact is Stale: ComputeRevision = %q, stamped Revision = %q", got, a.Revision)
	}
	if len(a.DerivedFrom) != 0 {
		t.Fatalf("DerivedFrom = %v, want empty (goal is a pipeline root)", a.DerivedFrom)
	}
	if a.ApprovedRevision != "" || a.ApprovedBy != "" || a.ApprovedAt != "" {
		t.Fatal("approval fields must be empty on a fresh skeleton")
	}

	wantHeadings := []string{"Goal", "Context", "Constraints", "Success Criteria"}
	if len(a.Sections) != len(wantHeadings) {
		t.Fatalf("got %d sections, want %d: %+v", len(a.Sections), len(wantHeadings), a.Sections)
	}
	for i, h := range wantHeadings {
		if a.Sections[i].Heading != h {
			t.Errorf("section %d heading = %q, want %q", i, a.Sections[i].Heading, h)
		}
		if a.Sections[i].Body == "" {
			t.Errorf("section %d (%s) has empty body, want placeholder prose", i, h)
		}
	}
	for _, h := range wantHeadings {
		if h == "Non-Goals" {
			t.Fatal("skeleton must not include a Non-Goals section")
		}
	}
}

// TestRunGoalInit_NoClobber confirms that re-running without --force exits
// non-zero and leaves the existing file byte-identical.
func TestRunGoalInit_NoClobber(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runGoalInit([]string{"init", "foo"}); code != 0 {
		t.Fatalf("first runGoalInit = %d, want 0", code)
	}

	path := filepath.Join(dir, ".forge", "features", "foo", "goal.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if code := runGoalInit([]string{"init", "foo"}); code == 0 {
		t.Fatal("second runGoalInit without --force = 0, want non-zero")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("goal.md was modified by a no-clobber run")
	}
}

// TestRunGoalInit_ForceOverwrites confirms --force overwrites an existing
// goal.md and re-stamps a fresh draft rather than preserving approval state.
func TestRunGoalInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	featureDir := filepath.Join(dir, ".forge", "features", "foo")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := &planning.Artifact{
		Kind:  planning.KindGoal,
		State: "approved",
		Sections: []planning.Section{
			{Heading: "Goal", Body: "hand-written goal"},
		},
	}
	existing.Revision = planning.ComputeRevision(existing)
	existing.ApprovedRevision = existing.Revision
	existing.ApprovedBy = "someone"
	existing.ApprovedAt = "2026-01-01T00:00:00Z"
	if err := os.WriteFile(filepath.Join(featureDir, "goal.md"), planning.Render(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runGoalInit([]string{"init", "foo", "--force"}); code != 0 {
		t.Fatalf("runGoalInit --force = %d, want 0", code)
	}

	data, err := os.ReadFile(filepath.Join(featureDir, "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.State != "draft" {
		t.Fatalf("State = %q, want draft after --force", a.State)
	}
	if a.ApprovedRevision != "" || a.ApprovedBy != "" || a.ApprovedAt != "" {
		t.Fatal("--force must not preserve approval fields")
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("re-stamped artifact is Stale: ComputeRevision = %q, Revision = %q", got, a.Revision)
	}
	if len(a.Sections) != 4 || a.Sections[0].Body == "hand-written goal" {
		t.Fatal("--force must overwrite with a fresh skeleton, not preserve old content")
	}
}

// TestRunGoalInit_RejectsUnsafeFeatureID confirms unsafe feature-ids are
// rejected before any filesystem write.
func TestRunGoalInit_RejectsUnsafeFeatureID(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runGoalInit([]string{"init", "../escape"}); code == 0 {
		t.Fatal("runGoalInit with unsafe feature-id = 0, want non-zero")
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge")); !os.IsNotExist(err) {
		t.Fatalf(".forge dir was created despite invalid feature-id, stat err = %v", err)
	}
}

// TestRunGoalInit_EditRestampsRevision confirms `--edit` opens the fake
// editor, and that the post-edit content is re-parsed and re-stamped so the
// file remains non-Stale even though its content changed after the initial
// write.
func TestRunGoalInit_EditRestampsRevision(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	editor := writeFakeEditor(t, dir)
	t.Setenv("EDITOR", editor)
	t.Setenv("VISUAL", "")

	if code := runGoalInit([]string{"init", "foo", "--edit"}); code != 0 {
		t.Fatalf("runGoalInit --edit = %d, want 0", code)
	}

	path := filepath.Join(dir, ".forge", "features", "foo", "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	last := a.Sections[len(a.Sections)-1]
	if !containsLine(last.Body, "Edited in-editor.") {
		t.Fatalf("edited content missing from last section body: %q", last.Body)
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("edited artifact is Stale: ComputeRevision = %q, stamped Revision = %q", got, a.Revision)
	}
}

func containsLine(body, want string) bool {
	for _, line := range splitLines(body) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

// TestRunGoalInit_EditWithoutEditorSet confirms `--edit` with neither
// $VISUAL nor $EDITOR set exits non-zero, leaving the already-written goal
// file in place untouched.
func TestRunGoalInit_EditWithoutEditorSet(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	if code := runGoalInit([]string{"init", "foo", "--edit"}); code == 0 {
		t.Fatal("runGoalInit --edit with no $VISUAL/$EDITOR = 0, want non-zero")
	}

	path := filepath.Join(dir, ".forge", "features", "foo", "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("goal.md should still exist after failed --edit: ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("skeleton is Stale: ComputeRevision = %q, stamped Revision = %q", got, a.Revision)
	}
}

// TestRunGoalInit_EditPostParseFailureKeepsAuthorEdits confirms that when
// the post-edit content fails to parse, runGoalInit reports the error and
// leaves the file exactly as the (fake) editor saved it, rather than
// discarding the author's edits.
func TestRunGoalInit_EditPostParseFailureKeepsAuthorEdits(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	script := filepath.Join(dir, "corrupt-editor.sh")
	body := "#!/bin/sh\nprintf 'not a valid artifact\\n' > \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("EDITOR", script)
	t.Setenv("VISUAL", "")

	if code := runGoalInit([]string{"init", "foo", "--edit"}); code == 0 {
		t.Fatal("runGoalInit --edit with unparseable post-edit content = 0, want non-zero")
	}

	path := filepath.Join(dir, ".forge", "features", "foo", "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "not a valid artifact\n" {
		t.Fatalf("author's edits were discarded: got %q", string(data))
	}
}

// TestRunGoalInit_FromWrapsExistingDoc confirms `--from <path>` adopts a
// freeform doc's content, split into `##` sections, as a valid non-Stale
// draft goal.md.
func TestRunGoalInit_FromWrapsExistingDoc(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	notes := "Some intro prose.\n\n## Goal\n\nShip the thing.\n\n## Constraints\n\nMust ship by Friday.\n"
	notesPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notesPath, []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runGoalInit([]string{"init", "foo", "--from", notesPath}); code != 0 {
		t.Fatalf("runGoalInit --from = %d, want 0", code)
	}

	path := filepath.Join(dir, ".forge", "features", "foo", "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.Kind != planning.KindGoal {
		t.Fatalf("Kind = %q, want %q", a.Kind, planning.KindGoal)
	}
	if a.State != "draft" {
		t.Fatalf("State = %q, want draft", a.State)
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("artifact is Stale: ComputeRevision = %q, stamped Revision = %q", got, a.Revision)
	}

	wantSections := []planning.Section{
		{Heading: "", Body: "Some intro prose."},
		{Heading: "Goal", Body: "Ship the thing."},
		{Heading: "Constraints", Body: "Must ship by Friday."},
	}
	if len(a.Sections) != len(wantSections) {
		t.Fatalf("got %d sections, want %d: %+v", len(a.Sections), len(wantSections), a.Sections)
	}
	for i, want := range wantSections {
		if a.Sections[i].Heading != want.Heading || a.Sections[i].Body != want.Body {
			t.Errorf("section %d = %+v, want %+v", i, a.Sections[i], want)
		}
	}
}

// TestRunGoalInit_FromWithoutHeadings confirms a source doc with no `##`
// headings is wrapped under a single "Goal" section rather than left
// unlabeled.
func TestRunGoalInit_FromWithoutHeadings(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	notesPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notesPath, []byte("Just some freeform notes with no headings.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runGoalInit([]string{"init", "foo", "--from", notesPath}); code != 0 {
		t.Fatalf("runGoalInit --from = %d, want 0", code)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", "foo", "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(a.Sections) != 1 {
		t.Fatalf("got %d sections, want 1: %+v", len(a.Sections), a.Sections)
	}
	if a.Sections[0].Heading != "Goal" {
		t.Fatalf("section heading = %q, want %q", a.Sections[0].Heading, "Goal")
	}
	if a.Sections[0].Body != "Just some freeform notes with no headings." {
		t.Fatalf("section body = %q", a.Sections[0].Body)
	}
}

// TestRunGoalInit_FromIssueDefaultsToFeatureID confirms --from-issue fetches
// the feature-id issue through gh, includes both title and body, and writes a
// freshly stamped draft goal.md.
func TestRunGoalInit_FromIssueDefaultsToFeatureID(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, binDir, `#!/bin/sh
if [ "$1" != "issue" ] || [ "$2" != "view" ] || [ "$3" != "245" ]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
cat <<'JSON'
{"title":"Seed from tracker","body":"## Context\n\nIssue context.\n\n## Success Criteria\n\nIt works."}
JSON
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code := runGoalInit([]string{"init", "245", "--from-issue"}); code != 0 {
		t.Fatalf("runGoalInit --from-issue = %d, want 0", code)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", "245", "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.State != "draft" {
		t.Fatalf("State = %q, want draft", a.State)
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("artifact is Stale: ComputeRevision = %q, stamped Revision = %q", got, a.Revision)
	}
	wantSections := []planning.Section{
		{Heading: "Goal", Body: "Seed from tracker"},
		{Heading: "Context", Body: "Issue context."},
		{Heading: "Success Criteria", Body: "It works."},
	}
	if len(a.Sections) != len(wantSections) {
		t.Fatalf("got %d sections, want %d: %+v", len(a.Sections), len(wantSections), a.Sections)
	}
	for i, want := range wantSections {
		if a.Sections[i].Heading != want.Heading || a.Sections[i].Body != want.Body {
			t.Errorf("section %d = %+v, want %+v", i, a.Sections[i], want)
		}
	}
}

// TestRunGoalInit_FromIssueOverrideFetchesDifferentIssue confirms an
// explicit --from-issue value selects the source issue independently of the
// feature-id path being initialized.
func TestRunGoalInit_FromIssueOverrideFetchesDifferentIssue(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, binDir, `#!/bin/sh
if [ "$3" != "999" ]; then
  echo "expected issue 999, got $3" >&2
  exit 2
fi
cat <<'JSON'
{"title":"Override source","body":"Overridden body."}
JSON
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code := runGoalInit([]string{"init", "local-feature", "--from-issue", "999"}); code != 0 {
		t.Fatalf("runGoalInit --from-issue 999 = %d, want 0", code)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", "local-feature", "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(a.Sections) != 1 {
		t.Fatalf("got %d sections, want 1: %+v", len(a.Sections), a.Sections)
	}
	if a.Sections[0].Heading != "Goal" || a.Sections[0].Body != "Override source\n\nOverridden body." {
		t.Fatalf("section = %+v, want title and overridden body under Goal", a.Sections[0])
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("artifact is Stale: ComputeRevision = %q, stamped Revision = %q", got, a.Revision)
	}
}

// TestRunGoalInit_FromIssueWithForce confirms --from-issue composes with
// --force to overwrite an existing goal.md.
func TestRunGoalInit_FromIssueWithForce(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runGoalInit([]string{"init", "245"}); code != 0 {
		t.Fatalf("runGoalInit = %d, want 0", code)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, binDir, `#!/bin/sh
cat <<'JSON'
{"title":"Forced source","body":"Replacement body."}
JSON
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code := runGoalInit([]string{"init", "245", "--from-issue", "--force"}); code != 0 {
		t.Fatalf("runGoalInit --from-issue --force = %d, want 0", code)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", "245", "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(a.Sections) != 1 || a.Sections[0].Body != "Forced source\n\nReplacement body." {
		t.Fatalf("--force did not overwrite with fetched issue content: %+v", a.Sections)
	}
}

// TestRunGoalInit_FromIssueWithEdit confirms --from-issue composes with
// --edit and re-stamps the revision after the author edits the fetched draft.
func TestRunGoalInit_FromIssueWithEdit(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, binDir, `#!/bin/sh
cat <<'JSON'
{"title":"Editable source","body":"Initial body."}
JSON
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	editor := writeFakeEditor(t, dir)
	t.Setenv("EDITOR", editor)
	t.Setenv("VISUAL", "")

	if code := runGoalInit([]string{"init", "245", "--from-issue", "--edit"}); code != 0 {
		t.Fatalf("runGoalInit --from-issue --edit = %d, want 0", code)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", "245", "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !containsLine(a.Sections[len(a.Sections)-1].Body, "Edited in-editor.") {
		t.Fatalf("edited content missing from fetched goal: %+v", a.Sections)
	}
	if got := planning.ComputeRevision(a); got != a.Revision {
		t.Fatalf("edited artifact is Stale: ComputeRevision = %q, stamped Revision = %q", got, a.Revision)
	}
}

// TestRunGoalInit_FromIssueMissingGHFailsWithoutWriting confirms a missing gh
// executable is reported before any goal.md is created.
func TestRunGoalInit_FromIssueMissingGHFailsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	emptyPath := filepath.Join(dir, "empty-path")
	if err := os.MkdirAll(emptyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", emptyPath)

	if code := runGoalInit([]string{"init", "245", "--from-issue"}); code == 0 {
		t.Fatal("runGoalInit --from-issue without gh = 0, want non-zero")
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge")); !os.IsNotExist(err) {
		t.Fatalf(".forge dir was created despite missing gh, stat err = %v", err)
	}
}

// TestRunGoalInit_FromIssueMissingIssueFailsWithoutWriting confirms a gh
// lookup failure, such as a missing issue, is reported before any goal.md is
// created.
func TestRunGoalInit_FromIssueMissingIssueFailsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, binDir, `#!/bin/sh
echo 'GraphQL: Could not resolve to an Issue with the number of 404.' >&2
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code := runGoalInit([]string{"init", "404", "--from-issue"}); code == 0 {
		t.Fatal("runGoalInit --from-issue for missing issue = 0, want non-zero")
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge")); !os.IsNotExist(err) {
		t.Fatalf(".forge dir was created despite missing issue, stat err = %v", err)
	}
}

// TestRunGoalInit_FromNoClobber confirms --from respects the same
// no-clobber default as the blank skeleton.
func TestRunGoalInit_FromNoClobber(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	notesPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notesPath, []byte("## Goal\n\nFirst.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runGoalInit([]string{"init", "foo", "--from", notesPath}); code != 0 {
		t.Fatalf("first runGoalInit --from = %d, want 0", code)
	}

	path := filepath.Join(dir, ".forge", "features", "foo", "goal.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if code := runGoalInit([]string{"init", "foo", "--from", notesPath}); code == 0 {
		t.Fatal("second runGoalInit --from without --force = 0, want non-zero")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("goal.md was modified by a no-clobber --from run")
	}
}

// TestRunGoalInit_FromWithForce confirms --from composes with --force to
// overwrite an existing goal.md.
func TestRunGoalInit_FromWithForce(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runGoalInit([]string{"init", "foo"}); code != 0 {
		t.Fatalf("runGoalInit = %d, want 0", code)
	}

	notesPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notesPath, []byte("## Goal\n\nAdopted content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runGoalInit([]string{"init", "foo", "--from", notesPath, "--force"}); code != 0 {
		t.Fatalf("runGoalInit --from --force = %d, want 0", code)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", "foo", "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(a.Sections) != 1 || a.Sections[0].Body != "Adopted content." {
		t.Fatalf("--force did not overwrite with adopted content: %+v", a.Sections)
	}
}

// TestRunGoalInit_FromMissingFile confirms a nonexistent --from path fails
// cleanly without writing anything.
func TestRunGoalInit_FromMissingFile(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runGoalInit([]string{"init", "foo", "--from", filepath.Join(dir, "missing.md")}); code == 0 {
		t.Fatal("runGoalInit --from <missing file> = 0, want non-zero")
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge")); !os.IsNotExist(err) {
		t.Fatalf(".forge dir was created despite missing --from source, stat err = %v", err)
	}
}

// TestRunGoalInit_ThenPlan confirms `forge plan`'s "no goal.md yet" branch
// -- the one that returns cleanly without a goal -- is not taken once
// `forge goal init` has run: loading the goal through the same
// fileArtifactLoader forge plan uses must now succeed with a valid,
// non-Stale Artifact.
func TestRunGoalInit_ThenPlan(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runGoalInit([]string{"init", "widget"}); code != 0 {
		t.Fatalf("runGoalInit = %d, want 0", code)
	}

	loader := &fileArtifactLoader{}
	goal, err := loader.LoadGoal(context.Background(), "widget")
	if err != nil {
		t.Fatalf("LoadGoal: %v", err)
	}
	if planning.ComputeRevision(goal) != goal.Revision {
		t.Fatal("loaded goal is Stale; forge plan would treat it as invalid compiler input")
	}
}
