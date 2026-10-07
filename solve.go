package gsb

import (
	"math"
	"sort"
)

// engine precomputes dense per-option consumption vectors and unit costs.
type engine struct {
	q     []int64     // quantity per demand
	cons  [][][]int64 // cons[d][o][m]: units of material m per demand unit via option o
	ocost [][]int64   // ocost[d][o]: cost per demand unit via option o
	omin  []int64     // omin[d]: cheapest option cost of demand d (0 if no options)
	ord   [][]int     // ord[d]: option indices sorted by ascending unit cost
	stock []int64
}

func newEngine(p *Problem) *engine {
	m := len(p.Materials)
	e := &engine{stock: make([]int64, m)}
	for i, mat := range p.Materials {
		e.stock[i] = mat.Stock
	}
	for _, dem := range p.Demands {
		e.q = append(e.q, dem.Quantity)
		var dcons [][]int64
		var dcost []int64
		var ord []int
		omin := int64(math.MaxInt64)
		for o, opt := range dem.Options {
			v := make([]int64, m)
			v[opt.Material] += opt.Ratio
			for _, a := range opt.Aux {
				v[a.Material] += a.Amount
			}
			var c int64
			for mm, n := range v {
				c += n * p.Materials[mm].UnitCost
			}
			dcons = append(dcons, v)
			dcost = append(dcost, c)
			ord = append(ord, o)
			if c < omin {
				omin = c
			}
		}
		if len(dem.Options) == 0 {
			omin = 0
		}
		sort.SliceStable(ord, func(a, b int) bool {
			return dcost[ord[a]] < dcost[ord[b]]
		})
		e.cons = append(e.cons, dcons)
		e.ocost = append(e.ocost, dcost)
		e.omin = append(e.omin, omin)
		e.ord = append(e.ord, ord)
	}
	return e
}

// afford returns how many demand units an option can serve within rem.
func afford(cons, rem []int64) int64 {
	max := int64(math.MaxInt64)
	for m, c := range cons {
		if c > 0 {
			if r := rem[m] / c; r < max {
				max = r
			}
		}
	}
	return max
}

func apply(cons []int64, n int64, rem []int64, sign int64) {
	for m, c := range cons {
		if c > 0 {
			rem[m] += sign * n * c
		}
	}
}

// maxOrdersBound is a loose upper bound on the number of complete orders.
func (e *engine) maxOrdersBound() int64 {
	const cap = int64(1) << 60
	k := int64(math.MaxInt64)
	for d := range e.q {
		var units int64
		for _, v := range e.cons[d] {
			units += afford(v, e.stock)
			if units > cap {
				units = cap
			}
		}
		if b := units / e.q[d]; b < k {
			k = b
		}
	}
	return k
}

// feasible reports whether k complete orders fit in the inventory.
func (e *engine) feasible(k int64) bool {
	rem := append([]int64(nil), e.stock...)
	return e.feasAt(0, k, rem)
}

func (e *engine) feasAt(d int, k int64, rem []int64) bool {
	if d == len(e.q) {
		return true
	}
	need := k * e.q[d]
	nOpt := len(e.cons[d])
	if nOpt == 0 {
		return need == 0 && e.feasAt(d+1, k, rem)
	}
	var rec func(o int, left int64) bool
	rec = func(o int, left int64) bool {
		if o == nOpt-1 {
			if left > afford(e.cons[d][o], rem) {
				return false
			}
			apply(e.cons[d][o], left, rem, -1)
			ok := e.feasAt(d+1, k, rem)
			apply(e.cons[d][o], left, rem, +1)
			return ok
		}
		maxN := afford(e.cons[d][o], rem)
		if maxN > left {
			maxN = left
		}
		for n := int64(0); n <= maxN; n++ {
			apply(e.cons[d][o], n, rem, -1)
			ok := rec(o+1, left-n)
			apply(e.cons[d][o], n, rem, +1)
			if ok {
				return true
			}
		}
		return false
	}
	return rec(0, need)
}

// optimize finds a minimum-cost allocation for exactly k complete orders,
// which must be feasible. Options are tried cheapest-first so the
// branch-and-bound bound tightens quickly; ties keep the first solution
// found, which makes the output deterministic.
func (e *engine) optimize(k int64) [][]int64 {
	D := len(e.q)
	need := make([]int64, D)
	suffix := make([]int64, D+1)
	for d := D - 1; d >= 0; d-- {
		need[d] = k * e.q[d]
		suffix[d] = suffix[d+1] + need[d]*e.omin[d]
	}
	rem := append([]int64(nil), e.stock...)
	counts := make([][]int64, D)
	for d := range counts {
		counts[d] = make([]int64, len(e.cons[d]))
	}
	var best [][]int64
	bestCost := int64(math.MaxInt64)
	var rec func(d int, cur int64)
	rec = func(d int, cur int64) {
		if cur+suffix[d] >= bestCost {
			return
		}
		if d == D {
			bestCost = cur
			best = copyCounts(counts)
			return
		}
		order := e.ord[d]
		var recOpt func(oi int, left int64, c2 int64)
		recOpt = func(oi int, left int64, c2 int64) {
			o := order[oi]
			if oi == len(order)-1 {
				if left > afford(e.cons[d][o], rem) {
					return
				}
				apply(e.cons[d][o], left, rem, -1)
				counts[d][o] = left
				rec(d+1, c2+left*e.ocost[d][o])
				counts[d][o] = 0
				apply(e.cons[d][o], left, rem, +1)
				return
			}
			maxN := afford(e.cons[d][o], rem)
			if maxN > left {
				maxN = left
			}
			for n := maxN; n >= 0; n-- {
				apply(e.cons[d][o], n, rem, -1)
				counts[d][o] = n
				recOpt(oi+1, left-n, c2+n*e.ocost[d][o])
				counts[d][o] = 0
				apply(e.cons[d][o], n, rem, +1)
			}
		}
		recOpt(0, need[d], cur)
	}
	rec(0, 0)
	return best
}

func copyCounts(counts [][]int64) [][]int64 {
	out := make([][]int64, len(counts))
	for i, c := range counts {
		out[i] = append([]int64(nil), c...)
	}
	return out
}

// Solve maximizes the number of complete orders and, among all allocations
// achieving that maximum, minimizes total material cost.
func Solve(p *Problem) (*Solution, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	e := newEngine(p)
	// Feasibility is monotone in k: every option use serves exactly one
	// demand unit, so any allocation for k orders restricted to fewer
	// units per demand witnesses k-1 orders. Binary search is valid.
	lo, hi := int64(0), e.maxOrdersBound()
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if e.feasible(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return e.buildSolution(0, make([][]int64, len(e.q))), nil
	}
	return e.buildSolution(lo, e.optimize(lo)), nil
}

func (e *engine) buildSolution(k int64, counts [][]int64) *Solution {
	s := &Solution{CompleteOrders: k}
	used := make([]int64, len(e.stock))
	for d := range counts {
		for o, n := range counts[d] {
			if n == 0 {
				continue
			}
			s.Lines = append(s.Lines, Line{Demand: d, Option: o, Units: n})
			s.Cost += n * e.ocost[d][o]
			for m, c := range e.cons[d][o] {
				if c > 0 {
					used[m] += n * c
				}
			}
		}
	}
	s.Usage = make([]Usage, len(e.stock))
	for m := range used {
		s.Usage[m] = Usage{Material: m, Used: used[m], Stock: e.stock[m]}
	}
	return s
}
