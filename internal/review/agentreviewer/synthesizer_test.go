package agentreviewer

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/review"
)

// finding builds one axisFinding literal for test fixtures.
func finding(severity string, confidence float64, file string, line int, message, evidence, remedy string) axisFinding {
	return axisFinding{
		Severity:   severity,
		Confidence: confidence,
		File:       file,
		Line:       line,
		Message:    message,
		Evidence:   evidence,
		Remedy:     remedy,
	}
}

// outcome builds one axisOutcome fixture for axis with the given findings.
func outcome(axis string, findings ...axisFinding) axisOutcome {
	return axisOutcome{axis: axis, env: envelope{Axis: axis, Findings: findings}}
}

func TestCombine_IdenticalFindingTwoAxes_DedupsAgreedByAndFoldsConfidence(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.6, "a.go", 10, "nil pointer dereference in Foo", "", "check err before use")),
		outcome("quality", finding("HIGH", 0.5, "a.go", 11, "nil pointer dereference in Foo", "", "check err before use")),
		outcome("docs", finding("HIGH", 0.5, "a.go", 10, "nil pointer dereference in Foo", "", "check err before use")),
	}
	result := combine(outcomes, 0.7)

	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 merged finding", result.Findings)
	}
	f := result.Findings[0]
	if f.AgreedBy != 3 {
		t.Errorf("AgreedBy = %d, want 3", f.AgreedBy)
	}
	// merged = 1 - (1-0.6)(1-0.5)(1-0.5) = 1 - 0.4*0.5*0.5 = 1 - 0.1 = 0.9
	if diff := f.Confidence - 0.9; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Confidence = %v, want 0.9", f.Confidence)
	}
	if f.Axis != "bugs+quality+docs" {
		t.Errorf("Axis = %q, want %q", f.Axis, "bugs+quality+docs")
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictChangesRequired)
	}
}

func TestCombine_AgreementLiftsConfidenceOverFloor_ChangesRequired(t *testing.T) {
	// Neither axis alone clears the 0.7 floor, but folded they do:
	// 1 - (1-0.5)(1-0.5) = 0.75 >= 0.7.
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.5, "a.go", 10, "unchecked error return in Save", "", "check the error")),
		outcome("quality", finding("HIGH", 0.5, "a.go", 10, "unchecked error return in Save", "", "check the error")),
	}
	result := combine(outcomes, 0.7)

	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 merged finding", result.Findings)
	}
	if got := result.Findings[0].Confidence; got < 0.7 {
		t.Fatalf("Confidence = %v, want >= 0.7 (folded over floor)", got)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q (agreement should lift over floor)", result.Verdict, review.VerdictChangesRequired)
	}
}

func TestCombine_LineDeltaTooLarge_StaysSeparate(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.6, "a.go", 10, "nil pointer dereference in Foo", "", "check err")),
		outcome("quality", finding("HIGH", 0.6, "a.go", 20, "nil pointer dereference in Foo", "", "check err")),
	}
	result := combine(outcomes, 0.7)

	if len(result.Findings) != 2 {
		t.Fatalf("Findings = %+v, want 2 (line delta > 3 must not merge)", result.Findings)
	}
	for _, f := range result.Findings {
		if f.AgreedBy != 1 {
			t.Errorf("AgreedBy = %d, want 1 for each unmerged finding", f.AgreedBy)
		}
	}
}

func TestCombine_DissimilarTitle_StaysSeparate(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.6, "a.go", 10, "nil pointer dereference in Foo", "", "check err")),
		outcome("quality", finding("HIGH", 0.6, "a.go", 11, "function has three responsibilities", "", "split it up")),
	}
	result := combine(outcomes, 0.7)

	if len(result.Findings) != 2 {
		t.Fatalf("Findings = %+v, want 2 (dissimilar titles must not merge)", result.Findings)
	}
}

