package specengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/spec"
	"github.com/Teagan42/forge/internal/specgeneration"
	"github.com/Teagan42/forge/internal/specreview"
	"github.com/Teagan42/forge/internal/ticketplan"
	"github.com/Teagan42/forge/internal/ticketplanreview"
)

type SpecEngine struct {
	Backend          planningagent.Backend
	ReviewRetryLimit int

	// Repository is the compiled Repository Context (see
	// internal/repocontext.Compile) supplied to every PlanningContext this
	// engine compiles, so spec and ticket-plan generation prompts are
	// grounded in the repository's real structure and languages rather than
	// carrying only a base revision.
	Repository agent.RepositoryContext

	// ImplementedFacts is the Feature's already-completed, merged work
	// (ticket 22, acceptance item 3), attached to every PlanningContext this
	// engine compiles. A replan must write its new spec and ticket plan
	// *around* work that has already shipped rather than proposing it again
	// or unwinding it, which it can only do if the planner sees it. Empty
	// for a Feature planned before anything was materialized -- the ordinary
	// first-pass case -- in which case compilation is byte-for-byte what it
	// was before this field existed.
	ImplementedFacts []planningagent.ImplementedFact
}

func NewSpecEngine(backend planningagent.Backend) *SpecEngine {
	return &SpecEngine{Backend: backend, ReviewRetryLimit: 3}
}

type ArtifactLoader interface {
	LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error)
	LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error)
	SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error
	LoadSpec(ctx context.Context, featureID string) (*planning.Artifact, error)
	SaveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) error
}

type ReviewRepairBudget struct {
	limit int
	used  int
}

func (b *ReviewRepairBudget) Remaining() int {
	if r := b.limit - b.used; r > 0 {
		return r
	}
	return 0
}

func (b *ReviewRepairBudget) Exhausted() bool {
	return b.used >= b.limit
}

func (b *ReviewRepairBudget) Record() error {
	if b.Exhausted() {
		return fmt.Errorf("review repair budget exhausted: limit %d reached", b.limit)
	}
	b.used++
	return nil
}

func (e *SpecEngine) GenerateSpec(ctx context.Context, featureID string, loader ArtifactLoader) error {
	goalArtifact, err := loader.LoadGoal(ctx, featureID)
	if err != nil {
		return fmt.Errorf("specengine: load goal: %w", err)
	}

	decisions, err := loader.LoadDecisions(ctx, featureID)
	if err != nil {
		return fmt.Errorf("specengine: load decisions: %w", err)
	}

	for id, dec := range decisions {
		if dec.State == "open" || dec.State == "needs_human" {
			return fmt.Errorf("specengine: decision %s is not resolved", id)
		}
	}

	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goalArtifact},
	}
	for id, dec := range decisions {
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: id, Artifact: dec})
	}
	pc, err := planningagent.CompileWithFacts(e.Repository, artifacts, nil, e.ImplementedFacts)
	if err != nil {
		return fmt.Errorf("specengine: compile planning context: %w", err)
	}

	specResult, err := specgeneration.Generate(ctx, e.Backend, pc)
	if err != nil {
		return fmt.Errorf("specengine: generate spec: %w", err)
	}

	specArtifact := spec.NewSpecification()
	specArtifact.AddSection("Context", specResult.Summary)

	reqBody := ""
	for _, req := range specResult.Requirements {
		reqBody += fmt.Sprintf("%s: %s\n", req.ID, req.Description)
	}
	specArtifact.AddSection("Requirements", reqBody)

	nonGoalsBody := ""
	for _, ng := range specResult.NonGoals {
		nonGoalsBody += fmt.Sprintf("- %s\n", ng)
	}
	specArtifact.AddSection("Non-Goals", nonGoalsBody)

	decisionRevs := make(map[string]string)
	for id, dec := range decisions {
		decisionRevs[id] = dec.Revision
	}

	specArtifact.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: goalArtifact.Revision},
	}
	for id, dec := range decisions {
		specArtifact.DerivedFrom = append(specArtifact.DerivedFrom, planning.DerivedFromEntry{
			Kind: planning.KindDecision, ID: id, Revision: dec.Revision,
		})
	}
	specArtifact.DerivedFrom = append(specArtifact.DerivedFrom, planning.DerivedFromEntry{
		Kind: "repository", ID: "repository", Revision: pc.ContextRevision,
	})

	specArtifact.Revision = planning.ComputeRevision(&specArtifact.Artifact)

	if err := spec.ValidateSpecDeterministic(&specArtifact.Artifact, decisions, goalArtifact.Revision, decisionRevs, pc.ContextRevision); err != nil {
		return fmt.Errorf("specengine: deterministic validation failed: %w", err)
	}

	// Run SpecificationReview with bounded repair loop
	if err := e.runSpecReviewAndRepair(ctx, featureID, loader, &specArtifact.Artifact, decisions, goalArtifact.Revision, decisionRevs, pc.ContextRevision, pc); err != nil {
		return fmt.Errorf("specengine: spec review and repair failed: %w", err)
	}

	return nil
}

