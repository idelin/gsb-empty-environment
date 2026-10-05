package solver

import "sort"

// Solve finds the minimum-cost set of authorized edges to cut so the graph
// becomes acyclic, then emits the topological build plan. Only edges marked
// cuttable may be cut, and each cut spends exactly the artifact designated
// for that edge.
func Solve(g *Graph) (*Plan, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	_, idx := nodeIndex(g)

	s := &search{
		g:        g,
		idx:      idx,
		active:   make([]bool, len(g.Edges)),
		bestCost: -1,
	}
	for i := range s.active {
		s.active[i] = true
	}

	// Greedy pass first: gives an initial incumbent and detects cycles that
	// no authorized edge can ever break.
	if !s.greedy() {
		return &Plan{Feasible: false, Reason: "graph contains a cycle with no authorized (cuttable) edge"}, nil
	}

	s.active = make([]bool, len(g.Edges))
	for i := range s.active {
		s.active[i] = true
	}
	s.cost = 0
	s.branch()
	if s.bestCost < 0 {
		return &Plan{Feasible: false, Reason: "graph contains a cycle with no authorized (cuttable) edge"}, nil
	}

	plan := &Plan{Feasible: true, TotalCost: s.bestCost}
	for i, cut := range s.bestCuts {
		if !cut {
			continue
		}
		e := g.Edges[i]
		plan.Cuts = append(plan.Cuts, Cut{
			EdgeID:   e.ID,
			From:     e.From,
			To:       e.To,
			Artifact: e.Artifact,
			Cost:     e.Cost,
		})
	}
	sort.Slice(plan.Cuts, func(a, b int) bool { return plan.Cuts[a].EdgeID < plan.Cuts[b].EdgeID })
	plan.TopoOrder = topoOrder(g, s.bestActive)
	return plan, nil
}

type search struct {
	g      *Graph
	idx    map[string]int
	active []bool
	cost   int64

	bestCost   int64
	bestCuts   []bool // per-edge: cut in the incumbent
	bestActive []bool // per-edge: active in the incumbent
}

// greedy repeatedly cuts the cheapest authorized edge of whatever cycle it
// finds, recording the result as the initial incumbent. It returns false if
// it hits a cycle with no authorized edge.
func (s *search) greedy() bool {
	cuts := make([]bool, len(s.g.Edges))
	var cost int64
	for {
		cycle := findCycle(s.g, s.idx, s.active)
		if cycle == nil {
			break
		}
		best := -1
		for _, ei := range cycle {
			e := s.g.Edges[ei]
			if !e.Cuttable {
				continue
			}
			if best < 0 || e.Cost < s.g.Edges[best].Cost {
				best = ei
			}
		}
		if best < 0 {
			return false
		}
		s.active[best] = false
		cuts[best] = true
		cost += s.g.Edges[best].Cost
	}
	s.record(cuts, cost)
	return true
}

func (s *search) record(cuts []bool, cost int64) {
	if s.bestCost >= 0 && cost >= s.bestCost {
		return
	}
	s.bestCost = cost
	s.bestCuts = append([]bool(nil), cuts...)
	s.bestActive = make([]bool, len(cuts))
	for i := range cuts {
		s.bestActive[i] = !cuts[i]
	}
}

// lowerBound sums, over a packing of edge-disjoint remaining cycles, the
// cheapest authorized edge of each cycle. Each such cycle forces a distinct
// cut, so the sum is a valid lower bound on the additional cost. It returns
// ok=false when an unbreakable cycle (no authorized edge) remains.
func (s *search) lowerBound() (bound int64, ok bool) {
	active := append([]bool(nil), s.active...)
	for {
		cycle := findCycle(s.g, s.idx, active)
		if cycle == nil {
			return bound, true
		}
		minCost := int64(-1)
		for _, ei := range cycle {
			active[ei] = false // edge-disjoint packing
			e := s.g.Edges[ei]
			if e.Cuttable && (minCost < 0 || e.Cost < minCost) {
				minCost = e.Cost
			}
		}
		if minCost < 0 {
			return 0, false
		}
		bound += minCost
	}
}

func (s *search) branch() {
	cycle := findCycle(s.g, s.idx, s.active)
	if cycle == nil {
		cuts := make([]bool, len(s.active))
		for i := range cuts {
			cuts[i] = !s.active[i]
		}
		s.record(cuts, s.cost)
		return
	}
	var options []int
	for _, ei := range cycle {
		if s.g.Edges[ei].Cuttable {
			options = append(options, ei)
		}
	}
	if len(options) == 0 {
		return // unbreakable cycle on this branch
	}
	if lb, ok := s.lowerBound(); !ok || (s.bestCost >= 0 && s.cost+lb >= s.bestCost) {
		return
	}
	// Try cheaper cuts first to find strong incumbents early.
	sort.Slice(options, func(a, b int) bool {
		return s.g.Edges[options[a]].Cost < s.g.Edges[options[b]].Cost
	})
	for _, ei := range options {
		s.active[ei] = false
		s.cost += s.g.Edges[ei].Cost
		s.branch()
		s.cost -= s.g.Edges[ei].Cost
		s.active[ei] = true
	}
}
