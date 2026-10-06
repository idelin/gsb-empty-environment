package railsim

import (
	"math/rand"
	"testing"
)

func changedSet(res PlanResult) map[TrainID]bool {
	s := make(map[TrainID]bool, len(res.Changed))
	for _, id := range res.Changed {
		s[id] = true
	}
	return s
}

// replayResult rebuilds full occupancy (committed + final suffixes) and
// verifies it by interval scan and event replay.
func replayResult(t *testing.T, plans []TrainPlan, res PlanResult) {
	t.Helper()
	occ := make(map[SegmentID][]Occupancy)
	for i := range plans {
		tp := &plans[i]
		k := len(tp.Committed)
		occ = AddOccupancy(occ, tp.ID, tp.Route[:k], tp.Committed)
		suffix := tp.Planned
		if ns, ok := res.Suffix[tp.ID]; ok {
			suffix = ns
		}
		occ = AddOccupancy(occ, tp.ID, tp.Route[k:], suffix)
	}
	if err := Scan(occ); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if err := Replay(occ); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

// 延误后只改最少列车: the delayed train alone absorbs the delay.
func TestReplanSingleTrainAbsorbsDelay(t *testing.T) {
	plans := []TrainPlan{
		{ID: "T1", Route: []SegmentID{"X"}, Dur: []int{4},
			Planned: []Interval{{0, 4}}, ReadyAt: 3}, // delayed by 3
		{ID: "T2", Route: []SegmentID{"X"}, Dur: []int{4},
			Planned: []Interval{{4, 8}}, ReadyAt: 4},
	}
	res, err := Replan(plans, 30)
	if err != nil || !res.OK {
		t.Fatalf("Replan: %v %+v", err, res)
	}
	if len(res.Changed) != 1 || res.Changed[0] != "T1" {
		t.Fatalf("Changed = %v, want [T1]", res.Changed)
	}
	// T1 must wait for T2's frozen [4,8) slot: [8,12), delay 12-4=8.
	if got := res.Suffix["T1"][0]; got != (Interval{8, 12}) {
		t.Fatalf("T1 suffix = %v, want [8,12)", got)
	}
	if res.TotalDelay != 8 {
		t.Fatalf("TotalDelay = %d, want 8", res.TotalDelay)
	}
	replayResult(t, plans, res)
}

// 连锁冲突: the horizon is too tight for the delayed train to simply wait,
// so a second train must be rescheduled — the minimum is two changed.
func TestReplanCascadingConflict(t *testing.T) {
	plans := []TrainPlan{
		{ID: "T1", Route: []SegmentID{"X"}, Dur: []int{3},
			Planned: []Interval{{1, 4}}, ReadyAt: 4}, // delayed
		{ID: "T2", Route: []SegmentID{"X"}, Dur: []int{2},
			Planned: []Interval{{5, 7}}, ReadyAt: 5},
	}
	res, err := Replan(plans, 9)
	if err != nil || !res.OK {
		t.Fatalf("Replan: %v %+v", err, res)
	}
	if len(res.Changed) != 2 {
		t.Fatalf("Changed = %v, want both trains", res.Changed)
	}
	// Only solution: T1 [4,7), T2 [7,9); delay (7-4)+(9-7)=5.
	if got := res.Suffix["T1"][0]; got != (Interval{4, 7}) {
		t.Fatalf("T1 suffix = %v, want [4,7)", got)
	}
	if got := res.Suffix["T2"][0]; got != (Interval{7, 9}) {
		t.Fatalf("T2 suffix = %v, want [7,9)", got)
	}
	if res.TotalDelay != 5 {
		t.Fatalf("TotalDelay = %d, want 5", res.TotalDelay)
	}
	replayResult(t, plans, res)
}

// 不能移动已进入轨段的占用: committed prefixes stay put; only suffixes move.
func TestReplanKeepsCommittedPrefix(t *testing.T) {
	plans := []TrainPlan{
		{ID: "T1", Route: []SegmentID{"A", "B"}, Dur: []int{2, 3},
			Committed: []Interval{{0, 2}},              // already inside segment A
			Planned:   []Interval{{2, 5}}, ReadyAt: 4}, // delayed exit from A
		{ID: "T2", Route: []SegmentID{"B"}, Dur: []int{2},
			Planned: []Interval{{5, 7}}, ReadyAt: 5},
	}
	res, err := Replan(plans, 20)
	if err != nil || !res.OK {
		t.Fatalf("Replan: %v %+v", err, res)
	}
	if len(res.Changed) != 1 || res.Changed[0] != "T1" {
		t.Fatalf("Changed = %v, want [T1]", res.Changed)
	}
	// T1 may not enter B before max(ReadyAt, committed exit) = 4, and B is
	// frozen by T2 over [5,7): earliest fit is [7,10).
	if got := res.Suffix["T1"][0]; got != (Interval{7, 10}) {
		t.Fatalf("T1 suffix = %v, want [7,10)", got)
	}
	if res.TotalDelay != 5 {
		t.Fatalf("TotalDelay = %d, want 5", res.TotalDelay)
	}
	replayResult(t, plans, res)
}

// No delay at all: zero trains change, zero delay.
func TestReplanNoChangeNeeded(t *testing.T) {
	plans := []TrainPlan{
		{ID: "T1", Route: []SegmentID{"X"}, Dur: []int{3},
			Planned: []Interval{{0, 3}}, ReadyAt: 0},
		{ID: "T2", Route: []SegmentID{"X"}, Dur: []int{3},
			Planned: []Interval{{3, 6}}, ReadyAt: 3},
	}
	res, err := Replan(plans, 20)
	if err != nil || !res.OK {
		t.Fatalf("Replan: %v %+v", err, res)
	}
	if len(res.Changed) != 0 || res.TotalDelay != 0 {
		t.Fatalf("got %+v, want no changes", res)
	}
}

// Infeasible: the delayed train cannot finish before the horizon no
// matter what.
func TestReplanInfeasible(t *testing.T) {
	plans := []TrainPlan{
		{ID: "T1", Route: []SegmentID{"X"}, Dur: []int{4},
			Planned: []Interval{{0, 4}}, ReadyAt: 8},
	}
	res, err := Replan(plans, 10)
	if err != nil {
		t.Fatalf("Replan: %v", err)
	}
	if res.OK {
		t.Fatalf("expected infeasible, got %+v", res)
	}
}

// 对拍: random small instances, Replan must match the exhaustive
// brute-force checker on (feasibility, changed count, total delay), and
// every result must replay cleanly. At most four trains, finite horizon.
func TestReplanMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	segPool := []SegmentID{"X", "Y", "Z"}
	for trial := 0; trial < 200; trial++ {
		n := 2 + rng.Intn(3) // 2..4 trains
		horizon := 8 + rng.Intn(5)
		// Build a consistent original plan by booking trains one by one.
		tb := NewTable()
		var plans []TrainPlan
		ok := true
		for i := 0; i < n && ok; i++ {
			routeLen := 1 + rng.Intn(2) // 1..2 segments
			route := make([]SegmentID, routeLen)
			dur := make([]int, routeLen)
			for j := range route {
				route[j] = segPool[rng.Intn(len(segPool))]
				dur[j] = 1 + rng.Intn(3)
			}
			ready := rng.Intn(4)
			ivs, err := tb.SchedulePath(TrainID(rune('A'+i)), route, dur, ready, horizon)
			if err != nil {
				ok = false
				break
			}
			tp := TrainPlan{
				ID:      TrainID(rune('A' + i)),
				Route:   route,
				Dur:     dur,
				Planned: ivs,
				ReadyAt: ready,
			}
			// Sometimes commit the first segment (train already entered).
			if rng.Intn(3) == 0 && routeLen > 1 {
				tp.Committed = ivs[:1]
				tp.Planned = ivs[1:]
				tp.ReadyAt = ivs[1].Start
			}
			plans = append(plans, tp)
		}
		if !ok {
			continue
		}
		// Delay one random train.
		d := &plans[rng.Intn(len(plans))]
		if len(d.Planned) > 0 {
			d.ReadyAt = d.Planned[0].Start + rng.Intn(4)
		}

		got, err := Replan(plans, horizon)
		if err != nil {
			t.Fatalf("trial %d: Replan error: %v", trial, err)
		}
		want, err := BruteForceReplan(plans, horizon)
		if err != nil {
			t.Fatalf("trial %d: BruteForce error: %v", trial, err)
		}
		if bruteForceAborted {
			continue // instance too large for the exhaustive checker
		}
		if got.OK != want.OK {
			t.Fatalf("trial %d: feasibility %v vs brute %v\nplans=%+v horizon=%d",
				trial, got.OK, want.OK, plans, horizon)
		}
		if !got.OK {
			continue
		}
		if len(got.Changed) != len(want.Changed) || got.TotalDelay != want.TotalDelay {
			t.Fatalf("trial %d: got (changed=%d delay=%d), brute (changed=%d delay=%d)\nplans=%+v horizon=%d",
				trial, len(got.Changed), got.TotalDelay,
				len(want.Changed), want.TotalDelay, plans, horizon)
		}
		replayResult(t, plans, got)
	}
}
