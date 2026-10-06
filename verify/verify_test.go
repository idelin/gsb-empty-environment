package verify

import (
	"testing"

	"groupopt"
)

func sampleInstance() groupopt.Instance {
	return groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 0, Group: 0, Cost: 2, Profit: 4},
			{ID: 1, Group: 0, Cost: 3, Profit: 5},
			{ID: 2, Group: 1, Cost: 1, Profit: 6, Requires: []int{0}},
		},
		SetupFee: map[int]int64{0: 3, 1: 2},
	}
}

func TestEvaluateSharedFeeAndClosure(t *testing.T) {
	inst := sampleInstance()
	// Fee of group 0 charged once for two options.
	cost, profit, ok := Evaluate(inst, map[int]bool{0: true, 1: true}, nil)
	if !ok || cost != 2+3+3 || profit != 9 {
		t.Fatalf("cost=%d profit=%d ok=%v", cost, profit, ok)
	}
	// Open set: option 2 without its requirement.
	if _, _, ok := Evaluate(inst, map[int]bool{2: true}, nil); ok {
		t.Fatal("open selection accepted")
	}
	// Sunk fee of a paid but unused group counts once.
	cost, _, ok = Evaluate(inst, map[int]bool{0: true}, map[int]bool{1: true})
	if !ok || cost != 2+3+2 {
		t.Fatalf("cost=%d, want 7", cost)
	}
}

func TestBruteForceGroundTruth(t *testing.T) {
	inst := sampleInstance()
	best, profit, cost, ok := BruteForce(inst, 6, nil, nil)
	if !ok {
		t.Fatal("no feasible selection")
	}
	// {0,2}: cost 2+1+3+2=8 too much; {0,1}: cost 2+3+3=8 too much;
	// {1}: cost 3+3=6 profit 5; {0}: cost 2+3=5 profit 4; {2,0} over budget.
	if profit != 5 || cost != 6 || len(best) != 1 || !best[1] {
		t.Fatalf("best=%v profit=%d cost=%d", best, profit, cost)
	}
}

func TestCheckResultRejectsTampering(t *testing.T) {
	inst := sampleInstance()
	s, err := groupopt.NewSession(inst, 6)
	if err != nil {
		t.Fatal(err)
	}
	res := s.Solve(0)
	if err := CheckResult(s.Instance(), s.State(), res); err != nil {
		t.Fatalf("honest result rejected: %v", err)
	}

	bad := res
	bad.Upper = res.Upper + 1
	if err := CheckResult(s.Instance(), s.State(), bad); err == nil {
		t.Fatal("inflated upper bound accepted")
	}
	bad = res
	bad.Lower = res.Lower + 1
	if err := CheckResult(s.Instance(), s.State(), bad); err == nil {
		t.Fatal("inflated lower bound accepted")
	}
	bad = res
	bad.Cost = res.Cost - 1
	if err := CheckResult(s.Instance(), s.State(), bad); err == nil {
		t.Fatal("understated cost accepted")
	}
	bad = res
	bad.Selection = append([]int(nil), res.Selection...)
	bad.Selection = append(bad.Selection, 2)
	if err := CheckResult(s.Instance(), s.State(), bad); err == nil {
		t.Fatal("dependency-open witness accepted")
	}
	bad = res
	bad.Epoch = res.Epoch + 1
	if err := CheckResult(s.Instance(), s.State(), bad); err == nil {
		t.Fatal("wrong-epoch result accepted")
	}
}
