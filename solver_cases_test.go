package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func TestSharedPreconditionCountedOnce(t *testing.T) {
	// Two OR branches both rely on the same prerequisite root. Cutting the
	// shared root with one cheap patch must win over any per-branch set.
	p := &Problem{
		Nodes: []Node{
			{ID: "root", Gate: GateAND},
			{ID: "a", Gate: GateAND, Preconditions: []string{"root"}},
			{ID: "b", Gate: GateAND, Preconditions: []string{"root"}},
			{ID: "target", Gate: GateOR, Preconditions: []string{"a", "b"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "p-root", Action: "root", Cost: 5},
			{ID: "p-a", Action: "a", Cost: 6},
			{ID: "p-b", Action: "b", Cost: 6},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"p-root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	validateSolution(t, p, s)
}

func TestCyclesDoNotActivateWithoutSeed(t *testing.T) {
	p := &Problem{
		Nodes: []Node{
			{ID: "x", Gate: GateAND, Preconditions: []string{"y"}},
			{ID: "y", Gate: GateAND, Preconditions: []string{"x"}},
			{ID: "target", Gate: GateOR, Preconditions: []string{"x"}},
		},
		Target:  "target",
		Patches: []Patch{{ID: "p", Action: "x", Cost: 1}},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if len(s.Selected) != 0 {
		t.Fatalf("selected %v, want empty set for seedless cycle", s.Selected)
	}
}

func TestValidationErrors(t *testing.T) {
	base := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "target", Gate: GateAND, Preconditions: []string{"a"}},
		},
		Target:  "target",
		Patches: []Patch{{ID: "p", Action: "a", Cost: 1}},
	}

	if _, err := Solve(&Problem{Target: "t"}); !errors.Is(err, ErrNoNodes) {
		t.Fatalf("empty nodes: %v", err)
	}

	missingTarget := *base
	missingTarget.Target = "ghost"
	if _, err := Solve(&missingTarget); !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("unknown target: %v", err)
	}

	badPre := *base
	badPre.Nodes = []Node{
		{ID: "a", Gate: GateAND},
		{ID: "target", Gate: GateAND, Preconditions: []string{"nope"}},
	}
	if _, err := Solve(&badPre); err == nil {
		t.Fatal("expected dangling precondition error")
	}

	badPatch := *base
	badPatch.Patches = []Patch{{ID: "p", Action: "ghost", Cost: 1}}
	if _, err := Solve(&badPatch); err == nil {
		t.Fatal("expected unknown patch action error")
	}

	negCost := *base
	negCost.Patches = []Patch{{ID: "p", Action: "a", Cost: -1}}
	if _, err := Solve(&negCost); err == nil {
		t.Fatal("expected negative cost error")
	}

	tooMany := *base
	tooMany.Patches = nil
	for i := 0; i < 21; i++ {
		tooMany.Patches = append(tooMany.Patches, Patch{ID: "p", Action: "a", Cost: 1})
	}
	if _, err := Solve(&tooMany); !errors.Is(err, ErrTooManyPatches) {
		t.Fatalf("too many patches: %v", err)
	}
}

func TestReachabilityHelper(t *testing.T) {
	p := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "b", Gate: GateAND, Preconditions: []string{"a"}},
			{ID: "target", Gate: GateOR, Preconditions: []string{"b"}},
		},
		Target: "target",
	}
	res, err := Reachability(p, nil)
	if err != nil {
		t.Fatalf("reachability: %v", err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable with no disabled action")
	}
	if got, want := res.ActivationOrder, []string{"a", "b", "target"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("activation %v, want %v", got, want)
	}

	blocked, err := Reachability(p, []string{"b"})
	if err != nil {
		t.Fatalf("blocked reachability: %v", err)
	}
	if blocked.Reachable {
		t.Fatal("target should be unreachable with b disabled")
	}
}

func TestZeroCostPatches(t *testing.T) {
	p := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "target", Gate: GateAND, Preconditions: []string{"a"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "free", Action: "a", Cost: 0},
			{ID: "paid", Action: "target", Cost: 1},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"free"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	if s.Cost != 0 {
		t.Fatalf("cost = %d, want 0", s.Cost)
	}
	validateSolution(t, p, s)
}

func TestTargetInitiallyUnreachable(t *testing.T) {
	p := &Problem{
		Nodes: []Node{
			{ID: "seed", Gate: GateAND},
			// Empty OR gate never activates.
			{ID: "target", Gate: GateOR},
		},
		Target:  "target",
		Patches: []Patch{{ID: "p", Action: "seed", Cost: 1}},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if len(s.Selected) != 0 {
		t.Fatalf("selected %v, want empty set", s.Selected)
	}
	if s.Cost != 0 || len(s.Proof.Minimality) != 0 {
		t.Fatalf("empty-set solution malformed: %+v", s)
	}
	if got, want := s.Proof.Blocking.ReachableClosure, []string{"seed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("closure = %v, want %v", got, want)
	}
}

func TestUnblockable(t *testing.T) {
	p := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "other", Gate: GateAND},
			{ID: "target", Gate: GateAND, Preconditions: []string{"a"}},
		},
		Target:  "target",
		Patches: []Patch{{ID: "p", Action: "other", Cost: 1}},
	}
	if _, err := Solve(p); !errors.Is(err, ErrUnblockable) {
		t.Fatalf("err = %v, want ErrUnblockable", err)
	}
}

