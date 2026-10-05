package match

import (
	"math/rand"
	"reflect"
	"testing"
)

func ids(lo, hi int64) []int64 {
	var out []int64
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}

func equalIDs(a, b []int64) bool {
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

func checkAgainstBrute(t *testing.T, tasks, employees []int64, edges []Edge) Result {
	t.Helper()
	res := NewSolver(tasks, employees, edges).Solve()
	want := BruteSolve(tasks, employees, edges)
	if res.NumAssigned != want.NumAssigned ||
		res.TotalProfit != want.TotalProfit ||
		!equalIDs(res.EmployeeByTask, want.EmployeeByTask) {
		t.Fatalf("mismatch for tasks=%v employees=%v edges=%v\ngot:  %+v\nwant: %+v",
			tasks, employees, edges, res, want)
	}
	if err := Verify(tasks, employees, edges, &res); err != nil {
		t.Fatalf("certificate rejected for tasks=%v employees=%v edges=%v: %v", tasks, employees, edges, err)
	}
	return res
}

func TestIsolatedVertices(t *testing.T) {
	res := checkAgainstBrute(t,
		ids(1, 3), ids(1, 2),
		[]Edge{{Task: 1, Employee: 1, Profit: 5}})
	if res.NumAssigned != 1 {
		t.Fatalf("want 1 assignment, got %d", res.NumAssigned)
	}
	// tasks 2,3 and employee 2 are isolated -> sentinel entries.
	if res.EmployeeByTask[1] != res.Sentinel || res.EmployeeByTask[2] != res.Sentinel {
		t.Fatalf("isolated tasks must map to sentinel: %v", res.EmployeeByTask)
	}
	if res.Sentinel <= 3 {
		t.Fatalf("sentinel %d must exceed every ID", res.Sentinel)
	}
}

func TestAllNegativeProfitsStillAssigned(t *testing.T) {
	// Dropping negative edges would give cardinality 0; the first
	// objective forces both tasks to be assigned at profit -8.
	res := checkAgainstBrute(t,
		ids(1, 2), ids(1, 2),
		[]Edge{
			{Task: 1, Employee: 1, Profit: -5},
			{Task: 1, Employee: 2, Profit: -1},
			{Task: 2, Employee: 2, Profit: -3},
		})
	if res.NumAssigned != 2 || res.TotalProfit != -8 {
		t.Fatalf("negative edges wrongly discarded: %+v", res)
	}
}

func TestMultipleEquivalentOptimaLexSmallest(t *testing.T) {
	// All profits equal: every perfect matching is (card, profit)-
	// optimal; the lexicographically smallest employee list wins.
	res := checkAgainstBrute(t,
		ids(1, 2), ids(1, 3),
		[]Edge{
			{Task: 1, Employee: 1, Profit: 7},
			{Task: 1, Employee: 2, Profit: 7},
			{Task: 1, Employee: 3, Profit: 7},
			{Task: 2, Employee: 1, Profit: 7},
			{Task: 2, Employee: 2, Profit: 7},
			{Task: 2, Employee: 3, Profit: 7},
		})
	if !reflect.DeepEqual(res.EmployeeByTask, []int64{1, 2}) {
		t.Fatalf("lex-minimum not chosen: %v", res.EmployeeByTask)
	}
}

func TestLexPrefersAssigningEarlyTask(t *testing.T) {
	// Two (card=1, profit=5) optima: assign task 1 or task 2.
	// employeeByTask [1,S] beats [S,1] because 1 < S.
	res := checkAgainstBrute(t,
		ids(1, 2), ids(1, 1),
		[]Edge{
			{Task: 1, Employee: 1, Profit: 5},
			{Task: 2, Employee: 1, Profit: 5},
		})
	if !reflect.DeepEqual(res.EmployeeByTask, []int64{1, res.Sentinel}) {
		t.Fatalf("lex-minimum not chosen: %v", res.EmployeeByTask)
	}
}

func TestDuplicateEdgesKeepMaxProfit(t *testing.T) {
	res := checkAgainstBrute(t,
		ids(1, 1), ids(1, 2),
		[]Edge{
			{Task: 1, Employee: 1, Profit: 3},
			{Task: 1, Employee: 1, Profit: 9}, // duplicate, higher profit wins
			{Task: 1, Employee: 1, Profit: -4},
			{Task: 1, Employee: 2, Profit: 9},
		})
	if res.TotalProfit != 9 || res.EmployeeByTask[0] != 1 {
		t.Fatalf("duplicate resolution wrong: %+v", res)
	}
}

func TestEmptyAndDegenerate(t *testing.T) {
	checkAgainstBrute(t, nil, nil, nil)
	checkAgainstBrute(t, ids(1, 3), ids(1, 2), nil)
	checkAgainstBrute(t, nil, ids(1, 2), []Edge{{Task: 1, Employee: 1, Profit: 5}}) // edge to unknown IDs ignored
}

func TestRemoveCurrentlyMatchedEdge(t *testing.T) {
	tasks, employees := ids(1, 2), ids(1, 2)
	edges := []Edge{
		{Task: 1, Employee: 1, Profit: 5},
		{Task: 1, Employee: 2, Profit: 4},
		{Task: 2, Employee: 2, Profit: 3},
	}
	s := NewSolver(tasks, employees, edges)
	before := s.Solve()
	if !reflect.DeepEqual(before.EmployeeByTask, []int64{1, 2}) {
		t.Fatalf("unexpected initial matching: %+v", before)
	}
	// Delete an edge of the current matching; the solver must fall
	// back to the best remaining plan: task1->emp1, task2 unassigned.
	s.RemoveEdge(2, 2)
	after := s.Solve()
	if after.NumAssigned != 1 || after.TotalProfit != 5 ||
		!reflect.DeepEqual(after.EmployeeByTask, []int64{1, after.Sentinel}) {
		t.Fatalf("unexpected result after removal: %+v", after)
	}
	// Must agree with a fresh solve and with brute force.
	fresh := NewSolver(tasks, employees, []Edge{
		{Task: 1, Employee: 1, Profit: 5},
		{Task: 1, Employee: 2, Profit: 4},
	}).Solve()
	if !reflect.DeepEqual(after.EmployeeByTask, fresh.EmployeeByTask) ||
		after.TotalProfit != fresh.TotalProfit {
		t.Fatalf("incremental update disagrees with fresh solve")
	}
	checkAgainstBrute(t, tasks, employees, []Edge{
		{Task: 1, Employee: 1, Profit: 5},
		{Task: 1, Employee: 2, Profit: 4},
	})
}

func TestRandomAgainstBruteUpTo7x7(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		nt := rng.Intn(8) // 0..7 tasks
		ne := rng.Intn(8) // 0..7 employees
		tasks := ids(1, int64(nt))
		employees := ids(1, int64(ne))
		var edges []Edge
		for _, ta := range tasks {
			for _, em := range employees {
				if rng.Float64() < 0.25+0.5*rng.Float64() {
					p := int64(rng.Intn(19) - 9) // profits in [-9, 9]
					edges = append(edges, Edge{Task: ta, Employee: em, Profit: p})
					if rng.Float64() < 0.1 { // inject conflicting duplicates
						edges = append(edges, Edge{Task: ta, Employee: em, Profit: int64(rng.Intn(19) - 9)})
					}
				}
			}
		}
		checkAgainstBrute(t, tasks, employees, edges)
	}
}