func TestTitleSimilarity_ExactlyAtThreshold_Merges(t *testing.T) {
	// A = {foo,bar,baz,qux} (4 tokens), B = {foo,bar,baz} (3 tokens, subset).
	// intersection=3, union=4 -> 0.75, exactly at threshold: must merge.
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.6, "a.go", 10, "foo bar baz qux", "", "r")),
		outcome("quality", finding("HIGH", 0.6, "a.go", 10, "foo bar baz", "", "r")),
	}
	result := combine(outcomes, 0.7)
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 merged finding (similarity == 0.75 threshold)", result.Findings)
	}
	if result.Findings[0].AgreedBy != 2 {
		t.Errorf("AgreedBy = %d, want 2", result.Findings[0].AgreedBy)
	}
}

func TestTitleSimilarity_BelowThreshold_StaysSeparate(t *testing.T) {
	// A = {foo,bar,baz,qux} (4), B = {foo,bar} (2). intersection=2, union=4 -> 0.5.
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.6, "a.go", 10, "foo bar baz qux", "", "r")),
		outcome("quality", finding("HIGH", 0.6, "a.go", 10, "foo bar", "", "r")),
	}
	result := combine(outcomes, 0.7)
	if len(result.Findings) != 2 {
		t.Fatalf("Findings = %+v, want 2 (similarity 0.5 < 0.75 threshold)", result.Findings)
	}
}

func TestCombine_ConflictingRemedies_TensionRecordedBothRetained(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.6, "a.go", 10, "nil pointer dereference in Foo", "", "add a nil check")),
		outcome("quality", finding("HIGH", 0.6, "a.go", 10, "nil pointer dereference in Foo", "", "return an error instead")),
	}
	result := combine(outcomes, 0.7)

	if len(result.Findings) != 2 {
		t.Fatalf("Findings = %+v, want both findings retained on remedy conflict", result.Findings)
	}
	remedies := map[string]bool{}
	for _, f := range result.Findings {
		remedies[f.Remedy] = true
		if f.AgreedBy != 1 {
			t.Errorf("AgreedBy = %d, want 1 for each retained conflicting finding", f.AgreedBy)
		}
	}
	if !remedies["add a nil check"] || !remedies["return an error instead"] {
		t.Errorf("remedies = %+v, want both conflicting remedies retained", remedies)
	}
	if !strings.Contains(result.Summary, "tension") {
		t.Errorf("Summary = %q, want it to mention the tension", result.Summary)
	}
	if !strings.Contains(result.Summary, "add a nil check") || !strings.Contains(result.Summary, "return an error instead") {
		t.Errorf("Summary = %q, want both conflicting remedies named", result.Summary)
	}
}

func TestCombine_AllClean_Approved(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs"),
		outcome("quality"),
		outcome("docs"),
	}
	result := combine(outcomes, 0.7)
	if result.Verdict != review.VerdictApproved {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
	if len(result.Findings) != 0 {
		t.Errorf("Findings = %+v, want empty", result.Findings)
	}
}

func TestCombine_ConfidenceFoldMathExact_ThreeAxes(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.2, "a.go", 10, "shared title here", "", "r")),
		outcome("quality", finding("HIGH", 0.3, "a.go", 10, "shared title here", "", "r")),
		outcome("docs", finding("HIGH", 0.5, "a.go", 10, "shared title here", "", "r")),
	}
	result := combine(outcomes, 0.9)
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 merged finding", result.Findings)
	}
	// 1 - (0.8 * 0.7 * 0.5) = 1 - 0.28 = 0.72
	got := result.Findings[0].Confidence
	if diff := got - 0.72; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Confidence = %v, want 0.72", got)
	}
	if result.Findings[0].AgreedBy != 3 {
		t.Errorf("AgreedBy = %d, want 3", result.Findings[0].AgreedBy)
	}
}

func TestCombine_SingleAxisFinding_AgreedByOneOwnConfidence(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs", finding("HIGH", 0.9, "a.go", 10, "lone finding", "", "r")),
		outcome("quality"),
		outcome("docs"),
	}
	result := combine(outcomes, 0.7)
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1", result.Findings)
	}
	f := result.Findings[0]
	if f.AgreedBy != 1 {
		t.Errorf("AgreedBy = %d, want 1", f.AgreedBy)
	}
	if f.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", f.Confidence)
	}
	if f.Axis != "bugs" {
		t.Errorf("Axis = %q, want %q", f.Axis, "bugs")
	}
}

