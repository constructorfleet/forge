package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

const (
	ringCap = 2000
	frameW  = 100
	frameH  = 20
)

func main() {
	dump := flag.String("dump", "", "render one named scenario and exit")
	compact := flag.Bool("compact", false, "one line per worker")
	bench := flag.Bool("bench", false, "synthetic high-rate load over the ring buffer")
	flag.Parse()

	switch {
	case *bench:
		runBench()
	case *dump != "":
		fmt.Println(scenario(*dump, *compact))
	default:
		fmt.Fprintln(os.Stderr, "usage: proto -dump <scenario> | -bench")
		os.Exit(2)
	}
}

func fill(r *ring, evs []event, width int, expanded bool) {
	for _, e := range evs {
		r.push(renderEvent(e, width, expanded)...)
	}
}

var sampleEvents = []event{
	{Seq: 1, Kind: "MESSAGE", Text: "Reading the failing test to find which assertion breaks. The error names TransitionIssue, so the CAS guard is the likely culprit."},
	{Seq: 2, Kind: "TOOL_CALL", Tool: "Read", Text: `{"file_path":"/repo/internal/storage/issues.go","offset":250,"limit":60}`},
	{Seq: 3, Kind: "TOOL_RESULT", Text: "271: func (s *SQLiteStore) TransitionIssue(ctx context.Context, executionID, issueID string, next domain.IssueState) (domain.Issue, error) {"},
	{Seq: 4, Kind: "TOOL_CALL", Tool: "Bash", Text: `{"command":"go test ./internal/storage/ -run TestTransition -count=1"}`},
	{Seq: 5, Kind: "TOOL_RESULT", Text: "--- FAIL: TestTransitionIssue_ConcurrentModification (0.02s)\n    issues_test.go:118: expected ErrConcurrentModification, got <nil>"},
	{Seq: 6, Kind: "MESSAGE", Text: "The guard compares against the state it read rather than the row's current state, so two writers both pass. Fixing the predicate."},
}

