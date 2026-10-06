package sched

import (
	"math/rand"
	"reflect"
	"testing"
)

func randInstance(r *rand.Rand, n int) *Instance {
	devs := []string{"d1", "d2", "d3"}
	groups := []string{"", "", "g1", "g2"}
	inst := &Instance{Cooldown: r.Intn(3)}
	for i := 0; i < n; i++ {
		inst.Candidates = append(inst.Candidates, Candidate{
			ID:     string(rune('a' + i)),
			Device: devs[r.Intn(len(devs))],
			Slot:   r.Intn(12),
			Profit: r.Intn(21) - 10,
			Group:  groups[r.Intn(len(groups))],
		})
	}
	return inst
}

func TestSolveMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		inst := randInstance(r, r.Intn(13))
		got := Solve(inst)
		want := BruteForce(inst)
		if got.Profit != want.Profit || !reflect.DeepEqual(got.IDs, want.IDs) {
			t.Fatalf("trial %d: Solve=%v/%v BruteForce=%v/%v", trial, got.Profit, got.IDs, want.Profit, want.IDs)
		}
	}
}

func TestSolveMatchesBruteForce20(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	inst := randInstance(r, 20)
	got := Solve(inst)
	want := BruteForce(inst)
	if got.Profit != want.Profit || !reflect.DeepEqual(got.IDs, want.IDs) {
		t.Fatalf("Solve=%v/%v BruteForce=%v/%v", got.Profit, got.IDs, want.Profit, want.IDs)
	}
}

func TestNegativeProfitsYieldEmpty(t *testing.T) {
	inst := &Instance{Cooldown: 0, Candidates: []Candidate{
		{ID: "a", Device: "d1", Slot: 0, Profit: -3},
		{ID: "b", Device: "d2", Slot: 0, Profit: -1},
	}}
	if sol := Solve(inst); sol.Profit != 0 || len(sol.IDs) != 0 {
		t.Fatalf("want empty solution, got %+v", sol)
	}
}

func TestCooldownBoundary(t *testing.T) {
	// c=2: slots 0 and 2 leave only one full slot between -> illegal;
	// slots 0 and 3 leave two full slots -> legal.
	mk := func(slotB int) *Instance {
		return &Instance{Cooldown: 2, Candidates: []Candidate{
			{ID: "a", Device: "d1", Slot: 0, Profit: 5},
			{ID: "b", Device: "d1", Slot: slotB, Profit: 5},
		}}
	}
	if sol := Solve(mk(2)); sol.Profit != 5 {
		t.Fatalf("slot gap 2 with c=2 should forbid pairing, got %+v", sol)
	}
	if sol := Solve(mk(3)); sol.Profit != 10 {
		t.Fatalf("slot gap 3 with c=2 should allow pairing, got %+v", sol)
	}
}

func TestMutexGroup(t *testing.T) {
	inst := &Instance{Cooldown: 0, Candidates: []Candidate{
		{ID: "a", Device: "d1", Slot: 0, Profit: 5, Group: "g"},
		{ID: "b", Device: "d2", Slot: 0, Profit: 4, Group: "g"},
		{ID: "c", Device: "d3", Slot: 0, Profit: 1},
	}}
	sol := Solve(inst)
	if sol.Profit != 6 || !reflect.DeepEqual(sol.IDs, []string{"a", "c"}) {
		t.Fatalf("got %+v", sol)
	}
}

func TestTieBreakFewerThenLexicographic(t *testing.T) {
	// Max profit 5 is reached by {a,c}, {b} and {d}: fewer experiments
	// wins, then the lexicographically smaller id list.
	inst := &Instance{Cooldown: 0, Candidates: []Candidate{
		{ID: "a", Device: "d1", Slot: 0, Profit: 2},
		{ID: "b", Device: "d1", Slot: 0, Profit: 5, Group: "g1"},
		{ID: "c", Device: "d2", Slot: 0, Profit: 3, Group: "g1"},
		{ID: "d", Device: "d1", Slot: 0, Profit: 5, Group: "g1"},
	}}
	sol := Solve(inst)
	if sol.Profit != 5 || !reflect.DeepEqual(sol.IDs, []string{"b"}) {
		t.Fatalf("got %+v", sol)
	}
}

func randTwoStage(r *rand.Rand, nCand, nScen int) *TwoStageInstance {
	devs := []string{"d1", "d2"}
	groups := []string{"", "", "g1"}
	inst := &TwoStageInstance{Cooldown: r.Intn(2), Stage1End: 2 + r.Intn(2)}
	for i := 0; i < nCand; i++ {
		inst.Candidates = append(inst.Candidates, Candidate{
			ID:     string(rune('a' + i)),
			Device: devs[r.Intn(len(devs))],
			Slot:   r.Intn(6),
			Group:  groups[r.Intn(len(groups))],
		})
	}
	for s := 0; s < nScen; s++ {
		sc := Scenario{ID: string(rune('A' + s)), Profits: map[string]int{}, Observe: map[string]int{}}
		for _, c := range inst.Candidates {
			sc.Profits[c.ID] = r.Intn(15) - 7
			sc.Observe[c.ID] = r.Intn(3)
		}
		inst.Scenarios = append(inst.Scenarios, sc)
	}
	return inst
}

