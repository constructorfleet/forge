package ticketplan

import (
	"strings"

	"github.com/Teagan42/forge/internal/planning"
)

const TicketKeyPrefix = "TKT-"

type Ticket struct {
	Key                   string
	Objective             string
	Requirements          []string
	AcceptanceCriteria    []string
	Dependencies          []string
	ImplementationContext []string
	Estimate              *planning.TicketEstimate
}

func ParseTicketPlan(artifact *planning.Artifact) ([]Ticket, error) {
	var tickets []Ticket
	for _, section := range artifact.Sections {
		if !strings.HasPrefix(section.Heading, "Ticket: ") {
			continue
		}
		key := strings.TrimPrefix(section.Heading, "Ticket: ")
		key = strings.TrimSpace(key)

		ticket := Ticket{Key: key}
		body := section.Body

		// Parse Objective
		objectiveStart := strings.Index(body, "### Objective")
		if objectiveStart == -1 {
			return nil, errMissingObjective(key)
		}
		objectiveEnd := findNextHeading(body, objectiveStart)
		objective := strings.TrimSpace(body[objectiveStart+len("### Objective") : objectiveEnd])
		if objective == "" {
			return nil, errMissingObjective(key)
		}
		ticket.Objective = objective

		// Parse Requirements
		reqStart := strings.Index(body, "### Requirements")
		if reqStart == -1 {
			return nil, errMissingRequirements(key)
		}
		reqEnd := findNextHeading(body, reqStart)
		reqBody := strings.TrimSpace(body[reqStart+len("### Requirements") : reqEnd])
		if reqBody != "" {
			for _, line := range strings.Split(reqBody, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "REQ-") {
					// Extract REQ-NNN
					parts := strings.Fields(line)
					if len(parts) > 0 {
						reqID := parts[0]
						reqID = strings.TrimSuffix(reqID, ":")
						reqID = strings.TrimSuffix(reqID, ".")
						ticket.Requirements = append(ticket.Requirements, reqID)
					}
				}
			}
		}
		if len(ticket.Requirements) == 0 {
			return nil, errMissingRequirements(key)
		}

		// Parse Acceptance Criteria
		acStart := strings.Index(body, "### Acceptance Criteria")
		if acStart == -1 {
			return nil, errMissingAcceptanceCriteria(key)
		}
		acEnd := findNextHeading(body, acStart)
		acBody := strings.TrimSpace(body[acStart+len("### Acceptance Criteria") : acEnd])
		if acBody == "" {
			return nil, errMissingAcceptanceCriteria(key)
		}
		for _, line := range strings.Split(acBody, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
				criterion := strings.TrimSpace(line[1:])
				if criterion != "" {
					ticket.AcceptanceCriteria = append(ticket.AcceptanceCriteria, criterion)
				}
			}
		}
		if len(ticket.AcceptanceCriteria) == 0 {
			return nil, errMissingAcceptanceCriteria(key)
		}

		// Parse Implementation Context (optional)
		icStart := strings.Index(body, "### Implementation Context")
		if icStart != -1 {
			icEnd := findNextHeading(body, icStart)
			icBody := strings.TrimSpace(body[icStart+len("### Implementation Context") : icEnd])
			if icBody != "" && !strings.EqualFold(icBody, "None") {
				for _, line := range strings.Split(icBody, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
						note := strings.TrimSpace(line[1:])
						if note != "" {
							ticket.ImplementationContext = append(ticket.ImplementationContext, note)
						}
					}
				}
			}
		}

		// Parse Dependencies
		depStart := strings.Index(body, "### Dependencies")
		if depStart != -1 {
			depEnd := findNextHeading(body, depStart)
			depBody := strings.TrimSpace(body[depStart+len("### Dependencies") : depEnd])
			if depBody != "" && !strings.EqualFold(depBody, "None") {
				for _, line := range strings.Split(depBody, "\n") {
					line = strings.TrimSpace(line)
					if line != "" && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "*") {
						ticket.Dependencies = append(ticket.Dependencies, line)
					} else if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
						dep := strings.TrimSpace(line[1:])
						if dep != "" {
							ticket.Dependencies = append(ticket.Dependencies, dep)
						}
					}
				}
			}
		}

		// Parse Estimate from artifact metadata
		if artifact.Estimates != nil {
			if est, ok := artifact.Estimates[key]; ok {
				// Copy the estimate to avoid pointer issues
				estCopy := est
				ticket.Estimate = &estCopy
			}
		}

		tickets = append(tickets, ticket)
	}
	return tickets, nil
}

