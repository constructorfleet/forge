package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tui"
)

func TestAttentionGlyph(t *testing.T) {
	cases := []struct {
		name string
		att  tui.Attention
		want string
	}{
		{"needs answer", tui.AttentionNeedsAnswer, "!"},
		{"running tool", tui.AttentionRunningTool, "*"},
		{"none", tui.AttentionNone, " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.AttentionGlyph(tc.att); got != tc.want {
				t.Fatalf("AttentionGlyph(%v) = %q, want %q", tc.att, got, tc.want)
			}
		})
	}
}

func TestLivenessGlyph(t *testing.T) {
	cases := []struct {
		name string
		live tui.Liveness
		want string
	}{
		{"live", tui.LivenessLive, "\u2022"},
		{"stale", tui.LivenessStale, "\u00d7"},
		{"none", tui.LivenessNone, " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.LivenessGlyph(tc.live); got != tc.want {
				t.Fatalf("LivenessGlyph(%v) = %q, want %q", tc.live, got, tc.want)
			}
		})
	}
}

func TestDeriveAttention(t *testing.T) {
	cases := []struct {
		name  string
		state domain.IssueState
		tool  string
		want  tui.Attention
	}{
		// GroupBlocked states await human input or a backoff; flag for attention.
		{"needs info", domain.StateNeedsInfo, "", tui.AttentionNeedsAnswer},
		{"needs replan", domain.StateNeedsReplan, "", tui.AttentionNeedsAnswer},
		{"provider limit", domain.StateProviderLimit, "", tui.AttentionNeedsAnswer},
		// A tool in flight marks a running Worker.
		{"tool running", domain.StateImplementing, "git status", tui.AttentionRunningTool},
		// Everything else is quiet.
		{"working idle", domain.StateImplementing, "", tui.AttentionNone},
		{"pending", domain.StatePending, "", tui.AttentionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.DeriveAttention(tc.state, tc.tool); got != tc.want {
				t.Fatalf("DeriveAttention(%s, %q) = %v, want %v", tc.state, tc.tool, got, tc.want)
			}
		})
	}
}

func TestDeriveLiveness(t *testing.T) {
	within := 3 * time.Second
	over := 30 * time.Second
	cases := []struct {
		name    string
		hasBeat bool
		age     time.Duration
		want    tui.Liveness
	}{
		{"live within window", true, within, tui.LivenessLive},
		{"stale over window", true, over, tui.LivenessStale},
		{"no heartbeat", false, 0, tui.LivenessNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.DeriveLiveness(tc.hasBeat, tc.age); got != tc.want {
				t.Fatalf("DeriveLiveness(%v, %s) = %v, want %v", tc.hasBeat, tc.age, got, tc.want)
			}
		})
	}
}

func TestLegalKeys(t *testing.T) {
	cases := []struct {
		state domain.IssueState
		want  []string
	}{
		// q (quit) always legal; c (cancel) legal from any non-terminal state.
		{domain.StatePending, []string{"[q] quit", "[c] cancel"}},
		{domain.StateImplementing, []string{"[q] quit", "[c] cancel"}},
		// r (retry) is the manual retry out of the terminal FAILED state.
		{domain.StateFailed, []string{"[q] quit", "[r] retry"}},
		// a (answer) is legal only while parked on a NEEDS_INFO decision.
		{domain.StateNeedsInfo, []string{"[q] quit", "[c] cancel", "[a] answer"}},
		// Terminal states carry only q: no further transitions.
		{domain.StateDone, []string{"[q] quit"}},
		{domain.StateCancelled, []string{"[q] quit"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			got := tui.LegalKeys(tc.state)
			if len(got) != len(tc.want) {
				t.Fatalf("LegalKeys(%s) = %v keys, want %v", tc.state, got, tc.want)
			}
			for i := range tc.want {
				if got[i].String() != tc.want[i] {
					t.Fatalf("LegalKeys(%s)[%d] = %q, want %q", tc.state, i, got[i].String(), tc.want[i])
				}
			}
		})
	}
}

func TestRender(t *testing.T) {
	vm := tui.ViewModel{
		Selection: 1,
		Workers: []tui.WorkerRow{
			{
				IssueID: "#1",
				Title:   "Write tests",
				State:   domain.StatePending,
			},
			{
				IssueID:      "#2",
				Title:        "Add roster frame",
				State:        domain.StateImplementing,
				Elapsed:      62 * time.Second,
				HasHeartbeat: true,
				HeartbeatAge: 3 * time.Second,
				Attempt:      2,
				Budget:       3,
				Tool:         "git status",
			},
		},
	}

	want := "" +
		"      pending  #1         Write tests\n" +
		"> * • working  #2         Add roster frame\n" +
		"IMPLEMENTING | elapsed 1m2s | beat 3s | attempt 2/3 | tool git status\n" +
		"[q] quit [c] cancel\n"

	if got := tui.Render(vm); got != want {
		t.Fatalf("Render mismatch.\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestRenderColorNeverLoadBearing(t *testing.T) {
	// The frame must carry no ANSI colour so it works in a no-colour terminal.
	vm := tui.ViewModel{
		Selection: 0,
		Workers: []tui.WorkerRow{
			{IssueID: "#1", Title: "t", State: domain.StateImplementing},
		},
	}
	if strings.Contains(tui.Render(vm), "\x1b[") {
		t.Fatalf("frame must not embed ANSI colour escapes")
	}
}

func TestRenderFooterAlwaysMatchesLegalKeys(t *testing.T) {
	// The footer must advertise exactly the keys legal for the selected row's
	// state, derived from the same view-model as the rows it annotates.
	states := []domain.IssueState{
		domain.StatePending, domain.StateImplementing, domain.StateFailed,
		domain.StateNeedsInfo, domain.StateDone, domain.StateCancelled,
	}
	for _, s := range states {
		vm := tui.ViewModel{
			Selection: 0,
			Workers: []tui.WorkerRow{
				{IssueID: "#1", Title: "t", State: s},
			},
		}
		out := tui.Render(vm)
		lines := splitLines(out)
		footer := lines[len(lines)-1]
		want := footerFor(tui.LegalKeys(s))
		if footer != want {
			t.Fatalf("state %s: frame footer %q, want %q (must mirror LegalKeys)",
				s, footer, want)
		}
	}
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func footerFor(keys []tui.KeyBinding) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, " ")
}
