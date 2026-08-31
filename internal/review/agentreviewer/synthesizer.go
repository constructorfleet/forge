package agentreviewer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/review"
)

// titleSimilarityThreshold is the minimum token-set Jaccard similarity (see
// titleSimilarity) two findings' titles must share, alongside a location
// match, to be considered the same finding across axes (issue #160). 0.75
// was chosen (over normalized Levenshtein) because it is cheap, order-
// independent (word reordering between two axes' phrasing of the same
// defect still matches), and tolerant of one axis appending or omitting a
// qualifying clause while still rejecting titles about unrelated defects.
const titleSimilarityThreshold = 0.75

// maxLineDelta is the maximum absolute difference between two findings'
// line numbers, within the same file, for them to be considered at the
// same location (issue #160 acceptance criteria: "within a few lines").
const maxLineDelta = 3

// tokenPattern splits a lowercased title into alphanumeric tokens for
// titleSimilarity's token-set Jaccard comparison.
var tokenPattern = regexp.MustCompile(`[a-z0-9]+`)

// titleSimilarity returns the token-set Jaccard similarity of a and b:
// |tokens(a) ∩ tokens(b)| / |tokens(a) ∪ tokens(b)|, computed over each
// title's lowercased alphanumeric tokens as a set (duplicates and word
// order do not matter). Two empty titles are trivially identical (1.0); one
// empty and one non-empty share nothing (0.0).
func titleSimilarity(a, b string) float64 {
	setA := tokenSet(a)
	setB := tokenSet(b)
	if len(setA) == 0 && len(setB) == 0 {
		return 1
	}
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}

	intersection := 0
	for t := range setA {
		if _, ok := setB[t]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	return float64(intersection) / float64(union)
}

func tokenSet(s string) map[string]struct{} {
	tokens := tokenPattern.FindAllString(strings.ToLower(s), -1)
	set := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		set[tok] = struct{}{}
	}
	return set
}

// candidate is one axis's finding, mapped onto review.Finding via
// findingsForAxis, paired with the axis that produced it and the raw title
// text used for cross-axis similarity matching.
//
// The envelope's axisFinding has no dedicated "title" field distinct from
// Message (rubric.md's output contract describes Message as "One-sentence
// description of the defect" — already title-shaped), so title is the raw,
// un-composed axisFinding.Message rather than composeMessage's
// message+evidence text: folding evidence in would make near-identical
// findings whose axes wrote different supporting evidence score as less
// similar than they should.
type candidate struct {
	axis    string
	title   string
	finding review.Finding
}

// buildCandidates maps every axisOutcome's findings onto candidates, in
// fixed axis order (bugs, quality, docs — the axes package var), preserving
// each axis's own finding order. This fixed traversal order is what makes
// clusterCandidates and the final ranking tiebreak deterministic.
func buildCandidates(outcomes []axisOutcome, confidenceFloor float64) []candidate {
	var candidates []candidate
	for _, o := range outcomes {
		findings, _ := findingsForAxis(o.env, o.axis, confidenceFloor)
		for i, f := range findings {
			candidates = append(candidates, candidate{
				axis:    o.axis,
				title:   o.env.Findings[i].Message,
				finding: f,
			})
		}
	}
	return candidates
}

