package initdiscovery

import "testing"

// TestClassifyRunLine_WordBoundaries is a regression test: keyword matching
// used to be plain substring Contains, so "latest" (containing "test") and
// "rebuild" (containing "build") were misclassified as test/build commands
// respectively.
func TestClassifyRunLine_WordBoundaries(t *testing.T) {
	hints := map[string]string{}
	classifyRunLine("npm run build:latest", hints)
	if _, ok := hints["test"]; ok {
		t.Errorf(`"npm run build:latest" should not classify as test (via "latest"), hints = %+v`, hints)
	}
	if cmd, ok := hints["build"]; !ok || cmd != "npm run build:latest" {
		t.Errorf(`"npm run build:latest" should classify as build, hints = %+v`, hints)
	}

	hints = map[string]string{}
	classifyRunLine("make rebuild", hints)
	if _, ok := hints["build"]; ok {
		t.Errorf(`"make rebuild" should not classify as build (via substring "build" inside "rebuild"), hints = %+v`, hints)
	}
}

// TestClassifyRunLine_SingleKindPerLine confirms one command line never
// populates more than one gate kind, even if it happens to contain more
// than one keyword.
func TestClassifyRunLine_SingleKindPerLine(t *testing.T) {
	hints := map[string]string{}
	// Contains both a "lint" keyword and a "build" keyword; lint is
	// checked first in ciKeywordOrder and must be the only one set.
	classifyRunLine("golangci-lint run && go build ./...", hints)
	if len(hints) != 1 {
		t.Fatalf("expected exactly one gate kind classified, got %+v", hints)
	}
	if _, ok := hints["lint"]; !ok {
		t.Errorf("expected lint to be classified (checked first), got %+v", hints)
	}
}

// TestDetectCIHints_BlockScalarScansAllLines confirms every physical
// command line inside a "run: |" block is classified, not just the first.
func TestDetectCIHints_BlockScalarScansAllLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".github/workflows/ci.yml", `on: push
jobs:
  ci:
    steps:
      - name: quality
        run: |
          go vet ./...
          golangci-lint run
          go build ./...
`)

	hints := detectCIHints(dir)
	if hints["typecheck"] != "go vet ./..." {
		t.Errorf("typecheck = %q, want %q", hints["typecheck"], "go vet ./...")
	}
	if hints["lint"] != "golangci-lint run" {
		t.Errorf("lint = %q, want %q", hints["lint"], "golangci-lint run")
	}
	if hints["build"] != "go build ./..." {
		t.Errorf("build = %q, want %q", hints["build"], "go build ./...")
	}
}
