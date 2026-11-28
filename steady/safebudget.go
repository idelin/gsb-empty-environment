package steady

import "math"

// SafeBudget is the maximum retry BudgetRatio on an edge that still admits a
// fully converged steady state under hard capacity.
type SafeBudget struct {
	From        string
	To          string
	MaxRatio    float64
	ConvergesAt bool
}

// MaxSafeBudgets computes, for every edge, the largest budget ratio that
// keeps the whole DAG converged with no node breaking hard capacity.
//
// The search applies the probed ratio uniformly to every edge (a service
// mesh typically rolls out one budget policy), then additionally reports
// each edge's individual limit, i.e. the largest ratio that edge may carry
// while every other edge is pinned to the safe global ratio. Edges that are
// safe even with an unlimited budget report +Inf.
func MaxSafeBudgets(t *Topology) ([]SafeBudget, error) {
	return MaxSafeBudgetsWith(t, DefaultSolverConfig(), 10.0, 40)
}

// MaxSafeBudgetsWith exposes iteration settings, the binary-search upper
// bound and the number of search steps.
func MaxSafeBudgetsWith(t *Topology, cfg SolverConfig, upperBound float64, steps int) ([]SafeBudget, error) {
	if upperBound <= 0 {
		upperBound = 10
	}
	if steps <= 0 {
		steps = 40
	}

	global, err := searchGlobal(t, cfg, upperBound, steps)
	if err != nil {
		return nil, err
	}

	out := make([]SafeBudget, 0, len(t.Edges))
	for i := range t.Edges {
		edge := t.Edges[i]
		// Individual limit: other edges pinned to the safe global ratio
		// (or unlimited when no global cap is needed), find this edge's max.
		probe := cloneTopology(t)
		for j := range probe.Edges {
			if j != i {
				probe.Edges[j].BudgetRatio = global
			}
		}
		if math.IsInf(global, 1) {
			for j := range probe.Edges {
				if j != i {
					probe.Edges[j].BudgetRatio = math.Inf(1)
				}
			}
		}
		indiv, err := searchOne(probe, i, cfg, upperBound, steps)
		if err != nil {
			return nil, err
		}
		out = append(out, SafeBudget{
			From: edge.From, To: edge.To,
			MaxRatio: indiv, ConvergesAt: true,
		})
	}
	return out, nil
}

// GlobalSafeRatio returns the uniform budget ratio that restores a fully
// converged, within-capacity steady state. +Inf means no cap is required.
func GlobalSafeRatio(t *Topology) (float64, error) {
	return searchGlobal(t, DefaultSolverConfig(), 10.0, 40)
}

func searchGlobal(t *Topology, cfg SolverConfig, upperBound float64, steps int) (float64, error) {
	unlim := cloneTopology(t)
	for i := range unlim.Edges {
		unlim.Edges[i].BudgetRatio = math.Inf(1)
	}
	res, err := SolveWith(unlim, cfg)
	if err != nil {
		return 0, err
	}
	if safe(res) {
		return math.Inf(1), nil
	}

	lo, hi := 0.0, upperBound
	// Find an unsafe hi by growing from a safe zero ratio.
	for {
		probe := cloneTopology(t)
		for i := range probe.Edges {
			probe.Edges[i].BudgetRatio = hi
		}
		res, err := SolveWith(probe, cfg)
		if err != nil {
			return 0, err
		}
		if !safe(res) || hi >= 1e6 {
			break
		}
		hi *= 2
		lo = hi / 2
	}
	for s := 0; s < steps; s++ {
		mid := (lo + hi) / 2
		probe := cloneTopology(t)
		for i := range probe.Edges {
			probe.Edges[i].BudgetRatio = mid
		}
		res, err := SolveWith(probe, cfg)
		if err != nil {
			return 0, err
		}
		if safe(res) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo, nil
}

func searchOne(t *Topology, edgeIdx int, cfg SolverConfig, upperBound float64, steps int) (float64, error) {
	probe := cloneTopology(t)
	probe.Edges[edgeIdx].BudgetRatio = math.Inf(1)
	res, err := SolveWith(probe, cfg)
	if err != nil {
		return 0, err
	}
	if safe(res) {
		return math.Inf(1), nil
	}
	lo, hi := 0.0, upperBound
	// Extend the upper bound until a safe point exists.
	for {
		p := cloneTopology(t)
		p.Edges[edgeIdx].BudgetRatio = hi
		r, err := SolveWith(p, cfg)
		if err != nil {
			return 0, err
		}
		if !safe(r) || hi >= 1e6 {
			break
		}
		hi *= 2
		lo = hi / 2
	}
	for s := 0; s < steps; s++ {
		mid := (lo + hi) / 2
		p := cloneTopology(t)
		p.Edges[edgeIdx].BudgetRatio = mid
		r, err := SolveWith(p, cfg)
		if err != nil {
			return 0, err
		}
		if safe(r) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo, nil
}

func safe(res *Result) bool {
	if !res.Converged || len(res.AvalancheNodes) > 0 {
		return false
	}
	for _, nr := range res.Nodes {
		if nr.OverCapacity {
			return false
		}
	}
	return true
}

func cloneTopology(t *Topology) *Topology {
	cp := &Topology{
		Nodes:   append([]NodeConfig(nil), t.Nodes...),
		Edges:   append([]EdgeConfig(nil), t.Edges...),
		Ingress: map[string]float64{},
	}
	for k, v := range t.Ingress {
		cp.Ingress[k] = v
	}
	return cp
}