func TestTwoStageMatchesBrute(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for trial := 0; trial < 200; trial++ {
		inst := randTwoStage(r, 1+r.Intn(8), 1+r.Intn(6))
		st := SolveTwoStage(inst)
		if err := VerifyStrategy(inst, st); err != nil {
			t.Fatalf("trial %d: verify failed: %v", trial, err)
		}
		if brute := BruteTwoStage(inst); brute != st.Worst {
			t.Fatalf("trial %d: solver worst=%d brute worst=%d", trial, st.Worst, brute)
		}
		if bound := ClairvoyantBound(inst); st.Worst > bound {
			t.Fatalf("trial %d: worst=%d exceeds clairvoyant bound %d", trial, st.Worst, bound)
		}
	}
}

func TestTwoStageObservationPays(t *testing.T) {
	// Running probe in stage 1 distinguishes the scenarios, letting stage 2
	// pick the profitable experiment in each world.
	inst := &TwoStageInstance{
		Cooldown:  1,
		Stage1End: 1,
		Candidates: []Candidate{
			{ID: "probe", Device: "d1", Slot: 0},
			{ID: "x", Device: "d1", Slot: 2},
			{ID: "y", Device: "d2", Slot: 2},
		},
		Scenarios: []Scenario{
			{ID: "s1", Profits: map[string]int{"probe": 0, "x": 10, "y": -5}, Observe: map[string]int{"probe": 1}},
			{ID: "s2", Profits: map[string]int{"probe": 0, "x": -5, "y": 10}, Observe: map[string]int{"probe": 2}},
		},
	}
	st := SolveTwoStage(inst)
	if st.Worst != 10 || !reflect.DeepEqual(st.Stage1, []string{"probe"}) {
		t.Fatalf("got %+v", st)
	}
	if err := VerifyStrategy(inst, st); err != nil {
		t.Fatal(err)
	}
	// The two scenarios must land on different branches with different
	// stage-2 actions, each legal under cross-stage replay.
	if len(st.Stage2) != 2 {
		t.Fatalf("expected 2 observation branches, got %v", st.Stage2)
	}
	seen := map[string]bool{}
	for _, ids := range st.Stage2 {
		if len(ids) != 1 {
			t.Fatalf("expected singleton stage-2 sets, got %v", st.Stage2)
		}
		seen[ids[0]] = true
	}
	if !seen["x"] || !seen["y"] {
		t.Fatalf("expected adaptive branches x and y, got %v", st.Stage2)
	}
}

func TestTwoStageIndistinguishableScenarios(t *testing.T) {
	// Identical observation vectors force one shared stage-2 branch; the
	// strategy cannot tailor to the scenario it cannot see.
	inst := &TwoStageInstance{
		Cooldown:  0,
		Stage1End: 0,
		Candidates: []Candidate{
			{ID: "p", Device: "d1", Slot: 0},
			{ID: "x", Device: "d1", Slot: 1},
			{ID: "y", Device: "d1", Slot: 1},
		},
		Scenarios: []Scenario{
			{ID: "s1", Profits: map[string]int{"p": 0, "x": 10, "y": -5}, Observe: map[string]int{"p": 1}},
			{ID: "s2", Profits: map[string]int{"p": 0, "x": -5, "y": 10}, Observe: map[string]int{"p": 1}},
		},
	}
	st := SolveTwoStage(inst)
	if err := VerifyStrategy(inst, st); err != nil {
		t.Fatal(err)
	}
	// One observation branch only; best safe play is the empty stage-2 set
	// (worst 0), since x or y alone risks -5.
	if len(st.Stage2) != 1 {
		t.Fatalf("expected 1 branch, got %v", st.Stage2)
	}
	if st.Worst != 0 {
		t.Fatalf("expected worst 0, got %d (%+v)", st.Worst, st)
	}
	if bound := ClairvoyantBound(inst); bound != 10 {
		t.Fatalf("clairvoyant bound should be 10, got %d", bound)
	}
}

func TestVerifyStrategyRejectsTampering(t *testing.T) {
	inst := randTwoStage(rand.New(rand.NewSource(9)), 6, 4)
	st := SolveTwoStage(inst)
	if err := VerifyStrategy(inst, st); err != nil {
		t.Fatal(err)
	}
	// Dropping a reachable branch must be caught.
	for k := range st.Stage2 {
		bad := Strategy{Stage1: st.Stage1, Stage2: map[string][]string{}, Worst: st.Worst}
		for k2, v := range st.Stage2 {
			if k2 != k {
				bad.Stage2[k2] = v
			}
		}
		if err := VerifyStrategy(inst, bad); err == nil {
			t.Fatalf("missing branch %q not detected", k)
		}
		break
	}
	// Inflating the claimed worst value must be caught.
	bad := Strategy{Stage1: st.Stage1, Stage2: st.Stage2, Worst: st.Worst + 1}
	if err := VerifyStrategy(inst, bad); err == nil {
		t.Fatal("inflated worst value not detected")
	}
}
