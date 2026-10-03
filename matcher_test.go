package matcher

import (
	"math"
	"math/rand"
	"testing"
)

func bruteForce(weight [][]float64, maskedW, maskedT []bool) float64 {
	workers := len(weight)
	tasks := 0
	if workers > 0 {
		tasks = len(weight[0])
	}
	best := math.Inf(-1)
	used := make([]bool, tasks)
	var dfs func(i int, acc float64)
	dfs = func(i int, acc float64) {
		if i == workers {
			if acc > best {
				best = acc
			}
			return
		}
		if maskedW[i] {
			dfs(i+1, acc)
			return
		}
		dfs(i+1, acc)
		for j := 0; j < tasks; j++ {
			if used[j] || maskedT[j] {
				continue
			}
			used[j] = true
			dfs(i+1, acc+weight[i][j])
			used[j] = false
		}
	}
	dfs(0, 0)
	return best
}

func makeMatcher(weight [][]float64) *Matcher {
	m := New(len(weight), len(weight[0]))
	for i := range weight {
		for j, w := range weight[i] {
			m.SetWeight(i, j, w)
		}
	}
	return m
}

func checkResult(t *testing.T, m *Matcher, weight [][]float64, maskedW, maskedT []bool) Result {
	t.Helper()
	res := m.Solve()
	want := bruteForce(weight, maskedW, maskedT)
	if res.Total != want {
		t.Fatalf("total = %v, want optimal %v (matches=%v)", res.Total, want, res.Matches)
	}
	seenW := map[int]bool{}
	seenT := map[int]bool{}
	for _, mt := range res.Matches {
		if seenW[mt.Worker] || seenT[mt.Task] {
			t.Fatalf("duplicate match at worker %d / task %d", mt.Worker, mt.Task)
		}
		seenW[mt.Worker] = true
		seenT[mt.Task] = true
		if maskedW[mt.Worker] || maskedT[mt.Task] {
			t.Fatalf("matched masked node: %+v", mt)
		}
		if mt.Weight != weight[mt.Worker][mt.Task] {
			t.Fatalf("match weight %v != edge weight %v", mt.Weight, weight[mt.Worker][mt.Task])
		}
	}
	return res
}

func TestClassic3x3(t *testing.T) {
	weight := [][]float64{
		{3, 5, 5},
		{2, 4, 8},
		{1, 2, 9},
	}
	m := makeMatcher(weight)
	res := checkResult(t, m, weight, make([]bool, 3), make([]bool, 3))
	if res.Total != 16 {
		t.Fatalf("total = %v, want 16", res.Total)
	}
	if len(res.Unmatched) != 0 {
		t.Fatalf("expected perfect matching, got unmatched %v", res.Unmatched)
	}
}

func TestRectangularMoreTasks(t *testing.T) {
	weight := [][]float64{
		{10, 1, 8, 3},
		{4, 9, 2, 7},
	}
	m := makeMatcher(weight)
	res := checkResult(t, m, weight, make([]bool, 2), make([]bool, 4))
	if len(res.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %v", res.Matches)
	}
	var unmatchedTasks int
	for _, u := range res.Unmatched {
		if u.Side != "task" {
			t.Fatalf("unexpected unmatched worker: %+v", u)
		}
		unmatchedTasks++
	}
	if unmatchedTasks != 2 {
		t.Fatalf("expected 2 unmatched tasks, got %d", unmatchedTasks)
	}
}

func TestRectangularMoreWorkers(t *testing.T) {
	weight := [][]float64{
		{6, 1},
		{8, 7},
		{9, 4},
		{2, 3},
	}
	m := makeMatcher(weight)
	res := checkResult(t, m, weight, make([]bool, 4), make([]bool, 2))
	if len(res.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %v", res.Matches)
	}
}

func TestMaskWorkerFastSwitch(t *testing.T) {
	weight := [][]float64{
		{100, 60, 10},
		{90, 55, 20},
		{80, 50, 30},
	}
	maskedW := make([]bool, 3)
	maskedT := make([]bool, 3)
	m := makeMatcher(weight)
	first := checkResult(t, m, weight, maskedW, maskedT)
	if first.Total != 100+55+30 {
		t.Fatalf("first total = %v, want 185", first.Total)
	}

	m.MaskWorker(0, true)
	maskedW[0] = true
	second := checkResult(t, m, weight, maskedW, maskedT)
	if second.Total != 90+50 {
		t.Fatalf("second total = %v, want 140", second.Total)
	}
	for _, mt := range second.Matches {
		if mt.Worker == 0 {
			t.Fatalf("masked worker 0 still matched: %+v", mt)
		}
	}

	m.MaskWorker(0, false)
	maskedW[0] = false
	third := checkResult(t, m, weight, maskedW, maskedT)
	if third.Total != first.Total {
		t.Fatalf("after unmask total = %v, want %v", third.Total, first.Total)
	}
}

