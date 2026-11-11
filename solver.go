package netsec

import "fmt"

// edge is a single difference constraint: x[to] - x[from] <= weight.
type edge struct {
	from   int
	to     int
	weight int64
	desc   string
}

// bellmanFord solves a system of difference constraints. Distances start at
// zero for every variable, which is equivalent to adding a super-source with
// zero-weight edges, so any negative cycle in the graph is detected.
//
// On success it returns distances that satisfy every edge (a feasible
// assignment). On failure it returns the edges forming a negative cycle,
// which is a minimal infeasibility proof.
func bellmanFord(numVars int, edges []edge) (dist []int64, cycle []edge) {
	dist = make([]int64, numVars)
	pred := make([]int, numVars)
	for i := range pred {
		pred[i] = -1
	}

	updated := -1
	for iter := 0; iter < numVars; iter++ {
		updated = -1
		for ei, e := range edges {
			if dist[e.to] > dist[e.from]+e.weight {
				dist[e.to] = dist[e.from] + e.weight
				pred[e.to] = ei
				updated = e.to
			}
		}
		if updated == -1 {
			return dist, nil
		}
	}

	// Walk predecessor pointers numVars times to land inside the cycle.
	v := updated
	for i := 0; i < numVars; i++ {
		v = edges[pred[v]].from
	}
	start := v
	for {
		e := edges[pred[v]]
		cycle = append(cycle, e)
		v = e.from
		if v == start {
			break
		}
	}
	return nil, cycle
}

// CycleEvidence is a negative-weight cycle of constraints proving that a
// system (or a system plus a hypothesised relation) is infeasible.
type CycleEvidence struct {
	Steps  []string // human-readable constraints forming the cycle, in order
	Weight int64    // total cycle weight; negative means contradiction
}

func evidenceFrom(cycle []edge) *CycleEvidence {
	ev := &CycleEvidence{}
	for _, e := range cycle {
		ev.Steps = append(ev.Steps, e.desc)
		ev.Weight += e.weight
	}
	return ev
}

func (ev *CycleEvidence) String() string {
	s := fmt.Sprintf("negative cycle (total weight %d ms):", ev.Weight)
	for _, step := range ev.Steps {
		s += "\n  " + step
	}
	return s
}
