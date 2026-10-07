// Package dectree builds optimal decision trees for identifying an unknown
// object among a small set of candidates using deterministic, costly tests.
//
// A strategy is a pure decision tree: the test performed at each step depends
// only on the outcomes observed so far, never on the unknown true object.
// Trees are optimized lexicographically: first minimize the worst-case total
// cost along any path, then the expected total cost under rational candidate
// probabilities. Every root-to-leaf path stays within the problem budget.
// Candidates that no test can tell apart are reported, together with the
// observations (evidence) that led to them.
package dectree

import (
	"errors"
	"fmt"
	"math/big"
)

// MaxCandidates is the largest supported candidate set.
const MaxCandidates = 8

// Candidate is one possible identity of the unknown object.
type Candidate struct {
	Name string
	// Prob is the prior probability (or weight) of this candidate.
	// It must be a non-negative rational; the values are normalized
	// internally, so weights such as 1/2, 1/4, 1/4 are fine.
	Prob *big.Rat
}

// Test is a deterministic experiment with an integer cost.
type Test struct {
	Name string
	Cost int
	// Outcome[i] is the result this test deterministically returns when the
	// unknown object is candidate i.
	Outcome []string
}

// Problem describes one identification task. Every root-to-leaf path of a
// valid strategy must spend at most Budget in total test fees.
type Problem struct {
	Candidates []Candidate
	Tests      []Test
	Budget     int
}

func (p *Problem) validate() error {
	n := len(p.Candidates)
	if n == 0 {
		return errors.New("dectree: no candidates")
	}
	if n > MaxCandidates {
		return fmt.Errorf("dectree: %d candidates exceeds maximum %d", n, MaxCandidates)
	}
	if p.Budget < 0 {
		return errors.New("dectree: negative budget")
	}
	sum := new(big.Rat)
	for i, c := range p.Candidates {
		if c.Prob == nil || c.Prob.Sign() < 0 {
			return fmt.Errorf("dectree: candidate %d has invalid probability", i)
		}
		sum.Add(sum, c.Prob)
	}
	if sum.Sign() == 0 {
		return errors.New("dectree: candidate probabilities sum to zero")
	}
	for i, t := range p.Tests {
		if t.Cost < 0 {
			return fmt.Errorf("dectree: test %d (%q) has negative cost", i, t.Name)
		}
		if len(t.Outcome) != n {
			return fmt.Errorf("dectree: test %d (%q) has %d outcomes, want %d", i, t.Name, len(t.Outcome), n)
		}
	}
	return nil
}

// Observation records one performed test and its outcome; the list of
// observations along a root-to-leaf path is the evidence for the leaf.
type Observation struct {
	Test    int
	Outcome string
}

// Node is one node of a strategy tree. An internal node applies Test and
// follows the branch labelled by the observed outcome; a leaf (Test == -1)
// reports the candidate set it could not further distinguish.
type Node struct {
	Test     int
	Branches map[string]*Node
	Leaf     []int
}

// IsLeaf reports whether n is a leaf.
func (n *Node) IsLeaf() bool { return n.Test < 0 }

// AmbiguousGroup is a set of candidates that could not be told apart,
// together with the evidence (the observations) that led to them.
type AmbiguousGroup struct {
	Candidates []int
	Evidence   []Observation
	// BudgetBound is true when some test could separate the group but the
	// remaining path budget forbids it; false means the candidates are
	// truly indistinguishable by every test in the problem.
	BudgetBound bool
}

// Solution is the optimal strategy for a Problem.
type Solution struct {
	Root *Node
	// WorstCost is the maximum total test fee over all paths.
	WorstCost int
	// Expected is the expected total test fee under the (normalized)
	// candidate probabilities.
	Expected *big.Rat
	// Ambiguous lists the indistinguishable candidate groups and their
	// evidence, in tree order.
	Ambiguous []AmbiguousGroup

	prob *Problem
}

// setMembers returns the candidate indices in a bitmask, in order.
func setMembers(set uint16) []int {
	var out []int
	for i := 0; set != 0; i, set = i+1, set>>1 {
		if set&1 != 0 {
			out = append(out, i)
		}
	}
	return out
}

// bitSet converts a candidate index list to a bitmask.
func bitSet(members []int) uint16 {
	var set uint16
	for _, i := range members {
		set |= 1 << uint(i)
	}
	return set
}

// sumProb returns the total probability weight of a candidate set.
func sumProb(p *Problem, set uint16) *big.Rat {
	s := new(big.Rat)
	for i := range p.Candidates {
		if set&(1<<uint(i)) != 0 {
			s.Add(s, p.Candidates[i].Prob)
		}
	}
	return s
}

// partition splits a candidate set by the outcome of test ti. It reports
// whether the test is informative (produces at least two non-empty groups).
// Outcomes are returned in first-appearance order for determinism.
func partition(p *Problem, set uint16, ti int) (outcomes []string, groups []uint16, informative bool) {
	index := map[string]int{}
	for i := 0; i < len(p.Candidates); i++ {
		if set&(1<<uint(i)) == 0 {
			continue
		}
		o := p.Tests[ti].Outcome[i]
		j, ok := index[o]
		if !ok {
			j = len(groups)
			index[o] = j
			outcomes = append(outcomes, o)
			groups = append(groups, 0)
		}
		groups[j] |= 1 << uint(i)
	}
	return outcomes, groups, len(groups) > 1
}

// allSameOutcome reports whether every candidate in set yields the same
// outcome under test ti.
func allSameOutcome(p *Problem, set uint16, ti int) bool {
	first := -1
	for i := range p.Candidates {
		if set&(1<<uint(i)) == 0 {
			continue
		}
		if first < 0 {
			first = i
			continue
		}
		if p.Tests[ti].Outcome[i] != p.Tests[ti].Outcome[first] {
			return false
		}
	}
	return true
}
