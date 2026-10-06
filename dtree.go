package diaglab

import (
	"fmt"
	"sort"
	"strings"
)

// MaxWorlds bounds the exact decision-tree DP (state space is 2^worlds).
const MaxWorlds = 8

// World is one candidate hypothesis: the true root set is RootSet.
type World struct {
	Name    string
	RootSet []string
}

// Test distinguishes worlds. Outcome maps a world index to an outcome key;
// running the test costs Cost from the shared budget.
type Test struct {
	Name    string
	Cost    int
	Outcome func(world int) string
}

// Policy is an adaptive, non-clairvoyant decision tree: the test chosen at
// each node depends only on outcomes observed so far.
type Policy struct {
	Leaf      bool
	RootSet   []string           // identified root set (leaf only)
	Test      string             // test to run (internal only)
	TestCost  int                // cost of that test
	Branches  map[string]*Policy // outcome key -> subtree (internal only)
	WorstCost int                // min worst-case remaining cost from this node
}

func rootSetKey(rs []string) string {
	cp := append([]string(nil), rs...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}

// OptimalPolicy computes a decision tree minimizing the worst-case total
// test cost needed to identify the true root set, exact over all 2^n world
// subsets. Identification only needs to pin down the root set: worlds
// sharing the same RootSet need not be separated.
func OptimalPolicy(worlds []World, tests []Test) (*Policy, error) {
	n := len(worlds)
	if n == 0 || n > MaxWorlds {
		return nil, fmt.Errorf("need 1..%d worlds, got %d", MaxWorlds, n)
	}
	for i, t := range tests {
		if t.Cost <= 0 {
			return nil, fmt.Errorf("test %q has non-positive cost", t.Name)
		}
		if t.Outcome == nil {
			return nil, fmt.Errorf("test %d (%q) has no outcome function", i, t.Name)
		}
	}

	labels := make([]string, n)
	for i, w := range worlds {
		labels[i] = rootSetKey(w.RootSet)
	}
	outcomes := make([][]string, len(tests))
	for t := range tests {
		outcomes[t] = make([]string, n)
		for w := 0; w < n; w++ {
			outcomes[t][w] = tests[t].Outcome(w)
		}
	}

	type entry struct {
		cost int // -1 = infeasible
		test int // -1 = leaf / none
	}
	memo := map[int]entry{}
	var solve func(mask int) entry
	solve = func(mask int) entry {
		if e, ok := memo[mask]; ok {
			return e
		}
		first := -1
		for w := 0; w < n; w++ {
			if mask&(1<<uint(w)) != 0 {
				first = w
				break
			}
		}
		terminal := true
		for w := 0; w < n; w++ {
			if mask&(1<<uint(w)) != 0 && labels[w] != labels[first] {
				terminal = false
				break
			}
		}
		if terminal {
			memo[mask] = entry{0, -1}
			return memo[mask]
		}
		best := entry{-1, -1}
		for t := range tests {
			groups := map[string]int{}
			for w := 0; w < n; w++ {
				if mask&(1<<uint(w)) != 0 {
					groups[outcomes[t][w]] |= 1 << uint(w)
				}
			}
			if len(groups) < 2 {
				continue // does not refine this ambiguity set
			}
			worst := 0
			feasible := true
			for _, g := range groups {
				if g == mask {
					feasible = false
					break
				}
				sub := solve(g)
				if sub.cost < 0 {
					feasible = false
					break
				}
				if sub.cost > worst {
					worst = sub.cost
				}
			}
			if !feasible {
				continue
			}
			total := tests[t].Cost + worst
			if best.cost < 0 || total < best.cost {
				best = entry{total, t}
			}
		}
		memo[mask] = best
		return best
	}

	full := (1 << uint(n)) - 1
	if solve(full).cost < 0 {
		return nil, fmt.Errorf("tests cannot distinguish all candidate root sets")
	}

	var build func(mask int) *Policy
	build = func(mask int) *Policy {
		e := memo[mask]
		if e.test < 0 {
			for w := 0; w < n; w++ {
				if mask&(1<<uint(w)) != 0 {
					return &Policy{Leaf: true, RootSet: worlds[w].RootSet, WorstCost: 0}
				}
			}
		}
		p := &Policy{
			Test:      tests[e.test].Name,
			TestCost:  tests[e.test].Cost,
			Branches:  map[string]*Policy{},
			WorstCost: e.cost,
		}
		groups := map[string]int{}
		for w := 0; w < n; w++ {
			if mask&(1<<uint(w)) != 0 {
				groups[outcomes[e.test][w]] |= 1 << uint(w)
			}
		}
		for oc, g := range groups {
			p.Branches[oc] = build(g)
		}
		return p
	}
	return build(full), nil
}

// Format renders the policy as an indented, non-clairvoyant strategy.
func (p *Policy) Format() string {
	var b strings.Builder
	var rec func(node *Policy, depth int)
	rec = func(node *Policy, depth int) {
		pad := strings.Repeat("  ", depth)
		if node.Leaf {
			fmt.Fprintf(&b, "%s=> identify root set {%s}\n", pad, rootSetKey(node.RootSet))
			return
		}
		fmt.Fprintf(&b, "%srun %s (cost %d, worst remaining %d):\n", pad, node.Test, node.TestCost, node.WorstCost)
		keys := make([]string, 0, len(node.Branches))
		for k := range node.Branches {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s  if %s:\n", pad, k)
			rec(node.Branches[k], depth+2)
		}
	}
	rec(p, 0)
	return b.String()
}

type ReplayStep struct {
	Test    string
	Outcome string
	Cost    int
}

type ReplayRecord struct {
	World      string
	Expected   []string
	Identified []string
	Steps      []ReplayStep
	TotalCost  int
	OK         bool
}

// Replay walks every world through the policy using only the outcomes that
// world would produce, verifying identification and accumulated cost.
func Replay(p *Policy, worlds []World, tests []Test) ([]ReplayRecord, error) {
	byName := map[string]Test{}
	for _, t := range tests {
		byName[t.Name] = t
	}
	var recs []ReplayRecord
	for w := 0; w < len(worlds); w++ {
		rec := ReplayRecord{World: worlds[w].Name, Expected: worlds[w].RootSet}
		node := p
		for !node.Leaf {
			t, ok := byName[node.Test]
			if !ok {
				return recs, fmt.Errorf("policy references unknown test %q", node.Test)
			}
			oc := t.Outcome(w)
			child, ok := node.Branches[oc]
			if !ok {
				return recs, fmt.Errorf("world %s: test %s produced uncovered outcome %q", worlds[w].Name, node.Test, oc)
			}
			rec.Steps = append(rec.Steps, ReplayStep{Test: node.Test, Outcome: oc, Cost: node.TestCost})
			rec.TotalCost += node.TestCost
			node = child
		}
		rec.Identified = node.RootSet
		rec.OK = rootSetKey(rec.Identified) == rootSetKey(rec.Expected)
		recs = append(recs, rec)
	}
	return recs, nil
}

// WorstReplayCost returns the maximum total cost over replayed worlds; it
// must equal the policy's root WorstCost.
func WorstReplayCost(recs []ReplayRecord) int {
	worst := 0
	for _, r := range recs {
		if r.TotalCost > worst {
			worst = r.TotalCost
		}
	}
	return worst
}
