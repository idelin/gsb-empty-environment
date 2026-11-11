package netsec

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// MaxRules is the maximum number of toggle rules an explanation request may
// carry. With at most 18 rules a full subset enumeration stays at or below
// 2^18 = 262144 callback invocations.
const MaxRules = 18

// RuleConfig is the on/off state of one desensitization toggle rule.
type RuleConfig struct {
	// RuleID is the stable identifier of the rule, e.g. "k_anonymize".
	RuleID string
	// Enabled is the rule's switch state inside a given policy version.
	Enabled bool
}

// AnonymityReport is the deterministic output of an AnonymityFunc for one
// concrete rule configuration.
type AnonymityReport struct {
	// CandidateSetSize is the size of the candidate set an adversary can
	// distinguish after desensitization. A value of 1 means the output has
	// collapsed to a single, directly identifiable person.
	CandidateSetSize int
}

// Identified reports whether the configuration reproduces the target effect:
// the candidate set has shrunk to exactly one person.
func (r AnonymityReport) Identified() bool {
	return r.CandidateSetSize == 1
}

// AnonymityFunc evaluates one complete rule configuration and returns its
// anonymity report. Implementations must be deterministic: the same
// configuration must always produce the same report, because the explanation
// treats every callback invocation as an auditable piece of evidence.
type AnonymityFunc func(config []RuleConfig) (AnonymityReport, error)

// ExplainRequest carries two policy versions to compare. Baseline is the
// reference version, Target is the version whose extra identifiability must be
// explained. Both slices must contain the same rules in the same order.
type ExplainRequest struct {
	Baseline []RuleConfig
	Target   []RuleConfig
	Evaluate AnonymityFunc
}

// InvocationRecord is the summary of one AnonymityFunc call. The evaluated
// configuration can be reconstructed with ExplainReport.Config(Mask).
type InvocationRecord struct {
	// Seq is the zero-based, reproducible call order (numeric subset order).
	Seq int
	// Mask is the global rule-index bit mask of the subset applied to the
	// baseline configuration, bit i corresponding to rule index i.
	Mask uint32
	// CandidateSetSize is the reported candidate-set size; it is -1 when
	// the callback returned an error.
	CandidateSetSize int
	// Identified records whether CandidateSetSize == 1 for this call.
	Identified bool
	// Err holds the callback error, if any.
	Err error
}

// String renders a compact single-line summary suitable for audit logs.
func (c InvocationRecord) String() string {
	if c.Err != nil {
		return fmt.Sprintf("call %d mask=%#0*x error=%v", c.Seq, maskDigits(c.Mask), c.Mask, c.Err)
	}
	return fmt.Sprintf("call %d mask=%#0*x candidate_set_size=%d identified=%t",
		c.Seq, maskDigits(c.Mask), c.Mask, c.CandidateSetSize, c.Identified)
}

// DeletionEvidence records what happens when one change of a minimal set is
// removed while all other changes of that set are kept.
type DeletionEvidence struct {
	// RuleIndex is the index of the removed rule in the request slices.
	RuleIndex int
	// RuleID is the identifier of the removed rule.
	RuleID string
	// ReducedMask is the minimal-set mask with RuleIndex cleared.
	ReducedMask uint32
	// CandidateSetSize is the candidate-set size observed for ReducedMask;
	// it is -1 when the callback returned an error.
	CandidateSetSize int
	// Identified reports whether the reduced configuration still collapses
	// the candidate set to one person. For a minimal set it must be false.
	Identified bool
	// Err holds the callback error, if any.
	Err error
}

// MinimalExplanation is one inclusion-minimal set of rule changes that
// reproduces the single-person collapse.
type MinimalExplanation struct {
	// Mask is the global rule-index bit mask of the rule changes.
	Mask uint32
	// RuleIndices are the involved rule indices, sorted ascending.
	RuleIndices []int
	// Cardinality is the number of rule changes in the set (popcount of Mask).
	Cardinality int
	// CandidateSetSize is the candidate-set size observed for Mask; it is
	// always 1 for a reproducing minimal set.
	CandidateSetSize int
	// DeletionProof holds one entry per rule in the set. Each entry proves
	// that removing exactly that rule breaks the reproduction.
	DeletionProof []DeletionEvidence
}

