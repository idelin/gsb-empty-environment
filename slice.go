package prof

import "sort"

// occupancy is the result of the time-slice occupancy model: a sweep-line
// over all call intervals that integrates "who occupies a thread" per time
// slice. It is computed independently of the call-tree math so the two can
// be cross-checked (see Verify).
type occupancy struct {
	wallBusy   int64            // union of busy intervals across all threads
	threadBusy map[string]int64 // busy time per thread (single thread: no overlap)
	funcUnion  map[string]int64 // union of intervals where a func is active
}

// buildOccupancy sweeps all closed call intervals. Events at the same
// timestamp collapse into one net delta before integration, so
// zero-duration calls and same-timestamp start/end pairs contribute no
// occupancy and need no special casing.
func buildOccupancy(calls []*Call) occupancy {
	global := make(map[int64]int)
	perThread := make(map[string]map[int64]int)
	perFunc := make(map[string]map[int64]int)
	for _, c := range calls {
		global[c.Start]++
		global[c.End]--
		addDelta(perThread, c.Thread, c.Start, c.End)
		addDelta(perFunc, c.Func, c.Start, c.End)
	}
	occ := occupancy{
		threadBusy: make(map[string]int64),
		funcUnion:  make(map[string]int64),
	}
	occ.wallBusy = integrate(global)
	for thread, d := range perThread {
		occ.threadBusy[thread] = integrate(d)
	}
	for fn, d := range perFunc {
		occ.funcUnion[fn] = integrate(d)
	}
	return occ
}

func addDelta(m map[string]map[int64]int, key string, start, end int64) {
	d := m[key]
	if d == nil {
		d = make(map[int64]int)
		m[key] = d
	}
	d[start]++
	d[end]--
}

// integrate returns the total length of intervals where occupancy > 0.
func integrate(deltas map[int64]int) int64 {
	ts := make([]int64, 0, len(deltas))
	for t := range deltas {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	var total, active, prev int64
	for i, t := range ts {
		if i > 0 && active > 0 {
			total += (t - prev) * 1
		}
		active += int64(deltas[t])
		prev = t
	}
	return total
}

// Verify cross-checks the call-tree math against the time-slice occupancy
// model and returns a list of human-readable violations (empty means the
// report is internally consistent). The invariants are:
//
//  1. TotalExclusive == sum of per-thread busy times: at any instant each
//     busy thread has exactly one innermost call accruing exclusive time.
//  2. WallBusy <= TotalExclusive: collapsing threads onto one wall-clock
//     axis can only shrink time (cross-thread overlap).
//  3. For every function, WallUnion <= Inclusive: recursion and async
//     overlap make the plain inclusive sum over-count real elapsed time.
//  4. Every call has Self >= 0 and Self <= Inclusive.
func Verify(r *Report) []string {
	var problems []string
	var threadBusySum int64
	for _, b := range r.ThreadBusy {
		threadBusySum += b
	}
	if got := r.TotalExclusive(); got != threadBusySum {
		problems = append(problems,
			"total exclusive time does not match per-thread busy time")
	}
	if r.WallBusy > threadBusySum {
		problems = append(problems,
			"wall busy time exceeds sum of per-thread busy times")
	}
	for _, name := range r.FuncOrder {
		s := r.Funcs[name]
		if s.WallUnion > s.Inclusive {
			problems = append(problems,
				"func "+name+": wall union exceeds inclusive sum")
		}
	}
	var walk func(c *Call)
	walk = func(c *Call) {
		if c.Self < 0 || c.Self > c.Inclusive() {
			problems = append(problems,
				"func "+c.Func+": self time out of range")
		}
		for _, ch := range c.Children {
			walk(ch)
		}
	}
	for _, c := range r.Roots {
		walk(c)
	}
	return problems
}
