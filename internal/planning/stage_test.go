package planning_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

func TestDeriveStage(t *testing.T) {
	goal := &planning.Artifact{Kind: planning.KindGoal}
	decision := &planning.Artifact{Kind: planning.KindDecision}
	spec := &planning.Artifact{Kind: planning.KindSpec}
	ticketPlan := &planning.Artifact{Kind: planning.KindTicketPlan}

	tests := []struct {
		name       string
		goal       *planning.Artifact
		decisions  []*planning.Artifact
		spec       *planning.Artifact
		ticketPlan *planning.Artifact
		want       planning.Stage
	}{
		{"no goal", nil, nil, nil, nil, planning.StageGoal},
		{"goal only", goal, nil, nil, nil, planning.StageDecisions},
		{"goal and decisions", goal, []*planning.Artifact{decision}, nil, nil, planning.StageSpec},
		{"goal decisions and spec", goal, []*planning.Artifact{decision}, spec, nil, planning.StageTicketPlan},
		{"everything produced", goal, []*planning.Artifact{decision}, spec, ticketPlan, planning.StageDone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planning.DeriveStage(tt.goal, tt.decisions, tt.spec, tt.ticketPlan)
			if got != tt.want {
				t.Errorf("DeriveStage() = %q, want %q", got, tt.want)
			}
		})
	}
}