// ExplainReport is the full, audit-ready result of Explain.
type ExplainReport struct {
	// DifferingRuleIndices are the rules whose switch state differs between
	// the baseline and the target, sorted ascending. Only these rules can be
	// part of a rule change.
	DifferingRuleIndices []int
	// BaselineCandidateSetSize is the candidate-set size of the unchanged
	// baseline configuration; it is -1 when the callback returned an error.
	BaselineCandidateSetSize int
	// TargetCandidateSetSize is the candidate-set size of the full target
	// configuration; it is -1 when the callback returned an error.
	TargetCandidateSetSize int
	// TotalSubsets is the number of subsets enumerated, equal to
	// 2^len(DifferingRuleIndices), including the empty subset.
	TotalSubsets int
	// ReproducingSubsets is the number of enumerated subsets whose
	// candidate-set size is exactly 1.
	ReproducingSubsets int
	// MinimumCardinality is the smallest number of changes among the
	// inclusion-minimal sets; it is -1 when no reproducing subset exists.
	MinimumCardinality int
	// MinimalSets contains every inclusion-minimal reproducing subset,
	// sorted by cardinality and then by mask for deterministic output.
	MinimalSets []MinimalExplanation
	// Invocations contains one summary per AnonymityFunc call, including
	// the baseline and target evaluations plus every enumerated subset, in
	// reproducible numeric subset order.
	Invocations []InvocationRecord

	baseline []RuleConfig
	target   []RuleConfig
	diff     []int
}

// Rules returns a fresh copy of the rule configuration carried by the mask:
// the baseline with every rule bit set in mask flipped to its target state.
func (r *ExplainReport) Rules(mask uint32) []RuleConfig {
	config := make([]RuleConfig, len(r.baseline))
	copy(config, r.baseline)
	for _, ruleIndex := range r.diff {
		if mask&(1<<uint(ruleIndex)) != 0 {
			config[ruleIndex] = r.target[ruleIndex]
		}
	}
	return config
}

// Config is an alias of Rules for callers that prefer the configuration name.
func (r *ExplainReport) Config(mask uint32) []RuleConfig {
	return r.Rules(mask)
}

// RuleIDs returns the identifiers of the rules selected by mask, sorted by
// rule index. It returns nil for an empty mask.
func (r *ExplainReport) RuleIDs(mask uint32) []string {
	var ids []string
	for _, ruleIndex := range r.diff {
		if mask&(1<<uint(ruleIndex)) != 0 {
			ids = append(ids, r.baseline[ruleIndex].RuleID)
		}
	}
	return ids
}

