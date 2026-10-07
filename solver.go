// Package sched provides exact single-machine scheduling for a small
// batch of orders (up to MaxOrders) with sequence-dependent changeover
// costs between order types.
//
// Model: every order has a type, a processing duration and a deadline.
// The machine starts in a given initial type. Whenever two consecutive
// orders (or the initial type and the first order) have different types,
// a changeover cost setup[prev][next] is paid; the matrix may be
// asymmetric. Changeover is a pure cost: it does not consume machine
// time, so completion times are cumulative durations only.
//
// The objective is lexicographic:
//  1. minimize the maximum lateness max(C_j - d_j) over all orders
//     (lateness may be negative when everything is early);
//  2. among those, minimize the total changeover cost;
//  3. remaining ties are broken by the lexicographically smallest
//     sequence of input indices, so the result is deterministic.
package sched

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strings"
)

// MaxOrders is the batch size Solve is designed for. Solve is exact and
// exponential in the number of orders, which is comfortable up to 12.
const MaxOrders = 12

// Order is a single job to schedule.
type Order struct {
	ID       string
	Type     int
	Duration int
	Deadline int
}

// Entry is one slot of the resulting timetable.
type Entry struct {
	Index    int   // index of the order in the input slice
	Order    Order // the scheduled order
	Setup    int   // changeover cost paid before this order (0 if same type as previous)
	Start    int   // start time (cumulative durations, changeovers not included)
	End      int   // completion time
	Lateness int   // End - Deadline (negative means early)
}

// Schedule is the result of Solve or Evaluate.
type Schedule struct {
	Entries     []Entry
	MaxLateness int // maximum Lateness over all entries (0 when empty)
	TotalSetup  int // sum of changeover costs
}

// point is one Pareto-optimal (maxLateness, totalSetup) cost pair.
type point struct {
	maxLat, setup int
}

// pareto reduces pts to the componentwise non-dominated subset: the
// result is sorted by ascending maxLat with strictly decreasing setup.
func pareto(pts []point) []point {
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].maxLat != pts[j].maxLat {
			return pts[i].maxLat < pts[j].maxLat
		}
		return pts[i].setup < pts[j].setup
	})
	out := pts[:0]
	bestSetup := math.MaxInt
	for _, p := range pts {
		if p.setup < bestSetup {
			out = append(out, p)
			bestSetup = p.setup
		}
	}
	return out
}

