package main

import (
	"math"
	"math/rand"
	"time"
)

// Heuristic solver for instances too large for the exact DP: greedy facility
// addition followed by budget-limited swap local search. Always returns a
// feasible solution (an upper bound on the optimum cost).

type nearest struct {
	d  float64
	id int
}

func lessNN(a, b nearest) bool {
	if a.d != b.d {
		return a.d < b.d
	}
	return a.id < b.id
}

// nnWS is reusable scratch space for nearestAll.
type nnWS struct {
	down []nearest
	up   []nearest
	out  []nearest
	pre  []nearest
	suf  []nearest
}

func newNNWS(t *tree) *nnWS {
	maxDeg := 0
	for _, ch := range t.children {
		if len(ch) > maxDeg {
			maxDeg = len(ch)
		}
	}
	return &nnWS{
		down: make([]nearest, t.n),
		up:   make([]nearest, t.n),
		out:  make([]nearest, t.n),
		pre:  make([]nearest, maxDeg+1),
		suf:  make([]nearest, maxDeg+1),
	}
}

// nearestAll computes, for every node, the nearest facility as a
// (distance, facilityID) pair with ties broken by smaller id. O(n) on a tree.
// ws may be nil; pass a reusable workspace in hot loops.
func nearestAll(t *tree, isFac []bool, ws *nnWS) []nearest {
	if ws == nil {
		ws = newNNWS(t)
	}
	down, up, out := ws.down, ws.up, ws.out
	inf := nearest{math.Inf(1), -1}
	// post-order: down[v] = best in subtree(v)
	for _, v := range t.order {
		best := inf
		if isFac[v] {
			best = nearest{0, v}
		}
		for _, c := range t.children[v] {
			cand := nearest{down[c].d + t.pw[c], down[c].id}
			if lessNN(cand, best) {
				best = cand
			}
		}
		down[v] = best
	}
	// pre-order: up[v] = best outside subtree(v)
	up[0] = inf
	stack := []int{0}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ch := t.children[v]
		m := len(ch)
		pre := ws.pre[:m+1]
		suf := ws.suf[:m+1]
		// candidates from v's own facility and from above v
		base := up[v]
		if isFac[v] && lessNN(nearest{0, v}, base) {
			base = nearest{0, v}
		}
		pre[0] = base
		suf[m] = base
		for i, c := range ch {
			cand := nearest{down[c].d + t.pw[c], down[c].id}
			if lessNN(cand, pre[i]) {
				pre[i+1] = cand
			} else {
				pre[i+1] = pre[i]
			}
		}
		for i := m - 1; i >= 0; i-- {
			c := ch[i]
			cand := nearest{down[c].d + t.pw[c], down[c].id}
			if lessNN(cand, suf[i+1]) {
				suf[i] = cand
			} else {
				suf[i] = suf[i+1]
			}
		}
		for i, c := range ch {
			best := pre[i]
			if lessNN(suf[i+1], best) {
				best = suf[i+1]
			}
			up[c] = nearest{best.d + t.pw[c], best.id}
			stack = append(stack, c)
		}
	}
	for v := 0; v < t.n; v++ {
		out[v] = down[v]
		if lessNN(up[v], out[v]) {
			out[v] = up[v]
		}
	}
	return out
}

func totalCost(nn []nearest, demands []float64) float64 {
	total := 0.0
	for v, b := range nn {
		total += demands[v] * b.d
	}
	return total
}

// oneMedian finds the allowed node minimizing sum(demand*dist) in O(n).
func oneMedian(t *tree, demands []float64, allowed []bool) int {
	n := t.n
	down := make([]float64, n) // demand-weighted dist sum within subtree
	sub := make([]float64, n)  // total demand in subtree
	for _, v := range t.order {
		s := demands[v]
		d := 0.0
		for _, c := range t.children[v] {
			s += sub[c]
			d += down[c] + sub[c]*t.pw[c]
		}
		sub[v] = s
		down[v] = d
	}
	best := -1
	bestCost := math.Inf(1)
	// reroot: cost[v] = cost of placing the single facility at v
	type frame struct {
		v    int
		cost float64
	}
	stack := []frame{{0, down[0]}}
	totalDemand := sub[0]
	for len(stack) > 0 {
		fr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		v := fr.v
		if allowed[v] && fr.cost < bestCost {
			bestCost = fr.cost
			best = v
		}
		for _, c := range t.children[v] {
			// moving the facility from v to c: nodes in subtree(c) get closer
			// by w, all others farther by w
			cc := fr.cost + (totalDemand-2*sub[c])*t.pw[c]
			stack = append(stack, frame{c, cc})
		}
	}
	return best
}

