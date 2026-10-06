// Package dectree builds optimal decision trees for identifying an
// unknown object drawn from at most eight candidates using
// deterministic, integer-priced tests under a per-path budget.
//
// The solver minimises, lexicographically, the worst-case total cost
// along any root-to-leaf path and then the expected total cost under
// rational candidate probabilities. Candidates that no affordable
// test can distinguish are reported together at a leaf, accompanied
// by the evidence (the sequence of observed test outcomes) that led
// there.
//
// A strategy is a pure decision tree: the test chosen at every node
// depends only on the outcomes observed so far, never on the unknown
// true object.
package dectree

import (
	"fmt"
	"math/big"
	"math/bits"
	"sort"
	"strings"
)

// Candidate is a possible identity of the unknown object.
type Candidate struct {
	ID   string
	Prob *big.Rat // prior probability, rational, >= 0
}

// Test is a deterministic experiment. Outcomes maps every candidate
// ID to the outcome label the test is guaranteed to return when that
// candidate is the true object.
type Test struct {
	Name     string
	Cost     int64 // non-negative integer fee, paid once per use on a path
	Outcomes map[string]string
}

// Instance is a full problem specification.
type Instance struct {
	Candidates []Candidate
	Tests      []Test
	Budget     int64 // maximum total test cost allowed on any single path
}

// Step is one observation: a test and the outcome it returned.
type Step struct {
	Test    string
	Outcome string
}

// Strategy is a decision-tree node. A leaf reports the candidates
// still possible; an internal node names the test to perform and has
// one branch per outcome observable from the current candidate set.
type Strategy struct {
	Leaf       bool
	Candidates []string             // candidates consistent with the evidence
	Evidence   []Step               // observations on the path from the root
	Test       string               // test chosen at an internal node
	Branches   map[string]*Strategy // outcome -> subtree

	PathCost int64    // total test cost spent from the root to this node
	Worst    int64    // maximum additional cost below this node
	Exp      *big.Rat // expected additional cost below this node (conditional on reaching it)
}

// Validate checks the instance for structural sanity.
func (inst *Instance) Validate() error {
	n := len(inst.Candidates)
	if n == 0 || n > 8 {
		return fmt.Errorf("dectree: need between 1 and 8 candidates, got %d", n)
	}
	seen := make(map[string]bool, n)
	total := new(big.Rat)
	for _, c := range inst.Candidates {
		if c.ID == "" {
			return fmt.Errorf("dectree: candidate with empty ID")
		}
		if seen[c.ID] {
			return fmt.Errorf("dectree: duplicate candidate %q", c.ID)
		}
		seen[c.ID] = true
		if c.Prob == nil || c.Prob.Sign() < 0 {
			return fmt.Errorf("dectree: candidate %q has nil or negative probability", c.ID)
		}
		total.Add(total, c.Prob)
	}
	if total.Sign() <= 0 {
		return fmt.Errorf("dectree: candidate probabilities sum to zero")
	}
	names := make(map[string]bool, len(inst.Tests))
	for _, t := range inst.Tests {
		if t.Name == "" {
			return fmt.Errorf("dectree: test with empty name")
		}
		if names[t.Name] {
			return fmt.Errorf("dectree: duplicate test %q", t.Name)
		}
		names[t.Name] = true
		if t.Cost < 0 {
			return fmt.Errorf("dectree: test %q has negative cost", t.Name)
		}
		for _, c := range inst.Candidates {
			if _, ok := t.Outcome(c.ID); !ok {
				return fmt.Errorf("dectree: test %q has no outcome for candidate %q", t.Name, c.ID)
			}
		}
	}
	if inst.Budget < 0 {
		return fmt.Errorf("dectree: negative budget %d", inst.Budget)
	}
	return nil
}

// Outcome returns the deterministic result of the test for a candidate.
func (t Test) Outcome(candidateID string) (string, bool) {
	o, ok := t.Outcomes[candidateID]
	return o, ok
}

// Solve computes the optimal strategy: among all decision trees whose
// every root-to-leaf path stays within the budget and that refine the
// candidates as far as the budget allows, it minimises the worst-case
// total cost first and the expected total cost second.
func Solve(inst *Instance) (*Strategy, error) {
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	s := newSolver(inst)
	full := uint8(1<<uint(len(inst.Candidates))) - 1
	return s.build(full, inst.Budget, nil, 0), nil
}

// ExpectedCost returns the expected total cost of the strategy,
// normalised by the sum of candidate probabilities.
func (n *Strategy) ExpectedCost() *big.Rat {
	return new(big.Rat).Set(n.Exp)
}

// Leaves returns all leaves of the strategy in deterministic order.
func (n *Strategy) Leaves() []*Strategy {
	if n.Leaf {
		return []*Strategy{n}
	}
	var out []*Strategy
	for _, k := range sortedOutcomes(n.Branches) {
		out = append(out, n.Branches[k].Leaves()...)
	}
	return out
}

// String renders the tree in a human-readable indented form.
func (n *Strategy) String() string {
	var b strings.Builder
	n.write(&b, 0)
	return b.String()
}

