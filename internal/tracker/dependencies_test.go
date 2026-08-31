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

func TestParseDependencyBlock_BulletWithAnnotation(t *testing.T) {
	body := "## Dependencies\n- #96 (SaveGoal loader method)\n- #97 (feature-id validation)\n"

	got, err := tracker.ParseDependencyBlock(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalSlices(got, []string{"96", "97"}) {
		t.Fatalf("got %v", got)
	}
}

func TestParseDependencyBlock_BulletWithDashDescription(t *testing.T) {
	// The forms Forge itself emits in generated issue bodies: an em dash,
	// en dash, hyphen, or colon introducing a human-readable description.
	for _, body := range []string{
		"## Dependencies\n- #198 \xe2\x80\x94 Add ModeStructured + Prompt/Schema fields to the Agent contract\n",
		"## Dependencies\n- #198 \xe2\x80\x93 en dash description\n",
		"## Dependencies\n- #198 - hyphen description\n",
		"## Dependencies\n- #198: colon description\n",
	} {
		got, err := tracker.ParseDependencyBlock(body)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", body, err)
		}
		if !equalSlices(got, []string{"198"}) {
			t.Fatalf("body %q: got %v", body, got)
		}
	}
}

func TestParseDependencyBlock_RejectsMultipleRefsOnOneBullet(t *testing.T) {
	// A description that hides a second issue ref must fail closed rather
	// than silently drop the extra dependency.
	body := "## Dependencies\n- #198 \xe2\x80\x94 also blocked by #199\n"

	_, err := tracker.ParseDependencyBlock(body)
	if err == nil {
		t.Fatal("expected an error for multiple refs on one bullet, got nil")
	}
	if !strings.Contains(err.Error(), "multiple issue refs") {
		t.Fatalf("error message %q does not describe multiple refs", err.Error())
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

func TestParseDependencyBlock_NoneLineInBlock(t *testing.T) {
	for _, body := range []string{
		"## Dependencies\nNone.\n",
		"## Dependencies\nNone\n",
		"## Dependencies\nnone\n",
		"## Dependencies\n\nNone.\n\n## Other\ntext",
	} {
		got, err := tracker.ParseDependencyBlock(body)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", body, err)
		}
		if len(got) != 0 {
			t.Fatalf("body %q: got %v, want empty", body, got)
		}
	}
}

func TestParseDependencyBlock_ThematicBreakClosesBlock(t *testing.T) {
	for _, body := range []string{
		"## Dependencies\n- #123\n\n---\nmore prose",
		"## Dependencies\n\n---\n\nUnrelated text",
		"## Dependencies\nNone.\n\n***\n",
		"## Dependencies\n- #7\n___\n",
	} {
		got, err := tracker.ParseDependencyBlock(body)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", body, err)
		}
		switch {
		case strings.Contains(body, "#123"):
			if !equalSlices(got, []string{"123"}) {
				t.Fatalf("body %q: got %v", body, got)
			}
		case strings.Contains(body, "#7"):
			if !equalSlices(got, []string{"7"}) {
				t.Fatalf("body %q: got %v", body, got)
			}
		default:
			if len(got) != 0 {
				t.Fatalf("body %q: got %v, want empty", body, got)
			}
		}
	}
}

func TestParseDependencyBlock_EmptyBlock(t *testing.T) {
	body := "## Dependencies\n\n## Other Section\ntext"

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

// Near-miss headers must fail closed: a human-written variant that isn't
// exactly "## Dependencies" or "## Dependencies: None" must error rather
// than silently be treated as "no Dependencies block present", since that
// would let a Worker launch before its real prerequisite merges.
func TestParseDependencyBlock_RejectsNearMissHeader_TrailingColon(t *testing.T) {
	body := "## Dependencies:\n- #123\n"

	_, err := tracker.ParseDependencyBlock(body)
	if err == nil {
		t.Fatal("expected an error for near-miss header with trailing colon, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("error message %q does not describe invalid syntax", err.Error())
	}
}

func TestParseDependencyBlock_RejectsNearMissHeader_TrailingParenthetical(t *testing.T) {
	body := "## Dependencies (blocked by)\n- #123\n"

	_, err := tracker.ParseDependencyBlock(body)
	if err == nil {
		t.Fatal("expected an error for near-miss header with trailing text, got nil")
	}
}

func TestParseDependencyBlock_RejectsNearMissHeader_TrailingDash(t *testing.T) {
	body := "## Dependencies \xe2\x80\x94\n- #123\n"

	_, err := tracker.ParseDependencyBlock(body)
	if err == nil {
		t.Fatal("expected an error for near-miss header with trailing em dash, got nil")
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
