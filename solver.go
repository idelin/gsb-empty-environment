package main

import (
	"sort"
	"time"
)

// Interval is a closed integer interval [L, R].
type Interval struct {
	L int64
	R int64
}

// solver is an exact branch-and-bound optimizer. Objective order:
// 1) minimize total cost, 2) minimize number of points, 3) lexicographically
// smallest sorted list of chosen coordinates.
type solver struct {
	ivs   []Interval
	xs    []int64 // sorted unique candidate coordinates
	cost  []int64 // cost[i] is the cost of xs[i]
	cmin  int64   // minimum candidate cost
	minIn []int64 // minIn[i] is the cheapest candidate cost inside ivs[i]

	bestPts  []int64
	bestCost int64
	hasBest  bool

	nodes       int64
	maxNodes    int64
	deadline    time.Time
	interrupted bool
	lbStack     []int64 // lower bounds of the frames on the current DFS path
	liveLB      int64   // min lower bound over the DFS path at interruption time
}

func newSolver(ivs []Interval, xs, cost []int64, maxNodes, timeoutMs int64) *solver {
	s := &solver{ivs: ivs, xs: xs, cost: cost, maxNodes: maxNodes}
	if len(cost) > 0 {
		s.cmin = cost[0]
		for _, c := range cost {
			if c < s.cmin {
				s.cmin = c
			}
		}
	}
	s.minIn = make([]int64, len(ivs))
	for i, iv := range ivs {
		lo := sort.Search(len(xs), func(j int) bool { return xs[j] >= iv.L })
		hi := sort.Search(len(xs), func(j int) bool { return xs[j] > iv.R })
		m := cost[lo] // feasibility is checked before the solver runs
		for j := lo; j < hi; j++ {
			if cost[j] < m {
				m = cost[j]
			}
		}
		s.minIn[i] = m
	}
	if timeoutMs > 0 {
		s.deadline = time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	}
	return s
}

// solve computes a greedy incumbent and then runs branch and bound.
func (s *solver) solve() {
	pts, c := s.greedy()
	s.bestPts, s.bestCost, s.hasBest = pts, c, true
	s.search(nil, 0)
}

// greedy builds a feasible solution: repeatedly stab the uncovered interval
// with the smallest right endpoint using its cheapest candidate, then drop
// redundant points (never harmful, costs are non-negative).
func (s *solver) greedy() ([]int64, int64) {
	m := len(s.ivs)
	covered := make([]bool, m)
	rem := m
	chosen := make(map[int]bool)
	for rem > 0 {
		t := -1
		for i, iv := range s.ivs {
			if covered[i] {
				continue
			}
			if t < 0 || iv.R < s.ivs[t].R || (iv.R == s.ivs[t].R && iv.L < s.ivs[t].L) {
				t = i
			}
		}
		iv := s.ivs[t]
		bi := -1
		for j, x := range s.xs {
			if x < iv.L || x > iv.R {
				continue
			}
			if bi < 0 || s.cost[j] < s.cost[bi] || (s.cost[j] == s.cost[bi] && x < s.xs[bi]) {
				bi = j
			}
		}
		chosen[bi] = true
		px := s.xs[bi]
		for i, w := range s.ivs {
			if !covered[i] && w.L <= px && px <= w.R {
				covered[i] = true
				rem--
			}
		}
	}
	// Redundancy removal, expensive points first.
	list := make([]int, 0, len(chosen))
	for j := range chosen {
		list = append(list, j)
	}
	sort.Slice(list, func(a, b int) bool {
		if s.cost[list[a]] != s.cost[list[b]] {
			return s.cost[list[a]] > s.cost[list[b]]
		}
		return s.xs[list[a]] > s.xs[list[b]]
	})
	cnt := make([]int, m)
	for j := range chosen {
		for i, iv := range s.ivs {
			if iv.L <= s.xs[j] && s.xs[j] <= iv.R {
				cnt[i]++
			}
		}
	}
	for _, j := range list {
		removable := true
		for i, iv := range s.ivs {
			if iv.L <= s.xs[j] && s.xs[j] <= iv.R && cnt[i] < 2 {
				removable = false
				break
			}
		}
		if removable {
			delete(chosen, j)
			for i, iv := range s.ivs {
				if iv.L <= s.xs[j] && s.xs[j] <= iv.R {
					cnt[i]--
				}
			}
		}
	}
	pts := make([]int64, 0, len(chosen))
	var total int64
	for j := range chosen {
		pts = append(pts, s.xs[j])
		total += s.cost[j]
	}
	sort.Slice(pts, func(a, b int) bool { return pts[a] < pts[b] })
	return pts, total
}

