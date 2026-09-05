package needsinfo

import "testing"

func TestAppendCommentMarker_RoundTrip(t *testing.T) {
	body := AppendCommentMarker("Please clarify the requirement.", KindNeedsInfo, "exec-1", "issue-42")

	if !IsForgeComment(body, KindNeedsInfo, "exec-1", "issue-42") {
		t.Fatalf("expected marker to round-trip through IsForgeComment, got body: %q", body)
	}
}

func TestIsForgeComment_FalseWithoutMarker(t *testing.T) {
	body := "Please clarify the requirement."

	if IsForgeComment(body, KindNeedsInfo, "exec-1", "issue-42") {
		t.Fatalf("expected IsForgeComment to be false for a body without a marker")
	}
}

func TestIsForgeComment_SpecificToExecutionAndItem(t *testing.T) {
	body := AppendCommentMarker("Please clarify the requirement.", KindNeedsInfo, "exec-1", "issue-42")

	cases := []struct {
		name        string
		executionID string
		itemID      string
	}{
		{"different execution", "exec-2", "issue-42"},
		{"different item", "exec-1", "issue-99"},
		{"different execution and item", "exec-2", "issue-99"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if IsForgeComment(body, KindNeedsInfo, tc.executionID, tc.itemID) {
				t.Fatalf("expected marker for (exec-1, issue-42) not to match (%s, %s)", tc.executionID, tc.itemID)
			}
		})
	}
}

func TestIsForgeComment_SpecificToKind(t *testing.T) {
	body := AppendCommentMarker("Decision needs human input.", KindNeedsHuman, "exec-1", "decision-7")

	if IsForgeComment(body, KindNeedsInfo, "exec-1", "decision-7") {
		t.Fatalf("expected KindNeedsHuman marker not to match a KindNeedsInfo lookup")
	}
}

func TestCommentMarker_FormatIncludesKindExecutionAndItem(t *testing.T) {
	got := CommentMarker(KindNeedsInfo, "exec-1", "issue-42")
	want := "<!-- forge:needs-info execution=exec-1 item=issue-42 -->"

	if got != want {
		t.Fatalf("CommentMarker() = %q, want %q", got, want)
	}
}
