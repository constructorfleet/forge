package materialize_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/materialize"
	"github.com/Teagan42/forge/internal/ticketplan"
	"github.com/Teagan42/forge/internal/tracker"
)

func testTickets() []ticketplan.Ticket {
	return []ticketplan.Ticket{
		{
			Key:                "TKT-001",
			Objective:          "Build the foundation",
			Requirements:       []string{"REQ-001"},
			AcceptanceCriteria: []string{"Foundation exists"},
		},
		{
			Key:                "TKT-002",
			Objective:          "Build on top",
			Requirements:       []string{"REQ-002"},
			AcceptanceCriteria: []string{"Built on top"},
			Dependencies:       []string{"TKT-001"},
		},
	}
}

func testOptions() materialize.Options {
	return materialize.Options{
		Project:      "my-feature",
		SpecRevision: "spec-rev-1",
		PlanRevision: "plan-rev-1",
		Decisions:    []string{"0001-use-go"},
	}
}

func TestMaterialize_HappyPath(t *testing.T) {
	trk := tracker.NewFakeTracker()

	result, err := materialize.Materialize(context.Background(), trk, testTickets(), testOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.IssueIDs) != 2 {
		t.Fatalf("got %d issue IDs, want 2: %+v", len(result.IssueIDs), result.IssueIDs)
	}

	id1, id2 := result.IssueIDs["TKT-001"], result.IssueIDs["TKT-002"]
	if id1 == "" || id2 == "" {
		t.Fatalf("missing issue IDs: %+v", result.IssueIDs)
	}

	issues, err := trk.GetIssues(context.Background(), []string{id1, id2})
	if err != nil {
		t.Fatalf("GetIssues: %v", err)
	}

	for _, issue := range issues {
		if err := tracker.ValidateExecutable(issue.ID, issue.Body); err != nil {
			t.Fatalf("issue %s not executable after successful materialization: %v", issue.ID, err)
		}
		prov, err := tracker.ParseForgeProvenance(issue.Body)
		if err != nil {
			t.Fatalf("issue %s: parse provenance: %v", issue.ID, err)
		}
		if prov.Status != tracker.ProvenanceReady {
			t.Fatalf("issue %s: status %q, want ready", issue.ID, prov.Status)
		}
		if prov.Project != "my-feature" || prov.SpecRevision != "spec-rev-1" || prov.PlanRevision != "plan-rev-1" {
			t.Fatalf("issue %s: unexpected provenance stamp: %+v", issue.ID, prov)
		}
		if len(prov.Decisions) != 1 || prov.Decisions[0] != "0001-use-go" {
			t.Fatalf("issue %s: unexpected decisions: %+v", issue.ID, prov.Decisions)
		}
	}

	// TKT-002 depends on TKT-001, rewritten to the real tracker ID.
	depIssue, err := trk.GetIssue(context.Background(), id2)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	deps, err := tracker.ParseDependencyBlock(depIssue.Body)
	if err != nil {
		t.Fatalf("ParseDependencyBlock: %v", err)
	}
	if len(deps) != 1 || deps[0] != id1 {
		t.Fatalf("got dependencies %v, want [%s]", deps, id1)
	}
}

