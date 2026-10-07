package dectree

import (
	"fmt"
	"sort"
	"strings"
)

// sortedOutcomes returns the branch labels of n in sorted order for
// deterministic rendering and traversal.
func sortedOutcomes(n *Node) []string {
	keys := make([]string, 0, len(n.Branches))
	for k := range n.Branches {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// String renders the strategy tree with candidate and test names when the
// solution was produced by Solve.
func (s *Solution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "worst=%d expected=%s\n", s.WorstCost, s.Expected.RatString())
	s.renderNode(&b, s.Root, "")
	for _, g := range s.Ambiguous {
		note := "indistinguishable"
		if g.BudgetBound {
			note = "unseparated (budget exhausted)"
		}
		fmt.Fprintf(&b, "%s: %s\n", note, s.names(g.Candidates))
		for _, obs := range g.Evidence {
			fmt.Fprintf(&b, "  evidence: %s -> %q\n", s.testName(obs.Test), obs.Outcome)
		}
	}
	return b.String()
}

func (s *Solution) renderNode(b *strings.Builder, n *Node, indent string) {
	if n.IsLeaf() {
		if len(n.Leaf) > 1 {
			fmt.Fprintf(b, "%sAMBIGUOUS %s\n", indent, s.names(n.Leaf))
		} else {
			fmt.Fprintf(b, "%sleaf %s\n", indent, s.names(n.Leaf))
		}
		return
	}
	fmt.Fprintf(b, "%stest %s (cost %d)\n", indent, s.testName(n.Test), s.testCost(n.Test))
	for _, o := range sortedOutcomes(n) {
		fmt.Fprintf(b, "%s  %q ->\n", indent, o)
		s.renderNode(b, n.Branches[o], indent+"    ")
	}
}

func (s *Solution) names(idx []int) string {
	parts := make([]string, len(idx))
	for i, c := range idx {
		if s.prob != nil && s.prob.Candidates[c].Name != "" {
			parts[i] = s.prob.Candidates[c].Name
		} else {
			parts[i] = fmt.Sprintf("#%d", c)
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func (s *Solution) testName(ti int) string {
	if s.prob != nil && ti >= 0 && ti < len(s.prob.Tests) && s.prob.Tests[ti].Name != "" {
		return s.prob.Tests[ti].Name
	}
	return fmt.Sprintf("test#%d", ti)
}

func (s *Solution) testCost(ti int) int {
	if s.prob != nil && ti >= 0 && ti < len(s.prob.Tests) {
		return s.prob.Tests[ti].Cost
	}
	return 0
}