func (n *Strategy) write(b *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)
	if n.Leaf {
		fmt.Fprintf(b, "%sleaf %v (path cost %d, evidence %s)\n",
			indent, n.Candidates, n.PathCost, formatEvidence(n.Evidence))
		return
	}
	fmt.Fprintf(b, "%stest %q (worst +%d, exp +%s)\n", indent, n.Test, n.Worst, n.Exp)
	for _, o := range sortedOutcomes(n.Branches) {
		fmt.Fprintf(b, "%s  on %q:\n", indent, o)
		n.Branches[o].write(b, depth+2)
	}
}

func formatEvidence(ev []Step) string {
	if len(ev) == 0 {
		return "[]"
	}
	parts := make([]string, len(ev))
	for i, s := range ev {
		parts[i] = s.Test + "=" + s.Outcome
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// ---------------------------------------------------------------------------
// solver internals
// ---------------------------------------------------------------------------

type state struct {
	mask   uint8
	budget int64
}

type decision struct {
	worst int64
	exp   *big.Rat // sum over candidates in the set of prob * additional cost
	test  int      // index into Instance.Tests, -1 for a terminal leaf
}

type solver struct {
	inst      *Instance
	pSum      [256]*big.Rat
	testIndex map[string]int
	memo      map[state]decision
	fmemo     map[state][]objective
}

func newSolver(inst *Instance) *solver {
	s := &solver{
		inst:      inst,
		testIndex: make(map[string]int, len(inst.Tests)),
		memo:      make(map[state]decision),
		fmemo:     make(map[state][]objective),
	}
	for i := range inst.Tests {
		s.testIndex[inst.Tests[i].Name] = i
	}
	for mask := 1; mask < 1<<uint(len(inst.Candidates)); mask++ {
		sum := new(big.Rat)
		for i, c := range inst.Candidates {
			if mask&(1<<uint(i)) != 0 {
				sum.Add(sum, c.Prob)
			}
		}
		s.pSum[mask] = sum
	}
	return s
}

// partition groups the candidates in mask by the outcome the test
// deterministically returns for them. ok is false when the test does
// not split the set.
func (s *solver) partition(mask uint8, ti int) (groups map[string]uint8, ok bool) {
	t := &s.inst.Tests[ti]
	groups = make(map[string]uint8)
	m := mask
	for m != 0 {
		i := uint(bits.TrailingZeros8(m))
		m &= m - 1
		groups[t.Outcomes[s.inst.Candidates[i].ID]] |= 1 << i
	}
	return groups, len(groups) >= 2
}

// solve returns the optimal decision for the given candidate set and
// remaining budget. When no affordable test splits the set, the only
// legal move is to stop, yielding a leaf that reports the set.
func (s *solver) solve(mask uint8, budget int64) decision {
	if bits.OnesCount8(mask) <= 1 {
		return decision{test: -1, exp: new(big.Rat)}
	}
	st := state{mask: mask, budget: budget}
	if d, ok := s.memo[st]; ok {
		return d
	}
	best := decision{test: -1, exp: new(big.Rat)}
	for ti := range s.inst.Tests {
		t := &s.inst.Tests[ti]
		if t.Cost > budget {
			continue
		}
		groups, ok := s.partition(mask, ti)
		if !ok {
			continue
		}
		exp := new(big.Rat).Mul(s.pSum[mask], big.NewRat(t.Cost, 1))
		var worst int64
		for _, g := range groups {
			d := s.solve(g, budget-t.Cost)
			if d.worst > worst {
				worst = d.worst
			}
			exp.Add(exp, d.exp)
		}
		cand := decision{worst: worst + t.Cost, exp: exp, test: ti}
		if best.test == -1 || cand.worst < best.worst ||
			(cand.worst == best.worst && cand.exp.Cmp(best.exp) < 0) {
			best = cand
		}
	}
	s.memo[st] = best
	return best
}

func (s *solver) build(mask uint8, budget int64, evidence []Step, pathCost int64) *Strategy {
	d := s.solve(mask, budget)
	node := &Strategy{
		Candidates: s.idsOf(mask),
		Evidence:   append([]Step(nil), evidence...),
		PathCost:   pathCost,
		Worst:      d.worst,
		Exp:        new(big.Rat).Quo(d.exp, s.pSum[mask]),
	}
	if d.test < 0 {
		node.Leaf = true
		return node
	}
	t := s.inst.Tests[d.test]
	node.Test = t.Name
	groups, _ := s.partition(mask, d.test)
	node.Branches = make(map[string]*Strategy, len(groups))
	for _, outcome := range sortedMaskKeys(groups) {
		ev := append(append([]Step(nil), evidence...), Step{Test: t.Name, Outcome: outcome})
		node.Branches[outcome] = s.build(groups[outcome], budget-t.Cost, ev, pathCost+t.Cost)
	}
	return node
}

func (s *solver) idsOf(mask uint8) []string {
	var ids []string
	for i, c := range s.inst.Candidates {
		if mask&(1<<uint(i)) != 0 {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func sortedMaskKeys(m map[string]uint8) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedOutcomes(m map[string]*Strategy) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
