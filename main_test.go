package main

import "testing"

func TestSelftestSmall(t *testing.T) {
	if err := selftest(40, 4, 5, 12345); err != nil {
		t.Fatal(err)
	}
}

func TestKnownCase(t *testing.T) {
	img, err := NewImage([]string{"0110", "1100", "0011", "0110"})
	if err != nil {
		t.Fatal(err)
	}
	sol := newSolver1(img).best(rect{0, 0, 4, 4}, 4)
	if sol.err != 4 || sol.leaves != 3 || sol.enc != "V01LV03LL" {
		t.Fatalf("got %+v", sol)
	}
}

func TestSharedTemplateBeatsNothing(t *testing.T) {
	// Two frames that disagree everywhere in one column: a shared tree must
	// still be found, and per-frame independent optima are not allowed.
	a, _ := NewImage([]string{"01", "01"})
	b, _ := NewImage([]string{"00", "11"})
	full := rect{0, 0, 2, 2}
	best, ok := pickBest2(newSolver2(a, b).front(full, 2), 2, 2, 2)
	if !ok {
		t.Fatal("expected feasible")
	}
	if best.errA+best.errB != 2 || best.leaves != 2 {
		t.Fatalf("got %+v", best)
	}
	// Infeasibility evidence path: caps below the frontier minimum.
	if _, ok := pickBest2(newSolver2(a, b).front(full, 1), 1, 0, 0); ok {
		t.Fatal("expected infeasible")
	}
}