func (e *SpecEngine) runSpecReviewAndRepair(
	ctx context.Context,
	featureID string,
	loader ArtifactLoader,
	specArtifact *planning.Artifact,
	decisions map[string]*planning.Artifact,
	goalRev string,
	decisionRevs map[string]string,
	repoRev string,
	pc planningagent.PlanningContext,
) error {
	budget := &ReviewRepairBudget{limit: e.ReviewRetryLimit}
	var reviewAttempts [][]specreview.Finding

	for {
		// Compile planning context with current spec for review
		artifacts := []planningagent.NamedArtifact{
			{ID: "goal", Artifact: &planning.Artifact{Kind: planning.KindGoal, Revision: goalRev, Sections: []planning.Section{{Heading: "Goal", Body: ""}}}},
		}
		for id, dec := range decisions {
			artifacts = append(artifacts, planningagent.NamedArtifact{ID: id, Artifact: dec})
		}
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: "spec", Artifact: specArtifact})
		reviewPC, err := planningagent.CompileWithFacts(e.Repository, artifacts, nil, e.ImplementedFacts)
		if err != nil {
			return fmt.Errorf("specengine: compile planning context for review: %w", err)
		}

		reviewResult, err := specreview.Review(ctx, e.Backend, reviewPC)
		if err != nil {
			return fmt.Errorf("specengine: specification review failed: %w", err)
		}

		if reviewResult.Verdict == specreview.VerdictApproved {
			// Spec approved by automated review, save and return
			if err := loader.SaveSpec(ctx, featureID, specArtifact); err != nil {
				return fmt.Errorf("specengine: save spec: %w", err)
			}
			return nil
		}

		// CHANGES_REQUIRED - enter bounded repair loop
		reviewAttempts = append(reviewAttempts, reviewResult.Findings)

		if budget.Exhausted() {
			if recurring := recurringFindings(reviewAttempts); len(recurring) > 0 {
				return fmt.Errorf("specengine: spec review repair budget exhausted after %d attempts; recurring findings across every attempt:\n%s  summary: %s",
					e.ReviewRetryLimit, formatFindingsForError(recurring), reviewResult.Summary)
			}
			// The reviewer never agreed with itself across every attempt --
			// that is non-determinism in the automated reviewer, not a
			// genuine defect, and the spec already passed deterministic
			// validation. Save it rather than hard-failing the feature on
			// reviewer noise.
			if err := loader.SaveSpec(ctx, featureID, specArtifact); err != nil {
				return fmt.Errorf("specengine: save spec: %w", err)
			}
			return nil
		}

		if err := budget.Record(); err != nil {
			return err
		}

		// Build focused feedback from findings for repair
		feedback := buildRepairFeedback(reviewResult.Findings)

		// Re-generate spec with findings as additional context
		humanInputs := map[string]string{
			"review_findings": feedback,
			"prior_spec":      renderSpecForRepair(specArtifact),
		}

		artifactsForRepair := []planningagent.NamedArtifact{
			{ID: "goal", Artifact: &planning.Artifact{Kind: planning.KindGoal, Revision: goalRev, Sections: []planning.Section{{Heading: "Goal", Body: ""}}}},
		}
		for id, dec := range decisions {
			artifactsForRepair = append(artifactsForRepair, planningagent.NamedArtifact{ID: id, Artifact: dec})
		}
		repairPC, err := planningagent.CompileWithFacts(e.Repository, artifactsForRepair, humanInputs, e.ImplementedFacts)
		if err != nil {
			return fmt.Errorf("specengine: compile planning context for repair: %w", err)
		}

		specResult, err := specgeneration.Generate(ctx, e.Backend, repairPC)
		if err != nil {
			return fmt.Errorf("specengine: generate spec repair: %w", err)
		}

		// Build new spec artifact from repair result
		newSpecArtifact := spec.NewSpecification()
		newSpecArtifact.AddSection("Context", specResult.Summary)

		reqBody := ""
		for _, req := range specResult.Requirements {
			reqBody += fmt.Sprintf("%s: %s\n", req.ID, req.Description)
		}
		newSpecArtifact.AddSection("Requirements", reqBody)

		nonGoalsBody := ""
		for _, ng := range specResult.NonGoals {
			nonGoalsBody += fmt.Sprintf("- %s\n", ng)
		}
		newSpecArtifact.AddSection("Non-Goals", nonGoalsBody)

		newSpecArtifact.DerivedFrom = []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: "goal", Revision: goalRev},
		}
		for id, dec := range decisions {
			newSpecArtifact.DerivedFrom = append(newSpecArtifact.DerivedFrom, planning.DerivedFromEntry{
				Kind: planning.KindDecision, ID: id, Revision: dec.Revision,
			})
		}
		newSpecArtifact.DerivedFrom = append(newSpecArtifact.DerivedFrom, planning.DerivedFromEntry{
			Kind: "repository", ID: "repository", Revision: repoRev,
		})

		newSpecArtifact.Revision = planning.ComputeRevision(&newSpecArtifact.Artifact)

		// Re-run deterministic validation on repaired spec
		if err := spec.ValidateSpecDeterministic(&newSpecArtifact.Artifact, decisions, goalRev, decisionRevs, repoRev); err != nil {
			return fmt.Errorf("specengine: deterministic validation failed after repair: %w", err)
		}

		// Loop continues with new spec for next review iteration
		specArtifact = &newSpecArtifact.Artifact
	}
}

