package fedpolicy

import "sort"

// TargetRule is one positive allow rule the target host can execute. It
// admits principals under one authority and path prefix that carry every
// label in Require (an empty Require set matches any label set). The target
// host has no deny rules: admission is the union of these positive allows,
// and everything else is refused by default.
type TargetRule struct {
	Prefix  NamePrefix
	Require []Label
}

// TargetPolicy is the importable projection: an order-independent set of
// positive allows plus the mandatory default-deny posture.
type TargetPolicy struct {
	// PublicLabels is the small label set the target host is willing to
	// evaluate. No projected rule ever names a label outside this set.
	PublicLabels []Label

	// Rules are positive allows; their relative order is irrelevant because
	// the target language is their union.
	Rules []TargetRule
}

// Allows reports whether the target host admits n. Labels on n must cover
// the rule's required labels; principals with no matching rule are refused.
func (p TargetPolicy) Allows(n Name) bool {
	public := newLabelSet(p.PublicLabels)
	have := labelSet{}
	for _, l := range n.Labels {
		if public.contains(l) {
			have[l] = struct{}{}
		}
	}
	for _, r := range p.Rules {
		if !r.Prefix.Matches(n) {
			continue
		}
		if newLabelSet(r.Require).isSubset(have) {
			return true
		}
	}
	return false
}

// RuleCount is the number of positive allow rules.
func (p TargetPolicy) RuleCount() int { return len(p.Rules) }

// RejectReason classifies why a candidate rule could not be migrated.
type RejectReason string

const (
	// RejectNonPublicLabel means the candidate names a label the target host
	// does not expose, so it cannot be expressed at all.
	RejectNonPublicLabel RejectReason = "non-public-label"

	// RejectOldDeny means at least one old principal in the candidate's image
	// is denied (or lacks a required label) under the old policy.
	RejectOldDeny RejectReason = "old-deny"

	// RejectNoPreimage means the candidate admits target identities that no old
	// principal maps to. Granting them would create permission out of thin
	// air, so they must be refused.
	RejectNoPreimage RejectReason = "no-preimage"
)

// Witness is one concrete piece of evidence behind a decision.
type Witness struct {
	// Old is the real old principal involved. For a no-preimage gap it is the
	// recovered shortest old principal reaching the image boundary.
	Old Name

	// Target is the target identity the witness concerns.
	Target Name

	// Trace is the transducer trajectory from Old to Target. It is nil when
	// the failure is purely about a label the target cannot express.
	Trace Trace

	// MatchedRule is the old rule index that decided Old, or -1 when the
	// policy default decided it.
	MatchedRule int

	// Effect is the old verdict on Old.
	Effect Effect

	// MissingLabels are the candidate-required labels Old does not carry.
	MissingLabels []Label
}

// Candidate is one input (prefix, label) combination to adjudicate.
type Candidate struct {
	Prefix  NamePrefix
	Require []Label
}

// Rule converts an accepted candidate into an executable target rule.
func (c Candidate) Rule() TargetRule {
	return TargetRule{Prefix: c.Prefix, Require: append([]Label(nil), c.Require...)}
}

// Decision is the adjudication of one candidate.
type Decision struct {
	Candidate Candidate
	Safe      bool

	// Reason is set exactly when Safe is false.
	Reason RejectReason

	// Witnesses evidence every distinct failure mode found.
	Witnesses []Witness

	// Covered is the number of distinct accepting product configurations that
	// qualify under both the old policy and the label requirement.
	Covered int

	// Preimages is the number of distinct accepting old paths explored.
	Preimages int
}

// Report is the complete projection result together with its proof data.
type Report struct {
	Policy TargetPolicy

	// Decisions holds one adjudication per input candidate, in input order.
	Decisions []*Decision

	// MinimumRuleCount is the size of the smallest candidate subset equivalent
	// to the safe set, proven by exhaustive search.
	MinimumRuleCount int
}

// Project adjudicates every candidate, builds the minimum equivalent
// positive-allow policy, and returns the proof-bearing report.
//
// The labeler must be finitary: the old policy's last-match decision and the
// principal labels must depend only on a bounded path prefix. This is the
// condition under which the unbounded-path analysis stays a finite automaton
// problem.
func Project(candidates []Candidate, mapper *Mapper, policy OldPolicy, labeler FinitaryLabeler, publicLabels []Label) *Report {
	mapper.Build()
	public := newLabelSet(publicLabels)
	report := &Report{Policy: TargetPolicy{PublicLabels: append([]Label(nil), publicLabels...)}}

	var safe []*Decision
	for i := range candidates {
		decision := analyzeCandidate(candidates[i], mapper, policy, labeler, public)
		report.Decisions = append(report.Decisions, decision)
		if decision.Safe {
			safe = append(safe, decision)
		}
	}

	chosen := minimumEquivalentSubset(safe)
	report.MinimumRuleCount = len(chosen)
	for _, decision := range chosen {
		report.Policy.Rules = append(report.Policy.Rules, decision.Candidate.Rule())
	}
	sort.SliceStable(report.Policy.Rules, func(i, j int) bool {
		return report.Policy.Rules[i].Prefix.String() < report.Policy.Rules[j].Prefix.String()
	})
	return report
}

