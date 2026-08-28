package planningagent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
)

// NamedArtifact pairs a Planning Artifact with the caller-assigned ID that
// identifies it (a decision's slug, or "goal"/"spec"/"ticket-plan" for the
// singleton kinds): planning.Artifact carries content but no identity of its
// own.
type NamedArtifact struct {
	ID       string
	Artifact *planning.Artifact
}

// ArtifactView is the typed, normalized projection of one Planning Artifact
// that a PlanningContext exposes to agents: definitional content only,
// sections keyed by heading -- never raw markdown.
type ArtifactView struct {
	ID          string
	Kind        planning.Kind
	Sections    map[string]string
	DerivedFrom []planning.DerivedFromEntry
}

// PlanningContext is the typed, normalized data a Phase 2 planning contract
// is compiled against: the reused Phase 1 Repository Context, the Planning
// Artifacts relevant to a Feature projected into typed views rather than raw
// markdown, and free-form human inputs collected for this compilation. It is
// scheduler-agnostic by construction (ticket 12): it carries no methods and
// knows nothing about how or when a caller invokes it.
type PlanningContext struct {
	Repository  agent.RepositoryContext
	Goal        *ArtifactView
	Decisions   []ArtifactView
	Spec        *ArtifactView
	TicketPlan  *ArtifactView
	HumanInputs map[string]string

	// ContextRevision is this PlanningContext's cache key: a hash of every
	// compiled artifact's (kind, ID, content revision) plus the Repository
	// Context's BaseRevision. Recompiling from unchanged sources always
	// reproduces the same key; it changes if and only if a source revision
	// changes, so callers can cache on it directly.
	ContextRevision string
}

// Compile produces a PlanningContext from repo, the Planning Artifacts
// relevant to one Feature, and humanInputs. Each artifact's *current*
// content revision (planning.ComputeRevision, not any possibly-stale
// recorded Artifact.Revision) feeds ContextRevision, so a hand-edit that
// hasn't been re-rendered still invalidates the cache immediately. Compile
// rejects a nil artifact, a blank ID, a duplicate ID, an unrecognized Kind,
// and more than one artifact for a singleton kind (goal, spec, ticket-plan)
// -- each would otherwise silently overwrite the last one compiled.
func Compile(repo agent.RepositoryContext, artifacts []NamedArtifact, humanInputs map[string]string) (PlanningContext, error) {
	pc := PlanningContext{
		Repository:  repo,
		HumanInputs: cloneStringMap(humanInputs),
	}

	type revEntry struct {
		kind     string
		id       string
		revision string
	}
	var revisions []revEntry
	seenIDs := make(map[string]bool, len(artifacts))

	for _, na := range artifacts {
		if na.Artifact == nil {
			return PlanningContext{}, fmt.Errorf("planningagent: artifact %q is nil", na.ID)
		}
		if na.ID == "" {
			return PlanningContext{}, fmt.Errorf("planningagent: artifact of kind %q has a blank ID", na.Artifact.Kind)
		}
		if seenIDs[na.ID] {
			return PlanningContext{}, fmt.Errorf("planningagent: duplicate artifact ID %q", na.ID)
		}
		seenIDs[na.ID] = true

		view := ArtifactView{
			ID:          na.ID,
			Kind:        na.Artifact.Kind,
			Sections:    sectionMap(na.Artifact.Sections),
			DerivedFrom: append([]planning.DerivedFromEntry(nil), na.Artifact.DerivedFrom...),
		}
		revisions = append(revisions, revEntry{
			kind:     string(na.Artifact.Kind),
			id:       na.ID,
			revision: planning.ComputeRevision(na.Artifact),
		})

		switch na.Artifact.Kind {
		case planning.KindGoal:
			if pc.Goal != nil {
				return PlanningContext{}, fmt.Errorf("planningagent: more than one goal artifact (%q and %q)", pc.Goal.ID, na.ID)
			}
			v := view
			pc.Goal = &v
		case planning.KindDecision:
			pc.Decisions = append(pc.Decisions, view)
		case planning.KindSpec:
			if pc.Spec != nil {
				return PlanningContext{}, fmt.Errorf("planningagent: more than one spec artifact (%q and %q)", pc.Spec.ID, na.ID)
			}
			v := view
			pc.Spec = &v
		case planning.KindTicketPlan:
			if pc.TicketPlan != nil {
				return PlanningContext{}, fmt.Errorf("planningagent: more than one ticket-plan artifact (%q and %q)", pc.TicketPlan.ID, na.ID)
			}
			v := view
			pc.TicketPlan = &v
		default:
			return PlanningContext{}, fmt.Errorf("planningagent: artifact %q has unrecognized kind %q", na.ID, na.Artifact.Kind)
		}
	}

	sort.Slice(pc.Decisions, func(i, j int) bool { return pc.Decisions[i].ID < pc.Decisions[j].ID })
	sort.Slice(revisions, func(i, j int) bool {
		if revisions[i].kind != revisions[j].kind {
			return revisions[i].kind < revisions[j].kind
		}
		return revisions[i].id < revisions[j].id
	})

	h := sha256.New()
	for _, r := range revisions {
		fmt.Fprintf(h, "%s\x1f%s\x1f%s\x1e", r.kind, r.id, r.revision)
	}
	fmt.Fprintf(h, "base\x1f%s\x1e", repo.BaseRevision)
	pc.ContextRevision = hex.EncodeToString(h.Sum(nil))

	return pc, nil
}

// sectionMap projects sections into a heading->body map. A section with an
// empty Heading (leading content before the first `##`) is dropped: it has
// no key an agent could reference.
func sectionMap(sections []planning.Section) map[string]string {
	var m map[string]string
	for _, s := range sections {
		if s.Heading == "" {
			continue
		}
		if m == nil {
			m = make(map[string]string, len(sections))
		}
		m[s.Heading] = s.Body
	}
	return m
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
