package main

import "fmt"

// Solve finds a minimum-makespan non-preemptive schedule. Among all
// optimal schedules it returns the lexicographically smallest start
// vector (ordered by job index). Zero-duration jobs occupy no time and
// no resources but their precedence constraints are enforced exactly
// like any other job's.
func Solve(inst *Instance) Result {
	if err := validate(inst); err != nil {
		return Result{Status: "infeasible", Reason: "invalid instance: " + err.Error()}
	}
	n := len(inst.Jobs)
	k := len(inst.Resources)

	preds := make([][]int, n)
	succs := make([][]int, n)
	for i := range inst.Jobs {
		for _, p := range inst.Jobs[i].Predecessors {
			preds[i] = append(preds[i], p)
			succs[p] = append(succs[p], i)
		}
	}

	// Static infeasibility: demand above capacity.
	for i := range inst.Jobs {
		if inst.Jobs[i].Duration == 0 {
			continue // zero-duration jobs never occupy resources
		}
		for r := 0; r < k; r++ {
			if inst.Jobs[i].Demands[r] > inst.Resources[r].Capacity {
				return Result{Status: "infeasible",
					Reason: fmt.Sprintf("job %d demands %d of resource %d but capacity is %d",
						i, inst.Jobs[i].Demands[r], r, inst.Resources[r].Capacity)}
			}
		}
	}

	// Earliest starts via longest paths over the precedence DAG; also
	// detects cycles. Works for zero-duration jobs (edge weight 0).
	es, ok := earliestStarts(inst, succs)
	if !ok {
		return Result{Status: "infeasible", Reason: "precedence graph contains a cycle"}
	}

	// Lower bound on the makespan: precedence chains and resource load.
	lb := 0
	for i := range inst.Jobs {
		if e := es[i] + inst.Jobs[i].Duration; e > lb {
			lb = e
		}
	}
	for r := 0; r < k; r++ {
		work := 0
		for i := range inst.Jobs {
			work += inst.Jobs[i].Duration * inst.Jobs[i].Demands[r]
		}
		if c := inst.Resources[r].Capacity; c > 0 && (work+c-1)/c > lb {
			lb = (work + c - 1) / c
		}
	}

	s := &search{
		inst:   inst,
		n:      n,
		k:      k,
		preds:  preds,
		succs:  succs,
		es:     es,
		usage:  make([][]int, k),
		starts: make([]int, n),
		done:   make([]bool, n),
	}
	for r := 0; r < k; r++ {
		s.usage[r] = make([]int, inst.Horizon+1)
	}

	// Increasing makespan bound: the first feasible bound is optimal,
	// and the DFS below yields the lexicographically smallest start
	// vector for that bound.
	for T := lb; T <= inst.Horizon; T++ {
		ls, ok := latestStarts(inst, preds, T)
		if !ok {
			continue
		}
		s.ls = ls
		for i := 0; i < n; i++ {
			s.done[i] = false
		}
		if sol, found := s.assign(0); found {
			return Result{
				Status:   "optimal",
				Makespan: T,
				Starts:   sol,
				Timeline: buildTimeline(inst, sol, T),
			}
		}
	}
	return Result{Status: "infeasible",
		Reason: fmt.Sprintf("no schedule with makespan <= horizon %d", inst.Horizon)}
}

type search struct {
	inst   *Instance
	n, k   int
	preds  [][]int
	succs  [][]int
	es     []int
	ls     []int
	usage  [][]int // usage[resource][time]
	starts []int
	done   []bool
}

// assign fixes start times for jobs idx..n-1 (in index order). It tries
// candidate starts in ascending order, so the first complete assignment
// found is the lexicographically smallest one for the current bound.
func (s *search) assign(idx int) ([]int, bool) {
	if idx == s.n {
		out := make([]int, s.n)
		copy(out, s.starts)
		return out, true
	}
	lo, hi := s.window(idx)
	for st := lo; st <= hi; st++ {
		if !s.fits(idx, st) {
			continue
		}
		s.place(idx, st, +1)
		s.starts[idx] = st
		s.done[idx] = true
		if s.forwardCheck(idx + 1) {
			if sol, ok := s.assign(idx + 1); ok {
				return sol, true
			}
		}
		s.done[idx] = false
		s.place(idx, st, -1)
	}
	return nil, false
}

