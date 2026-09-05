package planning

import (
	"bytes"
	"gopkg.in/yaml.v3"
	"sort"
)

// Render serializes a into canonical Planning Artifact form: the
// `<!-- forge ... -->` metadata block (fixed field order, derived_from
// sorted by ID) followed by a blank line and the `##` sections in order,
// each with a normalized body. Rendering a hand-aligned artifact
// re-serializes it to this canonical form regardless of the original
// formatting.
func Render(a *Artifact) []byte {
	derivedFrom := make([]DerivedFromEntry, len(a.DerivedFrom))
	copy(derivedFrom, a.DerivedFrom)
	sort.Slice(derivedFrom, func(i, j int) bool { return derivedFrom[i].ID < derivedFrom[j].ID })

	derived := make([]derivedYAML, len(derivedFrom))
	for i, d := range derivedFrom {
		derived[i] = derivedYAML{Kind: string(d.Kind), ID: d.ID, Revision: d.Revision}
	}

	estimates := make(map[string]TicketEstimate)
	for k, v := range a.Estimates {
		estimates[k] = TicketEstimate{Size: v.Size, Risk: v.Risk}
	}

	ticketKinds := make(map[string]TicketKind)
	for k, v := range a.TicketKinds {
		ticketKinds[k] = v
	}

	meta := metaBlock{
		Revision:         a.Revision,
		State:            a.State,
		ApprovedRevision: a.ApprovedRevision,
		ApprovedBy:       a.ApprovedBy,
		ApprovedAt:       a.ApprovedAt,
		ReviewedRevision: a.ReviewedRevision,
		Kind:             string(a.Kind),
		DerivedFrom:      derived,
		Estimates:        estimates,
		TicketKinds:      ticketKinds,
	}

	yamlBytes, err := yaml.Marshal(&meta)
	if err != nil {
		// meta is built entirely from strings and a slice of strings;
		// yaml.Marshal cannot fail on this shape.
		panic("planning: render failed: " + err.Error())
	}

	var buf bytes.Buffer
	buf.WriteString(blockOpen)
	buf.WriteByte('\n')
	buf.Write(yamlBytes)
	buf.WriteString(blockClose)
	buf.WriteByte('\n')

	for _, s := range a.Sections {
		buf.WriteByte('\n')
		body := normalizeBody(s.Body)
		if s.Heading != "" {
			buf.WriteString("## ")
			buf.WriteString(s.Heading)
			buf.WriteByte('\n')
			if body != "" {
				buf.WriteByte('\n')
			}
		}
		if body != "" {
			buf.WriteString(body)
			buf.WriteByte('\n')
		}
	}

	out := bytes.TrimRight(buf.Bytes(), "\n")
	out = append(out, '\n')
	return out
}
