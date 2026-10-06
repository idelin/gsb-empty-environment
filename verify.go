package railsim

import (
	"fmt"
	"sort"
)

// Scan interval-scans every segment's occupancies and reports the first
// overlap. Half-open neighbours ([a,b) then [b,c)) pass.

// bruteForceAborted reports that BruteForceReplan skipped an oversized
// instance (test-only observability).
var bruteForceAborted bool

func Scan(occ map[SegmentID][]Occupancy) error {
	for seg, list := range occ {
		sorted := make([]Occupancy, len(list))
		copy(sorted, list)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].Iv.Start != sorted[j].Iv.Start {
				return sorted[i].Iv.Start < sorted[j].Iv.Start
			}
			return sorted[i].Iv.End < sorted[j].Iv.End
		})
		for i := 1; i < len(sorted); i++ {
			if sorted[i].Iv.Start < sorted[i-1].Iv.End {
				return fmt.Errorf("railsim: segment %s: %s %v overlaps %s %v",
					seg, sorted[i-1].Train, sorted[i-1].Iv, sorted[i].Train, sorted[i].Iv)
			}
		}
	}
	return nil
}

// Replay simulates segment-by-segment occupancy in time order and verifies
// exclusive use: a segment may only be entered once its previous occupant
// has exited; an exit at tick T and an entry at the same tick T is legal.
func Replay(occ map[SegmentID][]Occupancy) error {
	type event struct {
		tick  int
		enter bool // exits sort before entries at the same tick
		occ   Occupancy
	}
	for seg, list := range occ {
		evs := make([]event, 0, 2*len(list))
		for _, o := range list {
			if !o.Iv.Valid() {
				return fmt.Errorf("railsim: segment %s: invalid interval %v", seg, o.Iv)
			}
			evs = append(evs, event{o.Iv.Start, true, o}, event{o.Iv.End, false, o})
		}
		sort.Slice(evs, func(i, j int) bool {
			if evs[i].tick != evs[j].tick {
				return evs[i].tick < evs[j].tick
			}
			return !evs[i].enter && evs[j].enter
		})
		var cur *Occupancy
		for i := range evs {
			ev := evs[i]
			if ev.enter {
				if cur != nil {
					return fmt.Errorf("railsim: segment %s: %s enters at %d while %s occupies until %d",
						seg, ev.occ.Train, ev.tick, cur.Train, cur.Iv.End)
				}
				c := ev.occ
				cur = &c
			} else {
				if cur == nil || cur.Train != ev.occ.Train {
					return fmt.Errorf("railsim: segment %s: %s exits at %d with no matching entry",
						seg, ev.occ.Train, ev.tick)
				}
				cur = nil
			}
		}
	}
	return nil
}

// AddOccupancy appends one train's (route, intervals) schedule to an
// occupancy map, allocating it when nil. Helper for building check inputs.
func AddOccupancy(occ map[SegmentID][]Occupancy, train TrainID, route []SegmentID, ivs []Interval) map[SegmentID][]Occupancy {
	if occ == nil {
		occ = make(map[SegmentID][]Occupancy)
	}
	for i, seg := range route {
		occ[seg] = append(occ[seg], Occupancy{Train: train, Iv: ivs[i]})
	}
	return occ
}

// EachStartCombo enumerates every start-time vector of length n with each
// value in [lo, hi], calling fn until it returns false.
func EachStartCombo(n, lo, hi int, fn func(starts []int) bool) {
	starts := make([]int, n)
	var rec func(i int) bool
	rec = func(i int) bool {
		if i == n {
			return fn(starts)
		}
		for s := lo; s <= hi; s++ {
			starts[i] = s
			if !rec(i + 1) {
				return false
			}
		}
		return true
	}
	rec(0)
}

// BruteForceReplan is an independent, exhaustive checker for Replan: it
// enumerates every legal suffix schedule of every adjustable-train subset
// inside the finite horizon and returns the lexicographically best
// (changed count, total delay) result. Exponential — for small tests only.
// If an instance is too large, it gives up and sets bruteForceAborted.
func BruteForceReplan(plans []TrainPlan, horizon int) (PlanResult, error) {
	bruteForceAborted = false
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
			options := make([][][]Interval, len(adj))
			product := 1
			for i, tp := range adj {
				options[i] = enumSuffixSchedules(tp, horizon)
				product *= len(options[i])
				if product > 4000000 {
					bruteForceAborted = true
					return // guard: instance too big for brute force
				}
			}
			choice := make([][]Interval, len(adj))
			var walk func(d int)
			walk = func(d int) {
				if d == len(adj) {
					occ := cloneOccupancy(fixed)
					suffixes := make(map[TrainID][]Interval, len(adj))
					for i, tp := range adj {
						suffixes[tp.ID] = choice[i]
						kk := len(tp.Committed)
						for j, iv := range choice[i] {
							occ[tp.Route[kk+j]] = insertSorted(occ[tp.Route[kk+j]],
								Occupancy{Train: tp.ID, Iv: iv})
						}
					}
					if err := Scan(occ); err != nil {
						return
					}
					res := buildResult(plans, inSub, suffixes)
					if !best.OK || res.TotalDelay < bestDelay {
						best, bestDelay = res, res.TotalDelay
					}
					return
				}
				for _, opt := range options[d] {
					choice[d] = opt
					walk(d + 1)
				}
			}
			walk(0)
		})
		if best.OK {
			return best, nil
		}
	}
	return PlanResult{OK: false}, nil
}

// enumSuffixSchedules lists every suffix schedule of tp that respects
// ReadyAt, the committed prefix exit, per-segment durations and the
// horizon, ignoring other trains.
func enumSuffixSchedules(tp *TrainPlan, horizon int) [][]Interval {
	var out [][]Interval
	cur := make([]Interval, 0, len(tp.Planned))
	var rec func(pos, lo int)
	rec = func(pos, lo int) {
		if pos == len(tp.Planned) {
			cp := make([]Interval, len(cur))
			copy(cp, cur)
			out = append(out, cp)
			return
		}
		abs := len(tp.Committed) + pos
		d := tp.Dur[abs]
		rem := 0
		for j := pos + 1; j < len(tp.Planned); j++ {
			rem += tp.Dur[len(tp.Committed)+j]
		}
		for e := lo; e+d+rem <= horizon; e++ {
			cur = append(cur, Interval{Start: e, End: e + d})
			rec(pos+1, e+d)
			cur = cur[:len(cur)-1]
		}
	}
	rec(0, tp.suffixBase())
	return out
}
