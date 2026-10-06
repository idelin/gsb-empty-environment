// Package sched solves a single-machine scheduling problem: up to twelve
// orders, each with a type, a processing duration and a deadline. Switching
// between two orders of different types costs a sequence-dependent setup
// time given by a (possibly asymmetric) matrix; the machine starts in a
// given initial type. Objectives are lexicographic: first minimize the
// maximum lateness (C_j - d_j, may be negative), then minimize the total
// setup time. Remaining ties are broken by the lexicographically smallest
// sequence of order indices, so the result is deterministic.
package sched

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Order is a single job to be scheduled.
type Order struct {
	ID       string // human-readable identifier, used only in output
	Type     int    // index into the setup matrix
	Duration int    // processing time
	Deadline int    // due time; lateness = Finish - Deadline
}

// Problem describes one scheduling instance.
type Problem struct {
	Orders      []Order // at most MaxOrders
	Setup       [][]int // Setup[fromType][toType]; may be asymmetric
	InitialType int     // machine type before the first order
}

// MaxOrders is the largest instance Solve handles (subset DP is O(2^n n^2)).
const MaxOrders = 12

// Slot is one scheduled order with its position in the timetable.
type Slot struct {
	Order    Order
	Index    int // position of the order in Problem.Orders
	Setup    int // changeover time incurred before this order starts
	Start    int // start of processing (after the setup)
	Finish   int // completion time
	Lateness int // Finish - Deadline
}

// Solution is the optimal schedule plus its objective values.
type Solution struct {
	Slots       []Slot
	MaxLateness int
	TotalSetup  int
}

// Sequence returns the scheduled order indices (into Problem.Orders).
func (s Solution) Sequence() []int {
	seq := make([]int, len(s.Slots))
	for i, sl := range s.Slots {
		seq[i] = sl.Index
	}
	return seq
}

// String renders the solution as an aligned timetable.
func (s Solution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-3s %-8s %-4s %-6s %-6s %-6s %-8s %-8s\n",
		"#", "Order", "Type", "Setup", "Start", "Finish", "Deadline", "Lateness")
	for i, sl := range s.Slots {
		id := sl.Order.ID
		if id == "" {
			id = fmt.Sprintf("J%d", sl.Index)
		}
		fmt.Fprintf(&b, "%-3d %-8s %-4d %-6d %-6d %-6d %-8d %-8d\n",
			i, id, sl.Order.Type, sl.Setup, sl.Start, sl.Finish,
			sl.Order.Deadline, sl.Lateness)
	}
	fmt.Fprintf(&b, "max lateness: %d, total setup: %d\n", s.MaxLateness, s.TotalSetup)
	return b.String()
}

func validate(p Problem) error {
	n := len(p.Orders)
	if n > MaxOrders {
		return fmt.Errorf("sched: %d orders exceeds maximum %d", n, MaxOrders)
	}
	if n == 0 {
		return nil
	}
	if len(p.Setup) == 0 {
		return errors.New("sched: setup matrix is empty")
	}
	for i, row := range p.Setup {
		if len(row) != len(p.Setup) {
			return fmt.Errorf("sched: setup row %d has length %d, want %d", i, len(row), len(p.Setup))
		}
		for _, v := range row {
			if v < 0 {
				return errors.New("sched: setup times must be non-negative")
			}
		}
	}
	if p.InitialType < 0 || p.InitialType >= len(p.Setup) {
		return fmt.Errorf("sched: initial type %d out of range", p.InitialType)
	}
	for i, o := range p.Orders {
		if o.Type < 0 || o.Type >= len(p.Setup) {
			return fmt.Errorf("sched: order %d has type %d out of range", i, o.Type)
		}
		if o.Duration < 0 {
			return fmt.Errorf("sched: order %d has negative duration", i)
		}
	}
	return nil
}

// lexLess reports whether index sequence a is lexicographically smaller than b.
func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// fpair is one point on a Pareto frontier: a schedule of some subset ending
// in some order, with completion time c and maximum lateness l.
type fpair struct {
	c, l int
}

// prune reduces a list of frontier points to the non-dominated ones:
// p dominates q iff p.c <= q.c && p.l <= q.l. The result is sorted by
// ascending completion with strictly decreasing lateness.
func prune(pts []fpair) []fpair {
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].c != pts[j].c {
			return pts[i].c < pts[j].c
		}
		return pts[i].l < pts[j].l
	})
	out := pts[:0]
	bestL := 0
	first := true
	for _, p := range pts {
		if first || p.l < bestL {
			out = append(out, p)
			bestL = p.l
			first = false
		}
	}
	return out
}