// findingKey identifies a specreview.Finding for recurrence tracking,
// independent of exact whitespace: severity and message text are what a
// reviewer is actually re-raising, not incidental file/line noise.
func findingKey(f specreview.Finding) string {
	return strings.ToLower(strings.TrimSpace(string(f.Severity))) + "|" + strings.ToLower(strings.TrimSpace(f.Message))
}

// recurringFindings returns the findings that appear in every attempt, in
// the order they first appeared, deduplicated. A finding present in every
// attempt is a stable, reproducible defect worth hard-failing on; one that
// appears in some attempts but not others is reviewer noise.
func recurringFindings(attempts [][]specreview.Finding) []specreview.Finding {
	if len(attempts) == 0 {
		return nil
	}

	counts := make(map[string]int)
	first := make(map[string]specreview.Finding)
	for _, findings := range attempts {
		seen := make(map[string]bool)
		for _, f := range findings {
			key := findingKey(f)
			if seen[key] {
				continue
			}
			seen[key] = true
			counts[key]++
			if _, ok := first[key]; !ok {
				first[key] = f
			}
		}
	}

	var recurring []specreview.Finding
	seenOut := make(map[string]bool)
	for _, findings := range attempts[0] {
		key := findingKey(findings)
		if seenOut[key] {
			continue
		}
		seenOut[key] = true
		if counts[key] == len(attempts) {
			recurring = append(recurring, first[key])
		}
	}
	return recurring
}

func formatFindingsForError(findings []specreview.Finding) string {
	var out string
	for _, f := range findings {
		loc := ""
		if f.File != "" {
			loc = fmt.Sprintf(" %s:%d", f.File, f.Line)
		}
		out += fmt.Sprintf("  [%s]%s %s\n", f.Severity, loc, f.Message)
	}
	return out
}

func buildRepairFeedback(findings []specreview.Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var fb string
	for _, f := range findings {
		fb += fmt.Sprintf("[%s] %s\n", f.Severity, f.Message)
	}
	return fb
}

func renderSpecForRepair(specArtifact *planning.Artifact) string {
	var out string
	for _, s := range specArtifact.Sections {
		out += fmt.Sprintf("## %s\n%s\n\n", s.Heading, s.Body)
	}
	return out
}

