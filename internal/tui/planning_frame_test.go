package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/tui"
)

// TestPlanningLegalKeysNeverOffersCancel proves the planning footer can
// never advertise cancel, whatever the Feature's Planning Execution status:
// planning has no cancel control (docs/specs/live-agent-tui.md section 6).
func TestPlanningLegalKeysNeverOffersCancel(t *testing.T) {
	cases := []struct {
		name         string
		approveLegal bool
		answerLegal  bool
		wantKeys     []string
	}{
		{name: "idle", wantKeys: []string{"q"}},
		{name: "needs approval", approveLegal: true, wantKeys: []string{"q", "p"}},
		{name: "needs answer", answerLegal: true, wantKeys: []string{"q", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := tui.PlanningViewModel{ApproveLegal: tc.approveLegal, AnswerLegal: tc.answerLegal}
			keys := tui.PlanningLegalKeys(vm)
			var got []string
			for _, k := range keys {
				if k.Key == "c" {
					t.Fatalf("PlanningLegalKeys must never offer cancel, got %+v", keys)
				}
				got = append(got, k.Key)
			}
			want := tc.wantKeys
			if len(got) != len(want) {
				t.Fatalf("keys = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("keys = %v, want %v", got, want)
				}
			}
		})
	}
}

// TestRenderPlanningShowsSingleCurrentStageLine proves the planning frame
// collapses the stage strip to one line: the current stage (the newest
// recorded row), the total attempt count, and an absolute activity time. It
// never renders one line per attempt, so a stage that re-runs many times can
// not grow the strip and push the transcript off screen.
func TestRenderPlanningShowsSingleCurrentStageLine(t *testing.T) {
	t1 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(5 * time.Minute)
	vm := tui.PlanningViewModel{
		Stages: []tui.PlanningStageRow{
			{Stage: "decision-resolution", LastActivity: t1},
			{Stage: "specification-generation", LastActivity: t2},
		},
	}
	out := tui.RenderPlanning(vm)

	if strings.Contains(out, "decision-resolution") {
		t.Fatalf("older stage rows must not render, got %q", out)
	}
	if !strings.Contains(out, "> specification-generation") {
		t.Fatalf("current stage must carry the cursor, got %q", out)
	}
	if !strings.Contains(out, "attempt 2") {
		t.Fatalf("stage line must show the total attempt count, got %q", out)
	}
	if strings.ContainsAny(out, "•×") {
		t.Fatalf("planning render must claim no liveness glyph, got %q", out)
	}
	if !strings.Contains(out, "last activity at") {
		t.Fatalf("detail strip must show an absolute last-activity timestamp, got %q", out)
	}
}

// TestRenderPlanningShowsPendingDecisionInline proves the frame renders a
// pending Decision's question and context inline while the answer control is
// legal, so the operator reads the question without opening $EDITOR.
func TestRenderPlanningShowsPendingDecisionInline(t *testing.T) {
	vm := tui.PlanningViewModel{
		Stages:           []tui.PlanningStageRow{{Stage: "decision-resolution"}},
		AnswerLegal:      true,
		DecisionQuestion: "Should autoapply proceed without a tracker issue?",
		DecisionContext:  "The Feature slug is local-only.",
	}
	out := tui.RenderPlanning(vm)
	if !strings.Contains(out, "decision needed") {
		t.Fatalf("expected a decision-needed header, got %q", out)
	}
	if !strings.Contains(out, "Should autoapply proceed without a tracker issue?") {
		t.Fatalf("expected the question inline, got %q", out)
	}
	if !strings.Contains(out, "The Feature slug is local-only.") {
		t.Fatalf("expected the context inline, got %q", out)
	}
}

// TestRenderPlanningEmptyHistoryShowsNotice proves an empty stage history
// (a Feature with no planning runs recorded yet) renders a notice instead of
// an empty strip, and the footer still offers only quit.
func TestRenderPlanningEmptyHistoryShowsNotice(t *testing.T) {
	vm := tui.PlanningViewModel{Notice: "no planning runs yet"}
	out := tui.RenderPlanning(vm)
	if !strings.Contains(out, "no planning runs yet") {
		t.Fatalf("expected notice in render, got %q", out)
	}
	if !strings.Contains(out, "[q] quit") {
		t.Fatalf("expected quit-only footer, got %q", out)
	}
	if strings.Contains(out, "[p]") || strings.Contains(out, "[a]") {
		t.Fatalf("no controls should be legal against an empty history, got %q", out)
	}
}

// TestRenderPlanningMarksTranscriptHeaderWhenLagging proves the planning
// frame marks its transcript pane header once the last committed read is
// older than transcriptLagMultiple poll intervals, mirroring
// TestRenderMarksTranscriptHeaderWhenLagging for the live roster: a planning
// transcript can suffer the same slow-store thinning as the live one.
func TestRenderPlanningMarksTranscriptHeaderWhenLagging(t *testing.T) {
	vm := tui.PlanningViewModel{PollInterval: time.Second, TranscriptLagAge: 4 * time.Second}
	if got := tui.RenderPlanning(vm); !strings.Contains(got, "lagging") {
		t.Fatalf("RenderPlanning does not mark a lagging transcript:\n%s", got)
	}
}

// TestRenderPlanningOmitsLagMarkerUnderThreshold proves the header stays
// quiet while the last committed read is still within transcriptLagMultiple
// poll intervals, mirroring TestRenderOmitsLagMarkerUnderThreshold.
func TestRenderPlanningOmitsLagMarkerUnderThreshold(t *testing.T) {
	vm := tui.PlanningViewModel{PollInterval: time.Second, TranscriptLagAge: time.Second}
	if got := tui.RenderPlanning(vm); strings.Contains(got, "lagging") {
		t.Fatalf("RenderPlanning marks lag under the threshold:\n%s", got)
	}
}
