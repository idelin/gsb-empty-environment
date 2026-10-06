package mcf

import (
	"encoding/json"
	"io/ioutil"
	"math"
	"os"
)

// StagedEdge extends Edge with an executed, already-shipped amount.
// Executed flow is sunk: it can never be reduced by re-optimization.
// A canceled edge keeps its executed amount but offers no remaining
// capacity, i.e. its flow is frozen at Executed.
type StagedEdge struct {
	Edge
	Executed int
	Canceled bool
}

// effLo/effCap are the bounds the re-optimization may use.
func (e StagedEdge) effLo() int { return e.Executed }
func (e StagedEdge) effCap() int {
	if e.Canceled || e.Cap < e.Executed {
		return e.Executed
	}
	return e.Cap
}

// StagedInstance is a re-optimization problem: supplies describe the
// new total required net outflow per node, and only the unexecuted
// part of each edge's flow may be adjusted, within remaining capacity.
type StagedInstance struct {
	N      int
	Edges  []StagedEdge
	Supply []int
	// Budget caps the number of enumerated flow vectors; <= 0 means
	// unlimited. On exhaustion the search frontier is saved and only
	// bounds are returned, never an optimality claim.
	Budget int64
	// FrontierPath, if non-empty, is where the resumable search
	// frontier is persisted (JSON).
	FrontierPath string
	// Resume continues from the frontier stored at FrontierPath.
	Resume bool
}

// CutEvidence certifies infeasibility: the node set X (sink side of a
// minimum cut) requires NetDemand more inflow than the CapacityIn that
// the remaining network can carry across the cut; Deficit > 0.
type CutEvidence struct {
	Nodes      []int
	NetDemand  int
	CapacityIn int
	Deficit    int
}

// StagedResult is the outcome of a staged re-optimization.
type StagedResult struct {
	Status Status
	// Flow is the optimal total per-edge flow (only when Optimal).
	Flow            []int
	TotalCost       int
	IncrementalCost int // TotalCost minus the sunk executed cost
	// Lower/Upper bracket the optimum when Status == BudgetExhausted.
	// Upper is meaningful only if HasIncumbent.
	Lower, Upper  int
	HasIncumbent  bool
	Cut           *CutEvidence
	Explored      int64
	FrontierSaved bool
}

// frontier is the persisted resumable search state.
type frontier struct {
	Position []int `json:"position"` // next vector to evaluate (free-edge coords)
	Best     []int `json:"best,omitempty"`
	BestCost int   `json:"bestCost"`
	HasBest  bool  `json:"hasBest"`
	Explored int64 `json:"explored"`
	Done     bool  `json:"done"`
}

func minTerm(cost, lo, hi int) int {
	if cost >= 0 {
		return cost * lo
	}
	return cost * hi
}

