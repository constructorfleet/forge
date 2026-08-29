package domain_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/domain"
)

func TestPlanningStatusValid(t *testing.T) {
	valid := []domain.PlanningStatus{
		domain.PlanningStatusActive,
		domain.PlanningStatusNeedsHuman,
		domain.PlanningStatusNeedsApproval,
		domain.PlanningStatusFailed,
		domain.PlanningStatusComplete,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if domain.PlanningStatus("BOGUS").Valid() {
		t.Error("expected BOGUS to be invalid")
	}
}

func TestPlanningStatusIsTerminal(t *testing.T) {
	terminal := []domain.PlanningStatus{domain.PlanningStatusFailed, domain.PlanningStatusComplete}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("expected %q to be terminal", s)
		}
	}

	nonTerminal := []domain.PlanningStatus{
		domain.PlanningStatusActive,
		domain.PlanningStatusNeedsHuman,
		domain.PlanningStatusNeedsApproval,
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("expected %q not to be terminal", s)
		}
	}
}