// locationAndTitleMatch reports whether a and b are the same finding per
// issue #160's dedup rule: same non-empty File, line numbers within
// maxLineDelta, and title similarity at or above titleSimilarityThreshold.
// Two findings with no File never match, so unanchored findings are never
// merged.
func locationAndTitleMatch(a, b candidate) bool {
	if a.finding.File == "" || b.finding.File == "" || a.finding.File != b.finding.File {
		return false
	}
	if absInt(a.finding.Line-b.finding.Line) > maxLineDelta {
		return false
	}
	return titleSimilarity(a.title, b.title) >= titleSimilarityThreshold
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// clusterCandidates greedily groups candidates (in the fixed order
// buildCandidates produced) into clusters: a candidate joins the first
// existing cluster containing any member it locationAndTitleMatch-es,
// otherwise it starts a new cluster. Cluster and member order both follow
// candidates' input order, keeping the whole synthesis deterministic.
func clusterCandidates(candidates []candidate) [][]candidate {
	var clusters [][]candidate
	for _, c := range candidates {
		placed := false
		for i := range clusters {
			for _, member := range clusters[i] {
				if locationAndTitleMatch(c, member) {
					clusters[i] = append(clusters[i], c)
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
		if !placed {
			clusters = append(clusters, []candidate{c})
		}
	}
	return clusters
}

// distinctRemedies returns the distinct non-empty remedies (case-
// insensitive, whitespace-trimmed) named across cluster, in cluster order.
func distinctRemedies(cluster []candidate) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cluster {
		remedy := strings.TrimSpace(c.finding.Remedy)
		if remedy == "" {
			continue
		}
		key := strings.ToLower(remedy)
		if !seen[key] {
			seen[key] = true
			out = append(out, remedy)
		}
	}
	return out
}

// severityRank orders review.Severity from most (0) to least (2) severe,
// for both merged-severity selection and ranking.
func severityRank(s review.Severity) int {
	switch s {
	case review.SeverityError:
		return 0
	case review.SeverityWarning:
		return 1
	default:
		return 2
	}
}

// axisPriority returns name's fixed position in the axes package var
// (bugs=0, quality=1, docs=2), used as a deterministic tiebreak. An unknown
// axis name sorts last.
func axisPriority(name string) int {
	for i, ax := range axes {
		if ax.name == name {
			return i
		}
	}
	return len(axes)
}

// mergeCluster folds a matched, non-conflicting cluster (one or more
// candidates from possibly different axes, all the same finding) into one
// review.Finding:
//   - Severity is the highest (most severe) severity across the cluster.
//   - Confidence is the folded probability 1 − Π(1−cᵢ) over every
//     contributing finding's confidence — cross-axis agreement can only
//     raise it (see the doc comment on combine).
//   - AgreedBy is the count of distinct axes represented in the cluster.
//   - File/Line/Message/Remedy are taken from the "canonical" member: the
//     one with the highest individual confidence (fixed axis priority,
//     then input order, breaks ties), so the merged finding's displayed
//     text is always some axis's own words rather than a synthetic blend.
//   - Axis is every distinct contributing axis, in fixed axis order,
//     joined with "+" (e.g. "bugs+quality"); a single-axis cluster reduces
//     to that axis's own name unchanged.
func mergeCluster(cluster []candidate) review.Finding {
	axisSet := map[string]bool{}
	product := 1.0
	severity := review.SeverityInfo
	canonical := cluster[0]

	for _, c := range cluster {
		axisSet[c.axis] = true
		product *= 1 - c.finding.Confidence
		if severityRank(c.finding.Severity) < severityRank(severity) {
			severity = c.finding.Severity
		}
		switch {
		case c.finding.Confidence > canonical.finding.Confidence:
			canonical = c
		case c.finding.Confidence == canonical.finding.Confidence && axisPriority(c.axis) < axisPriority(canonical.axis):
			canonical = c
		}
	}

	axisNames := make([]string, 0, len(axisSet))
	for _, ax := range axes {
		if axisSet[ax.name] {
			axisNames = append(axisNames, ax.name)
		}
	}

	return review.Finding{
		Severity:   severity,
		File:       canonical.finding.File,
		Line:       canonical.finding.Line,
		Message:    canonical.finding.Message,
		Confidence: 1 - product,
		Axis:       strings.Join(axisNames, "+"),
		Remedy:     canonical.finding.Remedy,
		AgreedBy:   len(axisSet),
	}
}

// synthesizeFindings implements issue #160's deterministic synthesis:
// dedup matched candidates into merged findings (mergeCluster), surface
// remedy conflicts within a matched cluster as tensions instead of
// force-merging them, and rank the result. It returns the final ranked
// findings alongside any tension descriptions for the caller to fold into
// Result.Summary.
//
// A cluster that matched on location+title but disagrees on remedy is a
// tension (issue #160 acceptance criteria): rather than picking one
// "canonical" remedy and silently discarding the other axis's contrary
// advice, every candidate in that cluster is kept as its own, unmerged
// Finding (AgreedBy=1, its own confidence), and one tension description
// naming both remedies is recorded. The prior-art algorithm also calls for
// a tension when "one axis lists something as an assurance while another
// flags it"; the current envelope (envelope.go) carries only Findings, no
// per-axis assurance/clean list, so that half is not implemented here —
// only the remedy-conflict half, which the current data model supports.
func synthesizeFindings(outcomes []axisOutcome, confidenceFloor float64) ([]review.Finding, []string) {
	candidates := buildCandidates(outcomes, confidenceFloor)
	clusters := clusterCandidates(candidates)

	var findings []review.Finding
	var tensions []string

	for _, cluster := range clusters {
		if len(cluster) > 1 {
			if remedies := distinctRemedies(cluster); len(remedies) > 1 {
				tensions = append(tensions, fmt.Sprintf(
					"conflicting remedies at %s:%d across axes: %s",
					cluster[0].finding.File, cluster[0].finding.Line, strings.Join(remedies, " vs "),
				))
				for _, c := range cluster {
					findings = append(findings, c.finding)
				}
				continue
			}
		}
		findings = append(findings, mergeCluster(cluster))
	}

	rankFindings(findings)
	return findings, tensions
}

// rankFindings sorts findings in place per issue #160's ranking rule:
// bucket by display severity (ERROR, then WARNING, then INFO); within a
// bucket, AgreedBy descending, then merged Confidence descending, then the
// fixed axis order (bugs, quality, docs — via primaryAxisPriority) as a
// tiebreak; finally File then Line, so two findings that are equal on
// every other dimension still sort in a fixed, run-to-run-stable order.
func rankFindings(findings []review.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if a.AgreedBy != b.AgreedBy {
			return a.AgreedBy > b.AgreedBy
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if pa, pb := primaryAxisPriority(a.Axis), primaryAxisPriority(b.Axis); pa != pb {
			return pa < pb
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}

// primaryAxisPriority returns the fixed axis priority (see axisPriority) of
// joinedAxis's first contributing axis, e.g. "bugs" for "bugs+quality".
func primaryAxisPriority(joinedAxis string) int {
	first, _, _ := strings.Cut(joinedAxis, "+")
	return axisPriority(first)
}