// Solve returns an optimal timetable for the given orders.
//
// setup[a][b] is the changeover cost when an order of type b directly
// follows type a; it may be asymmetric and a nil matrix means zero
// changeover cost everywhere. initialType is the machine type before the
// first order. See the package documentation for the exact objective.
func Solve(orders []Order, setup [][]int, initialType int) Schedule {
	n := len(orders)
	if n == 0 {
		return Schedule{Entries: []Entry{}}
	}

	nTypes := initialType
	for _, o := range orders {
		nTypes = max(nTypes, o.Type)
	}
	nTypes++
	if setup == nil {
		setup = make([][]int, nTypes)
		for i := range setup {
			setup[i] = make([]int, nTypes)
		}
	} else {
		if len(setup) < nTypes {
			panic("sched: setup matrix has fewer rows than the number of types")
		}
		for i := 0; i < nTypes; i++ {
			if len(setup[i]) < nTypes {
				panic("sched: setup matrix row shorter than the number of types")
			}
		}
	}
	// Changeover is paid only when the type actually changes; the
	// matrix diagonal is never consulted.
	changeover := func(from, to int) int {
		if from == to {
			return 0
		}
		return setup[from][to]
	}

	// durSum[mask] = total processing duration of the orders in mask.
	// Because changeovers cost no machine time, the completion time of
	// the last order of any fixed subset is always durSum[mask], which
	// is what makes subset dynamic programming exact here.
	size := 1 << n
	durSum := make([]int, size)
	for mask := 1; mask < size; mask++ {
		low := mask & -mask
		durSum[mask] = durSum[mask^low] + orders[bits.TrailingZeros(uint(low))].Duration
	}

	// Phase 1: forward subset DP for the optimal cost. dp[mask][last]
	// holds the Pareto frontier of (maxLateness, totalSetup) over all
	// orderings of exactly mask that end with order last. A single best
	// pair per state is NOT sufficient: a prefix with worse lateness
	// but cheaper setup can still win once future lateness dominates
	// both, so the full frontier must be kept.
	dp := make([][][]point, size)
	for mask := range dp {
		dp[mask] = make([][]point, n)
	}
	for j := 0; j < n; j++ {
		dp[1<<j][j] = []point{{
			maxLat: orders[j].Duration - orders[j].Deadline,
			setup:  changeover(initialType, orders[j].Type),
		}}
	}
	for mask := 1; mask < size; mask++ {
		for last := 0; last < n; last++ {
			frontier := dp[mask][last]
			if len(frontier) == 0 {
				continue
			}
			if mask&(mask-1) != 0 { // multi-bit masks accumulated candidates
				frontier = pareto(frontier)
				dp[mask][last] = frontier
			}
			for next := 0; next < n; next++ {
				if mask>>next&1 == 1 {
					continue
				}
				o := orders[next]
				lat := durSum[mask] + o.Duration - o.Deadline
				change := changeover(orders[last].Type, o.Type)
				for _, p := range frontier {
					dp[mask|1<<next][next] = append(dp[mask|1<<next][next], point{
						maxLat: max(p.maxLat, lat),
						setup:  p.setup + change,
					})
				}
			}
		}
	}
	full := size - 1
	opt := point{maxLat: math.MaxInt, setup: math.MaxInt}
	for last := 0; last < n; last++ {
		for _, p := range pareto(dp[full][last]) {
			if p.maxLat < opt.maxLat || (p.maxLat == opt.maxLat && p.setup < opt.setup) {
				opt = p
			}
		}
	}

	// Phase 2: backward feasibility DP. Every order j of an optimal
	// sequence meets its effective deadline Deadline_j + opt.maxLat, so
	// h[done][t] = minimum changeover cost to schedule all orders not
	// in `done` (previous type t, current time durSum[done]) while
	// meeting those effective deadlines; +inf when impossible.
	const inf = math.MaxInt / 4
	h := make([][]int, size)
	for done := range h {
		h[done] = make([]int, nTypes)
	}
	for done := size - 1; done >= 0; done-- {
		for t := 0; t < nTypes; t++ {
			if done == full {
				continue // h[full][t] = 0
			}
			best := inf
			for j := 0; j < n; j++ {
				if done>>j&1 == 1 {
					continue
				}
				o := orders[j]
				if durSum[done]+o.Duration > o.Deadline+opt.maxLat {
					continue
				}
				if c := changeover(t, o.Type) + h[done|1<<j][o.Type]; c < best {
					best = c
				}
			}
			h[done][t] = best
		}
	}

	// Phase 3: greedily build the lexicographically smallest optimal
	// sequence. Invariant: the prefix chosen so far can be extended to
	// a sequence with cost (opt.maxLat, opt.setup). The smallest order
	// index preserving the invariant is appended next.
	perm := make([]int, 0, n)
	done, prevType, usedSetup, maxLatSoFar := 0, initialType, 0, math.MinInt
	for len(perm) < n {
		chosen := -1
		for j := 0; j < n; j++ {
			if done>>j&1 == 1 {
				continue
			}
			o := orders[j]
			if lat := durSum[done] + o.Duration - o.Deadline; max(maxLatSoFar, lat) > opt.maxLat {
				continue
			}
			if s := usedSetup + changeover(prevType, o.Type); s+h[done|1<<j][o.Type] > opt.setup {
				continue
			}
			chosen = j
			break
		}
		if chosen < 0 {
			panic("sched: internal error: no feasible extension")
		}
		o := orders[chosen]
		maxLatSoFar = max(maxLatSoFar, durSum[done]+o.Duration-o.Deadline)
		usedSetup += changeover(prevType, o.Type)
		prevType = o.Type
		done |= 1 << chosen
		perm = append(perm, chosen)
	}
	return Evaluate(orders, setup, initialType, perm)
}

// Evaluate builds the timetable for an explicit permutation of order
// indices. It applies exactly the same cost model as Solve.
func Evaluate(orders []Order, setup [][]int, initialType int, perm []int) Schedule {
	entries := make([]Entry, len(perm))
	now, totalSetup, maxLat := 0, 0, math.MinInt
	prevType := initialType
	for i, idx := range perm {
		o := orders[idx]
		change := 0
		if setup != nil && o.Type != prevType {
			change = setup[prevType][o.Type]
		}
		end := now + o.Duration
		lat := end - o.Deadline
		entries[i] = Entry{Index: idx, Order: o, Setup: change, Start: now, End: end, Lateness: lat}
		totalSetup += change
		maxLat = max(maxLat, lat)
		now = end
		prevType = o.Type
	}
	if len(perm) == 0 {
		maxLat = 0
	}
	return Schedule{Entries: entries, MaxLateness: maxLat, TotalSetup: totalSetup}
}

// String renders the schedule as a human-readable timetable.
func (s Schedule) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-3s %-8s %-4s %-5s %-5s %-5s %s\n", "#", "Order", "Type", "Setup", "Start", "End", "Lateness")
	for i, e := range s.Entries {
		fmt.Fprintf(&b, "%-3d %-8s %-4d %-5d %-5d %-5d %d\n", i, e.Order.ID, e.Order.Type, e.Setup, e.Start, e.End, e.Lateness)
	}
	fmt.Fprintf(&b, "MaxLateness=%d TotalSetup=%d\n", s.MaxLateness, s.TotalSetup)
	return b.String()
}