func scenario(name string, compact bool) string {
	f := frame{Width: frameW, Height: frameH, Follow: true, Buf: newRing(ringCap), Compact: compact}

	switch name {
	case "running":
		f.Title = "forge watch 3f9c1a2e — 4 workers"
		f.Workers = []worker{
			{IssueID: "412", Title: "412 sync GitLab dependency links", State: "IMPLEMENTING", Group: "Working", Elapsed: 7*time.Minute + 12*time.Second, Attempt: 1, Tool: "Bash", Live: true, BeatAge: 2 * time.Second},
			{IssueID: "418", Title: "418 remote worker pool placement", State: "REVIEWING", Group: "Working", Elapsed: 12 * time.Minute, Attempt: 2, Tool: "Read", Live: true, BeatAge: 3 * time.Second},
			{IssueID: "421", Title: "421 provider limit backoff", State: "NEEDS_INFO", Group: "Blocked", Elapsed: 31 * time.Minute, Attempt: 1, Live: true, Attn: true, BeatAge: 4 * time.Second},
			{IssueID: "425", Title: "425 status reflection labels", State: "DONE", Group: "Finished", Elapsed: 4 * time.Minute, Attempt: 1, Live: false},
		}
		f.Sel = 0
		fill(f.Buf, sampleEvents, f.tranWidth(), false)

	case "expanded":
		f.Title = "forge watch 3f9c1a2e — 4 workers"
		f.Workers = []worker{{IssueID: "412", Title: "412 sync GitLab dependency links", State: "IMPLEMENTING", Group: "Working", Elapsed: 7 * time.Minute, Attempt: 1, Tool: "Bash", Live: true}}
		fill(f.Buf, sampleEvents[1:5], f.tranWidth(), true)

	case "reattach":
		f.Title = "forge watch 3f9c1a2e — attached mid-run"
		f.Workers = []worker{{IssueID: "418", Title: "418 remote worker pool placement", State: "REVIEWING", Group: "Working", Elapsed: 47 * time.Minute, Attempt: 3, Tool: "Grep", Live: true, BeatAge: 1 * time.Second}}
		f.Backfill = true
		f.Buf.push(fmt.Sprintf("%s earlier output clipped by the emitter", glyphTrunc))
		fill(f.Buf, sampleEvents, f.tranWidth(), false)

	case "paused":
		f.Title = "forge watch 3f9c1a2e — 4 workers"
		f.Follow = false
		f.Workers = []worker{{IssueID: "412", Title: "412 sync GitLab dependency links", State: "IMPLEMENTING", Group: "Working", Elapsed: 8 * time.Minute, Attempt: 1, Tool: "Bash", Live: true}}
		fill(f.Buf, sampleEvents, f.tranWidth(), false)
		f.Banner = "paused at line 12 of 47 — 9 new events below   f resume following   G jump to tail"

	case "wedged":
		f.Title = "forge watch 3f9c1a2e — 2 workers"
		f.Workers = []worker{
			{IssueID: "412", Title: "412 sync GitLab dependency links", State: "IMPLEMENTING", Group: "Working", Elapsed: 41 * time.Minute, Attempt: 1, Tool: "Bash", Live: false, BeatAge: 6 * time.Minute},
			{IssueID: "418", Title: "418 remote worker pool placement", State: "GATING", Group: "Working", Elapsed: 9 * time.Minute, Attempt: 1, Live: true, BeatAge: 2 * time.Second},
		}
		fill(f.Buf, sampleEvents[:3], f.tranWidth(), false)
		f.Buf.push("", "  no output for 6m — gate 'test' still running")

	case "planning":
		f.Title = "forge watch live-agent-tui — planning"
		f.Workers = []worker{
			{IssueID: "wayfinding", Title: "wayfinding", State: "COMPLETE", Group: "Finished", Elapsed: 3 * time.Minute, Attempt: 1, Planning: true},
			{IssueID: "survey", Title: "survey", State: "COMPLETE", Group: "Finished", Elapsed: 2 * time.Minute, Attempt: 1, Planning: true},
			{IssueID: "spec", Title: "spec", State: "RUNNING", Group: "Working", Elapsed: 90 * time.Second, Attempt: 1, Planning: true},
		}
		f.Sel = 2
		fill(f.Buf, sampleEvents[:4], f.tranWidth(), false)

	case "empty":
		f.Title = "forge watch 3f9c1a2e — no workers yet"
		fill(f.Buf, []event{{Kind: "MESSAGE", Text: "Execution 3f9c1a2e started 2s ago. No Worker has claimed an Issue yet."}}, f.tranWidth(), false)

	case "dead":
		f.Title = "forge watch 3f9c1a2e — no live worker"
		f.Workers = []worker{
			{IssueID: "412", Title: "412 sync GitLab dependency links", State: "IMPLEMENTING", Group: "Working", Elapsed: 3 * time.Hour, Attempt: 1, Live: false, BeatAge: 3*time.Hour + 2*time.Minute},
			{IssueID: "418", Title: "418 remote worker pool placement", State: "FAILED", Group: "Finished", Elapsed: 2 * time.Hour, Attempt: 2, Live: false, BeatAge: 3*time.Hour + 2*time.Minute},
		}
		fill(f.Buf, sampleEvents[:3], f.tranWidth(), false)
		f.Banner = "no heartbeat from any Worker for 3h02m — the orchestrator is gone. cancel releases the claims."

	default:
		return "unknown scenario " + name
	}
	return f.render()
}

func runBench() {
	kinds := []string{"MESSAGE", "TOOL_CALL", "TOOL_RESULT"}
	blob := strings.Repeat("output line with some detail in it ", 12)
	rng := rand.New(rand.NewSource(1))

	for _, n := range []int{10_000, 100_000, 1_000_000} {
		r := newRing(ringCap)
		start := time.Now()
		for i := 0; i < n; i++ {
			e := event{Seq: i, Kind: kinds[rng.Intn(len(kinds))], Tool: "Bash", Text: blob}
			r.push(renderEvent(e, 60, false)...)
		}
		ingest := time.Since(start)

		frames := 0
		fstart := time.Now()
		for time.Since(fstart) < 200*time.Millisecond {
			_ = strings.Join(r.lines(), "\n")
			frames++
		}
		perFrame := time.Since(fstart) / time.Duration(frames)

		fmt.Printf("%9d events  ingest %8s  %6.2f µs/event   held %d  evicted %d   frame materialize %7s\n",
			n, ingest.Round(time.Millisecond), float64(ingest.Microseconds())/float64(n), r.len(), r.evicted, perFrame.Round(time.Microsecond))
	}
}