func findNextHeading(body string, start int) int {
	next := strings.Index(body[start+1:], "### ")
	if next == -1 {
		return len(body)
	}
	return start + 1 + next
}

var (
	ErrMissingTitle              = &TicketPlanError{Code: "MISSING_TITLE"}
	ErrMissingObjective          = &TicketPlanError{Code: "MISSING_OBJECTIVE"}
	ErrMissingRequirements       = &TicketPlanError{Code: "MISSING_REQUIREMENTS"}
	ErrMissingAcceptanceCriteria = &TicketPlanError{Code: "MISSING_ACCEPTANCE_CRITERIA"}
	ErrDuplicateTempKey          = &TicketPlanError{Code: "DUPLICATE_TEMP_KEY"}
	ErrCyclicDependency          = &TicketPlanError{Code: "CYCLIC_DEPENDENCY"}
	ErrUnresolvableDependency    = &TicketPlanError{Code: "UNRESOLVABLE_DEPENDENCY"}
	ErrDependencyOnDecision      = &TicketPlanError{Code: "DEPENDENCY_ON_DECISION"}
	ErrSelfDependency            = &TicketPlanError{Code: "SELF_DEPENDENCY"}
	ErrUnmappedRequirement       = &TicketPlanError{Code: "UNMAPPED_REQUIREMENT"}
	ErrInvalidRequirementRef     = &TicketPlanError{Code: "INVALID_REQUIREMENT_REF"}
	ErrSpecRevisionMismatch      = &TicketPlanError{Code: "SPEC_REVISION_MISMATCH"}
	ErrRepoRevisionMismatch      = &TicketPlanError{Code: "REPO_REVISION_MISMATCH"}
)

type TicketPlanError struct {
	Code    string
	Ticket  string
	Message string
}

func (e *TicketPlanError) Error() string {
	if e.Ticket != "" {
		return "ticket-plan: " + e.Ticket + ": " + e.Code + ": " + e.Message
	}
	return "ticket-plan: " + e.Code + ": " + e.Message
}

func (e *TicketPlanError) WithTicket(ticket string) *TicketPlanError {
	return &TicketPlanError{Code: e.Code, Ticket: ticket, Message: e.Message}
}

func (e *TicketPlanError) WithMessage(msg string) *TicketPlanError {
	return &TicketPlanError{Code: e.Code, Ticket: e.Ticket, Message: msg}
}

func errMissingTitle() error { return &TicketPlanError{Code: "MISSING_TITLE"} }
func errMissingObjective(ticket string) error {
	return &TicketPlanError{Code: "MISSING_OBJECTIVE", Ticket: ticket}
}
func errMissingRequirements(ticket string) error {
	return &TicketPlanError{Code: "MISSING_REQUIREMENTS", Ticket: ticket}
}
func errMissingAcceptanceCriteria(ticket string) error {
	return &TicketPlanError{Code: "MISSING_ACCEPTANCE_CRITERIA", Ticket: ticket}
}
func errDuplicateTempKey(key string) error {
	return &TicketPlanError{Code: "DUPLICATE_TEMP_KEY", Message: key}
}
func errCyclicDependency(ticket string) error {
	return &TicketPlanError{Code: "CYCLIC_DEPENDENCY", Ticket: ticket}
}
func errUnresolvableDependency(ticket, target string) error {
	return &TicketPlanError{Code: "UNRESOLVABLE_DEPENDENCY", Ticket: ticket, Message: target}
}
func errDependencyOnDecision(ticket string) error {
	return &TicketPlanError{Code: "DEPENDENCY_ON_DECISION", Ticket: ticket}
}
func errSelfDependency(ticket string) error {
	return &TicketPlanError{Code: "SELF_DEPENDENCY", Ticket: ticket}
}
func errUnmappedRequirement(req string) error {
	return &TicketPlanError{Code: "UNMAPPED_REQUIREMENT", Message: req}
}
func errInvalidRequirementRef(ticket, req string) error {
	return &TicketPlanError{Code: "INVALID_REQUIREMENT_REF", Ticket: ticket, Message: req}
}
func errSpecRevisionMismatch(got, expected string) error {
	return &TicketPlanError{Code: "SPEC_REVISION_MISMATCH", Message: "got " + got + ", expected " + expected}
}
func errRepoRevisionMismatch(got, expected string) error {
	return &TicketPlanError{Code: "REPO_REVISION_MISMATCH", Message: "got " + got + ", expected " + expected}
}
