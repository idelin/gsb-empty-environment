package tempsched

import (
	"math"
	"testing"
)

func almostEq(a, b float64) bool { return a == b || math.Abs(a-b) < 1e-9 }

func checkWindow(t *testing.T, res *Result, task string, es, ls, ee, le float64) {
	t.Helper()
	w, ok := res.Windows[task]
	if !ok {
		t.Fatalf("no window for task %s", task)
	}
	if !almostEq(w.EarliestStart, es) || !almostEq(w.LatestStart, ls) ||
		!almostEq(w.EarliestEnd, ee) || !almostEq(w.LatestEnd, le) {
		t.Errorf("task %s window = [%g,%g]/[%g,%g], want [%g,%g]/[%g,%g]",
			task, w.EarliestStart, w.LatestStart, w.EarliestEnd, w.LatestEnd, es, ls, ee, le)
	}
}

// Acceptance scenario: A must finish before B starts, B partially overlaps C,
// and C must start before day 5. The solver must propagate the relations and
// derive A's latest end (day 5) and the global critical-path window.
func TestAcceptanceScenario(t *testing.T) {
	s := NewSolver()
	s.AddTask("A", 2, 2)
	s.AddTask("B", 4, 4)
	s.AddTask("C", 3, 3)
	s.Constrain("A", "B", Before)
	s.Constrain("B", "C", Overlaps)
	s.ConstrainStartBefore("C", 5)

	res := s.Solve()
	if !res.Consistent {
		t.Fatal("scenario must be consistent")
	}

	// Overlaps(B,C) implies eB <= eC <= sC+3 <= 8, and B lasts 4 days,
	// so sB <= 4 and therefore A's latest end is day 4 (not 5): the
	// deadline on C propagates through both ends of B.
	checkWindow(t, res, "A", 0, 2, 2, 4)
	// B starts after A ends (>=2); sB <= eB-4 <= eC-4 <= 4.
	checkWindow(t, res, "B", 2, 4, 6, 8)
	// sC >= sB+1: eC = sC+3 >= eB >= 6 forces sC >= 3.
	checkWindow(t, res, "C", 3, 5, 6, 8)

	// Global critical-path window of the project.
	if !almostEq(res.EarliestFinish, 6) || !almostEq(res.LatestFinish, 8) {
		t.Errorf("project window = [%g,%g], want [6,8]", res.EarliestFinish, res.LatestFinish)
	}

	// Transitive closure should infer that A strictly precedes C.
	if got := res.Network.Relation("A", "C"); got != Before {
		t.Errorf("inferred A->C = %v, want Before", got)
	}
}

func TestDeadlineMakesScenarioInconsistent(t *testing.T) {
	s := NewSolver()
	s.AddTask("A", 2, 2)
	s.AddTask("B", 4, 4)
	s.Constrain("A", "B", Before)
	s.ConstrainEndBefore("B", 5) // eB >= eA+4 >= 6 > 5: infeasible

	if res := s.Solve(); res.Consistent {
		t.Fatal("tight deadline must make the scenario inconsistent")
	}
}

func TestMeetsAndDurationRange(t *testing.T) {
	s := NewSolver()
	s.AddTask("A", 1, 3)
	s.AddTask("B", 2, 2)
	s.Constrain("A", "B", Meets) // B starts exactly when A ends

	res := s.Solve()
	if !res.Consistent {
		t.Fatal("scenario must be consistent")
	}
	checkWindow(t, res, "A", 0, math.Inf(1), 1, math.Inf(1))
	checkWindow(t, res, "B", 1, math.Inf(1), 3, math.Inf(1))
}

func TestEqualRelation(t *testing.T) {
	s := NewSolver()
	s.AddTask("A", 2, 2)
	s.AddTask("B", 2, 2)
	s.Constrain("A", "B", Equal)
	s.ConstrainStartBefore("A", 4)

	res := s.Solve()
	if !res.Consistent {
		t.Fatal("scenario must be consistent")
	}
	checkWindow(t, res, "B", 0, 4, 2, 6)
}
