package matchsim

import "time"

// Inf marks an infeasible fault->alert pairing in a cost matrix.
const Inf = int64(1) << 60

// feasible applies the matching rules: entity compatibility, symptom-class
// compatibility and the [start-MaxLead, start+MaxLag] time window. It
// returns the signed detection latency (negative means the alert led the
// fault).
func feasible(cfg Config, f Fault, a Alert) (time.Duration, bool) {
	if cfg.entityGroup(f.Entity) != cfg.entityGroup(a.Entity) {
		return 0, false
	}
	if cfg.symptomClass(f.Symptom) != cfg.symptomClass(a.Symptom) {
		return 0, false
	}
	lat := a.Time.Sub(f.Start)
	if lat < 0 {
		if cfg.EarlyAlertAsFP || -lat > cfg.MaxLead.Duration {
			return 0, false
		}
	} else if lat > cfg.MaxLag.Duration {
		return 0, false
	}
	return lat, true
}

// CostMatrix builds the bipartite cost matrix: rows are faults, columns are
// alerts, entries are detection latencies in nanoseconds or Inf.
func CostMatrix(cfg Config, faults []Fault, alerts []Alert) [][]int64 {
	cost := make([][]int64, len(faults))
	for i, f := range faults {
		cost[i] = make([]int64, len(alerts))
		for j, a := range alerts {
			if lat, ok := feasible(cfg, f, a); ok {
				cost[i][j] = int64(lat)
			} else {
				cost[i][j] = Inf
			}
		}
	}
	return cost
}

// MinCostMatching computes a one-to-one fault<->alert matching that first
// maximizes the number of matched pairs and then minimizes total latency.
// It uses successive shortest augmenting paths (Bellman-Ford, since edge
// costs may be negative), which is globally optimal, unlike per-alert
// greedy assignment. Returns [fault, alert] index pairs.
func MinCostMatching(cost [][]int64) [][2]int {
	nf := len(cost)
	if nf == 0 || len(cost[0]) == 0 {
		return nil
	}
	na := len(cost[0])

	const src = 0
	faultNode := func(i int) int { return 1 + i }
	alertNode := func(j int) int { return 1 + nf + j }
	sink := 1 + nf + na
	n := sink + 1

	type edge struct {
		to, rev, cap, ocap int
		cost               int64
	}
	g := make([][]edge, n)
	add := func(u, v, cap int, c int64) {
		g[u] = append(g[u], edge{to: v, rev: len(g[v]), cap: cap, ocap: cap, cost: c})
		g[v] = append(g[v], edge{to: u, rev: len(g[u]) - 1, cost: -c})
	}
	for i := 0; i < nf; i++ {
		add(src, faultNode(i), 1, 0)
	}
	for j := 0; j < na; j++ {
		add(alertNode(j), sink, 1, 0)
	}
	for i := 0; i < nf; i++ {
		for j := 0; j < na; j++ {
			if cost[i][j] < Inf {
				add(faultNode(i), alertNode(j), 1, cost[i][j])
			}
		}
	}

	// Successive shortest augmenting paths. Augmenting along a shortest
	// s-t path keeps the flow min-cost for its value; we stop when the
	// matching is saturated (no augmenting path remains).
	for {
		dist := make([]int64, n)
		for i := range dist {
			dist[i] = Inf
		}
		dist[src] = 0
		prevV := make([]int, n)
		prevE := make([]int, n)
		for iter := 0; iter < n; iter++ {
			updated := false
			for v := 0; v < n; v++ {
				if dist[v] == Inf {
					continue
				}
				for ei, e := range g[v] {
					if e.cap > 0 && dist[v]+e.cost < dist[e.to] {
						dist[e.to] = dist[v] + e.cost
						prevV[e.to] = v
						prevE[e.to] = ei
						updated = true
					}
				}
			}
			if !updated {
				break
			}
		}
		if dist[sink] == Inf {
			break
		}
		for v := sink; v != src; v = prevV[v] {
			u, ei := prevV[v], prevE[v]
			g[u][ei].cap--
			g[v][g[u][ei].rev].cap++
		}
	}

	var pairs [][2]int
	for i := 0; i < nf; i++ {
		for _, e := range g[faultNode(i)] {
			if e.ocap == 1 && e.cap == 0 && e.to >= alertNode(0) && e.to < alertNode(na) {
				pairs = append(pairs, [2]int{i, e.to - alertNode(0)})
			}
		}
	}
	return pairs
}

// MatchingCost sums the cost-matrix entries of a matching.
func MatchingCost(cost [][]int64, pairs [][2]int) int64 {
	var total int64
	for _, p := range pairs {
		total += cost[p[0]][p[1]]
	}
	return total
}

// EnumerateBest brute-forces every one-to-one matching (faults may stay
// unmatched) and returns the maximum match count and, among matchings of
// that size, the minimum total cost. Exponential: use only on small
// instances to verify MinCostMatching.
func EnumerateBest(cost [][]int64) (bestMatches int, bestCost int64) {
	nf := len(cost)
	na := 0
	if nf > 0 {
		na = len(cost[0])
	}
	bestCost = Inf
	used := make([]bool, na)
	var rec func(i, matches int, c int64)
	rec = func(i, matches int, c int64) {
		if i == nf {
			if matches > bestMatches || (matches == bestMatches && c < bestCost) {
				bestMatches, bestCost = matches, c
			}
			return
		}
		rec(i+1, matches, c) // leave fault i unmatched
		for j := 0; j < na; j++ {
			if !used[j] && cost[i][j] < Inf {
				used[j] = true
				rec(i+1, matches+1, c+cost[i][j])
				used[j] = false
			}
		}
	}
	rec(0, 0, 0)
	if bestCost == Inf {
		bestCost = 0
	}
	return bestMatches, bestCost
}
