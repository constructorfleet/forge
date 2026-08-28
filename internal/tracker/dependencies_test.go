package tracker_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestParseDependencyBlock_BulletList(t *testing.T) {
	body := "Some description.\n\n## Dependencies\n- #123\n- #456\n\n## Other Section\nmore text\n"

	got, err := tracker.ParseDependencyBlock(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"123", "456"}
	if !equalSlices(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseDependencyBlock_DependsOnLabelPlusList(t *testing.T) {
	body := "## Dependencies\nDepends on:\n- #7\n"

	got, err := tracker.ParseDependencyBlock(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalSlices(got, []string{"7"}) {
		t.Fatalf("got %v", got)
	}
}

func TestParseDependencyBlock_InlineDependsOn(t *testing.T) {
	body := "## Dependencies\nDepends on: #1, #2\n"

	got, err := tracker.ParseDependencyBlock(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalSlices(got, []string{"1", "2"}) {
		t.Fatalf("got %v", got)
	}
}

func TestParseDependencyBlock_None(t *testing.T) {
	body := "## Dependencies: None\n\nsome other text"

	got, err := tracker.ParseDependencyBlock(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestParseDependencyBlock_NoBlockPresent(t *testing.T) {
	body := "Just a normal issue body with no dependencies section."

	got, err := tracker.ParseDependencyBlock(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestParseDependencyBlock_RejectsFreeform(t *testing.T) {
	body := "## Dependencies\nThis depends on the auth work being done first.\n"

	_, err := tracker.ParseDependencyBlock(body)
	if err == nil {
		t.Fatal("expected an error for freeform dependency text, got nil")
	}
	if !strings.Contains(err.Error(), "invalid dependency syntax") {
		t.Fatalf("error message %q does not describe invalid syntax", err.Error())
	}
}

func TestParseDependencyBlock_RejectsMalformedBullet(t *testing.T) {
	body := "## Dependencies\n- issue 123\n"

	_, err := tracker.ParseDependencyBlock(body)
	if err == nil {
		t.Fatal("expected an error for malformed bullet, got nil")
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