// Explain enumerates every subset of the rules that differ between the two
// policy versions, evaluates each subset with the deterministic callback, and
// returns all inclusion-minimal subsets that shrink the candidate set to one
// person. It does not assume monotonicity: every subset is evaluated, so
// non-monotonic rule interactions are handled exactly.
//
// At most MaxRules rules are supported; the full enumeration performs
// 2^n + 1 callback invocations (each subset once, plus the explicit target
// evaluation; the empty subset is the baseline itself).
func Explain(req ExplainRequest) (*ExplainReport, error) {
	if req.Evaluate == nil {
		return nil, errors.New("netsec: nil anonymity callback")
	}
	if len(req.Baseline) == 0 {
		return nil, errors.New("netsec: empty baseline rules")
	}
	if len(req.Baseline) > MaxRules {
		return nil, fmt.Errorf("netsec: %d rules exceed maximum of %d", len(req.Baseline), MaxRules)
	}
	if len(req.Target) != len(req.Baseline) {
		return nil, fmt.Errorf("netsec: baseline has %d rules but target has %d",
			len(req.Baseline), len(req.Target))
	}
	for i := range req.Baseline {
		if req.Baseline[i].RuleID != req.Target[i].RuleID {
			return nil, fmt.Errorf("netsec: rule id mismatch at index %d: %q vs %q",
				i, req.Baseline[i].RuleID, req.Target[i].RuleID)
		}
	}

	diff := make([]int, 0, len(req.Baseline))
	for i := range req.Baseline {
		if req.Baseline[i].Enabled != req.Target[i].Enabled {
			diff = append(diff, i)
		}
	}

	report := &ExplainReport{
		DifferingRuleIndices:     append([]int(nil), diff...),
		MinimumCardinality:       -1,
		BaselineCandidateSetSize: -1,
		TargetCandidateSetSize:   -1,
		baseline:                 req.Baseline,
		target:                   req.Target,
		diff:                     diff,
	}

	baselineReport, err := req.Evaluate(cloneRules(req.Baseline))
	baselineCall := InvocationRecord{Seq: 0, Mask: 0, CandidateSetSize: -1, Err: err}
	if err == nil {
		baselineCall.CandidateSetSize = baselineReport.CandidateSetSize
		baselineCall.Identified = baselineReport.Identified()
		report.BaselineCandidateSetSize = baselineReport.CandidateSetSize
	}

	subsetCount := 1 << len(diff)
	report.TotalSubsets = subsetCount
	identified := make([]bool, subsetCount)
	candidateSizes := make([]int, subsetCount)
	callErrors := make([]error, subsetCount)
	for i := range candidateSizes {
		candidateSizes[i] = -1
	}

	// Subset 0 is the baseline; its evaluation is reused rather than repeated.
	identified[0] = baselineCall.Identified
	if err == nil {
		candidateSizes[0] = baselineReport.CandidateSetSize
	}
	callErrors[0] = err

	report.Invocations = make([]InvocationRecord, 0, subsetCount+1)
	report.Invocations = append(report.Invocations, baselineCall)

	for sub := 1; sub < subsetCount; sub++ {
		mask := localToGlobalMask(sub, diff)
		config := applyMask(req.Baseline, req.Target, diff, sub)
		anonymity, callErr := req.Evaluate(config)

		call := InvocationRecord{
			Seq:              sub,
			Mask:             mask,
			CandidateSetSize: -1,
			Err:              callErr,
		}
		if callErr == nil {
			call.CandidateSetSize = anonymity.CandidateSetSize
			call.Identified = anonymity.Identified()
			identified[sub] = call.Identified
			candidateSizes[sub] = anonymity.CandidateSetSize
			if call.Identified {
				report.ReproducingSubsets++
			}
		}
		report.Invocations = append(report.Invocations, call)
	}

	// Evaluate the full target configuration explicitly so the audit trail
	// records it; when diff rules exist it equals the all-diff subset.
	targetMask := localToGlobalMask(subsetCount-1, diff)
	targetReport, err := req.Evaluate(cloneRules(req.Target))
	targetCall := InvocationRecord{
		Seq:              subsetCount,
		Mask:             targetMask,
		CandidateSetSize: -1,
		Err:              err,
	}
	if err == nil {
		targetCall.CandidateSetSize = targetReport.CandidateSetSize
		targetCall.Identified = targetReport.Identified()
		report.TargetCandidateSetSize = targetReport.CandidateSetSize
	}
	report.Invocations = append(report.Invocations, targetCall)

	// Find every inclusion-minimal reproducing subset without assuming
	// monotonicity. hasSub[sub] is true when some strict submask of sub is
	// itself reproducing. Because the predicate need not be monotone, a
	// single lowest-bit recurrence misses strict submasks that contain that
	// bit; instead every set bit is removed in turn and both the immediate
	// submask and its own strict submasks are considered, in O(n*2^n).
	hasSub := make([]bool, subsetCount)
	for sub := 1; sub < subsetCount; sub++ {
		bitsLeft := sub
		for bitsLeft != 0 {
			bit := bitsLeft & -bitsLeft
			bitsLeft ^= bit
			strictSub := sub ^ bit
			if identified[strictSub] || hasSub[strictSub] {
				hasSub[sub] = true
				break
			}
		}
	}

	type candidate struct {
		local int
		mask  uint32
	}
	var candidates []candidate
	for sub := 0; sub < subsetCount; sub++ {
		if identified[sub] && !hasSub[sub] {
			candidates = append(candidates, candidate{
				local: sub,
				mask:  localToGlobalMask(sub, diff),
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		ci := bits.OnesCount32(candidates[i].mask)
		cj := bits.OnesCount32(candidates[j].mask)
		if ci != cj {
			return ci < cj
		}
		return candidates[i].mask < candidates[j].mask
	})

	report.MinimalSets = make([]MinimalExplanation, 0, len(candidates))
	for _, cand := range candidates {
		if report.MinimumCardinality == -1 {
			report.MinimumCardinality = bits.OnesCount32(cand.mask)
		}

		indices := make([]int, 0, bits.OnesCount32(cand.mask))
		for _, ruleIndex := range diff {
			if cand.mask&(1<<uint(ruleIndex)) != 0 {
				indices = append(indices, ruleIndex)
			}
		}

		proof := make([]DeletionEvidence, 0, len(indices))
		for _, ruleIndex := range indices {
			reducedGlobal := cand.mask ^ (1 << uint(ruleIndex))
			reducedLocal := globalToLocalMask(reducedGlobal, diff)
			proof = append(proof, DeletionEvidence{
				RuleIndex:        ruleIndex,
				RuleID:           req.Baseline[ruleIndex].RuleID,
				ReducedMask:      reducedGlobal,
				CandidateSetSize: candidateSizes[reducedLocal],
				Identified:       identified[reducedLocal],
				Err:              callErrors[reducedLocal],
			})
		}

		report.MinimalSets = append(report.MinimalSets, MinimalExplanation{
			Mask:             cand.mask,
			RuleIndices:      indices,
			Cardinality:      bits.OnesCount32(cand.mask),
			CandidateSetSize: candidateSizes[cand.local],
			DeletionProof:    proof,
		})
	}

	return report, nil
}

func cloneRules(rules []RuleConfig) []RuleConfig {
	clone := make([]RuleConfig, len(rules))
	copy(clone, rules)
	return clone
}

// applyMask builds the configuration for a local subset index over the
// differing rules: selected diff positions take the target state, every other
// rule keeps the baseline state.
func applyMask(baseline, target []RuleConfig, diff []int, localSubset int) []RuleConfig {
	config := cloneRules(baseline)
	for localIndex, ruleIndex := range diff {
		if localSubset&(1<<uint(localIndex)) != 0 {
			config[ruleIndex] = target[ruleIndex]
		}
	}
	return config
}

// localToGlobalMask converts a subset index over diff positions into a mask
// expressed in original rule-index positions.
func localToGlobalMask(localSubset int, diff []int) uint32 {
	var mask uint32
	for localIndex, ruleIndex := range diff {
		if localSubset&(1<<uint(localIndex)) != 0 {
			mask |= 1 << uint(ruleIndex)
		}
	}
	return mask
}

// globalToLocalMask converts an original rule-index mask back into a subset
// index over the diff positions.
func globalToLocalMask(globalMask uint32, diff []int) int {
	var localSubset int
	for localIndex, ruleIndex := range diff {
		if globalMask&(1<<uint(ruleIndex)) != 0 {
			localSubset |= 1 << uint(localIndex)
		}
	}
	return localSubset
}

// maskDigits gives a stable minimum width (in hex digits) for log formatting,
// keeping all masks in one report aligned to the same width.
func maskDigits(mask uint32) int {
	switch {
	case mask <= 0xff:
		return 2
	case mask <= 0xffff:
		return 4
	default:
		return 8
	}
}
