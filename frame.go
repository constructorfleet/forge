package main

import (
	"fmt"
	"strings"
	"time"
)

type worker struct {
	IssueID  string
	Title    string
	State    string // verbatim IssueState
	Group    string // canonical grouping (#453): Working | Blocked | Finished
	Elapsed  time.Duration
	Attempt  int
	Tool     string // current tool, "" when none
	Live     bool          // heartbeat within 15s (#453)
	BeatAge  time.Duration // since last heartbeat -- a different clock from Elapsed
	Attn     bool   // needs the operator
	Planning bool
}

type frame struct {
	Title    string
	Workers  []worker
	Sel      int
	Buf      *ring
	Follow   bool
	Height   int
	Compact  bool
	Width    int
	Banner   string // transient control feedback (#447)
	Backfill bool   // history evicted before the retained window (#449)
}

// attnGlyph draws the eye to the Worker that needs attention (question 4).
// Colour is not load-bearing: the glyph carries the signal so the frame
// survives a no-colour terminal.
func attnGlyph(w worker) string {
	switch {
	case w.Attn:
		return "!"
	case w.Planning:
		return " " // planning has no liveness claim at all (#443)
	case !w.Live:
		return "?"
	case w.Tool != "":
		return "*"
	default:
		return " "
	}
}

func (f frame) render() string {
	listW := 44
	if f.Compact {
		listW = 44
	}
	tranW := f.Width - listW - 3
	body := f.Height - 4
	if f.Compact {
		body--
	}

	rows := make([]string, 0, 2*len(f.Workers))
	for i, w := range f.Workers {
		cursor := " "
		if i == f.Sel {
			cursor = ">"
		}
		live := "\u2022"
		switch {
		case w.Planning:
			live = " " // no claim, so no badge
		case !w.Live:
			live = "\u00d7"
		}
		att := fmt.Sprintf("%d", w.Attempt)
		if w.Attempt == 1 {
			att = " "
		}
		if f.Compact {
			rows = append(rows, clip(fmt.Sprintf("%s%s%s %-*s %5s %s",
				cursor, attnGlyph(w), live, listW-13, clip(w.Title, listW-13), short(w.Elapsed), att), listW))
			continue
		}
		head := fmt.Sprintf("%s%s%s %s", cursor, attnGlyph(w), live, clip(w.Title, listW-5))
		meta := fmt.Sprintf("    %-9s %5s", w.Group, short(w.Elapsed))
		if w.Attempt > 1 {
			meta += fmt.Sprintf("  attempt %d", w.Attempt)
		}
		rows = append(rows, clip(head, listW), clip(meta, listW))
	}

	lines := f.Buf.lines()
	if f.Backfill || f.Buf.evicted > 0 {
		lines = append([]string{fmt.Sprintf("%s earlier events not retained", glyphEvicted)}, lines...)
	}
	if len(lines) > body {
		lines = lines[len(lines)-body:]
	}

	var b strings.Builder
	tail := "FOLLOW"
	if !f.Follow {
		tail = "PAUSED"
	}
	b.WriteString(clip(fmt.Sprintf("%s  —  %s", f.Title, tail), f.Width) + "\n")
	b.WriteString(strings.Repeat("─", f.Width) + "\n")
	for i := 0; i < body; i++ {
		l, r := "", ""
		if i < len(rows) {
			l = rows[i]
		}
		if i < len(lines) {
			r = lines[i]
		}
		b.WriteString(fmt.Sprintf("%-*s │ %s\n", listW, l, clip(r, tranW)))
	}
	b.WriteString(strings.Repeat("─", f.Width) + "\n")
	if f.Compact && len(f.Workers) > 0 {
		b.WriteString(clip("  "+f.detail(), f.Width) + "\n")
	}
	if f.Banner != "" {
		b.WriteString(clip("  "+f.Banner, f.Width))
	} else {
		b.WriteString(clip(f.footer(), f.Width))
	}
	return b.String()
}

// footer names only the actions legal for the selected Worker -- planning
// has no cancel at all (#443).
func (f frame) footer() string {
	keys := []string{"j/k move", "f follow", "q stop watching"}
	if len(f.Workers) > 0 {
		w := f.Workers[f.Sel]
		switch {
		case w.Attn:
			keys = append([]string{"a answer"}, keys...)
		case w.Planning:
			keys = append([]string{"A approve"}, keys...)
		}
		if !w.Planning && w.Group == "Working" {
			keys = append(keys, "c cancel")
		}
		if w.Group == "Finished" && w.State == "FAILED" {
			keys = append(keys, "r retry")
		}
	}
	return "  " + strings.Join(keys, "   ")
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02d", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02d", int(d.Hours()), int(d.Minutes())%60)
	}
}

// tranWidth is the transcript pane's usable width, so fixtures wrap text
// exactly as the frame will.
func (f frame) tranWidth() int {
	if f.Compact {
		return f.Width - 47
	}
	return f.Width - 47
}

// detail carries the selected Worker's verbatim state, so the list can stay
// coarse (#453) without hiding the real state from the operator.
func (f frame) detail() string {
	w := f.Workers[f.Sel]
	parts := []string{w.Title, w.State}
	if w.Attempt > 1 {
		parts = append(parts, fmt.Sprintf("attempt %d", w.Attempt))
	}
	if w.Tool != "" {
		parts = append(parts, "running "+w.Tool)
	}
	switch {
	case w.Planning:
		parts = append(parts, "last activity "+short(w.Elapsed)+" ago")
	case w.Live:
		parts = append(parts, "beat "+short(w.BeatAge)+" ago")
	default:
		parts = append(parts, "no beat for "+short(w.BeatAge))
	}
	return strings.Join(parts, "  ·  ")
}
