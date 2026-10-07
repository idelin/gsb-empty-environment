package graphcanon

import (
	"reflect"
	"testing"
)

func diamondGraph() *Graph {
	g, _ := NewGraph(4, 0, []string{"a", "a", "a", "a"})
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}} {
		g.AddEdge(e[0], e[1])
	}
	return g
}

// Repeated queries at the same version serve the cached result, but only
// after re-validating its proof.
func TestServiceCachesPerVersion(t *testing.T) {
	s := NewService(diamondGraph())
	r1 := s.Canonical()
	if s.computations != 1 {
		t.Fatalf("computations = %d, want 1", s.computations)
	}
	r2 := s.Canonical()
	if r1 != r2 {
		t.Fatal("same version should serve the cached result")
	}
	if s.computations != 1 {
		t.Fatal("same version must not recompute")
	}
	if s.revalidations == 0 || s.reuses == 0 {
		t.Fatal("cached result must be re-validated before being served")
	}
}

// A local modification that leaves the structure unchanged still bumps the
// version; the cache may then be reused, but only because revalidation
// re-verified every symmetry-affected numbering claim.
func TestServiceNoOpEditReusesCache(t *testing.T) {
	g := diamondGraph()
	s := NewService(g)
	r1 := s.Canonical()
	v := g.Version()
	if err := g.SetLabel(0, g.Label(0)); err != nil { // no-op edit
		t.Fatal(err)
	}
	if g.Version() == v {
		t.Fatal("mutation must increment the version")
	}
	r2 := s.Canonical()
	if r2 != r1 {
		t.Fatal("structurally identical graph should reuse the validated cache entry")
	}
	if s.computations != 1 {
		t.Fatalf("no-op edit forced a recompute: computations = %d", s.computations)
	}
	if s.reuses == 0 {
		t.Fatal("expected a validated cache reuse")
	}
}

// A real local modification invalidates the cached proof (the stored
// numbering no longer reproduces the stored code), so the service
// recomputes exactly and reports orbits in stable node identities.
func TestServiceLocalModification(t *testing.T) {
	g := diamondGraph()
	s := NewService(g)
	r1 := s.Canonical()
	if want := [][]int{{0}, {1, 2}, {3}}; !reflect.DeepEqual(r1.Orbits, want) {
		t.Fatalf("orbits = %v, want %v", r1.Orbits, want)
	}
	if err := g.AddEdge(0, 1); err != nil { // break the 1<->2 symmetry
		t.Fatal(err)
	}
	r2 := s.Canonical()
	if r2.Code == r1.Code {
		t.Fatal("structural modification must change the canonical code")
	}
	if s.computations != 2 {
		t.Fatalf("structural modification must recompute: computations = %d", s.computations)
	}
	if want := [][]int{{0}, {1}, {2}, {3}}; !reflect.DeepEqual(r2.Orbits, want) {
		t.Fatalf("orbits after symmetry-breaking edit = %v, want %v", r2.Orbits, want)
	}
	if len(r2.Numbering) != 4 || r2.Numbering[g.Root()] != 0 {
		t.Fatalf("bad numbering after edit: %v", r2.Numbering)
	}
}

// A corrupted cached proof must be detected by revalidation and replaced
// by an exact recomputation.
func TestServiceDetectsCorruptedProof(t *testing.T) {
	g := diamondGraph()
	s := NewService(g)
	r1 := s.Canonical()
	v := g.Version()
	bad := *r1
	bad.Automorphisms = append(append([][]int(nil), r1.Automorphisms...),
		[]int{1, 2, 3, 0})
	s.cache[v] = &bad
	s.last = &bad
	r2 := s.Canonical()
	if s.computations != 2 {
		t.Fatalf("corrupted proof must trigger recomputation: computations = %d", s.computations)
	}
	if r2 == &bad {
		t.Fatal("corrupted proof was served")
	}
	if r2.Code != r1.Code {
		t.Fatal("recomputed code differs from the original for an unchanged graph")
	}
	bad2 := *r1
	bad2.Numbering = append([]int(nil), r1.Numbering...)
	bad2.Numbering[1] = bad2.Numbering[2]
	s.cache[g.Version()] = &bad2
	s.last = &bad2
	before := s.computations
	if r3 := s.Canonical(); s.computations != before+1 || r3.Code != r1.Code {
		t.Fatal("corrupted numbering was not detected")
	}
}

// The service result must agree with a fresh exact canonicalization at
// every point of a mutation sequence.
func TestServiceTracksMutationSequence(t *testing.T) {
	g, _ := NewGraph(5, 0, []string{"a", "b", "a", "b", "a"})
	s := NewService(g)
	edits := [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 0}, {0, 2}, {1, 1}}
	for i, e := range edits {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
		got := s.Canonical()
		want := Canonicalize(g)
		if got.Code != want.Code ||
			!reflect.DeepEqual(got.Numbering, want.Numbering) ||
			!reflect.DeepEqual(got.Orbits, want.Orbits) {
			t.Fatalf("after edit %d: service result diverges from exact canonicalization", i)
		}
	}
	for i := len(edits) - 1; i >= 0; i-- {
		if err := g.RemoveEdge(edits[i][0], edits[i][1]); err != nil {
			t.Fatal(err)
		}
		got := s.Canonical()
		want := Canonicalize(g)
		if got.Code != want.Code || !reflect.DeepEqual(got.Orbits, want.Orbits) {
			t.Fatalf("after undoing edit %d: service result diverges", i)
		}
	}
}