func TestMaskTask(t *testing.T) {
	weight := [][]float64{
		{5, 9, 1},
		{3, 2, 8},
		{7, 4, 6},
	}
	maskedW := make([]bool, 3)
	maskedT := []bool{false, true, false}
	m := makeMatcher(weight)
	m.MaskTask(1, true)
	res := checkResult(t, m, weight, maskedW, maskedT)
	for _, mt := range res.Matches {
		if mt.Task == 1 {
			t.Fatalf("masked task 1 still matched: %+v", mt)
		}
	}
}

func TestSetWeightDynamic(t *testing.T) {
	weight := [][]float64{
		{10, 9},
		{9, 8},
	}
	m := makeMatcher(weight)
	maskedW := make([]bool, 2)
	maskedT := make([]bool, 2)
	if res := checkResult(t, m, weight, maskedW, maskedT); res.Total != 18 {
		t.Fatalf("total = %v, want 18", res.Total)
	}
	m.SetWeight(0, 1, 100)
	weight[0][1] = 100
	if res := checkResult(t, m, weight, maskedW, maskedT); res.Total != 109 {
		t.Fatalf("after SetWeight total = %v, want 109", res.Total)
	}
}

func TestOpportunityCost(t *testing.T) {
	weight := [][]float64{
		{10, 8},
		{9, 7},
		{6, 5},
	}
	m := makeMatcher(weight)
	res := checkResult(t, m, weight, make([]bool, 3), make([]bool, 2))
	if len(res.Unmatched) != 1 || res.Unmatched[0].Side != "worker" || res.Unmatched[0].Index != 2 {
		t.Fatalf("unmatched = %+v, want worker 2", res.Unmatched)
	}
	if res.Unmatched[0].OpportunityCost != 6 {
		t.Fatalf("opportunity cost = %v, want 6", res.Unmatched[0].OpportunityCost)
	}
	if res.MinOpportunityCost != 6 {
		t.Fatalf("min opportunity cost = %v, want 6", res.MinOpportunityCost)
	}
}

func TestLabelsFeasibleAfterSolve(t *testing.T) {
	weight := [][]float64{
		{4, 1, 3},
		{2, 0, 5},
		{3, 2, 2},
	}
	m := makeMatcher(weight)
	res := m.Solve()
	lx, ly := m.Labels()
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if lx[i]+ly[j] < weight[i][j]-1e-9 {
				t.Fatalf("infeasible label at (%d,%d): %v+%v < %v", i, j, lx[i], ly[j], weight[i][j])
			}
		}
	}
	var sum float64
	for i := 0; i < 3; i++ {
		sum += lx[i] + ly[i]
	}
	if math.Abs(sum-res.Total) > 1e-9 {
		t.Fatalf("dual sum %v != primal total %v", sum, res.Total)
	}
}

func TestFuzzAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		workers := 1 + rng.Intn(4)
		tasks := 1 + rng.Intn(4)
		weight := make([][]float64, workers)
		for i := range weight {
			weight[i] = make([]float64, tasks)
			for j := range weight[i] {
				weight[i][j] = float64(rng.Intn(21))
			}
		}
		maskedW := make([]bool, workers)
		maskedT := make([]bool, tasks)
		m := makeMatcher(weight)
		for step := 0; step < 4; step++ {
			switch rng.Intn(3) {
			case 0:
				i := rng.Intn(workers)
				j := rng.Intn(tasks)
				v := float64(rng.Intn(21))
				m.SetWeight(i, j, v)
				weight[i][j] = v
			case 1:
				i := rng.Intn(workers)
				maskedW[i] = !maskedW[i]
				m.MaskWorker(i, maskedW[i])
			case 2:
				j := rng.Intn(tasks)
				maskedT[j] = !maskedT[j]
				m.MaskTask(j, maskedT[j])
			}
			checkResult(t, m, weight, maskedW, maskedT)
		}
	}
}
