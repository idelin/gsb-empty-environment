package netsec

import (
	"strings"
	"testing"
	"time"
)

func inst(source, name, version, digest string) Instance {
	return Instance{Source: source, Name: name, Version: version, Digest: digest}
}

func mustWitness(t *testing.T, g *Graph) *Witness {
	t.Helper()
	w, err := g.FindConflictWitness()
	if err != nil {
		t.Fatalf("FindConflictWitness: %v", err)
	}
	if w == nil {
		t.Fatal("expected a witness, got nil")
	}
	return w
}

func mustNoWitness(t *testing.T, g *Graph) {
	t.Helper()
	w, err := g.FindConflictWitness()
	if err != nil {
		t.Fatalf("FindConflictWitness: %v", err)
	}
	if w != nil {
		t.Fatalf("expected no witness, got:\n%s", w)
	}
}

func TestDirectConflict(t *testing.T) {
	root := inst("main", "app", "1.0", "d0")
	a := inst("registry-a", "lib", "2.0", "aaa")
	b := inst("registry-b", "lib", "2.0", "bbb")
	g := &Graph{
		Roots: []Instance{root},
		Edges: map[Instance][]Edge{
			root: {{To: a}, {To: b}},
		},
	}
	w := mustWitness(t, g)
	if w.Assign != 0 {
		t.Fatalf("unexpected assign %b", w.Assign)
	}
	for _, p := range w.Paths {
		if len(p) != 2 || p[0] != root {
			t.Fatalf("unexpected path %v", p)
		}
	}
	if !w.Paths[0][1].ConflictsWith(w.Paths[1][1]) {
		t.Fatalf("witness endpoints do not conflict: %v vs %v", w.Paths[0][1], w.Paths[1][1])
	}
}

func TestMutexConflictNotReported(t *testing.T) {
	// Two conflicting instances of lib@2.0 are each reachable only under
	// a mutually exclusive variable, so no feasible build mixes them.
	root := inst("main", "app", "1.0", "d0")
	a := inst("registry-a", "lib", "2.0", "aaa")
	b := inst("registry-b", "lib", "2.0", "bbb")
	g := &Graph{
		NumVars: 2,
		Mutex:   [][]int{{0, 1}},
		Roots:   []Instance{root},
		Edges: map[Instance][]Edge{
			root: {{To: a, When: Var(0)}, {To: b, When: Var(1)}},
		},
	}
	mustNoWitness(t, g)

	// Dropping the mutex constraint makes the conflict real.
	g.Mutex = nil
	w := mustWitness(t, g)
	if w.Assign != 3 {
		t.Fatalf("expected assign with both vars set, got %b", w.Assign)
	}
}

func TestComplementaryConditionsNotReported(t *testing.T) {
	// a is reachable only when var0 holds, b only when it does not;
	// enumerating all assignments must not report a conflict.
	root := inst("main", "app", "1.0", "d0")
	a := inst("registry-a", "lib", "2.0", "aaa")
	b := inst("registry-b", "lib", "2.0", "bbb")
	g := &Graph{
		NumVars: 1,
		Roots:   []Instance{root},
		Edges: map[Instance][]Edge{
			root: {{To: a, When: Var(0)}, {To: b, When: Not(Var(0))}},
		},
	}
	mustNoWitness(t, g)
}

func TestOptionalEdgeTraversable(t *testing.T) {
	root := inst("main", "app", "1.0", "d0")
	a := inst("registry-a", "lib", "2.0", "aaa")
	b := inst("registry-b", "lib", "2.0", "bbb")
	g := &Graph{
		Roots: []Instance{root},
		Edges: map[Instance][]Edge{
			root: {{To: a}, {To: b, Optional: true}},
		},
	}
	mustWitness(t, g)
}

func TestShortestWitnessSelected(t *testing.T) {
	root := inst("main", "app", "1.0", "d0")
	near1 := inst("registry-a", "lib", "2.0", "aaa")
	near2 := inst("registry-b", "lib", "2.0", "bbb")
	mid1 := inst("main", "mid1", "1.0", "m1")
	mid2 := inst("main", "mid2", "1.0", "m2")
	far1 := inst("registry-a", "far", "3.0", "f1")
	far2 := inst("registry-b", "far", "3.0", "f2")
	g := &Graph{
		Roots: []Instance{root},
		Edges: map[Instance][]Edge{
			root: {{To: mid1}, {To: near1}, {To: near2}},
			mid1: {{To: mid2}},
			mid2: {{To: far1}, {To: far2}},
		},
	}
	w := mustWitness(t, g)
	total := len(w.Paths[0]) + len(w.Paths[1])
	if total != 4 { // root->near1 and root->near2
		t.Fatalf("expected shortest witness of total path length 4, got %d:\n%s", total, w)
	}
}

func TestCycleTerminates(t *testing.T) {
	a := inst("main", "a", "1.0", "da")
	b := inst("main", "b", "1.0", "db")
	g := &Graph{
		NumVars: 4,
		Roots:   []Instance{a},
		Edges: map[Instance][]Edge{
			a: {{To: b}},
			b: {{To: a}},
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mustNoWitness(t, g)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("FindConflictWitness did not terminate on a cyclic graph")
	}
}

func TestCycleWithConflict(t *testing.T) {
	a := inst("main", "a", "1.0", "da")
	b := inst("main", "b", "1.0", "db")
	x := inst("registry-a", "lib", "2.0", "aaa")
	y := inst("registry-b", "lib", "2.0", "bbb")
	g := &Graph{
		Roots: []Instance{a},
		Edges: map[Instance][]Edge{
			a: {{To: b}, {To: x}},
			b: {{To: a}, {To: y}},
		},
	}
	w := mustWitness(t, g)
	if !strings.Contains(w.String(), "lib@2.0") {
		t.Fatalf("witness does not reach conflicting lib instances:\n%s", w)
	}
}

func TestSameDigestNoConflict(t *testing.T) {
	// Identical instances reached via two paths are not a conflict.
	root := inst("main", "app", "1.0", "d0")
	mid1 := inst("main", "mid1", "1.0", "m1")
	mid2 := inst("main", "mid2", "1.0", "m2")
	shared := inst("registry-a", "lib", "2.0", "aaa")
	g := &Graph{
		Roots: []Instance{root},
		Edges: map[Instance][]Edge{
			root: {{To: mid1}, {To: mid2}},
			mid1: {{To: shared}},
			mid2: {{To: shared}},
		},
	}
	mustNoWitness(t, g)
}

func TestNumVarsOutOfRange(t *testing.T) {
	g := &Graph{NumVars: MaxVars + 1}
	if _, err := g.FindConflictWitness(); err == nil {
		t.Fatal("expected error for NumVars > MaxVars")
	}
}

func TestRootConflict(t *testing.T) {
	a := inst("registry-a", "lib", "2.0", "aaa")
	b := inst("registry-b", "lib", "2.0", "bbb")
	g := &Graph{Roots: []Instance{a, b}}
	w := mustWitness(t, g)
	if len(w.Paths[0]) != 1 || len(w.Paths[1]) != 1 {
		t.Fatalf("expected singleton paths, got:\n%s", w)
	}
}