// window computes the currently admissible start interval for job idx,
// tightened by already assigned neighbours.
func (s *search) window(idx int) (int, int) {
	lo, hi := s.es[idx], s.ls[idx]
	d := s.inst.Jobs[idx].Duration
	for _, p := range s.preds[idx] {
		if s.done[p] && s.starts[p]+s.inst.Jobs[p].Duration > lo {
			lo = s.starts[p] + s.inst.Jobs[p].Duration
		}
	}
	for _, q := range s.succs[idx] {
		if s.done[q] && s.starts[q]-d < hi {
			hi = s.starts[q] - d
		}
	}
	return lo, hi
}

// fits reports whether job idx can occupy [st, st+duration) given the
// current resource usage.
func (s *search) fits(idx, st int) bool {
	job := &s.inst.Jobs[idx]
	for t := st; t < st+job.Duration; t++ {
		for r := 0; r < s.k; r++ {
			if s.usage[r][t]+job.Demands[r] > s.inst.Resources[r].Capacity {
				return false
			}
		}
	}
	return true
}

func (s *search) place(idx, st, delta int) {
	job := &s.inst.Jobs[idx]
	for t := st; t < st+job.Duration; t++ {
		for r := 0; r < s.k; r++ {
			s.usage[r][t] += delta * job.Demands[r]
		}
	}
}

// forwardCheck verifies that every unassigned job still has at least
// one feasible start slot; it prunes hopeless branches early.
func (s *search) forwardCheck(from int) bool {
	for j := from; j < s.n; j++ {
		if s.done[j] {
			continue
		}
		lo, hi := s.window(j)
		if lo > hi {
			return false
		}
		if s.inst.Jobs[j].Duration == 0 {
			continue // consumes nothing; any slot in [lo,hi] works
		}
		feasible := false
		for st := lo; st <= hi && !feasible; st++ {
			if s.fits(j, st) {
				feasible = true
			}
		}
		if !feasible {
			return false
		}
	}
	return true
}

// earliestStarts computes longest-path earliest starts; ok=false on cycle.
func earliestStarts(inst *Instance, succs [][]int) ([]int, bool) {
	n := len(inst.Jobs)
	indeg := make([]int, n)
	for i := range inst.Jobs {
		indeg[i] = len(inst.Jobs[i].Predecessors)
	}
	es := make([]int, n)
	queue := []int{}
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	seen := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		seen++
		fin := es[u] + inst.Jobs[u].Duration
		for _, v := range succs[u] {
			if fin > es[v] {
				es[v] = fin
			}
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	return es, seen == n
}

// latestStarts computes latest starts such that every job finishes by T.
func latestStarts(inst *Instance, preds [][]int, T int) ([]int, bool) {
	n := len(inst.Jobs)
	ls := make([]int, n)
	outdeg := make([]int, n)
	succOf := make([][]int, n)
	for i := range inst.Jobs {
		for _, p := range inst.Jobs[i].Predecessors {
			succOf[p] = append(succOf[p], i)
			outdeg[i]++
		}
	}
	for i := 0; i < n; i++ {
		ls[i] = T - inst.Jobs[i].Duration
	}
	queue := []int{}
	for i := 0; i < n; i++ {
		if outdeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, p := range preds[u] {
			if cand := ls[u] - inst.Jobs[p].Duration; cand < ls[p] {
				ls[p] = cand
			}
			outdeg[p]--
			if outdeg[p] == 0 {
				queue = append(queue, p)
			}
		}
	}
	for i := 0; i < n; i++ {
		if ls[i] < 0 {
			return nil, false
		}
	}
	return ls, true
}
