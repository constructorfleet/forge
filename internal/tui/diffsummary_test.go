package tui_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Teagan42/forge/internal/tui"
)

const sampleDiff = `diff --git a/internal/tui/frame.go b/internal/tui/frame.go
index 1111111..2222222 100644
--- a/internal/tui/frame.go
+++ b/internal/tui/frame.go
@@ -1,4 +1,5 @@
 package tui
-// old
+// new
+// newer
 
diff --git a/docs/new.md b/docs/new.md
new file mode 100644
--- /dev/null
+++ b/docs/new.md
@@ -0,0 +1,2 @@
+# New
+body
diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1,3 +0,0 @@
-a
-b
-c
`

// TestSummarizeDiffCountsPerFile proves the diff pane's file list carries one
// entry per changed file with its own additions and deletions, plus totals.
func TestSummarizeDiffCountsPerFile(t *testing.T) {
	got := tui.SummarizeDiff(sampleDiff)
	want := []tui.DiffFile{
		{Path: "internal/tui/frame.go", Additions: 2, Deletions: 1},
		{Path: "docs/new.md", Additions: 2, Deletions: 0},
		{Path: "old.txt", Additions: 0, Deletions: 3},
	}
	if len(got.Files) != len(want) {
		t.Fatalf("SummarizeDiff returned %d files, want %d: %+v", len(got.Files), len(want), got.Files)
	}
	for i := range want {
		if got.Files[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, got.Files[i], want[i])
		}
	}
	if got.Additions != 4 || got.Deletions != 4 {
		t.Errorf("totals = +%d -%d, want +4 -4", got.Additions, got.Deletions)
	}
}

// TestSummarizeDiffIgnoresHeaderMarkers proves the +++ and --- file headers
// never count as changed lines, and an empty diff yields no file.
func TestSummarizeDiffIgnoresHeaderMarkers(t *testing.T) {
	got := tui.SummarizeDiff("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-x\n+y\n")
	if len(got.Files) != 1 || got.Files[0].Path != "x" || got.Files[0].Additions != 1 || got.Files[0].Deletions != 1 {
		t.Fatalf("SummarizeDiff(header-only diff) = %+v", got)
	}
	if empty := tui.SummarizeDiff(""); len(empty.Files) != 0 || empty.Additions != 0 {
		t.Fatalf("SummarizeDiff(empty) = %+v, want no files", empty)
	}
}

func TestBoundDiffLimitsBytesAndLines(t *testing.T) {
	input := strings.Repeat("+123456789\n", 10)
	got, truncated := tui.BoundDiff(input, 24, 3)
	if !truncated {
		t.Fatal("BoundDiff reported no truncation")
	}
	if len([]byte(got)) > 24 {
		t.Fatalf("BoundDiff returned %d bytes, want at most 24", len([]byte(got)))
	}
	if lines := strings.Count(got, "\n"); lines > 3 {
		t.Fatalf("BoundDiff returned %d lines, want at most 3", lines)
	}
}

func TestBoundDiffStopsBeforeAnOversizedUTF8Line(t *testing.T) {
	input := "+界界\n+ok\n"
	got, truncated := tui.BoundDiff(input, len([]byte("+界")), 0)
	if !truncated {
		t.Fatal("BoundDiff reported no truncation")
	}
	if got != "" {
		t.Fatalf("BoundDiff returned a partial line %q, want no oversized line", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("BoundDiff returned invalid UTF-8")
	}
}
