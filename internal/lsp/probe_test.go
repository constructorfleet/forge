package lsp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Teagan42/forge/internal/lsp"
)

// withFakePATH points PATH at a directory containing only the executables
// named in present, so exec.LookPath resolves those and only those —
// deterministic regardless of what's actually installed on the machine
// running the test.
func withFakePATH(t *testing.T, present ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range present {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func pythonSpec() lsp.LanguageSpec {
	for _, spec := range lsp.Languages {
		if spec.Language == "Python" {
			return spec
		}
	}
	panic("Python spec not found in lsp.Languages")
}

func TestProbeBinaries_SelectsPylspWhenPyrightAbsent(t *testing.T) {
	withFakePATH(t, "pylsp")

	found, ok := lsp.ProbeBinaries(pythonSpec())

	if !ok {
		t.Fatal("ProbeBinaries ok = false, want true (pylsp is on PATH)")
	}
	if found != "pylsp" {
		t.Errorf("ProbeBinaries found = %q, want %q", found, "pylsp")
	}
}

func TestProbeBinaries_PrefersFirstCandidateWhenBothPresent(t *testing.T) {
	withFakePATH(t, "pyright", "pylsp")

	found, ok := lsp.ProbeBinaries(pythonSpec())

	if !ok {
		t.Fatal("ProbeBinaries ok = false, want true")
	}
	if found != "pyright" {
		t.Errorf("ProbeBinaries found = %q, want %q (first candidate wins)", found, "pyright")
	}
}

func TestProbeBinaries_NoneFoundWhenAllCandidatesAbsent(t *testing.T) {
	withFakePATH(t)

	found, ok := lsp.ProbeBinaries(pythonSpec())

	if ok {
		t.Errorf("ProbeBinaries ok = true, want false (neither candidate on PATH)")
	}
	if found != "" {
		t.Errorf("ProbeBinaries found = %q, want empty", found)
	}
}