func TestUpdatesConsistentWithFreshSolve(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	tasks, employees := ids(1, 6), ids(1, 6)
	current := make(map[[2]int64]int64)
	var edges []Edge
	for _, ta := range tasks {
		for _, em := range employees {
			if rng.Float64() < 0.5 {
				p := int64(rng.Intn(21) - 10)
				current[[2]int64{ta, em}] = p
				edges = append(edges, Edge{Task: ta, Employee: em, Profit: p})
			}
		}
	}
	s := NewSolver(tasks, employees, edges)
	edgeList := func() []Edge {
		out := make([]Edge, 0, len(current))
		for k, p := range current {
			out = append(out, Edge{Task: k[0], Employee: k[1], Profit: p})
		}
		return out
	}
	for step := 0; step < 60; step++ {
		// Half of the removals target an edge of the current matching.
		if rng.Float64() < 0.5 && len(current) > 0 {
			var victim Edge
			res := s.Solve()
			if len(res.Assignment) > 0 && rng.Float64() < 0.5 {
				victim = res.Assignment[rng.Intn(len(res.Assignment))]
			} else {
				i := rng.Intn(len(current))
				for k := range current {
					if i == 0 {
						victim = Edge{Task: k[0], Employee: k[1]}
						break
					}
					i--
				}
			}
			s.RemoveEdge(victim.Task, victim.Employee)
			delete(current, [2]int64{victim.Task, victim.Employee})
		} else {
			ta := tasks[rng.Intn(len(tasks))]
			em := employees[rng.Intn(len(employees))]
			p := int64(rng.Intn(21) - 10)
			s.AddEdge(ta, em, p)
			k := [2]int64{ta, em}
			if old, ok := current[k]; !ok || p > old {
				current[k] = p
			}
		}
		got := s.Solve()
		want := NewSolver(tasks, employees, edgeList()).Solve()
		if got.NumAssigned != want.NumAssigned ||
			got.TotalProfit != want.TotalProfit ||
			!equalIDs(got.EmployeeByTask, want.EmployeeByTask) {
			t.Fatalf("step %d: incremental result disagrees with fresh solve\ngot:  %+v\nwant: %+v", step, got, want)
		}
		if err := Verify(tasks, employees, edgeList(), &got); err != nil {
			t.Fatalf("step %d: certificate rejected: %v", step, err)
		}
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	tasks, employees := ids(1, 2), ids(1, 2)
	edges := []Edge{
		{Task: 1, Employee: 1, Profit: 5},
		{Task: 1, Employee: 2, Profit: 4},
		{Task: 2, Employee: 2, Profit: 3},
	}
	good := NewSolver(tasks, employees, edges).Solve()
	if err := Verify(tasks, employees, edges, &good); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}

	// Inflated profit claim.
	bad := good
	bad.TotalProfit++
	if err := Verify(tasks, employees, edges, &bad); err == nil {
		t.Fatal("tampered totalProfit accepted")
	}

	// Vertex cover with a vertex removed: edge (1,2) becomes uncovered
	// only if the cover relies on task 1 or employee 2; drop whichever
	// cover vertex covers it.
	bad = good
	covT := append([]int64(nil), good.Certificate.VertexCoverTasks...)
	covE := append([]int64(nil), good.Certificate.VertexCoverEmployees...)
	inT := map[int64]bool{}
	for _, v := range covT {
		inT[v] = true
	}
	inE := map[int64]bool{}
	for _, v := range covE {
		inE[v] = true
	}
	switch {
	case inT[1]:
		bad.Certificate.VertexCoverTasks = removeID(covT, 1)
	case inE[2]:
		bad.Certificate.VertexCoverEmployees = removeID(covE, 2)
	default:
		t.Fatalf("test assumption broken: cover=%v %v", covT, covE)
	}
	if err := Verify(tasks, employees, edges, &bad); err == nil {
		t.Fatal("broken vertex cover accepted")
	}

	// Corrupted potential: task 1 is matched, so its backward arc to
	// the source is residual; raising the source potential breaks it.
	bad = good
	bad.Certificate.SourcePotential += 1000
	if err := Verify(tasks, employees, edges, &bad); err == nil {
		t.Fatal("corrupted potentials accepted")
	}

	// Brute-force result has no certificate and must be rejected.
	bf := BruteSolve(tasks, employees, edges)
	if err := Verify(tasks, employees, edges, &bf); err == nil {
		t.Fatal("certificate-less result accepted")
	}
}

func removeID(xs []int64, v int64) []int64 {
	out := xs[:0]
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
