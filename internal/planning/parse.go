package planning

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse reads a Planning Artifact file: the `<!-- forge ... -->` metadata
// block followed by `##` sections. It does not validate the artifact
// against ComputeRevision; callers that care about staleness call Stale
// separately.
func Parse(raw []byte) (*Artifact, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimLeft(text, "\n")

	if !strings.HasPrefix(text, blockOpen) {
		return nil, fmt.Errorf("planning: missing %q block", blockOpen)
	}
	closeIdx := strings.Index(text, blockClose)
	if closeIdx == -1 {
		return nil, fmt.Errorf("planning: unterminated %q block", blockOpen)
	}

	yamlText := text[len(blockOpen):closeIdx]
	var meta metaBlock
	if err := yaml.Unmarshal([]byte(yamlText), &meta); err != nil {
		return nil, fmt.Errorf("planning: parsing metadata block: %w", err)
	}

	derivedFrom := make([]DerivedFromEntry, len(meta.DerivedFrom))
	for i, d := range meta.DerivedFrom {
		derivedFrom[i] = DerivedFromEntry{Kind: Kind(d.Kind), ID: d.ID, Revision: d.Revision}
	}

	estimates := make(map[string]TicketEstimate)
	for k, v := range meta.Estimates {
		estimates[k] = TicketEstimate{Size: v.Size, Risk: v.Risk}
	}

	ticketKinds := make(map[string]TicketKind)
	for k, v := range meta.TicketKinds {
		ticketKinds[k] = v
	}

	rest := text[closeIdx+len(blockClose):]
	rest = strings.TrimLeft(rest, "\n")

	return &Artifact{
		Kind:             Kind(meta.Kind),
		Revision:         meta.Revision,
		State:            meta.State,
		ApprovedRevision: meta.ApprovedRevision,
		ApprovedBy:       meta.ApprovedBy,
		ApprovedAt:       meta.ApprovedAt,
		ReviewedRevision: meta.ReviewedRevision,
		DerivedFrom:      derivedFrom,
		Estimates:        estimates,
		TicketKinds:      ticketKinds,
		Sections:         parseSections(rest),
	}, nil
}

// ParseSections splits arbitrary markdown body text into `##` sections,
// exactly as Parse does for the body of a Planning Artifact file. Any
// content before the first `##` heading is kept as a leading Section with
// an empty Heading; if there is none, it is omitted. Callers that need to
// adopt freeform prose (e.g. `forge goal init --from`) use this to split it
// into Sections before wrapping it in a metadata block.
func ParseSections(body string) []Section {
	return parseSections(body)
}

// parseSections splits body text into `##` sections. Any content before
// the first `##` heading is kept as a leading Section with an empty
// Heading; if there is none, it is omitted.
func parseSections(body string) []Section {
	if strings.TrimSpace(body) == "" {
		return nil
	}

	lines := strings.Split(body, "\n")
	var sections []Section
	var heading string
	var buf []string
	haveSection := false

	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if heading != "" || text != "" {
			sections = append(sections, Section{Heading: heading, Body: text})
		}
		buf = nil
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if haveSection || len(buf) > 0 {
				flush()
			}
			heading = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			haveSection = true
			continue
		}
		buf = append(buf, line)
	}
	flush()

	return sections
}
