package main

import (
	"math/big"
)

// compareIDs lexicographically compares two sorted ID lists; a strict
// prefix is considered smaller.
func compareIDs(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// solLess implements the plan ordering: primary total cost (big integer),
// tie-broken by the lexicographic order of the sorted file ID list.
func solLess(costA *big.Int, idsA []string, costB *big.Int, idsB []string) bool {
	if c := costA.Cmp(costB); c != 0 {
		return c < 0
	}
	return compareIDs(idsA, idsB) < 0
}

// solver is a deterministic depth-first branch and bound over files sorted
// by ID. The budget counts expanded nodes (nodes whose two children are
// generated); the traversal order is fixed so identical input and budget
// reproduce identical output.
type solver struct {
	e           *env
	budget      int64 // <0 means unlimited
	expanded    int64
	bestCost    *big.Int
	bestIDs     []string
	lbStack     []*big.Rat // lower bounds of ancestor frames
	frontierMin *big.Rat   // min lower bound over the unexplored frontier
	exhausted   bool
}

// solve runs the full pipeline: validate, diagnose, search.
func solve(in *Input, budget int64) *SolveOutput {
	e, errs := buildEnv(in)
	if len(errs) > 0 {
		return &SolveOutput{Status: StatusError, Errors: errs, Budget: budget}
	}
	out := &SolveOutput{Diagnostics: e.diags, Budget: budget}
	s := &solver{e: e, budget: budget}
	s.search(newBitmap(e.n), newBitmap(e.n), new(big.Int), 0)
	out.NodesExpanded = s.expanded
	if s.bestCost != nil {
		out.Solution = &SolutionJSON{Files: s.bestIDs, TotalBytes: s.bestCost.String()}
	}
	switch {
	case s.exhausted:
		// Never claim optimality: report the best known feasible plan and
		// a valid lower bound (ceiled to an integer, which keeps it valid
		// because every plan cost is an integer).
		out.Status = StatusBudgetExhausted
		lb := s.frontierMin
		if s.bestCost != nil && (lb == nil || new(big.Rat).SetInt(s.bestCost).Cmp(lb) < 0) {
			lb = new(big.Rat).SetInt(s.bestCost)
		}
		if lb != nil {
			out.LowerBound = ceilRat(lb).String()
		}
	case s.bestCost != nil:
		out.Status = StatusOptimal
		out.LowerBound = s.bestCost.String()
	default:
		out.Status = StatusInfeasible
	}
	return out
}

// ceilRat returns the smallest integer >= r (r is always non-negative here).
func ceilRat(r *big.Rat) *big.Int {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func (s *solver) search(inc, exc bitmap, cost *big.Int, idx int) {
	if s.exhausted {
		return
	}
	e := s.e
	n := e.n
	// Skip files already decided (included via a dependency closure or
	// explicitly excluded); pass-throughs do not count as expansions.
	for idx < n && (inc.get(idx) || exc.get(idx)) {
		idx++
	}

	// Infeasibility prune: some shard unreachable even with every file
	// that could still legally be added.
	poss := e.viable(exc)
	for _, sh := range e.shards {
		if !shardReachable(sh, poss) {
			return
		}
	}

	satisfied := true
	for _, sh := range e.shards {
		if !shardReachable(sh, inc) {
			satisfied = false
			break
		}
	}
	if satisfied {
		s.record(inc, cost)
		// Adding files never lowers the cost, so only zero-cost additions
		// can still tie and win the ID-list tie-break.
		if !s.hasZeroUndecided(inc, exc) {
			return
		}
	}

	// Lower bound: current cost plus the maximum, over unsatisfied shards,
	// of a per-shard lower bound on the extra cost of satisfying that
	// shard alone. Valid for the joint problem because any completion
	// must satisfy every shard.
	lb := new(big.Rat).SetInt(cost)
	if !satisfied {
		add := s.additionalLB(inc, poss)
		if add == nil {
			return
		}
		lb.Add(lb, add)
	}
	if s.bestCost != nil && lb.Cmp(new(big.Rat).SetInt(s.bestCost)) > 0 {
		return
	}
	if idx >= n {
		return
	}

	if s.budget >= 0 && s.expanded >= s.budget {
		// Budget exhausted: the optimum of every unexplored subtree is
		// bounded below by some frame on the current stack.
		m := new(big.Rat).Set(lb)
		for _, l := range s.lbStack {
			if l.Cmp(m) < 0 {
				m.Set(l)
			}
		}
		if s.frontierMin == nil || m.Cmp(s.frontierMin) < 0 {
			s.frontierMin = m
		}
		s.exhausted = true
		return
	}
	s.expanded++
	s.lbStack = append(s.lbStack, lb)

	// Include branch first (fixed order), then exclude.
	if e.usable[idx] {
		newInc := inc.clone()
		newInc.orIn(e.closure[idx])
		if !newInc.intersects(exc) {
			cost2 := new(big.Int).Add(cost, e.addedCost(idx, inc))
			s.search(newInc, exc, cost2, idx+1)
		}
	}
	if !s.exhausted {
		exc2 := exc.clone()
		exc2.set(idx)
		s.search(inc, exc2, cost, idx+1)
	}
	s.lbStack = s.lbStack[:len(s.lbStack)-1]
}

func (s *solver) record(inc bitmap, cost *big.Int) {
	ids := s.e.idsOf(inc)
	if s.bestCost == nil || solLess(cost, ids, s.bestCost, s.bestIDs) {
		s.bestCost = new(big.Int).Set(cost)
		s.bestIDs = ids
	}
}

func (s *solver) hasZeroUndecided(inc, exc bitmap) bool {
	e := s.e
	for i := 0; i < e.n; i++ {
		if e.usable[i] && !inc.get(i) && !exc.get(i) && e.bytes[i].Sign() == 0 {
			return true
		}
	}
	return false
}

// additionalLB returns the maximum over shards not yet satisfied by inc of
// a lower bound on the extra cost of satisfying that shard alone, or nil if
// some shard cannot be satisfied at all from the viable files.
func (s *solver) additionalLB(inc, poss bitmap) *big.Rat {
	max := new(big.Rat)
	for _, sh := range s.e.shards {
		if shardReachable(sh, inc) {
			continue
		}
		d := s.shardLowerBound(sh, inc, poss)
		if d == nil {
			return nil
		}
		if d.Cmp(max) > 0 {
			max.Set(d)
		}
	}
	return max
}

// shardLowerBound is a shortest-path DP over the shard's version DAG
// (versions strictly increase along edges, so ascending order is a valid
// topological order). The weight of an edge served by file f is
//
//	sum over g in closure(f)\inc of bytes(g) / share_g
//
// where share_g counts the shard edges whose serving file's closure
// contains g. Along any path each g is then charged at most bytes(g) in
// total, so the path weight never exceeds the true extra cost of the path's
// file set, and the minimum path weight lower-bounds the extra cost of any
// completion. Shared files and shared dependencies are never double
// counted, which keeps the bound valid.
func (s *solver) shardLowerBound(sh shard, inc, poss bitmap) *big.Rat {
	dist := map[int64]*big.Rat{0: new(big.Rat)}
	for _, v := range sh.versions {
		d := dist[v]
		if d == nil {
			continue
		}
		for _, ed := range sh.adj[v] {
			if !poss.get(ed.file) {
				continue
			}
			cand := new(big.Rat).Add(d, s.edgeLowerWeight(sh, ed.file, inc))
			if cur := dist[ed.to]; cur == nil || cand.Cmp(cur) < 0 {
				dist[ed.to] = cand
			}
		}
	}
	return dist[sh.target]
}

func (s *solver) edgeLowerWeight(sh shard, f int, inc bitmap) *big.Rat {
	w := new(big.Rat)
	s.e.closure[f].forEachSet(s.e.n, func(g int) {
		if inc.get(g) {
			return
		}
		m := int64(sh.shareCount[g])
		if m < 1 {
			m = 1
		}
		w.Add(w, new(big.Rat).SetFrac(s.e.bytes[g], big.NewInt(m)))
	})
	return w
}