// Solve returns the optimal schedule for p.
//
// The processing-time part of a subset's completion is order independent, so
// a subset DP applies. Because setups make completion times order dependent,
// dp1[mask][last] keeps the whole Pareto frontier of (completion, max
// lateness) pairs; a single best pair would be incorrect (a prefix with
// worse lateness but much earlier completion can win globally).
//
// Phase 1 finds L*, the minimum achievable maximum lateness. Phase 2 then
// minimizes (total setup, index sequence) lexicographically among orderings
// whose every prefix keeps lateness <= L*. That restriction is sound because
// for a fixed (mask, last) the minimum-setup feasible prefix also has the
// minimum completion time (completion = sum of durations + total setup), so
// if even it pushes the next order past L* no other feasible prefix can do
// better.
func Solve(p Problem) (Solution, error) {
	if err := validate(p); err != nil {
		return Solution{}, err
	}
	n := len(p.Orders)
	if n == 0 {
		return Solution{}, nil
	}
	size := 1 << uint(n)

	sumDur := make([]int, size)
	for mask := 1; mask < size; mask++ {
		low := mask & (-mask)
		i := 0
		for low>>uint(i) > 1 {
			i++
		}
		sumDur[mask] = sumDur[mask^low] + p.Orders[i].Duration
	}

	// Phase 1: Pareto frontiers of (completion, maxLateness).
	dp1 := make([][][]fpair, size)
	for mask := 1; mask < size; mask++ {
		dp1[mask] = make([][]fpair, n)
		for last := 0; last < n; last++ {
			bit := 1 << uint(last)
			if mask&bit == 0 {
				continue
			}
			tl := p.Orders[last].Type
			if mask == bit {
				c := p.Setup[p.InitialType][tl] + p.Orders[last].Duration
				dp1[mask][last] = []fpair{{c: c, l: c - p.Orders[last].Deadline}}
				continue
			}
			prev := mask &^ bit
			var pts []fpair
			for j := 0; j < n; j++ {
				if prev&(1<<uint(j)) == 0 {
					continue
				}
				st := p.Setup[p.Orders[j].Type][tl]
				for _, f := range dp1[prev][j] {
					c := f.c + st + p.Orders[last].Duration
					l := f.l
					if nl := c - p.Orders[last].Deadline; nl > l {
						l = nl
					}
					pts = append(pts, fpair{c: c, l: l})
				}
			}
			dp1[mask][last] = prune(pts)
		}
	}
	full := size - 1
	lStar := 0
	first := true
	for last := 0; last < n; last++ {
		for _, f := range dp1[full][last] {
			if first || f.l < lStar {
				lStar = f.l
				first = false
			}
		}
	}

	// Phase 2: minimize (total setup, sequence) under max lateness <= L*.
	type cand struct {
		setup int
		seq   []int
		ok    bool
	}
	dp2 := make([][]cand, size)
	for i := range dp2 {
		dp2[i] = make([]cand, n)
	}
	better2 := func(a, b cand) bool {
		if a.setup != b.setup {
			return a.setup < b.setup
		}
		return lexLess(a.seq, b.seq)
	}
	for mask := 1; mask < size; mask++ {
		for last := 0; last < n; last++ {
			bit := 1 << uint(last)
			if mask&bit == 0 {
				continue
			}
			tl := p.Orders[last].Type
			dl := p.Orders[last].Deadline
			if mask == bit {
				st := p.Setup[p.InitialType][tl]
				if sumDur[mask]+st-dl <= lStar {
					dp2[mask][last] = cand{setup: st, seq: []int{last}, ok: true}
				}
				continue
			}
			prev := mask &^ bit
			var best cand
			for j := 0; j < n; j++ {
				if prev&(1<<uint(j)) == 0 || !dp2[prev][j].ok {
					continue
				}
				st := p.Setup[p.Orders[j].Type][tl]
				setup := dp2[prev][j].setup + st
				// Completion of `last` under this prefix; the prefix's own
				// latenesses are <= L* by induction.
				if sumDur[mask]+setup-dl > lStar {
					continue
				}
				c := cand{
					setup: setup,
					seq:   append(append([]int{}, dp2[prev][j].seq...), last),
					ok:    true,
				}
				if !best.ok || better2(c, best) {
					best = c
				}
			}
			dp2[mask][last] = best
		}
	}
	var best cand
	for last := 0; last < n; last++ {
		if dp2[full][last].ok && (!best.ok || better2(dp2[full][last], best)) {
			best = dp2[full][last]
		}
	}
	if !best.ok {
		return Solution{}, errors.New("sched: no feasible schedule (internal error)")
	}
	return buildSolution(p, best.seq), nil
}

// buildSolution materializes the timetable for a given order sequence.
func buildSolution(p Problem, seq []int) Solution {
	sol := Solution{Slots: make([]Slot, 0, len(seq))}
	time := 0
	prevType := p.InitialType
	maxLate := 0
	for i, idx := range seq {
		o := p.Orders[idx]
		st := p.Setup[prevType][o.Type]
		start := time + st
		finish := start + o.Duration
		late := finish - o.Deadline
		if i == 0 || late > maxLate {
			maxLate = late
		}
		sol.TotalSetup += st
		sol.Slots = append(sol.Slots, Slot{
			Order:    o,
			Index:    idx,
			Setup:    st,
			Start:    start,
			Finish:   finish,
			Lateness: late,
		})
		time = finish
		prevType = o.Type
	}
	sol.MaxLateness = maxLate
	return sol
}
