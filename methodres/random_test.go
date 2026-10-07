package methodres

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// randomGraph builds a random acyclic program: interfaces only extend
// lower-numbered interfaces, so inheritance is a DAG by construction.
func randomGraph(rng *rand.Rand) (*Graph, []string) {
	nIf := 2 + rng.Intn(6) // 2..7 interfaces
	methods := []string{"m0", "m1", "m2", "m3"}[:2+rng.Intn(3)]

	ifaces := make([]*Interface, 0, nIf)
	for i := 0; i < nIf; i++ {
		in := &Interface{Name: fmt.Sprintf("I%d", i), Defaults: map[string]bool{}}
		for j := 0; j < i; j++ {
			if rng.Float64() < 0.3 {
				in.Parents = append(in.Parents, fmt.Sprintf("I%d", j))
			}
		}
		for _, m := range methods {
			if rng.Float64() < 0.4 {
				in.Defaults[m] = true
			}
		}
		ifaces = append(ifaces, in)
	}

	nCl := 1 + rng.Intn(3)
	classes := make([]*Class, 0, nCl)
	for i := 0; i < nCl; i++ {
		c := &Class{
			Name:      fmt.Sprintf("C%d", i),
			Overrides: map[string]bool{},
			Picks:     map[string]string{},
		}
		for _, in := range ifaces {
			if rng.Float64() < 0.4 {
				c.Interfaces = append(c.Interfaces, in.Name)
			}
		}
		if len(c.Interfaces) == 0 {
			c.Interfaces = []string{ifaces[rng.Intn(len(ifaces))].Name}
		}
		for _, m := range methods {
			switch r := rng.Float64(); {
			case r < 0.15:
				c.Overrides[m] = true
			case r < 0.35:
				// Picks must be inheritable (enforced by NewGraph), so only
				// generate valid ones.
				if pick := randomValidPick(ifaces, c.Interfaces, m, rng); pick != "" {
					c.Picks[m] = pick
				}
			}
		}
		classes = append(classes, c)
	}

	g, err := NewGraph(ifaces, classes)
	if err != nil {
		panic(fmt.Sprintf("generated invalid graph: %v", err))
	}
	return g, methods
}

// randomValidPick returns a random interface reachable from roots that
// defines m, or "" if none exists.
func randomValidPick(ifaces []*Interface, roots []string, m string, rng *rand.Rand) string {
	byName := map[string]*Interface{}
	for _, in := range ifaces {
		byName[in.Name] = in
	}
	seen := map[string]bool{}
	var stack []string
	stack = append(stack, roots...)
	var valid []string
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		in := byName[n]
		if in.Defaults[m] {
			valid = append(valid, n)
		}
		stack = append(stack, in.Parents...)
	}
	if len(valid) == 0 {
		return ""
	}
	return valid[rng.Intn(len(valid))]
}

// TestRandomAgainstReference cross-checks the memoized Resolver against the
// naive path-enumeration reference on many random small graphs.
func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261008))
	for trial := 0; trial < 2000; trial++ {
		g, methods := randomGraph(rng)
		r := NewResolver(g)
		for _, c := range g.Classes {
			for _, m := range methods {
				got, gotErr := r.Resolve(c.Name, m)
				want, wantErr := ReferenceResolve(g, c.Name, m)
				if (gotErr == nil) != (wantErr == nil) {
					t.Fatalf("trial %d %s.%s: error mismatch: main=%v ref=%v",
						trial, c.Name, m, gotErr, wantErr)
				}
				if gotErr != nil {
					continue
				}
				if got.Kind != want.Kind || got.From != want.From ||
					!reflect.DeepEqual(got.Pairs, want.Pairs) {
					t.Fatalf("trial %d %s.%s:\n main=%+v\n ref =%+v",
						trial, c.Name, m, got, want)
				}
			}
		}
	}
}

// TestRandomSourceDAGAcyclic sanity-checks that inheritance edges of every
// generated SourceDAG form a DAG (no back edges by construction).
func TestRandomSourceDAGAcyclic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		g, _ := randomGraph(rng)
		r := NewResolver(g)
		for _, c := range g.Classes {
			dag, err := r.SourceDAG(c.Name, nil)
			if err != nil {
				t.Fatalf("trial %d class %s: %v", trial, c.Name, err)
			}
			// Kahn's algorithm over inheritance edges only.
			indeg := map[string]int{}
			adj := map[string][]string{}
			for _, e := range dag.Edges {
				if e.Kind != "inherits" {
					continue
				}
				adj[e.From] = append(adj[e.From], e.To)
				indeg[e.To]++
				if _, ok := indeg[e.From]; !ok {
					indeg[e.From] = 0
				}
			}
			var queue []string
			for n, d := range indeg {
				if d == 0 {
					queue = append(queue, n)
				}
			}
			visited := 0
			for len(queue) > 0 {
				n := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				visited++
				for _, to := range adj[n] {
					indeg[to]--
					if indeg[to] == 0 {
						queue = append(queue, to)
					}
				}
			}
			if visited != len(indeg) {
				t.Fatalf("trial %d class %s: inheritance edges contain a cycle", trial, c.Name)
			}
		}
	}
}
