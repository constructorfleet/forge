package ticketplan

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/Teagan42/forge/internal/planning"
)

var ticketKeyPattern = regexp.MustCompile(`^TKT-\d{3}$`)

func ValidateTicketPlanDeterministic(
	artifact *planning.Artifact,
	specReqIDs []string,
	specRev string,
	repoRev string,
) error {
	if artifact.Kind != planning.KindTicketPlan {
		return fmt.Errorf("not a ticket-plan artifact")
	}

	// Parse tickets
	tickets, err := ParseTicketPlan(artifact)
	if err != nil {
		return err
	}

	if len(tickets) == 0 {
		return errors.New("ticket-plan: no tickets found")
	}

	// Validate structure
	seenKeys := make(map[string]bool)
	for _, t := range tickets {
		if !ticketKeyPattern.MatchString(t.Key) {
			return fmt.Errorf("ticket-plan: invalid ticket key format: %s (expected TKT-NNN)", t.Key)
		}
		if seenKeys[t.Key] {
			return errDuplicateTempKey(t.Key)
		}
		seenKeys[t.Key] = true

		if t.Objective == "" {
			return errMissingObjective(t.Key)
		}
		if len(t.Requirements) == 0 {
			return errMissingRequirements(t.Key)
		}
		if len(t.AcceptanceCriteria) == 0 {
			return errMissingAcceptanceCriteria(t.Key)
		}
		if t.Kind != planning.TicketKindCode && t.Kind != planning.TicketKindNonCode {
			return fmt.Errorf("ticket-plan: ticket %s has invalid ticket kind %q (must be code or non-code)", t.Key, t.Kind)
		}

		// Validate requirement references
		for _, req := range t.Requirements {
			if !reqIDPattern(req) {
				return errInvalidRequirementRef(t.Key, req)
			}
			found := false
			for _, specReq := range specReqIDs {
				if specReq == req {
					found = true
					break
				}
			}
			if !found {
				return errInvalidRequirementRef(t.Key, req)
			}
		}

		// Validate estimate if present
		if t.Estimate != nil {
			if t.Estimate.Size == "" {
				return fmt.Errorf("ticket-plan: ticket %s has estimate with empty size", t.Key)
			}
			if !planning.ValidEstimateSizes[t.Estimate.Size] {
				return fmt.Errorf("ticket-plan: ticket %s has invalid estimate size %q (must be S, M, L, or XL)", t.Key, t.Estimate.Size)
			}
		}
	}

	// Build dependency graph and validate
	ticketMap := make(map[string]*Ticket)
	for i := range tickets {
		ticketMap[tickets[i].Key] = &tickets[i]
	}

	// Validate dependencies
	for _, t := range tickets {
		for _, dep := range t.Dependencies {
			// No self-dependency
			if dep == t.Key {
				return errSelfDependency(t.Key)
			}
			// No dependency on decision (decision IDs don't match TKT-NNN pattern)
			if !ticketKeyPattern.MatchString(dep) {
				return errDependencyOnDecision(t.Key)
			}
			// Target must exist
			target, ok := ticketMap[dep]
			if !ok {
				return errUnresolvableDependency(t.Key, dep)
			}
			if t.Kind == planning.TicketKindCode && target.Kind == planning.TicketKindNonCode {
				return fmt.Errorf("ticket-plan: ticket %s code ticket cannot depend on non-code ticket %s", t.Key, dep)
			}
		}
	}

	// Check for cycles using DFS
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var dfs func(string) error
	dfs = func(key string) error {
		visited[key] = true
		recStack[key] = true
		ticket := ticketMap[key]
		for _, dep := range ticket.Dependencies {
			if !visited[dep] {
				if err := dfs(dep); err != nil {
					return err
				}
			} else if recStack[dep] {
				return errCyclicDependency(key)
			}
		}
		recStack[key] = false
		return nil
	}

	for _, t := range tickets {
		if !visited[t.Key] {
			if err := dfs(t.Key); err != nil {
				return err
			}
		}
	}

	// Traceability: every spec requirement maps to at least one ticket
	reqToTickets := make(map[string][]string)
	for _, t := range tickets {
		for _, req := range t.Requirements {
			reqToTickets[req] = append(reqToTickets[req], t.Key)
		}
	}

	for _, req := range specReqIDs {
		if len(reqToTickets[req]) == 0 {
			return errUnmappedRequirement(req)
		}
	}

	// Provenance: validate derived_from
	if len(artifact.DerivedFrom) == 0 {
		return errors.New("ticket-plan: missing derived_from provenance")
	}

	specFound := false
	repoFound := false
	for _, d := range artifact.DerivedFrom {
		if d.Kind == planning.KindSpec && d.ID == "spec" {
			specFound = true
			if d.Revision != specRev {
				return errSpecRevisionMismatch(d.Revision, specRev)
			}
		}
		if d.Kind == "repository" && d.ID == "repository" {
			repoFound = true
			if d.Revision != repoRev {
				return errRepoRevisionMismatch(d.Revision, repoRev)
			}
		}
	}

	if !specFound {
		return errors.New("ticket-plan: missing derived_from spec entry")
	}
	if !repoFound {
		return errors.New("ticket-plan: missing derived_from repository entry")
	}

	return nil
}

func reqIDPattern(id string) bool {
	return regexp.MustCompile(`^REQ-\d{3}$`).MatchString(id)
}
