package sched

import (
	"math"
	"reflect"
	"testing"

	"temporal/allen"
)

// Acceptance scenario:
//   - task A must end before task B (A before B)
//   - B and C partially overlap (B overlaps C)
//   - C must start before project day 5
func buildScenario(t *testing.T) (*Solver, *Result) {
	t.Helper()
	s := NewSolver()
	a := s.AddTask("A", 2, 2)
	b := s.AddTask("B", 3, 3)
	c := s.AddTask("C", 4, 4)
	s.AddRelation(a, b, allen.Before)
	s.AddRelation(b, c, allen.Overlaps)
	s.MustStartBefore(c, 5)
	res, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return s, res
}

func checkWindow(t *testing.T, name string, w Window, earliest, latest float64, eExcl, lExcl bool) {
	t.Helper()
	if w.Earliest != earliest || w.Latest != latest ||
		w.EarliestExclusive != eExcl || w.LatestExclusive != lExcl {
		t.Errorf("%s window = [%v%v, %v%v], want [%v%v, %v%v]",
			name, excl(w.EarliestExclusive), w.Earliest, w.Latest, excl(w.LatestExclusive),
			excl(eExcl), earliest, latest, excl(lExcl))
	}
}

func excl(b bool) string {
	if b {
		return "("
	}
	return "["
}

func TestAcceptanceWindows(t *testing.T) {
	_, res := buildScenario(t)

	// A: starts at 0 at the earliest, must end strictly before day 5.
	checkWindow(t, "A.start", res.Tasks[0].Start, 0, 3, false, true)
	checkWindow(t, "A.end", res.Tasks[0].End, 2, 5, false, true)
	// B: starts after A ends, must start before day 5.
	checkWindow(t, "B.start", res.Tasks[1].Start, 2, 5, true, true)
	checkWindow(t, "B.end", res.Tasks[1].End, 5, 8, true, true)
	// C: must start strictly before day 5.
	checkWindow(t, "C.start", res.Tasks[2].Start, 2, 5, true, true)
	checkWindow(t, "C.end", res.Tasks[2].End, 6, 9, true, true)
	// Project: earliest finish is day 6 (exclusive), no deadline.
	if res.Project.Earliest != 6 || !res.Project.EarliestExclusive {
		t.Errorf("project earliest = %v (excl=%v), want 6 (exclusive)",
			res.Project.Earliest, res.Project.EarliestExclusive)
	}
	if !math.IsInf(res.Project.Latest, 1) {
		t.Errorf("project latest = %v, want +Inf", res.Project.Latest)
	}
}

func TestAcceptanceCriticalPath(t *testing.T) {
	_, res := buildScenario(t)
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(res.CriticalPath, want) {
		t.Errorf("critical path = %v, want %v", res.CriticalPath, want)
	}
}

func TestAcceptancePropagatedRelation(t *testing.T) {
	s, _ := buildScenario(t)
	// Propagation must derive that A ends before C.
	if got := s.Network().Relation(0, 2); got != allen.Before {
		t.Errorf("A->C = %v, want b", got)
	}
}

func TestInconsistentDeadline(t *testing.T) {
	s := NewSolver()
	a := s.AddTask("A", 2, 2)
	b := s.AddTask("B", 3, 3)
	c := s.AddTask("C", 4, 4)
	s.AddRelation(a, b, allen.Before)
	s.AddRelation(b, c, allen.Overlaps)
	// C cannot start before day 2: it starts strictly after day 2.
	s.MustStartBefore(c, 2)
	if _, err := s.Solve(); err == nil {
		t.Fatal("expected inconsistency, got nil error")
	}
}

func TestCyclicBeforeInconsistent(t *testing.T) {
	s := NewSolver()
	a := s.AddTask("A", 1, 1)
	b := s.AddTask("B", 1, 1)
	c := s.AddTask("C", 1, 1)
	s.AddRelation(a, b, allen.Before)
	s.AddRelation(b, c, allen.Before)
	s.AddRelation(c, a, allen.Before)
	if _, err := s.Solve(); err == nil {
		t.Fatal("expected cyclic-before inconsistency, got nil error")
	}
}

func TestMeetsChain(t *testing.T) {
	s := NewSolver()
	a := s.AddTask("A", 2, 2)
	b := s.AddTask("B", 3, 3)
	s.AddRelation(a, b, allen.Meets)
	s.SetDeadline(10)
	res, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// B starts exactly when A ends.
	checkWindow(t, "B.start", res.Tasks[1].Start, 2, 7, false, false)
	checkWindow(t, "project", res.Project, 5, 10, false, false)
	want := []string{"A", "B"}
	if !reflect.DeepEqual(res.CriticalPath, want) {
		t.Errorf("critical path = %v, want %v", res.CriticalPath, want)
	}
}

func TestDurationRange(t *testing.T) {
	s := NewSolver()
	s.AddTask("A", 2, 5)
	s.MustStartBy(0, 0)
	res, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	checkWindow(t, "A.end", res.Tasks[0].End, 2, 5, false, false)
}
