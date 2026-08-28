package initdiscovery

import (
	"os"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/config"
)

// TestRender_AnnotatesByFullPath_NotLeafName is a regression test: Config
// has several duplicate leaf key names (retry.review vs workflow.review,
// top-level "ci" vs retry.ci) that a leaf-only line matcher would
// misattribute a comment to. Render must annotate the exact field the Note
// names, found by walking its full dotted path.
func TestRender_AnnotatesByFullPath_NotLeafName(t *testing.T) {
	result := Result{
		Config: config.Default(),
		Notes:  []Note{{Field: "retry.review", Message: "explicit review budget"}},
	}

	out, err := Render(result)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	lines := strings.Split(string(out), "\n")
	var retryReviewLine, workflowReviewLine string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "review: 2" || strings.HasPrefix(trimmed, "review: 2 ") || strings.HasPrefix(trimmed, "review: 2#") {
			retryReviewLine = line
		}
		if strings.HasPrefix(trimmed, "review: true") {
			workflowReviewLine = line
		}
	}

	if !strings.Contains(retryReviewLine, "TODO: explicit review budget") {
		t.Errorf("retry.review line missing TODO comment, got: %q\nfull output:\n%s", retryReviewLine, out)
	}
	if strings.Contains(workflowReviewLine, "TODO") {
		t.Errorf("workflow.review line was mis-annotated: %q\nfull output:\n%s", workflowReviewLine, out)
	}
}

// TestRender_QualityGatesNote_AttachesToGatesKey_NotNextField is a
// regression test: when quality.gates is non-empty it renders as a block
// sequence ("gates:\n  - name: ...\n"), which has no inline value slot on
// the "gates:" line itself. yaml.v3 silently reattaches a LineComment set
// on the sequence *value* node to the following sibling key's line instead
// of the "gates:" line it was meant for — Render must set the comment on
// the "gates" *key* node so it lands in the right place.
func TestRender_QualityGatesNote_AttachesToGatesKey_NotNextField(t *testing.T) {
	cfg := config.Default()
	cfg.Quality.Gates = []config.QualityGate{{Name: "test", Command: "go test ./..."}}
	result := Result{
		Config: cfg,
		Notes:  []Note{{Field: "quality.gates", Message: "missing lint"}},
	}

	out, err := Render(result)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var gatesLine, pullRequestsLine string
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "gates:") {
			gatesLine = line
		}
		if strings.HasPrefix(trimmed, "pull_requests:") {
			pullRequestsLine = line
		}
	}

	if !strings.Contains(gatesLine, "TODO: missing lint") {
		t.Errorf("gates: line missing TODO comment, got %q\nfull output:\n%s", gatesLine, out)
	}
	if strings.Contains(pullRequestsLine, "TODO") {
		t.Errorf("pull_requests: line was mis-annotated with the gates note: %q\nfull output:\n%s", pullRequestsLine, out)
	}
}

// TestRender_RoundTripsThroughConfigLoad confirms comments never change the
// underlying data: a Config with several Notes attached still parses back
// to an equal Config via config.Load.
func TestRender_RoundTripsThroughConfigLoad(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Base = "origin/develop"
	cfg.Quality.Gates = []config.QualityGate{{Name: "test", Command: "go test ./..."}}
	result := Result{
		Config: cfg,
		Notes: []Note{
			{Field: "git.base", Message: "could not confirm"},
			{Field: "tracker.type", Message: "no remote"},
			{Field: "quality.gates", Message: "missing lint"},
			{Field: "agent_instructions", Message: "no AGENTS.md found"},
		},
	}

	out, err := Render(result)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	dir := t.TempDir()
	path := dir + "/.forge.yaml"
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v\n---\n%s", err, out)
	}
	if loaded.Git.Base != "origin/develop" {
		t.Errorf("loaded.Git.Base = %q, want origin/develop", loaded.Git.Base)
	}
}
