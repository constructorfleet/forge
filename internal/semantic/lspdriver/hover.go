package lspdriver

import (
	"regexp"
	"strings"

	"go.lsp.dev/protocol"
)

const fence = "```"

// pyrightAnnotation matches the leading "(function)"/"(class)"/etc. kind
// annotation pyright prefixes its hover signature with.
var pyrightAnnotation = regexp.MustCompile(`^\([a-zA-Z]+\)\s*`)

// splitHoverContents separates a Hover's contents into a signature and
// documentation, per style. Non-markdown hover shapes degrade to returning
// their text as documentation with no signature, regardless of style.
func splitHoverContents(style HoverStyle, contents protocol.HoverContents) (signature, documentation string) {
	switch v := contents.(type) {
	case *protocol.MarkupContent:
		if v == nil {
			return "", ""
		}
		return splitHoverMarkdown(style, v.Value)
	case protocol.String:
		return "", string(v)
	case protocol.MarkedStringSlice:
		var docs []string
		for _, m := range v {
			if s, ok := m.(protocol.String); ok {
				docs = append(docs, string(s))
			}
		}
		return "", strings.Join(docs, "\n")
	default:
		return "", ""
	}
}

// splitHoverMarkdown splits a hover's raw markdown value into a signature
// and documentation, per the server's HoverStyle.
func splitHoverMarkdown(style HoverStyle, value string) (signature, documentation string) {
	switch style {
	case HoverStyleRustTwoFence:
		return splitMarkdownRustTwoFence(value)
	case HoverStylePyrightAnnotated:
		return splitMarkdownPyrightAnnotated(value)
	default:
		return splitMarkdown(value)
	}
}

// splitMarkdown pulls the first ```-fenced block out of value as the
// signature, treating everything else as documentation. This is the
// firstFence HoverStyle's behavior (go, typescript).
func splitMarkdown(value string) (signature, documentation string) {
	start := strings.Index(value, fence)
	if start == -1 {
		return "", strings.TrimSpace(value)
	}

	before := strings.TrimSpace(value[:start])
	rest := value[start+len(fence):]
	if nl := strings.Index(rest, "\n"); nl != -1 {
		rest = rest[nl+1:]
	}

	end := strings.Index(rest, fence)
	if end == -1 {
		return strings.TrimSpace(rest), before
	}

	signature = strings.TrimSpace(rest[:end])
	after := strings.TrimSpace(rest[end+len(fence):])

	documentation = strings.TrimSpace(strings.Join([]string{before, after}, "\n"))
	return signature, documentation
}

// splitMarkdownRustTwoFence takes the second fenced code block as the
// signature — rust-analyzer's first fence is the crate/module path, not
// the signature — and joins everything else (the crate path and any
// surrounding prose) as documentation.
func splitMarkdownRustTwoFence(value string) (signature, documentation string) {
	before, firstFence, rest, ok := cutFence(value)
	if !ok {
		return "", strings.TrimSpace(value)
	}

	between, secondFence, after, ok := cutFence(rest)
	if !ok {
		// Only one fence present; degrade to treating it as the signature.
		return strings.TrimSpace(firstFence), strings.TrimSpace(before)
	}

	documentation = strings.TrimSpace(strings.Join(
		nonEmpty(before, firstFence, between, after), "\n",
	))
	return strings.TrimSpace(secondFence), documentation
}

// splitMarkdownPyrightAnnotated splits like splitMarkdown, then strips
// pyright's leading kind annotation from the signature and cuts the
// documentation at the "---" horizontal rule pyright places before the
// docstring, keeping only what follows it.
func splitMarkdownPyrightAnnotated(value string) (signature, documentation string) {
	sig, doc := splitMarkdown(value)
	signature = pyrightAnnotation.ReplaceAllString(sig, "")

	if idx := strings.Index(doc, "---"); idx != -1 {
		doc = doc[idx+len("---"):]
	}
	documentation = strings.TrimSpace(doc)
	return signature, documentation
}

// cutFence finds the next ```-fenced block in value, returning the prose
// before it, the fence's content (with its language tag line stripped),
// and everything after the closing fence. ok is false if value has no
// complete fenced block.
func cutFence(value string) (before, content, after string, ok bool) {
	start := strings.Index(value, fence)
	if start == -1 {
		return "", "", "", false
	}
	before = value[:start]
	rest := value[start+len(fence):]
	if nl := strings.Index(rest, "\n"); nl != -1 {
		rest = rest[nl+1:]
	}

	end := strings.Index(rest, fence)
	if end == -1 {
		return before, rest, "", false
	}
	content = rest[:end]
	after = rest[end+len(fence):]
	return before, content, after, true
}

// nonEmpty returns the parts whose trimmed content is non-empty, so
// joining them doesn't produce stray blank lines.
func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
