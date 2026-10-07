package methodres

import (
	"reflect"
	"testing"
)

func mustGraph(t *testing.T, ifaces []*Interface, classes []*Class) *Graph {
	t.Helper()
	g, err := NewGraph(ifaces, classes)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func TestClassOverrideWins(t *testing.T) {
	g := mustGraph(t,
		[]*Interface{{Name: "I", Defaults: map[string]bool{"m": true}}},
		[]*Class{{Name: "C", Interfaces: []string{"I"}, Overrides: map[string]bool{"m": true}}},
	)
	res, err := NewResolver(g).Resolve("C", "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ClassOverride || res.From != "C" {
		t.Fatalf("got %+v", res)
	}
}

func TestDiamondCountsOnce(t *testing.T) {
	// D -> B,C -> A; only A defines m. Diamond must not conflict.
	g := mustGraph(t,
		[]*Interface{
			{Name: "A", Defaults: map[string]bool{"m": true}},
			{Name: "B", Parents: []string{"A"}},
			{Name: "C", Parents: []string{"A"}},
			{Name: "D", Parents: []string{"B", "C"}},
		},
		[]*Class{{Name: "K", Interfaces: []string{"D"}}},
	)
	res, err := NewResolver(g).Resolve("K", "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Inherited || res.From != "A" {
		t.Fatalf("got %+v", res)
	}
}

func TestMostSpecificWins(t *testing.T) {
	// B overrides A's default; B must win for a class implementing B.
	g := mustGraph(t,
		[]*Interface{
			{Name: "A", Defaults: map[string]bool{"m": true}},
			{Name: "B", Parents: []string{"A"}, Defaults: map[string]bool{"m": true}},
		},
		[]*Class{{Name: "K", Interfaces: []string{"B"}}},
	)
	res, err := NewResolver(g).Resolve("K", "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Inherited || res.From != "B" {
		t.Fatalf("got %+v", res)
	}
}

func TestIncomparableDefaultsConflict(t *testing.T) {
	g := mustGraph(t,
		[]*Interface{
			{Name: "B", Defaults: map[string]bool{"m": true}},
			{Name: "C", Defaults: map[string]bool{"m": true}},
			{Name: "D", Parents: []string{"B", "C"}},
		},
		[]*Class{{Name: "K", Interfaces: []string{"D"}}},
	)
	res, err := NewResolver(g).Resolve("K", "m")
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"B", "C"}}
	if res.Kind != Conflict || !reflect.DeepEqual(res.Pairs, want) {
		t.Fatalf("got %+v", res)
	}
}

func TestThreeWayConflictPairs(t *testing.T) {
	g := mustGraph(t,
		[]*Interface{
			{Name: "A", Defaults: map[string]bool{"m": true}},
			{Name: "B", Defaults: map[string]bool{"m": true}},
			{Name: "C", Defaults: map[string]bool{"m": true}},
		},
		[]*Class{{Name: "K", Interfaces: []string{"A", "B", "C"}}},
	)
	res, err := NewResolver(g).Resolve("K", "m")
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"A", "B"}, {"A", "C"}, {"B", "C"}}
	if res.Kind != Conflict || !reflect.DeepEqual(res.Pairs, want) {
		t.Fatalf("got %+v", res)
	}
}

func TestExplicitPickValid(t *testing.T) {
	// Conflict between B and C is settled by explicitly picking A's default.
	g := mustGraph(t,
		[]*Interface{
			{Name: "A", Defaults: map[string]bool{"m": true}},
			{Name: "B", Parents: []string{"A"}, Defaults: map[string]bool{"m": true}},
			{Name: "C", Parents: []string{"A"}},
		},
		[]*Class{
			{Name: "Auto", Interfaces: []string{"B", "C"}},
			{Name: "Picky", Interfaces: []string{"B", "C"}, Picks: map[string]string{"m": "A"}},
		},
	)
	r := NewResolver(g)
	auto, err := r.Resolve("Auto", "m")
	if err != nil {
		t.Fatal(err)
	}
	if auto.Kind != Inherited || auto.From != "B" {
		t.Fatalf("auto: got %+v", auto)
	}
	picky, err := r.Resolve("Picky", "m")
	if err != nil {
		t.Fatal(err)
	}
	if picky.Kind != ExplicitPick || picky.From != "A" {
		t.Fatalf("picky: got %+v", picky)
	}
}

func TestExplicitPickRejectsNonInheritable(t *testing.T) {
	ifaces := []*Interface{
		{Name: "A", Defaults: map[string]bool{"m": true}},
		{Name: "Unrelated", Defaults: map[string]bool{"m": true}},
		{Name: "NoDefault"},
	}
	for _, tc := range []struct {
		name  string
		class *Class
		want  string
	}{
		{"BadReach", &Class{Name: "BadReach", Interfaces: []string{"A"}, Picks: map[string]string{"m": "Unrelated"}}, "not reachable"},
		{"BadDefault", &Class{Name: "BadDefault", Interfaces: []string{"A", "NoDefault"}, Picks: map[string]string{"m": "NoDefault"}}, "no default"},
		{"BadName", &Class{Name: "BadName", Interfaces: []string{"A"}, Picks: map[string]string{"m": "Ghost"}}, "not an interface"},
	} {
		if _, err := NewGraph(ifaces, []*Class{tc.class}); err == nil {
			t.Fatalf("%s: expected error containing %q", tc.name, tc.want)
		} else if got := err.Error(); !contains(got, tc.want) {
			t.Fatalf("%s: error %q does not contain %q", tc.name, got, tc.want)
		}
	}
}

func TestCyclicInheritanceRejected(t *testing.T) {
	cycles := [][]*Interface{
		{{Name: "A", Parents: []string{"A"}}},
		{{Name: "A", Parents: []string{"B"}}, {Name: "B", Parents: []string{"A"}}},
		{
			{Name: "A", Parents: []string{"B"}},
			{Name: "B", Parents: []string{"C"}},
			{Name: "C", Parents: []string{"A"}},
		},
	}
	for i, ifaces := range cycles {
		if _, err := NewGraph(ifaces, nil); err == nil {
			t.Fatalf("cycle %d: expected rejection", i)
		}
	}
}

func TestSourceDAG(t *testing.T) {
	g := mustGraph(t,
		[]*Interface{
			{Name: "A", Defaults: map[string]bool{"m": true}},
			{Name: "B", Parents: []string{"A"}, Defaults: map[string]bool{"m": true, "n": true}},
			{Name: "C", Defaults: map[string]bool{"n": true}},
		},
		[]*Class{{Name: "K", Interfaces: []string{"B", "C"}, Overrides: map[string]bool{"o": true}}},
	)
	dag, err := NewResolver(g).SourceDAG("K", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := dag.Selected, map[string]string{"m": "B", "o": "K"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected: got %v want %v", got, want)
	}
	if got, want := dag.Conflicts, map[string][][2]string{"n": {{"B", "C"}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicts: got %v want %v", got, want)
	}
	wantNodes := []string{"K", "A", "B", "C"}
	if !reflect.DeepEqual(dag.Nodes, wantNodes) {
		t.Fatalf("nodes: got %v want %v", dag.Nodes, wantNodes)
	}
	var resolves int
	for _, e := range dag.Edges {
		if e.Kind == "resolves" {
			resolves++
		}
	}
	if resolves != 2 {
		t.Fatalf("expected 2 resolves edges, got %d (%v)", resolves, dag.Edges)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
