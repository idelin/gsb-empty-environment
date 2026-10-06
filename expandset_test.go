package treeplan

import (
	"math/rand"
	"reflect"
	"testing"
)

// Two targets share the root: its expansion cost must be paid once.
func TestExpandSetSharedAncestorCost(t *testing.T) {
	spec := &Spec{
		Root: &Group{
			Name:       "root",
			ExpandCost: 5,
			Groups: []*Group{
				{Name: "a", ExpandCost: 1, Leaves: []Leaf{{Name: "t1"}}},
				{Name: "b", ExpandCost: 1, Leaves: []Leaf{{Name: "t2"}}},
			},
		},
		Targets: []string{"t1", "t2"},
	}
	tr := mustBuild(t, spec)
	res, err := tr.SolveExpandSet()
	if err != nil {
		t.Fatalf("SolveExpandSet: %v", err)
	}
	if res.Cost != 7 { // 5 + 1 + 1, root charged once
		t.Fatalf("cost = %d, want 7 (shared ancestor charged once)", res.Cost)
	}
	if err := tr.VerifyExpandSet(res); err != nil {
		t.Fatalf("VerifyExpandSet: %v", err)
	}
}

// Expanding a group exposes its non-target leaves; their penalties count.
// A cheap group exposing an expensive leaf must be avoided when possible.
func TestExpandSetExposurePenalty(t *testing.T) {
	spec := &Spec{
		Root: &Group{
			Name:       "root",
			ExpandCost: 0,
			Groups: []*Group{
				{Name: "direct", ExpandCost: 1, Leaves: []Leaf{{Name: "t"}, {Name: "junk", Penalty: 50}}},
				{Name: "empty", ExpandCost: 4},
			},
		},
		Targets: []string{"t"},
	}
	tr := mustBuild(t, spec)
	res, err := tr.SolveExpandSet()
	if err != nil {
		t.Fatalf("SolveExpandSet: %v", err)
	}
	// Reaching t forces expanding "direct", which exposes junk: 1 + 50.
	// The "empty" group stays collapsed and costs nothing.
	if res.Cost != 51 {
		t.Fatalf("cost = %d, want 51", res.Cost)
	}
	want := []string{"root", "direct"}
	if !reflect.DeepEqual(res.Expanded, want) {
		t.Fatalf("expanded = %v, want %v", res.Expanded, want)
	}
	if !reflect.DeepEqual(res.ExtraLeaves, []string{"junk"}) {
		t.Fatalf("extra leaves = %v, want [junk]", res.ExtraLeaves)
	}
}

// Enumeration and the independent tree DP must agree on random instances,
// and every result must pass the independent verifier.
func TestExpandSetEnumerationMatchesDP(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		spec := randomSpec(r, 1+r.Intn(10))
		tr := mustBuild(t, spec)
		enum, err := tr.SolveExpandSet()
		if err != nil {
			t.Fatalf("iter %d: SolveExpandSet: %v", iter, err)
		}
		dp := tr.SolveExpandSetDP()
		if enum.Cost != dp.Cost {
			t.Fatalf("iter %d: enumeration cost %d != DP cost %d", iter, enum.Cost, dp.Cost)
		}
		if err := tr.VerifyExpandSet(enum); err != nil {
			t.Fatalf("iter %d: verify enumeration: %v", iter, err)
		}
		if err := tr.VerifyExpandSet(dp); err != nil {
			t.Fatalf("iter %d: verify DP: %v", iter, err)
		}
	}
}

// The enumeration must handle the documented maximum of 20 groups.
func TestExpandSetTwentyGroups(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	spec := randomSpec(r, MaxEnumerateGroups)
	tr := mustBuild(t, spec)
	enum, err := tr.SolveExpandSet()
	if err != nil {
		t.Fatalf("SolveExpandSet: %v", err)
	}
	dp := tr.SolveExpandSetDP()
	if enum.Cost != dp.Cost {
		t.Fatalf("enumeration cost %d != DP cost %d", enum.Cost, dp.Cost)
	}
	if err := tr.VerifyExpandSet(enum); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// The verifier must reject broken results.
func TestExpandSetVerifyRejects(t *testing.T) {
	spec := &Spec{
		Root: &Group{
			Name:       "root",
			ExpandCost: 1,
			Groups: []*Group{
				{Name: "a", ExpandCost: 1, Leaves: []Leaf{{Name: "t"}}},
			},
		},
		Targets: []string{"t"},
	}
	tr := mustBuild(t, spec)
	cases := []*ExpandSetResult{
		{Expanded: []string{"a"}, Cost: 1},              // closure violated
		{Expanded: []string{"root"}, Cost: 1},           // target hidden
		{Expanded: []string{"root", "a"}, Cost: 1},      // wrong cost
		{Expanded: []string{"root", "nope"}, Cost: 2},   // unknown group
		{Expanded: []string{"root", "a", "a"}, Cost: 2}, // duplicate
	}
	for i, res := range cases {
		if err := tr.VerifyExpandSet(res); err == nil {
			t.Fatalf("case %d: verifier accepted bad result %+v", i, res)
		}
	}
	ok := &ExpandSetResult{Expanded: []string{"root", "a"}, Cost: 2}
	if err := tr.VerifyExpandSet(ok); err != nil {
		t.Fatalf("verifier rejected good result: %v", err)
	}
}
