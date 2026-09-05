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

// TestRenderPlanningShowsStageHistoryWithOneLiveHead proves the planning
// frame renders a strip of every recorded stage row and marks only the
// newest one — the single live head — with the cursor, never claiming
// liveness (no heartbeat glyph, no elapsed figure: only an absolute
// "last activity at" timestamp).
func TestRenderPlanningShowsStageHistoryWithOneLiveHead(t *testing.T) {
	t1 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(5 * time.Minute)
	vm := tui.PlanningViewModel{
		Stages: []tui.PlanningStageRow{
			{Stage: "decision-resolution", LastActivity: t1},
			{Stage: "specification-generation", LastActivity: t2},
		},
	}
	out := tui.RenderPlanning(vm)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 rendered lines, got %d: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], " ") || !strings.Contains(lines[0], "decision-resolution") {
		t.Fatalf("first row must be unmarked, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], ">") || !strings.Contains(lines[1], "specification-generation") {
		t.Fatalf("newest row must carry the live-head cursor, got %q", lines[1])
	}
	if strings.ContainsAny(out, "•×") {
		t.Fatalf("planning render must claim no liveness glyph, got %q", out)
	}
	if !strings.Contains(out, "last activity at") {
		t.Fatalf("detail strip must show an absolute last-activity timestamp, got %q", out)
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