// SolveStaged re-optimizes around executed commitments. It enumerates
// the free-edge flow vectors in lexicographic order so the search
// frontier is a single vector that can be checkpointed and resumed.
func SolveStaged(inst StagedInstance) StagedResult {
	n := inst.N
	res := StagedResult{}
	sum := 0
	for _, s := range inst.Supply {
		sum += s
	}
	if sum != 0 || len(inst.Supply) != n {
		res.Status = Infeasible
		res.Cut = cutEvidence(inst)
		return res
	}

	// Split edges into fixed (frozen at executed) and free.
	var free []int
	fixedCost := 0
	balBase := make([]int, n)
	executedCost := 0
	for i, e := range inst.Edges {
		lo, hi := e.effLo(), e.effCap()
		executedCost += e.Cost * e.Executed
		if lo < hi {
			free = append(free, i)
		} else {
			fixedCost += e.Cost * lo
			balBase[e.From] += lo
			balBase[e.To] -= lo
		}
	}
	m := len(free)
	lo := make([]int, m)
	hi := make([]int, m)
	cost := make([]int, m)
	for j, i := range free {
		e := inst.Edges[i]
		lo[j], hi[j], cost[j] = e.effLo(), e.effCap(), e.Cost
	}

	// Suffix sums of relaxed per-edge minima for bound computation.
	suffixMin := make([]int, m+1)
	for j := m - 1; j >= 0; j-- {
		suffixMin[j] = suffixMin[j+1] + minTerm(cost[j], lo[j], hi[j])
	}

	fr := frontier{Position: append([]int{}, lo...)}
	if inst.Resume && inst.FrontierPath != "" {
		if data, err := ioutil.ReadFile(inst.FrontierPath); err == nil {
			var saved frontier
			if json.Unmarshal(data, &saved) == nil && len(saved.Position) == m && !saved.Done {
				fr = saved
			}
		}
	}

	cur := make([]int, m)
	copy(cur, fr.Position)
	best := fr.Best
	bestCost := fr.BestCost
	hasBest := fr.HasBest
	explored := fr.Explored
	budget := inst.Budget
	saveFrontier := func(done bool) {
		if inst.FrontierPath == "" {
			return
		}
		fr.Position = append([]int(nil), cur...)
		fr.Best = best
		fr.BestCost = bestCost
		fr.HasBest = hasBest
		fr.Explored = explored
		fr.Done = done
		if data, err := json.MarshalIndent(&fr, "", "  "); err == nil {
			tmp := inst.FrontierPath + ".tmp"
			if ioutil.WriteFile(tmp, data, 0644) == nil {
				os.Rename(tmp, inst.FrontierPath)
			}
		}
	}

	bal := make([]int, n)
	exhausted := false
	done := false
	for !done {
		if budget > 0 && explored >= budget {
			exhausted = true
			break
		}
		// Evaluate cur.
		copy(bal, balBase)
		total := fixedCost
		for j, i := range free {
			f := cur[j]
			e := inst.Edges[i]
			bal[e.From] += f
			bal[e.To] -= f
			total += cost[j] * f
		}
		ok := true
		for v := 0; v < n; v++ {
			if bal[v] != inst.Supply[v] {
				ok = false
				break
			}
		}
		if ok && (!hasBest || total < bestCost) {
			hasBest = true
			bestCost = total
			best = append([]int(nil), cur...)
		}
		explored++
		if explored%65536 == 0 {
			saveFrontier(false)
		}
		// Odometer increment (last coordinate fastest).
		k := m - 1
		for k >= 0 {
			if cur[k] < hi[k] {
				cur[k]++
				break
			}
			cur[k] = lo[k]
			k--
		}
		if k < 0 {
			done = true // fully enumerated
		}
	}

	if exhausted {
		// Lower bound over the unexplored lexicographic suffix:
		// parts k fix coords < k at cur, range coord k over
		// (cur_k, hi_k], and relax coords > k to their full range.
		var lb int64 = math.MaxInt64
		prefix := fixedCost
		for k := 0; k < m; k++ {
			if cur[k] < hi[k] {
				part := int64(prefix) + int64(minTerm(cost[k], cur[k]+1, hi[k])) + int64(suffixMin[k+1])
				if part < lb {
					lb = part
				}
			}
			prefix += cost[k] * cur[k]
		}
		lower := int(lb)
		if hasBest && bestCost < lower {
			lower = bestCost
		}
		res.Status = BudgetExhausted
		res.Lower = lower
		res.HasIncumbent = hasBest
		if hasBest {
			res.Upper = bestCost
		}
		res.Explored = explored
		saveFrontier(false)
		res.FrontierSaved = inst.FrontierPath != ""
		return res
	}

	if !hasBest {
		res.Status = Infeasible
		res.Cut = cutEvidence(inst)
		res.Explored = explored
		saveFrontier(true)
		res.FrontierSaved = inst.FrontierPath != ""
		return res
	}

	// Assemble the full per-edge flow vector.
	flow := make([]int, len(inst.Edges))
	for i, e := range inst.Edges {
		flow[i] = e.effLo()
	}
	for j, i := range free {
		flow[i] = best[j]
	}
	res.Status = Optimal
	res.Flow = flow
	res.TotalCost = bestCost
	res.IncrementalCost = bestCost - executedCost
	res.Explored = explored
	saveFrontier(true)
	res.FrontierSaved = inst.FrontierPath != ""
	return res
}

// cutEvidence builds the feasibility network for the staged instance
// and derives a min-cut certificate of infeasibility.
func cutEvidence(inst StagedInstance) *CutEvidence {
	g := Graph{N: inst.N}
	for _, e := range inst.Edges {
		g.Edges = append(g.Edges, Edge{From: e.From, To: e.To, Lo: e.effLo(), Cap: e.effCap()})
	}
	net, bal, S, T := build(g, inst.Supply)
	dinic(net, S, T)
	// Reachable set from S in the residual graph.
	seen := make([]bool, len(net.g))
	queue := []int{S}
	seen[S] = true
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, a := range net.g[v] {
			if a.cap > 0 && !seen[a.to] {
				seen[a.to] = true
				queue = append(queue, a.to)
			}
		}
	}
	ev := &CutEvidence{}
	inX := make([]bool, inst.N)
	for v := 0; v < inst.N; v++ {
		if !seen[v] {
			inX[v] = true
			ev.Nodes = append(ev.Nodes, v)
			ev.NetDemand -= bal[v]
		}
	}
	for _, e := range g.Edges {
		if !inX[e.From] && inX[e.To] {
			ev.CapacityIn += e.Cap - e.Lo
		}
	}
	ev.Deficit = ev.NetDemand - ev.CapacityIn
	return ev
}
