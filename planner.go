package railsim

import (
	"fmt"
	"sort"
	"strings"
)

// TrainPlan describes one committed train at replanning time.
//
// Route is the full remaining route; Committed holds the fixed occupancies
// of the already-entered prefix Route[:k] (they can never be moved), and
// Planned holds the originally promised occupancies of the adjustable
// suffix Route[k:]. ReadyAt is the earliest tick at which the train may
// enter Route[k]; a delay is expressed by raising it.
type TrainPlan struct {
	ID        TrainID
	Route     []SegmentID
	Dur       []int
	Committed []Interval
	Planned   []Interval
	ReadyAt   int
}

func (tp *TrainPlan) validate() error {
	if len(tp.Route) == 0 || len(tp.Route) != len(tp.Dur) {
		return fmt.Errorf("railsim: train %s: bad route/duration pair", tp.ID)
	}
	if len(tp.Committed)+len(tp.Planned) != len(tp.Route) {
		return fmt.Errorf("railsim: train %s: committed+planned != route length", tp.ID)
	}
	for _, d := range tp.Dur {
		if d <= 0 {
			return fmt.Errorf("railsim: train %s: non-positive duration", tp.ID)
		}
	}
	for _, iv := range tp.Committed {
		if !iv.Valid() {
			return fmt.Errorf("railsim: train %s: invalid committed interval %v", tp.ID, iv)
		}
	}
	for _, iv := range tp.Planned {
		if !iv.Valid() {
			return fmt.Errorf("railsim: train %s: invalid planned interval %v", tp.ID, iv)
		}
	}
	return nil
}

// suffixBase is the earliest legal entry into the first suffix segment.
func (tp *TrainPlan) suffixBase() int {
	base := tp.ReadyAt
	if n := len(tp.Committed); n > 0 && tp.Committed[n-1].End > base {
		base = tp.Committed[n-1].End
	}
	return base
}

// origCompletion is the originally promised arrival time.
func (tp *TrainPlan) origCompletion() int {
	if n := len(tp.Planned); n > 0 {
		return tp.Planned[n-1].End
	}
	if n := len(tp.Committed); n > 0 {
		return tp.Committed[n-1].End
	}
	return 0
}

// PlanResult is the outcome of Replan.
type PlanResult struct {
	OK         bool                   // false: no legal plan within the horizon
	Changed    []TrainID              // trains whose suffix actually moved
	TotalDelay int                    // sum of (new - original) completion times
	Suffix     map[TrainID][]Interval // new suffix occupancies of changed trains
}

// Replan reschedules the adjustable suffixes of committed trains after a
// delay. Objectives are lexicographic: first minimise the number of trains
// whose suffix changes, then minimise total completion delay. Committed
// prefixes are never moved. Designed for at most four trains over a finite
// horizon; the search is exact (serial schedule generation over every
// priority order, which covers all active — hence all optimal — schedules
// for regular objectives).
func Replan(plans []TrainPlan, horizon int) (PlanResult, error) {
	for i := range plans {
		if err := plans[i].validate(); err != nil {
			return PlanResult{}, err
		}
	}
	base := make(map[SegmentID][]Occupancy)
	for i := range plans {
		tp := &plans[i]
		for j, iv := range tp.Committed {
			base[tp.Route[j]] = insertSorted(base[tp.Route[j]], Occupancy{Train: tp.ID, Iv: iv})
		}
	}
	if err := Scan(base); err != nil {
		return PlanResult{}, fmt.Errorf("railsim: committed prefixes inconsistent: %v", err)
	}

	n := len(plans)
	for k := 0; k <= n; k++ {
		best := PlanResult{OK: false}
		bestDelay := 0
		forEachSubset(n, k, func(sub []int) {
			fixed, ok := frozenOccupancy(plans, sub, base)
			if !ok {
				return
			}
			var adj []*TrainPlan
			inSub := make(map[TrainID]bool, len(sub))
			for _, i := range sub {
				inSub[plans[i].ID] = true
				if len(plans[i].Planned) > 0 {
					adj = append(adj, &plans[i])
				}
			}
			suffixes, feasible := searchSuffixes(adj, fixed, horizon)
			if !feasible {
				return
			}
			res := buildResult(plans, inSub, suffixes)
			if !best.OK || res.TotalDelay < bestDelay {
				best, bestDelay = res, res.TotalDelay
			}
		})
		if best.OK {
			return best, nil
		}
	}
	return PlanResult{OK: false}, nil
}

// frozenOccupancy layers the planned suffixes of the trains not in sub
// onto the committed base. It fails if a frozen suffix violates its
// train's ReadyAt or overlaps anything already fixed.
func frozenOccupancy(plans []TrainPlan, sub []int, base map[SegmentID][]Occupancy) (map[SegmentID][]Occupancy, bool) {
	inSub := make([]bool, len(plans))
	for _, i := range sub {
		inSub[i] = true
	}
	fixed := cloneOccupancy(base)
	for i := range plans {
		tp := &plans[i]
		if inSub[i] || len(tp.Planned) == 0 {
			continue
		}
		if tp.Planned[0].Start < tp.suffixBase() {
			return nil, false // frozen plan infeasible under the new delay
		}
		k := len(tp.Committed)
		for j, iv := range tp.Planned {
			fixed[tp.Route[k+j]] = insertSorted(fixed[tp.Route[k+j]], Occupancy{Train: tp.ID, Iv: iv})
		}
	}
	if err := Scan(fixed); err != nil {
		return nil, false
	}
	return fixed, true
}

func cloneOccupancy(src map[SegmentID][]Occupancy) map[SegmentID][]Occupancy {
	out := make(map[SegmentID][]Occupancy, len(src))
	for seg, list := range src {
		cp := make([]Occupancy, len(list))
		copy(cp, list)
		out[seg] = cp
	}
	return out
}

