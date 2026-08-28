package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/gittest"
)

func TestGitDiffProducer_ReturnsDiffBetweenBaseAndHead(t *testing.T) {
	root, base := gittest.NewTempRepo(t)

	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write new.txt: %v", err)
	}
	gittest.RunGit(t, root, "add", "new.txt")
	gittest.RunGit(t, root, "commit", "-q", "-m", "add new.txt")

	diff, err := gitDiffProducer{}.Diff(context.Background(), root, base)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff == "" {
		t.Fatal("Diff returned empty string, want a non-empty diff")
	}
	if want := "new.txt"; !strings.Contains(diff, want) {
		t.Errorf("Diff = %q, want it to mention %q", diff, want)
	}
}

func TestGitDiffProducer_ReturnsErrorForInvalidBase(t *testing.T) {
	root, _ := gittest.NewTempRepo(t)

	if _, err := (gitDiffProducer{}).Diff(context.Background(), root, "not-a-real-revision"); err == nil {
		t.Fatal("Diff: want error for an invalid base revision, got nil")
	}
}