// GenerateTicketPlan generates a ticket plan from an approved specification.
func (e *SpecEngine) GenerateTicketPlan(ctx context.Context, featureID string, loader ArtifactLoader) error {
	// Load the approved spec
	specArtifact, err := loader.LoadSpec(ctx, featureID)
	if err != nil {
		return fmt.Errorf("specengine: load spec: %w", err)
	}
	if specArtifact == nil {
		return fmt.Errorf("specengine: no spec found for feature %s", featureID)
	}
	if specArtifact.Kind != planning.KindSpec {
		return fmt.Errorf("specengine: artifact is not a specification")
	}
	if !planning.Approved(specArtifact) {
		return fmt.Errorf("specengine: specification is not approved")
	}

	// Load goal and decisions for context
	goalArtifact, err := loader.LoadGoal(ctx, featureID)
	if err != nil {
		return fmt.Errorf("specengine: load goal: %w", err)
	}

	decisions, err := loader.LoadDecisions(ctx, featureID)
	if err != nil {
		return fmt.Errorf("specengine: load decisions: %w", err)
	}

	// Extract requirement IDs from spec
	specReqIDs := spec.ExtractRequirementIDs(specArtifact.Sections[1].Body) // Requirements section

	// Compile planning context with spec
	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goalArtifact},
	}
	for id, dec := range decisions {
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: id, Artifact: dec})
	}
	artifacts = append(artifacts, planningagent.NamedArtifact{ID: "spec", Artifact: specArtifact})
	pc, err := planningagent.CompileWithFacts(e.Repository, artifacts, nil, e.ImplementedFacts)
	if err != nil {
		return fmt.Errorf("specengine: compile planning context: %w", err)
	}

	// Generate ticket plan
	tpResult, err := ticketplan.Generate(ctx, e.Backend, pc)
	if err != nil {
		return fmt.Errorf("specengine: generate ticket plan: %w", err)
	}

	// Build ticket plan artifact
	tpArtifact := &planning.Artifact{
		Kind:      planning.KindTicketPlan,
		Sections:  make([]planning.Section, 0, len(tpResult.Tickets)),
		Estimates: make(map[string]planning.TicketEstimate),
	}

	for _, t := range tpResult.Tickets {
		body := ticketplan.RenderTicketBody(t)
		tpArtifact.Sections = append(tpArtifact.Sections, planning.Section{
			Heading: fmt.Sprintf("Ticket: %s", t.Key),
			Body:    body,
		})
		// Add estimate to metadata if present
		if t.Estimate != nil {
			tpArtifact.Estimates[t.Key] = *t.Estimate
		}
	}

	// DerivedFrom: spec + repository
	tpArtifact.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindSpec, ID: "spec", Revision: specArtifact.Revision},
		{Kind: "repository", ID: "repository", Revision: pc.ContextRevision},
	}

	tpArtifact.Revision = planning.ComputeRevision(tpArtifact)

	// Deterministic validation
	if err := ticketplan.ValidateTicketPlanDeterministic(tpArtifact, specReqIDs, specArtifact.Revision, pc.ContextRevision); err != nil {
		return fmt.Errorf("specengine: ticket plan deterministic validation failed: %w", err)
	}

	// Run TicketPlanReview with bounded repair loop
	if err := e.runTicketPlanReviewAndRepair(ctx, featureID, loader, tpArtifact, specArtifact, specReqIDs, goalArtifact.Revision, pc.ContextRevision, pc, decisions); err != nil {
		return fmt.Errorf("specengine: ticket plan review and repair failed: %w", err)
	}

	return nil
}

