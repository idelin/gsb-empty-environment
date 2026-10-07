package methodres

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func mustGraph(t *testing.T, ifaces ...*Interface) *Graph {
	t.Helper()
	g := NewGraph()
	for _, in := range ifaces {
		if err := g.AddInterface(in); err != nil {
			t.Fatalf("AddInterface(%q): %v", in.Name, err)
		}
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return g
}

func defs(ms ...string) map[string]bool {
	out := map[string]bool{}
	for _, m := range ms {
		out[m] = true
	}
	return out
}

func TestDiamondAncestorCountedOnce(t *testing.T) {
	g := mustGraph(t,
		&Interface{Name: "A", Defaults: defs("m")},
		&Interface{Name: "B", Parents: []string{"A"}},
		&Interface{Name: "C", Parents: []string{"A"}},
		&Interface{Name: "D", Parents: []string{"B", "C"}},
	)
	c := &Class{Name: "K", Interfaces: []string{"D"}}
	res, err := g.Resolve(c, "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Unique || res.Winner != "A" {
		t.Fatalf("got %v winner=%q, want unique winner=A", res.Kind, res.Winner)
	}
	wantNodes := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	if !reflect.DeepEqual(res.Sources.Nodes, wantNodes) {
		t.Fatalf("source DAG nodes = %v, want %v", res.Sources.Nodes, wantNodes)
	}
	wantEdges := map[string][]string{"D": {"B", "C"}, "B": {"A"}, "C": {"A"}}
	if !reflect.DeepEqual(res.Sources.Edges, wantEdges) {
		t.Fatalf("source DAG edges = %v, want %v", res.Sources.Edges, wantEdges)
	}
}

func TestMostSpecificWins(t *testing.T) {
	g := mustGraph(t,
		&Interface{Name: "A", Defaults: defs("m")},
		&Interface{Name: "B", Parents: []string{"A"}, Defaults: defs("m")},
	)
	c := &Class{Name: "K", Interfaces: []string{"B"}}
	res, err := g.Resolve(c, "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Unique || res.Winner != "B" {
		t.Fatalf("got %v winner=%q, want unique winner=B", res.Kind, res.Winner)
	}
}

func TestConflictIncomparable(t *testing.T) {
	g := mustGraph(t,
		&Interface{Name: "Base"},
		&Interface{Name: "A", Parents: []string{"Base"}, Defaults: defs("m")},
		&Interface{Name: "B", Parents: []string{"Base"}, Defaults: defs("m")},
		&Interface{Name: "C", Parents: []string{"A", "B"}},
	)
	c := &Class{Name: "K", Interfaces: []string{"C"}}
	res, err := g.Resolve(c, "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Conflict {
		t.Fatalf("got %v, want conflict", res.Kind)
	}
	want := []ConflictPair{{A: "A", B: "B"}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("conflicts = %v, want %v", res.Conflicts, want)
	}
}

func TestClassOverrideWins(t *testing.T) {
	g := mustGraph(t, &Interface{Name: "A", Defaults: defs("m")})
	c := &Class{Name: "K", Interfaces: []string{"A"}, Overrides: defs("m")}
	res, err := g.Resolve(c, "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ClassOverride || res.Winner != "K" {
		t.Fatalf("got %v winner=%q, want class-override winner=K", res.Kind, res.Winner)
	}
}

func TestExplicitSelectResolvesConflict(t *testing.T) {
	g := mustGraph(t,
		&Interface{Name: "A", Defaults: defs("m")},
		&Interface{Name: "B", Defaults: defs("m")},
		&Interface{Name: "C", Parents: []string{"A", "B"}},
	)
	c := &Class{Name: "K", Interfaces: []string{"C"}, Selects: map[string]string{"m": "B"}}
	res, err := g.Resolve(c, "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ExplicitSelect || res.Winner != "B" {
		t.Fatalf("got %v winner=%q, want explicit-select winner=B", res.Kind, res.Winner)
	}
}

func TestExplicitSelectMustBeInheritable(t *testing.T) {
	g := mustGraph(t,
		&Interface{Name: "A", Defaults: defs("m")},
		&Interface{Name: "B"},
		&Interface{Name: "Unrelated", Defaults: defs("m")},
	)
	// Not inherited.
	c := &Class{Name: "K", Interfaces: []string{"A"}, Selects: map[string]string{"m": "Unrelated"}}
	if _, err := g.Resolve(c, "m"); err == nil {
		t.Fatal("selecting a non-inherited interface must fail")
	}
	// Inherited but defines no default for m.
	c2 := &Class{Name: "K", Interfaces: []string{"A", "B"}, Selects: map[string]string{"m": "B"}}
	if _, err := g.Resolve(c2, "m"); err == nil {
		t.Fatal("selecting an interface without a default must fail")
	}
}

func TestCyclicInheritanceRejected(t *testing.T) {
	g := NewGraph()
	_ = g.AddInterface(&Interface{Name: "A", Parents: []string{"B"}})
	_ = g.AddInterface(&Interface{Name: "B", Parents: []string{"A"}})
	if err := g.Validate(); err == nil {
		t.Fatal("two-node cycle must be rejected")
	}
	g2 := NewGraph()
	_ = g2.AddInterface(&Interface{Name: "A", Parents: []string{"A"}})
	if err := g2.Validate(); err == nil {
		t.Fatal("self-loop must be rejected")
	}
}

func TestUnknownParentRejected(t *testing.T) {
	g := NewGraph()
	_ = g.AddInterface(&Interface{Name: "A", Parents: []string{"Missing"}})
	if err := g.Validate(); err == nil {
		t.Fatal("unknown parent must be rejected")
	}
}

func TestNoDefinition(t *testing.T) {
	g := mustGraph(t, &Interface{Name: "A"})
	c := &Class{Name: "K", Interfaces: []string{"A"}}
	res, err := g.Resolve(c, "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != None {
		t.Fatalf("got %v, want none", res.Kind)
	}
}

// --- Reference implementation: explicit path enumeration ---

// enumPaths lists every inheritance path starting at each root, walking
// parent edges to the top of the hierarchy. Used only by the tests as an
// independent reference.
func enumPaths(g *Graph, roots []string) [][]string {
	var out [][]string
	var rec func(path []string)
	rec = func(path []string) {
		out = append(out, append([]string(nil), path...))
		last := path[len(path)-1]
		for _, p := range g.interfaces[last].Parents {
			rec(append(path, p))
		}
	}
	for _, r := range roots {
		rec([]string{r})
	}
	return out
}

// refResolve mirrors the spec by brute-force path enumeration: collect
// every path endpoint and intermediate node that defines the method, dedup,
// then keep definitions not reachable from any other definition.
func refResolve(g *Graph, c *Class, method string) (Kind, string, []ConflictPair, *SourceDAG) {
	if c.Overrides[method] {
		return ClassOverride, c.Name, nil, nil
	}
	if sel, ok := c.Selects[method]; ok {
		return ExplicitSelect, sel, nil, refSourceDAG(g, c.Interfaces, method)
	}
	paths := enumPaths(g, c.Interfaces)
	definers := map[string]bool{}
	for _, p := range paths {
		for _, node := range p {
			if g.interfaces[node].Defines(method) {
				definers[node] = true
			}
		}
	}
	if len(definers) == 0 {
		return None, "", nil, nil
	}
	// Proper-ancestor relation via path enumeration from each definer.
	properAnc := map[string]map[string]bool{}
	for d := range definers {
		properAnc[d] = map[string]bool{}
		for _, p := range enumPaths(g, []string{d}) {
			for i := 1; i < len(p); i++ {
				properAnc[d][p[i]] = true
			}
		}
	}
	var winners []string
	for d := range definers {
		dominated := false
		for other := range definers {
			if other != d && properAnc[other][d] {
				dominated = true
				break
			}
		}
		if !dominated {
			winners = append(winners, d)
		}
	}
	sort.Strings(winners)
	dag := refSourceDAG(g, c.Interfaces, method)
	if len(winners) == 1 {
		return Unique, winners[0], nil, dag
	}
	var pairs []ConflictPair
	for i := 0; i < len(winners); i++ {
		for j := i + 1; j < len(winners); j++ {
			pairs = append(pairs, ConflictPair{A: winners[i], B: winners[j]})
		}
	}
	return Conflict, "", pairs, dag
}

// refSourceDAG unions every path prefix that ends at a definition.
func refSourceDAG(g *Graph, roots []string, method string) *SourceDAG {
	dag := &SourceDAG{Nodes: map[string]bool{}, Edges: map[string][]string{}}
	edgeSet := map[string]map[string]bool{}
	for _, p := range enumPaths(g, roots) {
		for i, node := range p {
			if !g.interfaces[node].Defines(method) {
				continue
			}
			for j := 0; j <= i; j++ {
				dag.Nodes[p[j]] = true
				if j+1 <= i {
					if edgeSet[p[j]] == nil {
						edgeSet[p[j]] = map[string]bool{}
					}
					edgeSet[p[j]][p[j+1]] = true
				}
			}
		}
	}
	for child, parents := range edgeSet {
		for p := range parents {
			dag.Edges[child] = append(dag.Edges[child], p)
		}
		sort.Strings(dag.Edges[child])
	}
	return dag
}

// --- Randomized cross-check ---

func TestRandomGraphsMatchReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261008))
	methods := []string{"m", "n", "o"}
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(7) // 1..7 interfaces
		g := NewGraph()
		names := make([]string, n)
		for i := 0; i < n; i++ {
			names[i] = "I" + string(rune('0'+i))
		}
		for i := 0; i < n; i++ {
			in := &Interface{Name: names[i], Defaults: map[string]bool{}}
			// Parents only among higher indices: acyclic by construction.
			for j := i + 1; j < n; j++ {
				if rng.Float64() < 0.35 {
					in.Parents = append(in.Parents, names[j])
				}
			}
			for _, m := range methods {
				if rng.Float64() < 0.35 {
					in.Defaults[m] = true
				}
			}
			if err := g.AddInterface(in); err != nil {
				t.Fatal(err)
			}
		}
		if err := g.Validate(); err != nil {
			t.Fatalf("trial %d: generated graph must be acyclic: %v", trial, err)
		}
		// Random class implementing a random subset.
		c := &Class{
			Name:      "K",
			Overrides: map[string]bool{},
			Selects:   map[string]string{},
		}
		for _, name := range names {
			if rng.Float64() < 0.5 {
				c.Interfaces = append(c.Interfaces, name)
			}
		}
		reach := g.reachable(c.Interfaces)
		for _, m := range methods {
			if rng.Float64() < 0.15 {
				c.Overrides[m] = true
				continue
			}
			if rng.Float64() < 0.3 {
				var definers []string
				for name := range reach {
					if g.interfaces[name].Defines(m) {
						definers = append(definers, name)
					}
				}
				if len(definers) > 0 {
					c.Selects[m] = definers[rng.Intn(len(definers))]
				}
			}
		}
		for _, m := range g.Methods(c) {
			res, err := g.Resolve(c, m)
			if err != nil {
				t.Fatalf("trial %d method %s: %v", trial, m, err)
			}
			wantKind, wantWinner, wantPairs, wantDAG := refResolve(g, c, m)
			if res.Kind != wantKind {
				t.Fatalf("trial %d method %s: kind %v, want %v", trial, m, res.Kind, wantKind)
			}
			if res.Winner != wantWinner {
				t.Fatalf("trial %d method %s: winner %q, want %q", trial, m, res.Winner, wantWinner)
			}
			if !reflect.DeepEqual(res.Conflicts, wantPairs) {
				t.Fatalf("trial %d method %s: conflicts %v, want %v", trial, m, res.Conflicts, wantPairs)
			}
			if (res.Sources == nil) != (wantDAG == nil) {
				t.Fatalf("trial %d method %s: source DAG nil mismatch", trial, m)
			}
			if res.Sources != nil {
				if !reflect.DeepEqual(res.Sources.Nodes, wantDAG.Nodes) {
					t.Fatalf("trial %d method %s: DAG nodes %v, want %v", trial, m, res.Sources.Nodes, wantDAG.Nodes)
				}
				if !reflect.DeepEqual(res.Sources.Edges, wantDAG.Edges) {
					t.Fatalf("trial %d method %s: DAG edges %v, want %v", trial, m, res.Sources.Edges, wantDAG.Edges)
				}
			}
		}
	}
}
