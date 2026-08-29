package spec

import (
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

func TestSpecArtifactKind(t *testing.T) {
	spec := NewSpecification()
	if spec.Kind != planning.KindSpec {
		t.Errorf("expected kind %s, got %s", planning.KindSpec, spec.Kind)
	}
}

func TestSpecArtifactRequiredSections(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement\nREQ-002: Second requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	err := ValidateSpec(&spec.Artifact)
	if err != nil {
		t.Fatalf("ValidateSpec failed: %v", err)
	}
}

func TestSpecArtifactMissingRequiredSection(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")

	err := ValidateSpec(&spec.Artifact)
	if err == nil {
		t.Fatal("expected error for missing Non-Goals section")
	}
}

func TestSpecArtifactDuplicateReqIDs(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement\nREQ-001: Duplicate requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	err := ValidateSpec(&spec.Artifact)
	if err == nil {
		t.Fatal("expected error for duplicate REQ IDs")
	}
}

func TestSpecArtifactUnknownReferences(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement\nREQ-999: Out of sequence")
	spec.AddSection("Non-Goals", "Not doing this")

	err := ValidateSpec(&spec.Artifact)
	if err == nil {
		t.Fatal("expected error for out of sequence REQ ID")
	}
}

func TestSpecArtifactStaleDetection(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	spec.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev-1"},
		{Kind: planning.KindDecision, ID: "001-storage", Revision: "dec-rev-1"},
		{Kind: "repository", ID: "repository", Revision: "repo-rev-1"},
	}

	decisions := map[string]*planning.Artifact{
		"001-storage": {State: "resolved", Revision: "dec-rev-1"},
	}

	err := ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev-1", map[string]string{"001-storage": "dec-rev-1"}, "repo-rev-1")
	if err != nil {
		t.Fatalf("ValidateSpecDeterministic failed: %v", err)
	}

	err = ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev-2", map[string]string{"001-storage": "dec-rev-1"}, "repo-rev-1")
	if err == nil {
		t.Fatal("expected error for stale goal revision")
	}

	err = ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev-1", map[string]string{"001-storage": "dec-rev-2"}, "repo-rev-1")
	if err == nil {
		t.Fatal("expected error for stale decision revision")
	}

	err = ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev-1", map[string]string{"001-storage": "dec-rev-1"}, "repo-rev-2")
	if err == nil {
		t.Fatal("expected error for stale repository revision")
	}
}

func TestSpecArtifactRevisionStability(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	reqIDs := ExtractRequirementIDs(spec.Sections[1].Body)
	if len(reqIDs) != 1 || reqIDs[0] != "REQ-001" {
		t.Errorf("expected [REQ-001], got %v", reqIDs)
	}
}

func TestSpecArtifactDerivedFromOrdering(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	spec.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
		{Kind: planning.KindDecision, ID: "001-storage", Revision: "dec-rev"},
	}

	decisions := map[string]*planning.Artifact{
		"001-storage": {State: "resolved", Revision: "dec-rev"},
	}

	err := ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev", map[string]string{"001-storage": "dec-rev"}, "repo-rev")
	if err != nil {
		t.Fatalf("ValidateSpecDeterministic failed with unordered derived_from: %v", err)
	}
}

func TestExtractRequirementIDs(t *testing.T) {
	content := `REQ-001: First requirement
REQ-002: Second requirement
Some other text
REQ-003: Third requirement`

	ids := ExtractRequirementIDs(content)
	if len(ids) != 3 || ids[0] != "REQ-001" || ids[1] != "REQ-002" || ids[2] != "REQ-003" {
		t.Errorf("expected [REQ-001 REQ-002 REQ-003], got %v", ids)
	}
}

func TestUniqueRequirementIDs(t *testing.T) {
	ids := []string{"REQ-001", "REQ-002", "REQ-003"}
	unique, err := UniqueRequirementIDs(ids)
	if err != nil {
		t.Fatalf("UniqueRequirementIDs failed: %v", err)
	}
	if len(unique) != 3 {
		t.Errorf("expected 3 unique IDs, got %d", len(unique))
	}
}

func TestUniqueRequirementIDsDuplicates(t *testing.T) {
	ids := []string{"REQ-001", "REQ-002", "REQ-001"}
	_, err := UniqueRequirementIDs(ids)
	if err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
}

func TestExtractRequirementIDsEmpty(t *testing.T) {
	ids := ExtractRequirementIDs("No requirements here")
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestValidateRequirementIDs(t *testing.T) {
	ids := []string{"REQ-001", "REQ-002", "REQ-003"}
	err := ValidateRequirementIDs(ids)
	if err != nil {
		t.Fatalf("ValidateRequirementIDs failed: %v", err)
	}
}

func TestValidateRequirementIDsDuplicates(t *testing.T) {
	ids := []string{"REQ-001", "REQ-001"}
	err := ValidateRequirementIDs(ids)
	if err == nil {
		t.Fatal("expected error for duplicate sequential IDs")
	}
}

func TestValidateSpecDeterministic_Success(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	spec.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
		{Kind: planning.KindDecision, ID: "001-storage", Revision: "dec-rev"},
		{Kind: "repository", ID: "repository", Revision: "repo-rev"},
	}

	decisions := map[string]*planning.Artifact{
		"001-storage": {State: "resolved", Revision: "dec-rev"},
	}

	err := ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev", map[string]string{"001-storage": "dec-rev"}, "repo-rev")
	if err != nil {
		t.Fatalf("ValidateSpecDeterministic failed: %v", err)
	}
}

func TestValidateSpecDeterministic_BlockingDecision(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	spec.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
		{Kind: planning.KindDecision, ID: "001-storage", Revision: "dec-rev"},
	}

	decisions := map[string]*planning.Artifact{
		"001-storage": {State: "open", Revision: "dec-rev"},
	}

	err := ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev", map[string]string{"001-storage": "dec-rev"}, "repo-rev")
	if err == nil {
		t.Fatal("expected error for blocking decision")
	}
}

func TestValidateSpecDeterministic_DerivedFromMismatch(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	spec.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev-old"},
	}

	decisions := map[string]*planning.Artifact{}

	err := ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev-new", map[string]string{}, "repo-rev")
	if err == nil {
		t.Fatal("expected error for derived_from mismatch")
	}
}

func TestValidateSpecDeterministic_StaleInput(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")
	spec.AddSection("Non-Goals", "Not doing this")

	spec.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
		{Kind: planning.KindDecision, ID: "001-storage", Revision: "dec-rev"},
		{Kind: "repository", ID: "repository", Revision: "repo-rev"},
	}

	decisions := map[string]*planning.Artifact{
		"001-storage": {State: "resolved", Revision: "dec-rev"},
	}

	err := ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev", map[string]string{"001-storage": "dec-rev"}, "repo-rev-different")
	if err == nil {
		t.Fatal("expected error for stale repository revision")
	}
}

func TestValidateSpecDeterministic_MissingRequiredSection(t *testing.T) {
	spec := NewSpecification()
	spec.AddSection("Context", "Test context")
	spec.AddSection("Requirements", "REQ-001: First requirement")

	spec.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
	}

	decisions := map[string]*planning.Artifact{}

	err := ValidateSpecDeterministic(&spec.Artifact, decisions, "goal-rev", map[string]string{}, "repo-rev")
	if err == nil {
		t.Fatal("expected error for missing Non-Goals section")
	}
}
