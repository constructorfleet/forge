package spec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Teagan42/forge/internal/planning"
)

var reqIDPattern = regexp.MustCompile(`^REQ-\d{3}$`)

func ExtractRequirementIDs(content string) []string {
	var ids []string
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "REQ-") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				id := parts[0]
				if strings.HasSuffix(id, ":") || strings.HasSuffix(id, ".") {
					id = id[:len(id)-1]
				}
				if reqIDPattern.MatchString(id) {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

func UniqueRequirementIDs(ids []string) ([]string, error) {
	seen := make(map[string]bool)
	var unique []string
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("duplicate requirement ID: %s", id)
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique, nil
}

func ValidateRequirementIDs(ids []string) error {
	expected := 1
	for _, id := range ids {
		if !reqIDPattern.MatchString(id) {
			return fmt.Errorf("invalid requirement ID format: %s (expected REQ-NNN)", id)
		}
		if expected != 1 && id != fmt.Sprintf("REQ-%03d", expected) {
			return fmt.Errorf("requirement ID out of sequence: got %s, expected REQ-%03d", id, expected)
		}
		expected++
	}
	return nil
}

func findDerivedFrom(derived []planning.DerivedFromEntry, id string) *planning.DerivedFromEntry {
	for i := range derived {
		if derived[i].ID == id {
			return &derived[i]
		}
	}
	return nil
}

func ValidateSpecDeterministic(
	spec *planning.Artifact,
	decisions map[string]*planning.Artifact,
	goalRev string,
	decisionRevs map[string]string,
	repoRev string,
) error {
	if spec.Kind != planning.KindSpec {
		return fmt.Errorf("not a specification artifact")
	}

	for _, req := range RequiredSections {
		found := false
		for _, s := range spec.Sections {
			if s.Heading == req {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing required section: %s", req)
		}
	}

	allContent := ""
	for _, s := range spec.Sections {
		allContent += s.Body + "\n"
	}
	reqIDs := ExtractRequirementIDs(allContent)
	if _, err := UniqueRequirementIDs(reqIDs); err != nil {
		return err
	}
	if err := ValidateRequirementIDs(reqIDs); err != nil {
		return err
	}

	for id, dec := range decisions {
		if dec.State == "open" || dec.State == "needs_human" {
			return fmt.Errorf("blocking decision %s is not resolved", id)
		}
	}

	if spec.DerivedFrom == nil || len(spec.DerivedFrom) == 0 {
		return errors.New("specification missing derived_from provenance")
	}

	if entry := findDerivedFrom(spec.DerivedFrom, "goal"); entry != nil {
		if entry.Revision != goalRev {
			return fmt.Errorf("spec derived_from goal revision mismatch: got %s, expected %s", entry.Revision, goalRev)
		}
	}

	for decID, decRev := range decisionRevs {
		if entry := findDerivedFrom(spec.DerivedFrom, decID); entry != nil {
			if entry.Revision != decRev {
				return fmt.Errorf("spec derived_from decision %s revision mismatch: got %s, expected %s", decID, entry.Revision, decRev)
			}
		}
	}

	if entry := findDerivedFrom(spec.DerivedFrom, "repository"); entry != nil {
		if entry.Revision != repoRev {
			return fmt.Errorf("spec derived_from repository revision mismatch: got %s, expected %s", entry.Revision, repoRev)
		}
	}

	return nil
}

func ValidateSpec(spec *planning.Artifact) error {
	if spec.Kind != planning.KindSpec {
		return fmt.Errorf("not a specification artifact")
	}

	for _, req := range RequiredSections {
		found := false
		for _, s := range spec.Sections {
			if s.Heading == req {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing required section: %s", req)
		}
	}

	allContent := ""
	for _, s := range spec.Sections {
		allContent += s.Body + "\n"
	}
	reqIDs := ExtractRequirementIDs(allContent)
	if _, err := UniqueRequirementIDs(reqIDs); err != nil {
		return err
	}
	if err := ValidateRequirementIDs(reqIDs); err != nil {
		return err
	}

	return nil
}