// buildResult diffs the found suffixes against the original plans.
func buildResult(plans []TrainPlan, inSub map[TrainID]bool, suffixes map[TrainID][]Interval) PlanResult {
	res := PlanResult{OK: true, Suffix: make(map[TrainID][]Interval)}
	for i := range plans {
		tp := &plans[i]
		final := tp.Planned
		if inSub[tp.ID] {
			final = suffixes[tp.ID]
		}
		if !equalIntervals(final, tp.Planned) {
			res.Changed = append(res.Changed, tp.ID)
			res.Suffix[tp.ID] = final
		}
		completion := tp.origCompletion()
		if len(final) > 0 {
			completion = final[len(final)-1].End
		}
		res.TotalDelay += completion - tp.origCompletion()
	}
	sort.Slice(res.Changed, func(i, j int) bool { return res.Changed[i] < res.Changed[j] })
	return res
}

func equalIntervals(a, b []Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// forEachSubset calls fn with every k-element subset of {0..n-1}.
func forEachSubset(n, k int, fn func([]int)) {
	idx := make([]int, 0, k)
	var rec func(start int)
	rec = func(start int) {
		if len(idx) == k {
			cp := make([]int, k)
			copy(cp, idx)
			fn(cp)
			return
		}
		for i := start; i <= n-(k-len(idx)); i++ {
			idx = append(idx, i)
			rec(i + 1)
			idx = idx[:len(idx)-1]
		}
	}
	rec(0)
}

// trainState is the mutable search state of one adjustable train.
type trainState struct {
	tp    *TrainPlan
	next  int // next suffix position to place
	ready int // earliest entry for that segment
	ivs   []Interval
}

func (ts *trainState) done() bool { return ts.next == len(ts.tp.Planned) }

func (ts *trainState) absIdx() int { return len(ts.tp.Committed) + ts.next }

func (ts *trainState) remainingDur() int {
	sum := 0
	k := len(ts.tp.Committed)
	for j := ts.next; j < len(ts.tp.Planned); j++ {
		sum += ts.tp.Dur[k+j]
	}
	return sum
}

// suffixSearch finds suffix schedules for the adjustable trains minimising
// the sum of completion times, given fixed occupancy of everyone else.
type suffixSearch struct {
	horizon int
	occ     map[SegmentID][]Occupancy
	states  []trainState
	best    int
	bestIvs map[TrainID][]Interval
	memo    map[string]int
}

// searchSuffixes returns the optimal suffix schedule per adjustable train.
func searchSuffixes(adj []*TrainPlan, fixed map[SegmentID][]Occupancy, horizon int) (map[TrainID][]Interval, bool) {
	out := make(map[TrainID][]Interval, len(adj))
	if len(adj) == 0 {
		return out, true
	}
	s := &suffixSearch{
		horizon: horizon,
		occ:     fixed,
		best:    int(^uint(0) >> 1),
		memo:    make(map[string]int),
	}
	for _, tp := range adj {
		s.states = append(s.states, trainState{tp: tp, ready: tp.suffixBase()})
	}
	s.dfs(0)
	if s.bestIvs == nil {
		return nil, false
	}
	for id, ivs := range s.bestIvs {
		out[id] = ivs
	}
	return out, true
}

func (s *suffixSearch) dfs(acc int) {
	lb := acc
	allDone := true
	for i := range s.states {
		ts := &s.states[i]
		if ts.done() {
			continue
		}
		allDone = false
		lb += ts.ready + ts.remainingDur()
	}
	if lb >= s.best {
		return
	}
	if allDone {
		s.best = acc
		s.bestIvs = make(map[TrainID][]Interval, len(s.states))
		for i := range s.states {
			ts := &s.states[i]
			cp := make([]Interval, len(ts.ivs))
			copy(cp, ts.ivs)
			s.bestIvs[ts.tp.ID] = cp
		}
		return
	}
	key := s.key()
	if v, ok := s.memo[key]; ok && v <= acc {
		return
	}
	s.memo[key] = acc

	for i := range s.states {
		ts := &s.states[i]
		if ts.done() {
			continue
		}
		abs := ts.absIdx()
		seg := ts.tp.Route[abs]
		d := ts.tp.Dur[abs]
		e := earliestGap(s.occ[seg], ts.ready, d)
		if e+d > s.horizon {
			// Occupancy only grows from here, so this train can never
			// finish: the whole state is dead.
			return
		}
		iv := Interval{Start: e, End: e + d}
		s.occ[seg] = insertSorted(s.occ[seg], Occupancy{Train: ts.tp.ID, Iv: iv})
		ts.ivs = append(ts.ivs, iv)
		oldReady, oldNext := ts.ready, ts.next
		ts.ready, ts.next = e+d, ts.next+1
		nextAcc := acc
		if ts.done() {
			nextAcc += e + d
		}
		s.dfs(nextAcc)
		ts.ready, ts.next = oldReady, oldNext
		ts.ivs = ts.ivs[:len(ts.ivs)-1]
		s.occ[seg] = removeOccupancy(s.occ[seg], ts.tp.ID, iv)
	}
}

func (s *suffixSearch) key() string {
	var b strings.Builder
	for i := range s.states {
		ts := &s.states[i]
		fmt.Fprintf(&b, "%d:%d[", ts.next, ts.ready)
		for _, iv := range ts.ivs {
			fmt.Fprintf(&b, "%d-%d,", iv.Start, iv.End)
		}
		b.WriteString("]")
	}
	return b.String()
}

func removeOccupancy(list []Occupancy, train TrainID, iv Interval) []Occupancy {
	for i, o := range list {
		if o.Train == train && o.Iv == iv {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}
