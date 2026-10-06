package diag

import (
	"fmt"
	"sort"
)

// MaxWorlds is the maximum number of candidate worlds for strategy search.
const MaxWorlds = 8

// Test is a finite discriminative test. Outcome[w] is the deterministic
// result observed when the true root set is world w. Running the test once
// consumes Cost from the shared budget.
type Test struct {
	Name    string   `json:"name"`
	Cost    int      `json:"cost"`
	Outcome []string `json:"outcome"` // per world
}

// DecisionNode is one node of a non-clairvoyant strategy: it depends only
// on outcomes observed so far. Internal nodes name a test and branch per
// outcome; leaves identify the world.
type DecisionNode struct {
	Test     string                   `json:"test,omitempty"`
	Cost     int                      `json:"cost,omitempty"`
	Branches map[string]*DecisionNode `json:"branches,omitempty"`
	World    int                      `json:"world,omitempty"` // -1 when internal
}

// Strategy is an optimal decision tree together with its worst-case cost.
type Strategy struct {
	Worlds    []string      `json:"worlds"`
	WorstCost int           `json:"worstCost"`
	Root      *DecisionNode `json:"root"`
}

// OptimalStrategy computes a decision tree that identifies the true world
// for every candidate world, minimizing the worst-case cumulative test cost
// (the shared budget spent along the most expensive path). It is exact: a
// memoized minimax search over world subsets.
func OptimalStrategy(worlds []string, tests []Test) (*Strategy, error) {
	n := len(worlds)
	if n == 0 {
		return nil, fmt.Errorf("diag: no candidate worlds given")
	}
	if n > MaxWorlds {
		return nil, fmt.Errorf("diag: %d worlds exceed limit %d", n, MaxWorlds)
	}
	for i, t := range tests {
		if len(t.Outcome) != n {
			return nil, fmt.Errorf("diag: test %q has %d outcomes, want %d", t.Name, len(t.Outcome), n)
		}
		if t.Cost < 0 {
			return nil, fmt.Errorf("diag: test %q has negative cost", t.Name)
		}
		if t.Name == "" {
			return nil, fmt.Errorf("diag: test %d has empty name", i)
		}
	}
	// Identifiability: two worlds with identical outcome vectors can never
	// be distinguished.
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			same := true
			for _, t := range tests {
				if t.Outcome[a] != t.Outcome[b] {
					same = false
					break
				}
			}
			if same {
				return nil, fmt.Errorf("diag: worlds %q and %q are indistinguishable", worlds[a], worlds[b])
			}
		}
	}

	full := (1 << uint(n)) - 1
	memo := make(map[int]int)
	choice := make(map[int]int) // state -> chosen test index

	// partition splits state by the outcome of test ti; ok=false when the
	// test does not discriminate within state.
	partition := func(state, ti int) (map[string]int, bool) {
		groups := make(map[string]int)
		for w := 0; w < n; w++ {
			if state&(1<<uint(w)) != 0 {
				groups[tests[ti].Outcome[w]] |= 1 << uint(w)
			}
		}
		return groups, len(groups) > 1
	}

	var solve func(state int) int
	solve = func(state int) int {
		if state&(state-1) == 0 { // single world: identified
			return 0
		}
		if v, ok := memo[state]; ok {
			return v
		}
		best := -1
		bestTest := -1
		for ti := range tests {
			groups, ok := partition(state, ti)
			if !ok {
				continue
			}
			worst := 0
			for _, sub := range groups {
				if c := solve(sub); c > worst {
					worst = c
				}
			}
			total := tests[ti].Cost + worst
			if best < 0 || total < best {
				best = total
				bestTest = ti
			}
		}
		if best < 0 {
			// Cannot happen after the identifiability check, but guard
			// against degenerate recursion anyway.
			best = int(^uint(0) >> 1)
		}
		memo[state] = best
		choice[state] = bestTest
		return best
	}
	worst := solve(full)

	var build func(state int) *DecisionNode
	build = func(state int) *DecisionNode {
		if state&(state-1) == 0 {
			for w := 0; w < n; w++ {
				if state&(1<<uint(w)) != 0 {
					return &DecisionNode{World: w}
				}
			}
		}
		ti := choice[state]
		groups, _ := partition(state, ti)
		node := &DecisionNode{
			Test:     tests[ti].Name,
			Cost:     tests[ti].Cost,
			Branches: make(map[string]*DecisionNode),
			World:    -1,
		}
		for outcome, sub := range groups {
			node.Branches[outcome] = build(sub)
		}
		return node
	}
	return &Strategy{
		Worlds:    append([]string(nil), worlds...),
		WorstCost: worst,
		Root:      build(full),
	}, nil
}

// ReplayStep is one test performed while replaying a world.
type ReplayStep struct {
	Test    string `json:"test"`
	Outcome string `json:"outcome"`
	Cost    int    `json:"cost"`
}

// ReplayPath records the full trace of replaying one world against a
// strategy, including the identified world and a consistency flag.
type ReplayPath struct {
	World      string       `json:"world"`
	Steps      []ReplayStep `json:"steps"`
	TotalCost  int          `json:"totalCost"`
	Identified string       `json:"identified"`
	OK         bool         `json:"ok"`
}

// ReplayStrategy non-clairvoyantly replays every world through the
// strategy: at each internal node it applies the world's own outcome for
// the chosen test, never peeking at the world's identity. It verifies that
// each path identifies its world and returns the worst cost across paths.
func ReplayStrategy(s *Strategy, tests []Test) ([]ReplayPath, int, error) {
	byName := make(map[string]Test, len(tests))
	for _, t := range tests {
		byName[t.Name] = t
	}
	paths := make([]ReplayPath, 0, len(s.Worlds))
	worst := 0
	for w, wname := range s.Worlds {
		p := ReplayPath{World: wname}
		node := s.Root
		for node.World < 0 {
			t, ok := byName[node.Test]
			if !ok {
				return nil, 0, fmt.Errorf("diag: strategy references unknown test %q", node.Test)
			}
			outcome := t.Outcome[w]
			next, ok := node.Branches[outcome]
			if !ok {
				return nil, 0, fmt.Errorf("diag: world %q produced unhandled outcome %q for test %q",
					wname, outcome, node.Test)
			}
			p.Steps = append(p.Steps, ReplayStep{Test: node.Test, Outcome: outcome, Cost: t.Cost})
			p.TotalCost += t.Cost
			node = next
		}
		p.Identified = s.Worlds[node.World]
		p.OK = node.World == w
		if p.TotalCost > worst {
			worst = p.TotalCost
		}
		paths = append(paths, p)
	}
	return paths, worst, nil
}

// Format renders the strategy as an indented, human-readable tree with
// per-branch outcomes and cumulative worst-cost annotation.
func (s *Strategy) Format() string {
	var buf []byte
	var walk func(n *DecisionNode, depth int)
	walk = func(n *DecisionNode, depth int) {
		indent := ""
		for i := 0; i < depth; i++ {
			indent += "  "
		}
		if n.World >= 0 {
			buf = append(buf, fmt.Sprintf("%s=> identify %q\n", indent, s.Worlds[n.World])...)
			return
		}
		buf = append(buf, fmt.Sprintf("%stest %q (cost %d)\n", indent, n.Test, n.Cost)...)
		keys := make([]string, 0, len(n.Branches))
		for k := range n.Branches {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			buf = append(buf, fmt.Sprintf("%s [%s]:\n", indent, k)...)
			walk(n.Branches[k], depth+1)
		}
	}
	walk(s.Root, 0)
	return string(buf)
}