// minimumEquivalentSubset returns the smallest subset of safe decisions
// whose union of admitted target identities equals the union of all of them.
// Equivalence of two rule sets is decided structurally over the finite
// candidate set (ruleContainment); the optimum is found by exhaustive
// increasing-size search, with a branch-and-bound path for larger inputs.
func minimumEquivalentSubset(safe []*Decision) []*Decision {
	n := len(safe)
	if n <= 1 {
		return append([]*Decision(nil), safe...)
	}

	// redundant[i] is true when rule i alone covers nothing that the other
	// rules do not cover jointly. It is the standard pre-filter that cannot
	// change the optimum but shrinks the exhaustive search.
	essential := make([]*Decision, 0, n)
	for i, di := range safe {
		coveredByOthers := false
		for j, dj := range safe {
			if i != j && ruleContainment(di.Candidate, dj.Candidate) {
				coveredByOthers = true
				break
			}
		}
		if !coveredByOthers {
			essential = append(essential, di)
		}
	}

	// Small inputs: exhaustive subset search proves global minimality.
	if len(essential) <= 18 {
		return exhaustiveMinimum(safe, essential)
	}
	return branchAndBoundMinimum(safe, essential)
}

// exhaustiveMinimum enumerates subsets of the essential rules in increasing
// cardinality and returns the first whose union equals the full safe union.
func exhaustiveMinimum(all, essential []*Decision) []*Decision {
	for size := 0; size <= len(essential); size++ {
		var best []*Decision
		found := false
		forEachCombination(len(essential), size, func(idx []int) bool {
			subset := make([]*Decision, size)
			for k, v := range idx {
				subset[k] = essential[v]
			}
			if equivalentUnion(all, subset) {
				best = subset
				found = true
				return false
			}
			return true
		})
		if found {
			return best
		}
	}
	return all
}

// branchAndBoundMinimum is the fallback for larger essential sets. It still
// returns a minimum: the lower-bound pruning only discards branches that
// cannot beat the incumbent, never an improving one.
func branchAndBoundMinimum(all, essential []*Decision) []*Decision {
	incumbent := append([]*Decision(nil), essential...)
	best := len(incumbent)

	var search func(start int, chosen []*Decision)
	search = func(start int, chosen []*Decision) {
		if equivalentUnion(all, chosen) {
			if len(chosen) < best {
				best = len(chosen)
				incumbent = append([]*Decision(nil), chosen...)
			}
			return
		}
		if len(chosen)+(len(essential)-start) >= best {
			return
		}
		for i := start; i < len(essential); i++ {
			search(i+1, append(chosen, essential[i]))
		}
	}
	search(0, nil)
	return incumbent
}

// equivalentUnion reports whether subset covers every safe rule's language.
func equivalentUnion(all, subset []*Decision) bool {
outer:
	for _, di := range all {
		for _, dj := range subset {
			if ruleContainment(di.Candidate, dj.Candidate) {
				continue outer
			}
		}
		return false
	}
	return true
}

// ruleContainment reports whether every target identity admitted by inner is
// also admitted by outer. Same authority is required; the outer path prefix
// must be at most as long and equal on its length, and outer's required
// labels must be a subset of inner's (fewer demanded labels admits more).
func ruleContainment(inner, outer Candidate) bool {
	if inner.Prefix.Header() != outer.Prefix.Header() {
		return false
	}
	if !HasPrefix(inner.Prefix.Path, outer.Prefix.Path) {
		return false
	}
	return newLabelSet(outer.Require).isSubset(newLabelSet(inner.Require))
}

// forEachCombination invokes fn with every size-element index combination of
// [0,n); iteration stops early when fn returns false.
func forEachCombination(n, size int, fn func([]int) bool) {
	if size == 0 {
		fn(nil)
		return
	}
	combo := make([]int, size)
	var rec func(pos, start int) bool
	rec = func(pos, start int) bool {
		if pos == size {
			return fn(combo)
		}
		for v := start; v <= n-(size-pos); v++ {
			combo[pos] = v
			if !rec(pos+1, v+1) {
				return false
			}
		}
		return true
	}
	rec(0, 0)
}