func TestDuplicatePatchesSameAction(t *testing.T) {
	p := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "target", Gate: GateAND, Preconditions: []string{"a"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "cheap", Action: "a", Cost: 2},
			{ID: "pricey", Action: "a", Cost: 5},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"cheap"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	validateSolution(t, p, s)
}

func TestSharedActionPatchMinimality(t *testing.T) {
	// Two zero-cost patches disable the same action. A set containing both is
	// not inclusion-minimal, so the solver must return a singleton, and the
	// returned witness must be computed against the actually-removed patch.
	p := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "target", Gate: GateAND, Preconditions: []string{"a"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "p1", Action: "a", Cost: 0},
			{ID: "p2", Action: "a", Cost: 0},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if len(s.Selected) != 1 {
		t.Fatalf("selected %v, want a single inclusion-minimal patch", s.Selected)
	}
	if got, want := s.Selected, []string{"p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	if len(s.Proof.Minimality) != 1 {
		t.Fatalf("got %d witnesses, want 1", len(s.Proof.Minimality))
	}
	w := s.Proof.Minimality[0]
	if w.OmittedPatch != "p1" || !reflect.DeepEqual(w.ActivationOrder, []string{"a", "target"}) {
		t.Fatalf("unexpected witness %+v", w)
	}
	validateSolution(t, p, s)
}

func TestCostOptimalAcrossBranches(t *testing.T) {
	// OR target with two independent branches: both branches must be cut.
	// The cheapest blocking set picks the least expensive patch per branch.
	p := &Problem{
		Nodes: []Node{
			{ID: "x", Gate: GateAND},
			{ID: "y", Gate: GateAND},
			{ID: "left", Gate: GateAND, Preconditions: []string{"x"}},
			{ID: "right", Gate: GateAND, Preconditions: []string{"y"}},
			{ID: "target", Gate: GateOR, Preconditions: []string{"left", "right"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "p-left", Action: "left", Cost: 3},
			{ID: "p-x", Action: "x", Cost: 4},
			{ID: "p-right", Action: "right", Cost: 9},
			{ID: "p-y", Action: "y", Cost: 10},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"p-left", "p-right"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	if s.Cost != 12 {
		t.Fatalf("cost = %d, want 12", s.Cost)
	}
	validateSolution(t, p, s)
}

func TestTieBreakLexicographic(t *testing.T) {
	// An OR target whose two branches are the leaf nodes directly. Cutting
	// either leaf alone blocks it, so this is a genuine tie between singleton
	// sets; the smaller ID list wins.
	p := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "b", Gate: GateAND},
			{ID: "ta", Gate: GateOR, Preconditions: []string{"a"}},
			{ID: "tb", Gate: GateOR, Preconditions: []string{"b"}},
			{ID: "target", Gate: GateAND, Preconditions: []string{"ta", "tb"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "p-b", Action: "b", Cost: 5},
			{ID: "p-a", Action: "a", Cost: 5},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"p-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	validateSolution(t, p, s)
}

func TestTieBreakShorterListAmongMinimal(t *testing.T) {
	// Two singleton blocking sets tie on cost (both 10); the lexicographically
	// smaller patch ID wins regardless of the order patches were supplied in.
	p := &Problem{
		Nodes: []Node{
			{ID: "a", Gate: GateAND},
			{ID: "target", Gate: GateAND, Preconditions: []string{"a"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "zzz", Action: "target", Cost: 10},
			{ID: "aaa", Action: "a", Cost: 10},
			{ID: "p-t", Action: "target", Cost: 10},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"aaa"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	validateSolution(t, p, s)
}

func TestLexLessHelper(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a"}, []string{"a", "b"}, true},
		{[]string{"a", "b"}, []string{"a"}, false},
		{[]string{"a", "c"}, []string{"a", "b"}, false},
		{[]string{"a", "b"}, []string{"a", "b"}, false},
		{nil, []string{"a"}, true},
	}
	for _, tc := range cases {
		if got := lexLess(tc.a, tc.b); got != tc.want {
			t.Fatalf("lexLess(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestANDGateRequiresAllCuts(t *testing.T) {
	// An AND target needs every precondition; disabling any single one cuts
	// it, so the cheapest singleton must win.
	p := &Problem{
		Nodes: []Node{
			{ID: "f1", Gate: GateAND},
			{ID: "f2", Gate: GateAND},
			{ID: "f3", Gate: GateAND},
			{ID: "target", Gate: GateAND, Preconditions: []string{"f1", "f2", "f3"}},
		},
		Target: "target",
		Patches: []Patch{
			{ID: "p1", Action: "f1", Cost: 1},
			{ID: "p2", Action: "f2", Cost: 2},
			{ID: "p3", Action: "f3", Cost: 4},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	if s.Cost != 1 {
		t.Fatalf("cost = %d, want 1", s.Cost)
	}
	validateSolution(t, p, s)
}
