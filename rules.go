// Package handshake is an importable decision package for accepting a rolling
// handshake-policy release plan. It combines:
//
//   - ordered first-match rule sets over a symbolic capability ROBDD,
//   - fallback sequences with per-node effective old/new rule sets and session
//     ticket revalidation starting at the ticket's target node,
//   - a symbolic transition safety check that never enumerates capability
//     vectors,
//   - a scheduler over maintenance windows, precedence dependencies and atomic
//     same-batch switch groups, with a machine-checkable blocking certificate
//     when no legal schedule exists.
package handshake

import (
	"fmt"

	"prescreen.local/ps-802b9bdc43/bdd"
)

// Reject is returned when a rule explicitly denies authentication or when no
// rule condition matches an input. By convention reject is weaker than every
// authentication level.
const Reject = 0

// Outcome is one first-match result. Level == Reject means denial; any
// positive level is an authentication level, where larger values represent
// stronger authentication under the default strength function.
type Outcome struct {
	Reject bool
	Level  int
}

// RejectOutcome is the denial outcome.
func RejectOutcome() Outcome { return Outcome{Reject: true} }

// AuthOutcome is an authentication outcome. Levels must be positive.
func AuthOutcome(level int) Outcome {
	if level <= 0 {
		panic(fmt.Sprintf("handshake: authentication level must be positive, got %d", level))
	}
	return Outcome{Level: level}
}

// IsReject reports whether the outcome denies access.
func (o Outcome) IsReject() bool { return o.Reject || o.Level == Reject }

// String renders an outcome in compact diagnostic form.
func (o Outcome) String() string {
	if o.IsReject() {
		return "REJECT"
	}
	return fmt.Sprintf("AUTH(%d)", o.Level)
}

// Rule is one ordered first-match rule.
type Rule struct {
	Name    string
	When    bdd.BDD
	Outcome Outcome
}

// RuleSet is an ordered rule list. Evaluation takes the first rule whose
// condition holds. Inputs matching none of the listed rules are rejected.
type RuleSet struct {
	Name  string
	Rules []Rule
}

// Evaluate evaluates the ordered rule set on an explicit capability vector.
func (r RuleSet) Evaluate(manager *bdd.Manager, capabilities []bool) (Outcome, error) {
	for _, rule := range r.Rules {
		matched, err := manager.Eval(rule.When, capabilities)
		if err != nil {
			return RejectOutcome(), err
		}
		if matched {
			return rule.Outcome, nil
		}
	}
	return RejectOutcome(), nil
}

// Region is a disjoint partition of capability space produced by the ordered
// first-match semantics. Every input belongs to exactly one region.
type Region struct {
	RuleName string
	Index    int
	Cover    bdd.BDD
	Outcome  Outcome
}

// Regions returns the symbolic disjoint regions induced by first-match
// evaluation. The final region covers every previously unmatched input and is
// therefore an implicit deny region when the listed rules do not cover the
// whole capability space.
func (r RuleSet) Regions(manager *bdd.Manager) []Region {
	regions := make([]Region, 0, len(r.Rules)+1)
	covered := bdd.Zero
	for index, rule := range r.Rules {
		cover := manager.And(rule.When, manager.Not(covered))
		covered = manager.Or(covered, rule.When)
		if manager.IsZero(cover) {
			continue
		}
		regions = append(regions, Region{
			RuleName: rule.Name,
			Index:    index,
			Cover:    cover,
			Outcome:  rule.Outcome,
		})
	}
	remainder := manager.Not(covered)
	if manager.IsZero(remainder) {
		return regions
	}
	regions = append(regions, Region{
		RuleName: "",
		Index:    len(r.Rules),
		Cover:    remainder,
		Outcome:  RejectOutcome(),
	})
	return regions
}

// OutcomeBDDs maps each distinct outcome to the capability set that reaches it
// under first-match semantics. The partition covers all inputs, including the
// implicit deny remainder.
func (r RuleSet) OutcomeBDDs(manager *bdd.Manager) map[Outcome]bdd.BDD {
	result := map[Outcome]bdd.BDD{}
	for _, region := range r.Regions(manager) {
		result[region.Outcome] = manager.Or(result[region.Outcome], region.Cover)
	}
	return result
}

// Partition is an ordered list of adjacent rule indices that are equivalent:
// every capability vector has the same outcome across the whole group. It
// exposes the equivalence-class partition of an ordered rule list.
type Partition struct {
	RuleIndices []int
	Outcome     Outcome
	Cover       bdd.BDD
}

// Partitions merges adjacent first-match regions with identical outcomes.
// Non-adjacent regions are never merged, which preserves the ordered-rule
// interpretation while making the equivalence boundaries explicit.
func (r RuleSet) Partitions(manager *bdd.Manager) []Partition {
	regions := r.Regions(manager)
	partitions := make([]Partition, 0, len(regions))
	for _, region := range regions {
		if len(partitions) > 0 && partitions[len(partitions)-1].Outcome == region.Outcome {
			last := &partitions[len(partitions)-1]
			last.RuleIndices = append(last.RuleIndices, region.Index)
			last.Cover = manager.Or(last.Cover, region.Cover)
			continue
		}
		partitions = append(partitions, Partition{
			RuleIndices: []int{region.Index},
			Outcome:     region.Outcome,
			Cover:       region.Cover,
		})
	}
	return partitions
}

// EquivalentRuleSets reports whether two ordered rule sets define the same
// outcome for every capability vector. It compares symbolic outcome partitions
// and therefore does not enumerate assignments.
func EquivalentRuleSets(manager *bdd.Manager, left, right RuleSet) bool {
	leftOutcomes := left.OutcomeBDDs(manager)
	rightOutcomes := right.OutcomeBDDs(manager)
	if len(leftOutcomes) != len(rightOutcomes) {
		return false
	}
	for outcome, leftBDD := range leftOutcomes {
		rightBDD, ok := rightOutcomes[outcome]
		if !ok || !manager.Equivalent(leftBDD, rightBDD) {
			return false
		}
	}
	return true
}
