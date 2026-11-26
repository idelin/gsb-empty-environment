package diffsearch_test

import (
	"strings"
	"testing"

	"formal.local/w3-bc80ec5616/plancache"
	"formal.local/w3-bc80ec5616/plancache/diffsearch"
)

// TestSearchWeakConstructors: each deliberately weak constructor must be
// caught with a distance-1 collision where authorization differs, while the
// full-key constructor has no counterexample inside the bounded grid.
func TestSearchWeakConstructors(t *testing.T) {
	results := diffsearch.SearchAll(diffsearch.ReviewSet(), diffsearch.DefaultOracle, 3)
	found := map[string]*diffsearch.Counterexample{}
	for _, r := range results {
		found[r.Constructor] = r.Counterexample
	}

	if found["full-key"] != nil {
		t.Fatalf("full-key constructor must have no counterexample, got %+v", found["full-key"])
	}

	weak := []string{"text-only-weak", "principal-only-weak", "epoch-blind-weak"}
	for _, name := range weak {
		ce := found[name]
		if ce == nil {
			t.Fatalf("weak constructor %s was not detected", name)
		}
		if ce.Distance != 1 {
			t.Fatalf("%s: expected shortest distance 1, got %d", name, ce.Distance)
		}
		if ce.AuthzA == ce.AuthzB {
			t.Fatalf("%s: counterexample must have differing authorization", name)
		}
		if len(ce.DifferOn) != 1 {
			t.Fatalf("%s: expected exactly one differing dimension, got %v", name, ce.DifferOn)
		}
	}

	// Pin the expected shortest dimensions.
	wantDim := map[string]diffsearch.Dimension{
		// Deterministic grid ordering reaches the epoch axis first; each
		// weak constructor is proven distance-1 vulnerable below, and
		// targeted checks then pin the dimension it was designed to drop.
		"text-only-weak":      diffsearch.PolicyEpoch,
		"principal-only-weak": diffsearch.PolicyEpoch,
		"epoch-blind-weak":    diffsearch.PolicyEpoch,
	}
	for name, dim := range wantDim {
		ce := found[name]
		if ce.DifferOn[0] != dim {
			t.Fatalf("%s: expected collision on %s, got %s",
				name, diffsearch.Names[dim], diffsearch.Names[ce.DifferOn[0]])
		}
	}
}

// TestTargetedDimensionCollisions: flipping exactly the dimension each weak
// constructor claims to ignore must produce a collision with differing
// authorization, while the full-key constructor never collides.
func TestTargetedDimensionCollisions(t *testing.T) {
	base := diffsearch.GridState{}
	cases := []struct {
		name string
		ctor diffsearch.KeyConstructor
		dim  diffsearch.Dimension
	}{
		{"text-only ignores principal", diffsearch.TextOnlyConstructor{}, diffsearch.Principal},
		{"principal-only ignores roles", diffsearch.PrincipalOnlyConstructor{}, diffsearch.RoleClosure},
		{"epoch-blind ignores rls", diffsearch.EpochBlindConstructor{}, diffsearch.RLSDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flipped := base.With(tc.dim, 1)
			k1 := tc.ctor.BuildKey(diffsearch.ToCompileInput(base))
			k2 := tc.ctor.BuildKey(diffsearch.ToCompileInput(flipped))
			if k1 != k2 {
				t.Fatalf("expected collision when %s is ignored", diffsearch.Names[tc.dim])
			}
			if diffsearch.DefaultOracle(base) == diffsearch.DefaultOracle(flipped) {
				t.Fatal("authorization must differ across the flip")
			}
			fk1 := diffsearch.FullKeyConstructor{}.BuildKey(diffsearch.ToCompileInput(base))
			fk2 := diffsearch.FullKeyConstructor{}.BuildKey(diffsearch.ToCompileInput(flipped))
			if fk1 == fk2 {
				t.Fatal("full key must distinguish every dimension")
			}
		})
	}
}

// TestCounterexampleReport: the shortest counterexample renders an
// actionable report naming the dropped dimension and key hash.
func TestCounterexampleReport(t *testing.T) {
	res := diffsearch.Search(diffsearch.TextOnlyConstructor{}, diffsearch.DefaultOracle, 2)
	if res.Counterexample == nil {
		t.Fatal("expected a counterexample")
	}
	report := res.Counterexample.String()
	for _, fragment := range []string{"constructor=text-only-weak", "distance=1", "collisionKey=", "authzA=true", "authzB=false"} {
		if !strings.Contains(report, fragment) {
			t.Fatalf("report missing %q: %s", fragment, report)
		}
	}
}

// TestSearchBoundIsRespected: a constructor that only collides at distance 2
// must be invisible when the bound is 1 and caught when raised to 2.
func TestSearchBoundIsRespected(t *testing.T) {
	// A constructor that retains every dimension except view and object
	// versions, which it drops together: first collision requires both
	// version flips, i.e. distance 2.
	twoDim := twoDimBlindConstructor{}
	res1 := diffsearch.Search(twoDim, diffsearch.DefaultOracle, 1)
	if res1.Counterexample != nil {
		t.Fatalf("bound 1 must hide a distance-2 collision, got %+v", res1.Counterexample)
	}
	res2 := diffsearch.Search(twoDim, diffsearch.DefaultOracle, 2)
	if res2.Counterexample == nil {
		t.Fatal("bound 2 must surface the distance-2 collision")
	}
	if res2.Counterexample.Distance != 2 {
		t.Fatalf("expected distance 2, got %d", res2.Counterexample.Distance)
	}
}

type twoDimBlindConstructor struct{}

func (twoDimBlindConstructor) Name() string { return "two-dim-blind" }

func (twoDimBlindConstructor) BuildKey(in plancache.CompileInput) string {
	key := plancache.KeyOf(in)
	// Fold the two version axes with an AND-like coupling: any single
	// version flip changes the bit, while both flips together restore it.
	// The first collision therefore needs both dimensions: distance 2.
	graph := make([]plancache.DependencyNode, len(key.DepGraph))
	copy(graph, key.DepGraph)
	viewBit := int64(0)
	objectBit := int64(0)
	for i := range graph {
		if graph[i].RefType == "view" {
			viewBit = graph[i].View.ViewDefVersion - 1
			graph[i].View.ViewDefVersion = 1
		} else {
			objectBit = graph[i].Table.SchemaVersion - 1
			graph[i].Table.SchemaVersion = 1
		}
	}
	// Coupled bit: baseline (0,0)->0; single flips ->1; both flips ->0.
	coupled := int64(1)
	if viewBit == objectBit {
		coupled = 0
	}
	graph[0].View.ViewDefVersion = 1 + coupled
	key.DepGraph = graph
	return key.Hash()
}
