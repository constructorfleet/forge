package tracker

// MergeEligibilityInput assembles the capability slices EvaluateMergeEligibility
// composes into one neutral MergeEligibility verdict (issue #284/#296: "the
// merge verdict is composed, not owned").
//
// ChecksBlocker is the CI slice's already-classified checks blocker (nil when
// every required check currently reports Success). Check-vs-requirement
// comparison stays a polling-specific concern owned by internal/ci (it must
// respect required-check ordering and an observed-check settle race that has
// no equivalent in the neutral model), so it is handed in pre-classified
// rather than re-derived here from raw checks.
//
// MergeStatus is the SCM slice's Conflict/Behind report. It is the
// provider-general half of the composition (spec #284, US10: "conflict/behind
// from SCM"): an SCM-provider caller that has no upstream gate of its own for
// these states wires them in here so the neutral verdict carries them. The
// GitHub composition deliberately leaves MergeStatus at its zero value —
// GitHub routes conflict/behind through action-taking pollers earlier in the
// same poll (see internal/ci.Supervisor.evaluateMergeEligibility), so wiring
// them a second time here would double-gate and defeat that degradation. This
// slice being unwired by GitHub is by design, not an omission.
//
// Merged is deliberately not part of this composition — it is a terminal-
// success signal the CI Supervisor checks directly against its own already-
// fetched merge status (see internal/ci.Supervisor.evaluateMergeEligibility),
// not something EvaluateMergeEligibility itself reads.
type MergeEligibilityInput struct {
	ChecksBlocker *MergeBlocker
	MergeStatus   ChangeRequestMergeStatus
}

// EvaluateMergeEligibility composes Forge's neutral MergeEligibility verdict
// from independently-sourced CI and SCM capability slices. Each blocker
// records which capability reported it (Source) and the provider's raw
// diagnostic (RawDetail) alongside the neutral Reason orchestration acts on.
func EvaluateMergeEligibility(in MergeEligibilityInput) MergeEligibility {
	var blockers []MergeBlocker

	if in.ChecksBlocker != nil {
		blockers = append(blockers, *in.ChecksBlocker)
	}
	if in.MergeStatus.Conflicted {
		blockers = append(blockers, MergeBlocker{Reason: Conflict, Source: CapabilitySCM, RawDetail: in.MergeStatus.RawDetail})
	}
	if in.MergeStatus.Behind {
		blockers = append(blockers, MergeBlocker{Reason: Behind, Source: CapabilitySCM, RawDetail: in.MergeStatus.RawDetail})
	}

	return MergeEligibility{Mergeable: len(blockers) == 0, Blockers: blockers}
}
