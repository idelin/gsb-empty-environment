package groupopt_test

import (
	"math/rand"
	"testing"
	"time"

	"groupopt"
	"groupopt/verify"
)

func mustSession(t *testing.T, inst groupopt.Instance, budget int64) *groupopt.Session {
	t.Helper()
	s, err := groupopt.NewSession(inst, budget)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s
}

func mustVerify(t *testing.T, s *groupopt.Session, res groupopt.Result) {
	t.Helper()
	if err := verify.CheckResult(s.Instance(), s.State(), res); err != nil {
		t.Fatalf("verify.CheckResult: %v", err)
	}
}

func hasID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// The one-time group fee must be charged exactly once even when several
// options of the group are selected.
func TestSharedFeeChargedOnce(t *testing.T) {
	inst := groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 0, Group: 0, Cost: 1, Profit: 5},
			{ID: 1, Group: 0, Cost: 1, Profit: 5},
		},
		SetupFee: map[int]int64{0: 10},
	}
	s := mustSession(t, inst, 12)
	res := s.Solve(0)
	if res.Status != groupopt.Exact {
		t.Fatalf("status = %v", res.Status)
	}
	// Both options fit only if the fee is paid once: 1+1+10 = 12.
	if len(res.Selection) != 2 {
		t.Fatalf("selection = %v, want both options", res.Selection)
	}
	if res.Cost != 12 || res.Lower != 10 {
		t.Fatalf("cost=%d profit=%d, want 12/10", res.Cost, res.Lower)
	}
	mustVerify(t, s, res)
}

// A selection is only feasible when closed under Requires.
func TestDependencyClosure(t *testing.T) {
	inst := groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 0, Group: 0, Cost: 1, Profit: 10, Requires: []int{1}},
			{ID: 1, Group: 0, Cost: 1, Profit: 0},
		},
		SetupFee: map[int]int64{0: 0},
	}
	// Budget 1: option 0 cannot be taken without option 1, so nothing fits.
	s := mustSession(t, inst, 1)
	res := s.Solve(0)
	if len(res.Selection) != 0 || res.Lower != 0 {
		t.Fatalf("selection = %v profit %d, want empty/0", res.Selection, res.Lower)
	}
	mustVerify(t, s, res)
	// Budget 2: both are taken together.
	s2 := mustSession(t, inst, 2)
	res2 := s2.Solve(0)
	if len(res2.Selection) != 2 || res2.Lower != 10 {
		t.Fatalf("selection = %v profit %d, want {0,1}/10", res2.Selection, res2.Lower)
	}
	mustVerify(t, s2, res2)
}

// Equal profits: cheaper total cost wins; still tied: smaller id set wins.
func TestTieBreak(t *testing.T) {
	inst := groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 0, Group: 0, Cost: 2, Profit: 5},
			{ID: 1, Group: 0, Cost: 1, Profit: 5},
		},
		SetupFee: map[int]int64{0: 0},
	}
	s := mustSession(t, inst, 2)
	res := s.Solve(0)
	if len(res.Selection) != 1 || res.Selection[0] != 1 {
		t.Fatalf("selection = %v, want [1] (cheaper)", res.Selection)
	}
	mustVerify(t, s, res)

	inst2 := groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 7, Group: 0, Cost: 1, Profit: 5},
			{ID: 3, Group: 0, Cost: 1, Profit: 5},
		},
		SetupFee: map[int]int64{0: 0},
	}
	s2 := mustSession(t, inst2, 1)
	res2 := s2.Solve(0)
	if len(res2.Selection) != 1 || res2.Selection[0] != 3 {
		t.Fatalf("selection = %v, want [3] (smaller id set)", res2.Selection)
	}
	mustVerify(t, s2, res2)
}