// greedyBound returns k, the exact minimum number of additional points needed
// to stab the given uncovered intervals (classic rightmost-point greedy), and
// the max over those intervals of the cheapest in-interval candidate cost.
func (s *solver) greedyBound(unc []int) (int64, int64) {
	ord := append([]int(nil), unc...)
	sort.Slice(ord, func(a, b int) bool {
		ia, ib := s.ivs[ord[a]], s.ivs[ord[b]]
		if ia.R != ib.R {
			return ia.R < ib.R
		}
		return ia.L < ib.L
	})
	var k, maxMin int64
	var last int64
	has := false
	for _, i := range ord {
		iv := s.ivs[i]
		if s.minIn[i] > maxMin {
			maxMin = s.minIn[i]
		}
		if has && last >= iv.L {
			continue
		}
		j := sort.Search(len(s.xs), func(j int) bool { return s.xs[j] > iv.R }) - 1
		last = s.xs[j]
		has = true
		k++
	}
	return k, maxMin
}

func (s *solver) search(sel []int64, cost int64) {
	if s.interrupted {
		return
	}
	s.nodes++
	if (s.maxNodes > 0 && s.nodes > s.maxNodes) || (!s.deadline.IsZero() && time.Now().After(s.deadline)) {
		s.interrupted = true
		s.liveLB = 0
		for i, v := range s.lbStack {
			if i == 0 || v < s.liveLB {
				s.liveLB = v
			}
		}
		return
	}
	var unc []int
	for i, iv := range s.ivs {
		j := sort.Search(len(sel), func(j int) bool { return sel[j] >= iv.L })
		if j >= len(sel) || sel[j] > iv.R {
			unc = append(unc, i)
		}
	}
	if len(unc) == 0 {
		if !s.hasBest || cost < s.bestCost ||
			(cost == s.bestCost && len(sel) < len(s.bestPts)) ||
			(cost == s.bestCost && len(sel) == len(s.bestPts) && lexLess(sel, s.bestPts)) {
			s.bestPts = append([]int64(nil), sel...)
			s.bestCost = cost
			s.hasBest = true
		}
		return
	}
	k, maxMin := s.greedyBound(unc)
	add := k * s.cmin
	if maxMin > add {
		add = maxMin
	}
	lb := cost + add
	lbCnt := len(sel) + int(k)
	if s.hasBest && (lb > s.bestCost || (lb == s.bestCost && lbCnt > len(s.bestPts))) {
		return
	}
	s.lbStack = append(s.lbStack, lb)
	defer func() { s.lbStack = s.lbStack[:len(s.lbStack)-1] }()

	t := unc[0]
	for _, i := range unc[1:] {
		if s.ivs[i].R < s.ivs[t].R || (s.ivs[i].R == s.ivs[t].R && s.ivs[i].L < s.ivs[t].L) {
			t = i
		}
	}
	iv := s.ivs[t]
	lo := sort.Search(len(s.xs), func(j int) bool { return s.xs[j] >= iv.L })
	hi := sort.Search(len(s.xs), func(j int) bool { return s.xs[j] > iv.R })
	idx := make([]int, 0, hi-lo)
	for j := lo; j < hi; j++ {
		idx = append(idx, j)
	}
	sort.Slice(idx, func(a, b int) bool {
		if s.cost[idx[a]] != s.cost[idx[b]] {
			return s.cost[idx[a]] < s.cost[idx[b]]
		}
		return s.xs[idx[a]] < s.xs[idx[b]]
	})
	for _, j := range idx {
		x := s.xs[j]
		pos := sort.Search(len(sel), func(p int) bool { return sel[p] >= x })
		nsel := make([]int64, len(sel)+1)
		copy(nsel, sel[:pos])
		nsel[pos] = x
		copy(nsel[pos+1:], sel[pos:])
		s.search(nsel, cost+s.cost[j])
		if s.interrupted {
			return
		}
	}
}

func lexLess(a, b []int64) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
