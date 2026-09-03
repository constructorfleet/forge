package main

import (
	"fmt"
	"strings"
)

// Mirrors internal/agent.TranscriptEvent's shape; fake data only.
type event struct {
	Seq        int
	Kind       string // MESSAGE | TOOL_CALL | TOOL_RESULT | TRUNCATION
	Role       string
	Text       string
	Tool       string
	ToolCallID string
	Subagent   string
}

const (
	glyphMessage = " " // prose carries no glyph; the divider is enough
	glyphCall    = "▸" // right triangle: invocation
	glyphResult  = "└" // corner: pairs back to its call
	glyphTrunc   = "░" // shade: a window boundary, not an error
	glyphEvicted = "░"
)

// renderEvent maps one event to display lines. Tool calls collapse to a
// single line; the result folds onto the same visual group via glyphResult
// rather than repeating the tool name.
func renderEvent(e event, width int, expanded bool) []string {
	switch e.Kind {
	case "TRUNCATION":
		return []string{fmt.Sprintf("%s earlier output clipped by the emitter", glyphTrunc)}
	case "TOOL_CALL":
		head := fmt.Sprintf("%s %s(%s)", glyphCall, e.Tool, oneline(e.Text, width-len(e.Tool)-6))
		if !expanded {
			return []string{clip(head, width)}
		}
		return append([]string{clip(fmt.Sprintf("%s %s", glyphCall, e.Tool), width)}, wrap(e.Text, width-4, "    ")...)
	case "TOOL_RESULT":
		if !expanded {
			return []string{clip(fmt.Sprintf("%s %s", glyphResult, oneline(e.Text, width-4)), width)}
		}
		return append([]string{fmt.Sprintf("%s", glyphResult)}, wrap(e.Text, width-4, "    ")...)
	default:
		return wrap(e.Text, width-2, glyphMessage+" ")
	}
}

func oneline(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return clip(strings.TrimSpace(s), max)
}

func clip(s string, max int) string {
	r := []rune(s)
	if max < 4 || len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func wrap(s string, width int, prefix string) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, prefix)
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > width {
				out = append(out, prefix+line)
				line = w
				continue
			}
			line += " " + w
		}
		out = append(out, prefix+line)
	}
	return out
}