func TestCombine_RankingOrder_SeverityThenAgreedByThenConfidenceThenAxisThenLocation(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs",
			// r1: ERROR, agreedBy 1, confidence 0.9
			finding("HIGH", 0.9, "r1.go", 1, "r1 title unique alpha", "", "x"),
			// r4: ERROR, agreedBy 1, confidence 0.99 (should rank ahead of r1: same severity/agreedBy, higher confidence)
			finding("HIGH", 0.99, "r4.go", 1, "r4 title unique bravo", "", "x"),
			// merges with quality below into r2: ERROR, agreedBy 2
			finding("HIGH", 0.9, "r2.go", 1, "r2 shared title charlie", "", "same remedy"),
		),
		outcome("quality",
			finding("HIGH", 0.9, "r2.go", 1, "r2 shared title charlie", "", "same remedy"),
			// r3: WARNING severity
			finding("MED", 0.5, "r3.go", 1, "r3 title unique delta", "", "y"),
		),
		outcome("docs",
			// r5: INFO severity, lowest
			finding("LOW", 0.5, "r5.go", 1, "r5 title unique echo", "", "z"),
		),
	}
	result := combine(outcomes, 0.99) // high floor so verdict math doesn't interfere with this test's focus

	if len(result.Findings) != 5 {
		t.Fatalf("Findings = %+v, want 5", result.Findings)
	}
	wantFiles := []string{"r2.go", "r4.go", "r1.go", "r3.go", "r5.go"}
	for i, f := range result.Findings {
		if f.File != wantFiles[i] {
			t.Errorf("Findings[%d].File = %q, want %q (full order: %+v)", i, f.File, wantFiles[i], result.Findings)
		}
	}
}

func TestCombine_RankingTiebreak_FixedAxisOrderThenFileThenLine(t *testing.T) {
	// Same severity, agreedBy, and confidence: tiebreak by fixed axis
	// priority (bugs > quality > docs), then File, then Line.
	outcomes := []axisOutcome{
		outcome("docs", finding("MED", 0.5, "z.go", 1, "docs finding unique title", "", "r")),
		outcome("quality", finding("MED", 0.5, "y.go", 1, "quality finding unique title", "", "r")),
		outcome("bugs", finding("MED", 0.5, "x.go", 1, "bugs finding unique title", "", "r")),
	}
	result := combine(outcomes, 0.7)
	if len(result.Findings) != 3 {
		t.Fatalf("Findings = %+v, want 3", result.Findings)
	}
	wantAxes := []string{"bugs", "quality", "docs"}
	for i, f := range result.Findings {
		if f.Axis != wantAxes[i] {
			t.Errorf("Findings[%d].Axis = %q, want %q", i, f.Axis, wantAxes[i])
		}
	}
}

func TestCombine_RankingStableTiebreak_SameAxisSortsFileThenLine(t *testing.T) {
	outcomes := []axisOutcome{
		outcome("bugs",
			finding("MED", 0.5, "b.go", 5, "second finding unique title", "", "r"),
			finding("MED", 0.5, "a.go", 9, "third finding unique title", "", "r"),
			finding("MED", 0.5, "a.go", 1, "first finding unique title", "", "r"),
		),
	}
	result := combine(outcomes, 0.7)
	if len(result.Findings) != 3 {
		t.Fatalf("Findings = %+v, want 3", result.Findings)
	}
	wantFiles := []struct {
		file string
		line int
	}{
		{"a.go", 1},
		{"a.go", 9},
		{"b.go", 5},
	}
	for i, f := range result.Findings {
		if f.File != wantFiles[i].file || f.Line != wantFiles[i].line {
			t.Errorf("Findings[%d] = %s:%d, want %s:%d", i, f.File, f.Line, wantFiles[i].file, wantFiles[i].line)
		}
	}
}
