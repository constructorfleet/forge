package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// ComputeRevision returns the content revision for a: sha256 of the
// canonicalized definitional representation (Kind, DerivedFrom, Sections).
// Workflow fields (Revision, State, ApprovedRevision, ApprovedBy,
// ApprovedAt) never participate.
func ComputeRevision(a *Artifact) string {
	sum := sha256.Sum256(canonicalBytes(a))
	return hex.EncodeToString(sum[:])
}

// canonicalBytes builds a deterministic byte representation of a's
// definitional content. It relies on encoding/json's guarantee that
// map[string]any keys are marshaled in sorted order at every nesting
// level, so reformatting or reordering the source file's metadata block
// never changes the result. derived_from is explicitly sorted by ID (its
// key) since it is a list, not a map, and list order is otherwise
// preserved as written. Section bodies are normalized (trailing
// whitespace stripped per line, LF line endings, no trailing blank lines)
// so reflowing whitespace does not change the revision.
func canonicalBytes(a *Artifact) []byte {
	derivedFrom := make([]DerivedFromEntry, len(a.DerivedFrom))
	copy(derivedFrom, a.DerivedFrom)
	sort.Slice(derivedFrom, func(i, j int) bool { return derivedFrom[i].ID < derivedFrom[j].ID })

	derived := make([]map[string]any, len(derivedFrom))
	for i, d := range derivedFrom {
		derived[i] = map[string]any{
			"kind":     string(d.Kind),
			"id":       d.ID,
			"revision": d.Revision,
		}
	}

	sections := make([]map[string]any, len(a.Sections))
	for i, s := range a.Sections {
		sections[i] = map[string]any{
			"heading": s.Heading,
			"body":    normalizeBody(s.Body),
		}
	}

	def := map[string]any{
		"kind":         string(a.Kind),
		"derived_from": derived,
		"sections":     sections,
	}

	b, err := json.Marshal(def)
	if err != nil {
		// def is built entirely from strings and slices/maps of strings;
		// json.Marshal cannot fail on this shape.
		panic("planning: canonicalization failed: " + err.Error())
	}
	return b
}

// normalizeBody strips trailing whitespace from every line, normalizes
// line endings to LF, and trims trailing blank lines.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
