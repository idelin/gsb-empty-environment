package main

import (
	"fmt"
	"sort"
	"strings"
)

// cutCapacity independently sums the capacities of all original edges
// crossing from the source side to the sink side.
func cutCapacity(g *Graph, sourceSide []string) int64 {
	in := make(map[string]bool, len(sourceSide))
	for _, n := range sourceSide {
		in[n] = true
	}
	var cap int64
	for _, e := range g.Edges {
		if e.From == e.To {
			continue
		}
		if in[e.From] && !in[e.To] {
			cap += e.Capacity
		}
	}
	return cap
}

// verifyCutCapacities checks every enumerated cut against the maximum
// flow value using an independent capacity computation.
func verifyCutCapacities(g *Graph, maxFlow int64, cuts [][]string) error {
	for _, cut := range cuts {
		if c := cutCapacity(g, cut); c != maxFlow {
			return fmt.Errorf("cut %v has capacity %d, want %d", cut, c, maxFlow)
		}
	}
	return nil
}

// bruteForceMinCuts exhaustively enumerates all 2^(n-2) s-t cuts and
// returns the minimum capacity together with every cut attaining it.
func bruteForceMinCuts(g *Graph) (int64, [][]string) {
	nodes := g.NodeList()
	var rest []string
	for _, n := range nodes {
		if n != g.Source && n != g.Sink {
			rest = append(rest, n)
		}
	}
	best := int64(-1)
	var cuts [][]string
	for mask := 0; mask < (1 << uint(len(rest))); mask++ {
		side := []string{g.Source}
		for i, n := range rest {
			if mask&(1<<uint(i)) != 0 {
				side = append(side, n)
			}
		}
		sort.Strings(side)
		c := cutCapacity(g, side)
		if best < 0 || c < best {
			best = c
			cuts = [][]string{side}
		} else if c == best {
			cuts = append(cuts, side)
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return lexLess(cuts[i], cuts[j]) })
	return best, cuts
}

// bruteForceCheck recomputes all minimum cuts by full subset enumeration
// and reconciles them with the solver output. When the solver was
// truncated, only membership of the emitted cuts is verified.
func bruteForceCheck(g *Graph, res *SolveResult) error {
	minCap, want := bruteForceMinCuts(g)
	if minCap != res.MaxFlow {
		return fmt.Errorf("brute-force minimum capacity %d != max flow %d", minCap, res.MaxFlow)
	}
	wantSet := make(map[string]bool, len(want))
	for _, c := range want {
		wantSet[strings.Join(c, "\x01")] = true
	}
	for _, c := range res.Cuts {
		if !wantSet[strings.Join(c, "\x01")] {
			return fmt.Errorf("enumerated cut %v is not a minimum cut", c)
		}
	}
	if res.Complete && len(res.Cuts) != len(want) {
		return fmt.Errorf("enumeration claims completeness with %d cuts, brute force found %d", len(res.Cuts), len(want))
	}
	return nil
}