// Exhaustive cross-check of the branch-and-bound solver against subset
// enumeration on randomized instances, including random commitments and
// sunk setup fees.
func TestSolverMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		n := 1 + r.Intn(12)
		groups := 1 + r.Intn(3)
		opts := make([]groupopt.Option, n)
		for i := 0; i < n; i++ {
			o := groupopt.Option{
				ID:     i,
				Group:  r.Intn(groups),
				Cost:   int64(r.Intn(10)),
				Profit: int64(r.Intn(21) - 5),
			}
			for j := 0; j < n; j++ {
				if j != i && r.Intn(12) == 0 {
					o.Requires = append(o.Requires, j)
				}
			}
			opts[i] = o
		}
		used := make(map[int]bool)
		for _, o := range opts {
			used[o.Group] = true
		}
		usedGroups := make([]int, 0, len(used))
		for g := range used {
			usedGroups = append(usedGroups, g)
		}
		fees := make(map[int]int64)
		for _, g := range usedGroups {
			fees[g] = int64(r.Intn(15))
		}
		inst := groupopt.Instance{Options: opts, SetupFee: fees}
		budget := int64(r.Intn(40))
		s, err := groupopt.NewSession(inst, budget)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		// Random committed options and paid groups.
		for i := 0; i < n; i++ {
			if r.Intn(8) == 0 {
				if err := s.CommitOption(i); err != nil {
					t.Fatalf("trial %d: %v", trial, err)
				}
			}
		}
		for _, g := range usedGroups {
			if r.Intn(4) == 0 {
				if err := s.ExecuteSetup(g); err != nil {
					t.Fatalf("trial %d: %v", trial, err)
				}
			}
		}
		res := s.Solve(0)
		st := s.State()
		if err := verify.CheckResult(s.Instance(), st, res); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		committed := make(map[int]bool)
		for _, id := range st.Committed {
			committed[id] = true
		}
		paid := make(map[int]bool)
		for _, g := range st.PaidGroups {
			paid[g] = true
		}
		best, bp, bc, ok := verify.BruteForce(s.Instance(), st.Budget, committed, paid)
		if res.Status == groupopt.Infeasible {
			if ok {
				t.Fatalf("trial %d: solver says infeasible, enumeration found %v", trial, best)
			}
			continue
		}
		if !ok {
			t.Fatalf("trial %d: enumeration infeasible, solver returned %v", trial, res.Selection)
		}
		if res.Lower != bp || res.Cost != bc {
			t.Fatalf("trial %d: solver %d/%d, enumeration %d/%d", trial, res.Lower, res.Cost, bp, bc)
		}
		if len(res.Selection) != len(best) {
			t.Fatalf("trial %d: selection %v, enumeration %v", trial, res.Selection, best)
		}
		for _, id := range res.Selection {
			if !best[id] {
				t.Fatalf("trial %d: selection %v, enumeration %v", trial, res.Selection, best)
			}
		}
	}
}

// Committed options and sunk setup fees must survive checkpoint revisions
// of profits and budget; old certificates must stop verifying.
func TestCheckpointRevision(t *testing.T) {
	inst := groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 0, Group: 0, Cost: 4, Profit: 9},
			{ID: 1, Group: 0, Cost: 3, Profit: 6},
			{ID: 2, Group: 1, Cost: 5, Profit: 12, Requires: []int{3}},
			{ID: 3, Group: 1, Cost: 2, Profit: 1},
			{ID: 4, Group: 2, Cost: 1, Profit: 2},
		},
		SetupFee: map[int]int64{0: 2, 1: 4, 2: 1},
	}
	s := mustSession(t, inst, 20)
	res1 := s.Solve(0)
	if res1.Status != groupopt.Exact {
		t.Fatalf("status = %v", res1.Status)
	}
	mustVerify(t, s, res1)
	epoch1 := s.State().Epoch
	staleInst := s.Instance()
	staleState := s.State()

	// Commit an option the optimum did not pick, and pay a setup.
	pick := -1
	for _, o := range inst.Options {
		if !hasID(res1.Selection, o.ID) {
			pick = o.ID
			break
		}
	}
	if pick < 0 {
		t.Fatal("expected an unselected option")
	}
	if err := s.CommitOption(pick); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecuteSetup(2); err != nil {
		t.Fatal(err)
	}
	if err := s.Revise(15, map[int]int64{0: 20, 1: 1}); err != nil {
		t.Fatal(err)
	}

	res2 := s.Solve(0)
	if res2.Status != groupopt.Exact {
		t.Fatalf("status = %v", res2.Status)
	}
	mustVerify(t, s, res2)
	if !hasID(res2.Selection, pick) {
		t.Fatalf("committed option %d missing from %v", pick, res2.Selection)
	}
	// The committed option's requirements must be dragged in as well.
	closure, ok := closureOf(s.Instance(), res2.Selection)
	if !ok || !closure {
		t.Fatalf("selection %v not dependency-closed", res2.Selection)
	}
	// Sunk fee of group 2 counts even if group 2 is unused.
	if !hasID(res2.Selection, 4) {
		cost, _, ok := verify.Evaluate(s.Instance(), idSet(res2.Selection), map[int]bool{2: true})
		if !ok || cost != res2.Cost {
			t.Fatalf("sunk setup fee not accounted: cost=%d res.Cost=%d", cost, res2.Cost)
		}
	}

	// The epoch-1 certificate must no longer verify against the revised
	// state: stale optimality proofs are rejected.
	if err := verify.CheckResult(s.Instance(), s.State(), res1); err == nil {
		t.Fatal("stale certificate accepted after revision")
	}
	if err := verify.CheckResult(staleInst, staleState, res1); err != nil {
		t.Fatalf("epoch-%d proof should still verify against epoch-%d state: %v", epoch1, epoch1, err)
	}
}

