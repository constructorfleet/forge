package tracker

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ForgeProvenanceStatus is the materialization state stamped onto a
// materialized Issue's `## Forge Provenance` block (see the Materialization
// ticket: "Phase A creates all Issues in a non-executable materializing
// state"; "Phase C ... Issues become executable only once the whole graph
// validates").
type ForgeProvenanceStatus string

const (
	// ProvenanceMaterializing is the status every materialized Issue is
	// stamped with when it is first created (Phase A) and while its
	// Dependencies/provenance are being rewritten (Phase B). An Issue at
	// this status is not executable — see ValidateExecutable.
	ProvenanceMaterializing ForgeProvenanceStatus = "materializing"
	// ProvenanceReady is the status Phase C stamps onto every Issue in the
	// materialized graph, but only once the whole graph has been
	// re-fetched and validated — never earlier, and never for a subset.
	ProvenanceReady ForgeProvenanceStatus = "ready"
)

// ForgeProvenance is Forge's normalized representation of the canonical
// `## Forge Provenance` block a materialized Issue's body carries: the
// materialization Status gate plus the provenance stamp (project, spec/plan
// revision, requirement IDs, and relevant Decision references) Phase 1
// compiles its normalized context from — without ever navigating the
// planning tree (`.scratch/<feature>/`), since everything it needs is
// already stamped into the Issue body it fetched from the tracker.
type ForgeProvenance struct {
	Status       ForgeProvenanceStatus `yaml:"status"`
	TempKey      string                `yaml:"temp_key,omitempty"`
	Project      string                `yaml:"project"`
	SpecRevision string                `yaml:"spec_revision"`
	PlanRevision string                `yaml:"plan_revision"`
	Requirements []string              `yaml:"requirements,omitempty"`
	Decisions    []string              `yaml:"decisions,omitempty"`
}

const (
	forgeProvenanceHeading    = "## Forge Provenance"
	forgeProvenanceBlockOpen  = "<!-- forge"
	forgeProvenanceBlockClose = "-->"
)

var (
	reProvenanceHeader         = regexp.MustCompile(`(?i)^##\s+Forge Provenance\s*$`)
	reProvenanceNearMissHeader = regexp.MustCompile(`(?i)^##\s+Forge Provenance\b`)
)

// ParseForgeProvenance extracts the `## Forge Provenance` block from an
// Issue body, mirroring ParseDependencyBlock's strict-syntax, fail-closed
// approach: freeform content where the block is expected is a deterministic
// error rather than a best-effort guess. If the body contains no `## Forge
// Provenance` heading at all, it returns (nil, nil) — the Issue is simply
// not materialization-gated (e.g. it predates materialization, or was
// created outside Forge's planning compiler).
func ParseForgeProvenance(body string) (*ForgeProvenance, error) {
	lines := strings.Split(body, "\n")

	for i, line := range lines {
		normalized := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if !reProvenanceHeader.MatchString(normalized) {
			if reProvenanceNearMissHeader.MatchString(normalized) {
				return nil, fmt.Errorf(
					"tracker: invalid %q heading syntax: %q (expected exactly %q)",
					forgeProvenanceHeading, normalized, forgeProvenanceHeading,
				)
			}
			continue
		}

		rest := lines[i+1:]
		blockStart := -1
		for j, l := range rest {
			t := strings.TrimSpace(strings.TrimRight(l, "\r"))
			if t == "" {
				continue
			}
			if strings.HasPrefix(t, "#") {
				// Next heading reached with no block found.
				return nil, fmt.Errorf(
					"tracker: %q block missing its %q metadata comment",
					forgeProvenanceHeading, forgeProvenanceBlockOpen,
				)
			}
			if t == forgeProvenanceBlockOpen {
				blockStart = j
				break
			}
			return nil, fmt.Errorf(
				"tracker: unexpected content before %q in %q block: %q",
				forgeProvenanceBlockOpen, forgeProvenanceHeading, t,
			)
		}
		if blockStart == -1 {
			return nil, fmt.Errorf(
				"tracker: %q block missing its %q metadata comment",
				forgeProvenanceHeading, forgeProvenanceBlockOpen,
			)
		}

		var yamlLines []string
		closed := false
		for _, l := range rest[blockStart+1:] {
			t := strings.TrimRight(l, "\r")
			if strings.TrimSpace(t) == forgeProvenanceBlockClose {
				closed = true
				break
			}
			yamlLines = append(yamlLines, t)
		}
		if !closed {
			return nil, fmt.Errorf(
				"tracker: %q block missing its closing %q",
				forgeProvenanceHeading, forgeProvenanceBlockClose,
			)
		}

		var p ForgeProvenance
		if err := yaml.Unmarshal([]byte(strings.Join(yamlLines, "\n")), &p); err != nil {
			return nil, fmt.Errorf("tracker: invalid %q metadata: %w", forgeProvenanceHeading, err)
		}
		if p.Status != ProvenanceMaterializing && p.Status != ProvenanceReady {
			return nil, fmt.Errorf(
				"tracker: %q has invalid status %q (expected %q or %q)",
				forgeProvenanceHeading, p.Status, ProvenanceMaterializing, ProvenanceReady,
			)
		}
		return &p, nil
	}

	return nil, nil
}

// RenderForgeProvenance renders p as the canonical `## Forge Provenance`
// body section, ready to be concatenated into an Issue body.
func RenderForgeProvenance(p ForgeProvenance) string {
	out, err := yaml.Marshal(p)
	if err != nil {
		// p is a fixed, statically-typed struct of strings/slices; yaml.Marshal
		// only fails on unsupported types (channels, funcs, cyclic refs), none
		// of which ForgeProvenance can contain.
		panic(fmt.Sprintf("tracker: render forge provenance: %v", err))
	}
	var b strings.Builder
	b.WriteString(forgeProvenanceHeading)
	b.WriteString("\n")
	b.WriteString(forgeProvenanceBlockOpen)
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(string(out), "\n"))
	b.WriteString("\n")
	b.WriteString(forgeProvenanceBlockClose)
	b.WriteString("\n")
	return b.String()
}