// candidatePool picks nodes to evaluate as new facilities: a mix of the
// worst-served demand nodes (by demand*currentDistance) and random samples.
func candidatePool(rng *rand.Rand, allowedList []int, isFac []bool, nn []nearest, demands []float64, size int) []int {
	pool := make([]int, 0, size)
	seen := make(map[int]bool, size)
	// top worst-served allowed nodes
	idx := make([]int, 0, len(allowedList))
	for _, v := range allowedList {
		if !isFac[v] {
			idx = append(idx, v)
		}
	}
	score := func(v int) float64 { return demands[v] * nn[v].d }
	half := size / 2
	// keep the top-half scorers via one pass over a small sorted buffer
	top := make([]int, 0, half+1)
	for _, v := range idx {
		sv := score(v)
		// insertion position: descending by score, ascending by id on ties
		lo, hi := 0, len(top)
		for lo < hi {
			mid := (lo + hi) / 2
			sm := score(top[mid])
			if sm > sv || (sm == sv && top[mid] < v) {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo == half {
			continue
		}
		top = append(top, 0)
		copy(top[lo+1:], top[lo:])
		top[lo] = v
		if len(top) > half {
			top = top[:half]
		}
	}
	for _, v := range top {
		if !seen[v] {
			seen[v] = true
			pool = append(pool, v)
		}
	}
	// random fill
	for _, i := range rng.Perm(len(allowedList)) {
		if len(pool) >= size {
			break
		}
		v := allowedList[i]
		if !isFac[v] && !seen[v] {
			seen[v] = true
			pool = append(pool, v)
		}
	}
	return pool
}

// costIfAdded returns the total cost if facility x were added, given the
// current nearest distances cur. One O(n) BFS from x. dist and stamp are
// scratch; gen must be incremented before every call.
func costIfAdded(t *tree, demands, cur []float64, x int, dist []float64, stamp []int, gen int, stack []int) float64 {
	stamp[x] = gen
	dist[x] = 0
	stack = stack[:0]
	stack = append(stack, x)
	total := 0.0
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		d := dist[v]
		if d < cur[v] {
			total += demands[v] * d
		} else {
			total += demands[v] * cur[v]
		}
		for _, nb := range t.adj[v] {
			if stamp[nb.to] != gen {
				stamp[nb.to] = gen
				dist[nb.to] = d + nb.w
				stack = append(stack, nb.to)
			}
		}
	}
	return total
}

func solveHeuristic(in *Input, t *tree, demands []float64, allowed []bool, K int, budget time.Duration) Output {
	n := in.N
	start := time.Now()
	deadline := func(frac float64) bool {
		return time.Since(start) > time.Duration(float64(budget)*frac)
	}
	rng := rand.New(rand.NewSource(42))
	ws := newNNWS(t)

	allowedList := []int{}
	for v := 0; v < n; v++ {
		if allowed[v] {
			allowedList = append(allowedList, v)
		}
	}

	isFac := make([]bool, n)
	facilities := []int{}
	add := func(v int) {
		isFac[v] = true
		facilities = append(facilities, v)
	}

	// seed with the 1-median
	add(oneMedian(t, demands, allowed))

	// pool size scaled so one greedy round costs ~4M node evaluations
	poolSize := 4000000 / n
	if poolSize < 64 {
		poolSize = 64
	}
	if poolSize > len(allowedList) {
		poolSize = len(allowedList)
	}

	// greedy additions
	cur := make([]float64, n)
	distBuf := make([]float64, n)
	stamp := make([]int, n)
	gen := 0
	bfsStack := make([]int, 0, 1024)
	for len(facilities) < K && !deadline(0.6) {
		nn := nearestAll(t, isFac, ws)
		for v := 0; v < n; v++ {
			cur[v] = nn[v].d
		}
		pool := candidatePool(rng, allowedList, isFac, nn, demands, poolSize)
		bestV := -1
		bestCost := math.Inf(1)
		for _, cand := range pool {
			gen++
			c := costIfAdded(t, demands, cur, cand, distBuf, stamp, gen, bfsStack)
			if c < bestCost {
				bestCost = c
				bestV = cand
			}
			if deadline(0.6) {
				break
			}
		}
		if bestV < 0 {
			break
		}
		add(bestV)
	}

	bestFac := append([]int(nil), facilities...)
	bestIsFac := append([]bool(nil), isFac...)
	bestCost := totalCost(nearestAll(t, isFac, ws), demands)

	// swap local search until the budget runs out
	swapPool := 2000000 / n
	if swapPool < 32 {
		swapPool = 32
	}
	for !deadline(1.0) {
		improved := false
		facs := append([]int(nil), facilities...)
		rng.Shuffle(len(facs), func(i, j int) { facs[i], facs[j] = facs[j], facs[i] })
		for _, f := range facs {
			if deadline(1.0) {
				break
			}
			nn := nearestAll(t, isFac, ws)
			pool := candidatePool(rng, allowedList, isFac, nn, demands, swapPool)
			swapped := false
			for _, cand := range pool {
				isFac[f] = false
				isFac[cand] = true
				c := totalCost(nearestAll(t, isFac, ws), demands)
				if c < bestCost-1e-12 {
					bestCost = c
					// apply swap
					for i, x := range facilities {
						if x == f {
							facilities[i] = cand
							break
						}
					}
					bestFac = append([]int(nil), facilities...)
					bestIsFac = append([]bool(nil), isFac...)
					improved = true
					swapped = true
				} else {
					isFac[f] = true
					isFac[cand] = false
				}
				if swapped || deadline(1.0) {
					break
				}
			}
			if improved {
				break
			}
		}
		if !improved {
			break
		}
	}

	nn := nearestAll(t, bestIsFac, ws)
	assign := make([]int, n)
	for v := 0; v < n; v++ {
		assign[v] = nn[v].id
	}
	return Output{
		Status:      "feasible",
		Facilities:  bestFac,
		Assignments: assign,
		TotalCost:   costPtr(totalCost(nn, demands)),
		Optimal:     false,
	}
}
