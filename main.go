package main

import (
	"fmt"
	"strings"
)

func feats(ids ...string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// demoProblem builds the sample scenario: versions v0..v5 forming a DAG of
// rollback edges, three nodes, a shared maintenance budget, forbidden
// versions from irreversible operations, and a compatibility relation
// (versions whose indices differ by more than 2 are incompatible).
func demoProblem() *Problem {
	versions := []*Version{
		{ID: "v0", Features: feats()},
		{ID: "v1", Features: feats("a")},
		{ID: "v2", Features: feats("a", "b")},
		{ID: "v3", Features: feats("a", "b", "c")},
		{ID: "v4", Features: feats("a", "b", "c", "d")},
		{ID: "v5", Features: feats("a", "b", "c", "d", "e")},
	}
	edges := []Edge{
		{From: "v5", To: "v4", Cost: 1},
		{From: "v5", To: "v3", Cost: 6},
		{From: "v4", To: "v3", Cost: 2},
		{From: "v4", To: "v2", Cost: 3},
		{From: "v3", To: "v2", Cost: 1},
		{From: "v3", To: "v1", Cost: 2},
		{From: "v2", To: "v1", Cost: 1},
		{From: "v2", To: "v0", Cost: 1},
		{From: "v1", To: "v0", Cost: 1},
	}
	g := NewGraph(versions, edges)
	num := func(s string) int {
		var n int
		fmt.Sscanf(s, "v%d", &n)
		return n
	}
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	return &Problem{
		Graph:  g,
		Budget: 5,
		Nodes: []*Node{
			{Name: "N1", Current: "v5", Forbidden: feats()},
			{Name: "N2", Current: "v4", Forbidden: feats("v0")},
			{Name: "N3", Current: "v3", Forbidden: feats()},
		},
		Compatible: func(a, b string) bool { return abs(num(a)-num(b)) <= 2 },
	}
}

func main() {
	p := demoProblem()

	fmt.Println("== Candidate ranking (features desc, total steps asc) ==")
	for _, c := range RankCandidates(p) {
		fmt.Printf("target %s: retained features=%d, total steps=%d\n", c.Target, c.Features, c.TotalSteps)
	}

	cand, plan, err := Solve(p)
	if err != nil {
		fmt.Println("no feasible rollback plan:", err)
		return
	}

	fmt.Printf("\n== Selected common target: %s (features=%d, total steps=%d) ==\n",
		cand.Target, cand.Features, cand.TotalSteps)
	for i, n := range p.Nodes {
		fmt.Printf("%s path: %s\n", n.Name, strings.Join(cand.Paths[i], " -> "))
	}

	fmt.Printf("\n== Transition trajectory (node order, budget %d, total cost %d) ==\n",
		p.Budget, plan.TotalCost)
	for step, tr := range plan.Transitions {
		fmt.Printf("step %d: %s %s -> %s (cost %d, undoes [%s]) => %s\n",
			step+1, p.Nodes[tr.Node].Name, tr.Edge.From, tr.Edge.To, tr.Edge.Cost,
			strings.Join(p.Graph.UndoneFeatures(tr.Edge), ","), strings.Join(tr.Versions, " "))
	}

	fmt.Println("\n== Independent verification ==")
	if err := VerifyCandidate(p, cand); err != nil {
		fmt.Println("candidate verification FAILED:", err)
	} else {
		fmt.Println("candidate and per-node paths verified OK")
	}
	if err := VerifyPlan(p, plan); err != nil {
		fmt.Println("trajectory verification FAILED:", err)
	} else {
		fmt.Println("trajectory verified OK (order, edges, budget, forbidden, compatibility)")
	}
}