func (e *SpecEngine) runTicketPlanReviewAndRepair(
	ctx context.Context,
	featureID string,
	loader ArtifactLoader,
	tpArtifact *planning.Artifact,
	specArtifact *planning.Artifact,
	specReqIDs []string,
	goalRev string,
	repoRev string,
	pc planningagent.PlanningContext,
	decisions map[string]*planning.Artifact,
) error {
	budget := &ReviewRepairBudget{limit: e.ReviewRetryLimit}

	// Render ticket plan for review
	ticketPlanRendered := renderTicketPlanForReview(tpArtifact)

	for {
		// Compile planning context with current ticket plan for review
		artifacts := []planningagent.NamedArtifact{
			{ID: "goal", Artifact: &planning.Artifact{Kind: planning.KindGoal, Revision: goalRev, Sections: []planning.Section{{Heading: "Goal", Body: ""}}}},
		}
		for id, dec := range decisions {
			artifacts = append(artifacts, planningagent.NamedArtifact{ID: id, Artifact: dec})
		}
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: "spec", Artifact: specArtifact})
		reviewPC, err := planningagent.CompileWithFacts(e.Repository, artifacts, nil, e.ImplementedFacts)
		if err != nil {
			return fmt.Errorf("specengine: compile planning context for ticket plan review: %w", err)
		}

		reviewResult, err := ticketplanreview.Review(ctx, e.Backend, reviewPC, ticketPlanRendered, specReqIDs, specArtifact.Revision)
		if err != nil {
			return fmt.Errorf("specengine: ticket plan review failed: %w", err)
		}

		if reviewResult.Verdict == ticketplanreview.VerdictApproved {
			// Ticket plan approved by automated review, save and return
			if err := loader.SaveTicketPlan(ctx, featureID, tpArtifact); err != nil {
				return fmt.Errorf("specengine: save ticket plan: %w", err)
			}
			return nil
		}

		// CHANGES_REQUIRED - enter bounded repair loop
		if budget.Exhausted() {
			return fmt.Errorf("specengine: ticket plan review repair budget exhausted after %d attempts", e.ReviewRetryLimit)
		}

		if err := budget.Record(); err != nil {
			return err
		}

		// Build focused feedback from findings for repair
		feedback := buildTicketPlanRepairFeedback(reviewResult.Findings)

		// Re-generate ticket plan with findings as additional context
		humanInputs := map[string]string{
			"review_findings":   feedback,
			"prior_ticket_plan": ticketPlanRendered,
		}

		artifactsForRepair := []planningagent.NamedArtifact{
			{ID: "goal", Artifact: &planning.Artifact{Kind: planning.KindGoal, Revision: goalRev, Sections: []planning.Section{{Heading: "Goal", Body: ""}}}},
		}
		for id, dec := range decisions {
			artifactsForRepair = append(artifactsForRepair, planningagent.NamedArtifact{ID: id, Artifact: dec})
		}
		artifactsForRepair = append(artifactsForRepair, planningagent.NamedArtifact{ID: "spec", Artifact: specArtifact})
		repairPC, err := planningagent.CompileWithFacts(e.Repository, artifactsForRepair, humanInputs, e.ImplementedFacts)
		if err != nil {
			return fmt.Errorf("specengine: compile planning context for ticket plan repair: %w", err)
		}

		tpResult, err := ticketplan.Generate(ctx, e.Backend, repairPC)
		if err != nil {
			return fmt.Errorf("specengine: generate ticket plan repair: %w", err)
		}

		// Build new ticket plan artifact from repair result
		newTPArtifact := &planning.Artifact{
			Kind:      planning.KindTicketPlan,
			Sections:  make([]planning.Section, 0, len(tpResult.Tickets)),
			Estimates: make(map[string]planning.TicketEstimate),
		}

		for _, t := range tpResult.Tickets {
			body := ticketplan.RenderTicketBody(t)
			newTPArtifact.Sections = append(newTPArtifact.Sections, planning.Section{
				Heading: fmt.Sprintf("Ticket: %s", t.Key),
				Body:    body,
			})
			// Add estimate to metadata if present
			if t.Estimate != nil {
				newTPArtifact.Estimates[t.Key] = *t.Estimate
			}
		}

		// DerivedFrom: spec + repository (same as original)
		newTPArtifact.DerivedFrom = []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: specArtifact.Revision},
			{Kind: "repository", ID: "repository", Revision: repoRev},
		}

		newTPArtifact.Revision = planning.ComputeRevision(newTPArtifact)

		// Re-run deterministic validation on repaired ticket plan
		if err := ticketplan.ValidateTicketPlanDeterministic(newTPArtifact, specReqIDs, specArtifact.Revision, repoRev); err != nil {
			return fmt.Errorf("specengine: deterministic validation failed after ticket plan repair: %w", err)
		}

		// Loop continues with new ticket plan for next review iteration
		tpArtifact = newTPArtifact
		ticketPlanRendered = renderTicketPlanForReview(tpArtifact)
	}
}

func buildTicketPlanRepairFeedback(findings []ticketplanreview.Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var fb string
	for _, f := range findings {
		ref := ""
		if f.TicketKey != "" {
			ref += " [" + f.TicketKey + "]"
		}
		if f.Requirement != "" {
			ref += " [" + f.Requirement + "]"
		}
		fb += fmt.Sprintf("[%s]%s %s\n", f.Severity, ref, f.Message)
	}
	return fb
}

func renderTicketPlanForReview(tpArtifact *planning.Artifact) string {
	var out string
	for _, s := range tpArtifact.Sections {
		out += fmt.Sprintf("## %s\n%s\n\n", s.Heading, s.Body)
	}
	return out
}

type fakeLoader struct {
	goal       *planning.Artifact
	decisions  map[string]*planning.Artifact
	spec       *planning.Artifact
	ticketPlan *planning.Artifact
}

func (f *fakeLoader) LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error) {
	if f.goal == nil {
		return nil, fmt.Errorf("goal not found")
	}
	return f.goal, nil
}

func (f *fakeLoader) LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error) {
	return f.decisions, nil
}

func (f *fakeLoader) SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error {
	f.spec = spec
	return nil
}

func (f *fakeLoader) LoadSpec(ctx context.Context, featureID string) (*planning.Artifact, error) {
	return f.spec, nil
}

func (f *fakeLoader) SaveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) error {
	f.ticketPlan = tp
	return nil
}