func TestMaterialize_NoDependencies_RendersNone(t *testing.T) {
	trk := tracker.NewFakeTracker()
	tickets := []ticketplan.Ticket{{
		Key:                "TKT-001",
		Objective:          "Solo ticket",
		Requirements:       []string{"REQ-001"},
		AcceptanceCriteria: []string{"Done"},
	}}

	result, err := materialize.Materialize(context.Background(), trk, tickets, testOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	issue, err := trk.GetIssue(context.Background(), result.IssueIDs["TKT-001"])
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	deps, err := tracker.ParseDependencyBlock(issue.Body)
	if err != nil {
		t.Fatalf("ParseDependencyBlock: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("got dependencies %v, want none", deps)
	}
}

// failingTracker wraps a FakeTracker and fails CreateIssue once a
// configured number of successful creates have happened, simulating a
// mid-Phase-A network failure.
type failingTracker struct {
	*tracker.FakeTracker
	failAfterCreates int
	creates          int
}

func (f *failingTracker) CreateIssue(ctx context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error) {
	if f.creates >= f.failAfterCreates {
		return tracker.CreatedIssue{}, errors.New("simulated network failure")
	}
	f.creates++
	return f.FakeTracker.CreateIssue(ctx, req)
}

func TestMaterialize_PartialPhaseAFailure_LeavesNoExecutableIssues(t *testing.T) {
	trk := &failingTracker{FakeTracker: tracker.NewFakeTracker(), failAfterCreates: 1}

	tickets := append(testTickets(), ticketplan.Ticket{
		Key:                "TKT-003",
		Objective:          "Never gets created",
		Requirements:       []string{"REQ-003"},
		AcceptanceCriteria: []string{"n/a"},
	})

	_, err := materialize.Materialize(context.Background(), trk, tickets, testOptions())
	if err == nil {
		t.Fatal("expected an error")
	}
	var partial *materialize.PartialFailureError
	if !errors.As(err, &partial) {
		t.Fatalf("got %T, want *materialize.PartialFailureError", err)
	}
	if len(partial.IssueIDs) != 1 {
		t.Fatalf("got %d created issues, want exactly 1 orphaned issue: %+v", len(partial.IssueIDs), partial.IssueIDs)
	}

	// The one Issue that was created must still be non-executable — no
	// partial failure may leave an executable Issue behind.
	for _, id := range partial.IssueIDs {
		issue, getErr := trk.GetIssue(context.Background(), id)
		if getErr != nil {
			t.Fatalf("GetIssue: %v", getErr)
		}
		if execErr := tracker.ValidateExecutable(issue.ID, issue.Body); execErr == nil {
			t.Fatalf("issue %s is executable after a partial materialization failure", issue.ID)
		}
	}
}

// updateFailingTracker fails UpdateIssue for a specific issue ID, letting
// tests simulate a Phase B/C failure that strands some Issues mid-flight.
type updateFailingTracker struct {
	*tracker.FakeTracker
	failFor string
}

func (f *updateFailingTracker) UpdateIssue(ctx context.Context, id string, req tracker.UpdateIssueRequest) error {
	if id == f.failFor {
		return errors.New("simulated update failure")
	}
	return f.FakeTracker.UpdateIssue(ctx, id, req)
}

func TestMaterialize_PhaseBFailure_LeavesAllIssuesNonExecutable(t *testing.T) {
	inner := tracker.NewFakeTracker()
	tickets := testTickets()

	// Discover the ID FakeTracker will assign to TKT-002 (the second
	// created issue) so the wrapper can target its Phase B UpdateIssue.
	trk := &updateFailingTracker{FakeTracker: inner, failFor: "2"}

	_, err := materialize.Materialize(context.Background(), trk, tickets, testOptions())
	if err == nil {
		t.Fatal("expected an error")
	}
	var partial *materialize.PartialFailureError
	if !errors.As(err, &partial) {
		t.Fatalf("got %T, want *materialize.PartialFailureError", err)
	}

	for _, id := range partial.IssueIDs {
		issue, getErr := inner.GetIssue(context.Background(), id)
		if getErr != nil {
			t.Fatalf("GetIssue: %v", getErr)
		}
		if execErr := tracker.ValidateExecutable(issue.ID, issue.Body); execErr == nil {
			t.Fatalf("issue %s is executable after a Phase B failure", issue.ID)
		}
	}
}

func TestMaterialize_EmptyTicketList_Errors(t *testing.T) {
	trk := tracker.NewFakeTracker()
	_, err := materialize.Materialize(context.Background(), trk, nil, testOptions())
	if err == nil {
		t.Fatal("expected an error for an empty ticket list")
	}
}

func TestMaterialize_UnresolvableDependency_Errors(t *testing.T) {
	trk := tracker.NewFakeTracker()
	tickets := []ticketplan.Ticket{{
		Key:                "TKT-001",
		Objective:          "Depends on nothing that exists",
		Requirements:       []string{"REQ-001"},
		AcceptanceCriteria: []string{"n/a"},
		Dependencies:       []string{"TKT-999"},
	}}
	_, err := materialize.Materialize(context.Background(), trk, tickets, testOptions())
	if err == nil {
		t.Fatal("expected an error for an unresolvable dependency")
	}
}

func TestMaterialize_CyclicDependency_Errors(t *testing.T) {
	trk := tracker.NewFakeTracker()
	tickets := []ticketplan.Ticket{
		{
			Key:                "TKT-001",
			Objective:          "A",
			Requirements:       []string{"REQ-001"},
			AcceptanceCriteria: []string{"n/a"},
			Dependencies:       []string{"TKT-002"},
		},
		{
			Key:                "TKT-002",
			Objective:          "B",
			Requirements:       []string{"REQ-002"},
			AcceptanceCriteria: []string{"n/a"},
			Dependencies:       []string{"TKT-001"},
		},
	}
	_, err := materialize.Materialize(context.Background(), trk, tickets, testOptions())
	if err == nil {
		t.Fatal("expected an error for a cyclic dependency")
	}
}