func idSet(ids []int) map[int]bool {
	out := make(map[int]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func closureOf(inst groupopt.Instance, ids []int) (bool, bool) {
	_, _, ok := verify.Evaluate(inst, idSet(ids), nil)
	return ok, ok
}

// A paid setup fee is sunk: it counts against the budget even when no
// option of that group ends up selected, and is charged only once when
// the group is used.
func TestSunkSetupFee(t *testing.T) {
	inst := groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 0, Group: 0, Cost: 6, Profit: 10},
			{ID: 1, Group: 1, Cost: 5, Profit: 0}, // unattractive
		},
		SetupFee: map[int]int64{0: 2, 1: 3},
	}
	s := mustSession(t, inst, 11)
	if err := s.ExecuteSetup(1); err != nil {
		t.Fatal(err)
	}
	res := s.Solve(0)
	// Committed nothing, but fee 3 of group 1 is sunk: taking option 0
	// costs 6+2+3 = 11, exactly the budget.
	if !hasID(res.Selection, 0) || res.Cost != 11 {
		t.Fatalf("selection=%v cost=%d, want option 0 at cost 11", res.Selection, res.Cost)
	}
	mustVerify(t, s, res)

	// Tighter budget: the sunk fee makes option 0 unaffordable.
	s2 := mustSession(t, inst, 10)
	if err := s2.ExecuteSetup(1); err != nil {
		t.Fatal(err)
	}
	res2 := s2.Solve(0)
	if len(res2.Selection) != 0 || res2.Cost != 3 {
		t.Fatalf("selection=%v cost=%d, want empty at sunk cost 3", res2.Selection, res2.Cost)
	}
	mustVerify(t, s2, res2)
}

// A truncated run must return certified bounds; resuming must converge to
// the exact optimum; every intermediate result must verify.
func TestDeadlineResume(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	n := 20
	opts := make([]groupopt.Option, n)
	for i := 0; i < n; i++ {
		cost := int64(3 + r.Intn(12))
		opts[i] = groupopt.Option{
			ID:     i,
			Group:  i % 4,
			Cost:   cost,
			Profit: 2*cost + int64(r.Intn(5)) - 2,
		}
		if i > 0 && r.Intn(5) == 0 {
			opts[i].Requires = []int{r.Intn(i)}
		}
	}
	inst := groupopt.Instance{
		Options:  opts,
		SetupFee: map[int]int64{0: 5, 1: 8, 2: 3, 3: 11},
	}
	s := mustSession(t, inst, 60)

	res := s.Solve(time.Nanosecond)
	if res.Status != groupopt.Truncated {
		t.Fatalf("status = %v, want Truncated", res.Status)
	}
	if res.Lower > res.Upper {
		t.Fatalf("lower %d > upper %d", res.Lower, res.Upper)
	}
	mustVerify(t, s, res)

	for i := 0; i < 100 && res.Status == groupopt.Truncated; i++ {
		res = s.Solve(50 * time.Millisecond)
		mustVerify(t, s, res)
	}
	if res.Status != groupopt.Exact {
		t.Fatalf("search did not converge: %v", res.Status)
	}
	if res.Lower != res.Upper {
		t.Fatalf("exact result with lower %d != upper %d", res.Lower, res.Upper)
	}
	best, bp, _, ok := verify.BruteForce(s.Instance(), s.State().Budget, nil, nil)
	if !ok || bp != res.Lower {
		t.Fatalf("optimum %d (%v), solver claims %d", bp, best, res.Lower)
	}
}

// When the committed closure alone exceeds the budget the instance is
// infeasible, and the verifier confirms it by enumeration.
func TestInfeasible(t *testing.T) {
	inst := groupopt.Instance{
		Options: []groupopt.Option{
			{ID: 0, Group: 0, Cost: 5, Profit: 1},
			{ID: 1, Group: 0, Cost: 5, Profit: 1},
		},
		SetupFee: map[int]int64{0: 1},
	}
	s := mustSession(t, inst, 5)
	if err := s.CommitOption(0); err != nil {
		t.Fatal(err)
	}
	res := s.Solve(0)
	if res.Status != groupopt.Infeasible {
		t.Fatalf("status = %v, want Infeasible", res.Status)
	}
	mustVerify(t, s, res)
}
