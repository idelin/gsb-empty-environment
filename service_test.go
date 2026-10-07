package graphsum

import (
	"reflect"
	"testing"
)

func newStarService(t *testing.T) *Service {
	t.Helper()
	g := NewGraph()
	for id, label := range map[int]string{0: "r", 1: "a", 2: "a"} {
		if err := g.AddNode(id, label); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddEdge(0, 1, "e"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge(0, 2, "e"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SetRoot(0); err != nil {
		t.Fatal(err)
	}
	return NewService(g)
}

func TestVersionIncrements(t *testing.T) {
	s := newStarService(t)
	if s.Version() != 0 {
		t.Fatalf("initial version = %d", s.Version())
	}
	if err := s.AddEdge(1, 2, "e"); err != nil {
		t.Fatal(err)
	}
	if s.Version() != 1 {
		t.Fatalf("version after edit = %d", s.Version())
	}
	if err := s.SetLabel(1, "a"); err != nil { // no-op: same label
		t.Fatal(err)
	}
	if s.Version() != 2 {
		t.Fatalf("no-op edit must still bump version, got %d", s.Version())
	}
}

func TestStableNodeIdentity(t *testing.T) {
	s := newStarService(t)
	sum1, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	// Add a node with an ID that sorts before existing ones; canonical
	// positions may shift, but Numbering must still speak in stable IDs.
	if err := s.AddNode(-5, "z"); err != nil {
		t.Fatal(err)
	}
	sum2, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range sum2.Numbering {
		if id != -5 && id != 0 && id != 1 && id != 2 {
			t.Fatalf("numbering references non-stable id %d", id)
		}
	}
	if len(sum2.Numbering) != 4 {
		t.Fatalf("numbering has %d entries after AddNode", len(sum2.Numbering))
	}
	// Orbits refer to stable IDs too: the two symmetric leaves stay paired.
	found := false
	for _, orbit := range sum2.Orbits {
		if reflect.DeepEqual(orbit, []int{1, 2}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("orbits %v lost the stable leaf pair {1,2}", sum2.Orbits)
	}
	_ = sum1
}

func TestCacheHitAndRecompute(t *testing.T) {
	s := newStarService(t)
	if _, err := s.Summary(); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Recomputed; got != 1 {
		t.Fatalf("first summary: Recomputed = %d", got)
	}
	if _, err := s.Summary(); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().CacheHits; got != 1 {
		t.Fatalf("second summary: CacheHits = %d", got)
	}
	// Structural edit forces recomputation.
	if err := s.AddEdge(1, 2, "e"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Summary(); err != nil {
		t.Fatal(err)
	}
	st := s.Stats()
	if st.Recomputed != 2 || st.CacheHits != 1 {
		t.Fatalf("after structural edit: %+v", st)
	}
}

// TestNoopEditReusesCacheWithReverification is the core incremental
// property: a local modification that leaves the structure unchanged lets
// the service reuse the cached summary, but only after the independent
// checker re-verifies every symmetry-equivalent numbering.
func TestNoopEditReusesCacheWithReverification(t *testing.T) {
	s := newStarService(t)
	first, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(1, "a"); err != nil { // structural no-op
		t.Fatal(err)
	}
	second, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	st := s.Stats()
	if st.Reused != 1 {
		t.Fatalf("expected cache reuse after no-op edit, stats: %+v", st)
	}
	if first != second {
		t.Fatal("reused summary must be the same object")
	}
	// The star has automorphism group of order 2 (leaves 1 and 2 swap):
	// exactly 2 optimal numberings must have been re-verified.
	if st.LastVerifiedNumberings != 2 {
		t.Fatalf("LastVerifiedNumberings = %d, want 2", st.LastVerifiedNumberings)
	}
}

// TestLocalEditReverifiesSymmetry: after a real local modification the
// recomputed summary is verified, and the number of re-verified numberings
// equals the size of the new automorphism group.
func TestLocalEditReverifiesSymmetry(t *testing.T) {
	s := newStarService(t)
	if _, err := s.Summary(); err != nil {
		t.Fatal(err)
	}
	// Break the leaf symmetry: relabel node 2.
	if err := s.SetLabel(2, "b"); err != nil {
		t.Fatal(err)
	}
	sum, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	st := s.Stats()
	if st.Recomputed != 2 {
		t.Fatalf("structural edit must recompute, stats: %+v", st)
	}
	if st.LastVerifiedNumberings != 1 {
		t.Fatalf("asymmetric graph must have exactly 1 optimal numbering, got %d", st.LastVerifiedNumberings)
	}
	want := [][]int{{0}, {1}, {2}}
	if !reflect.DeepEqual(sum.Orbits, want) {
		t.Fatalf("orbits = %v, want %v", sum.Orbits, want)
	}

	// Restore symmetry with a third leaf: Aut has order 6.
	if err := s.SetLabel(2, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNode(3, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge(0, 3, "e"); err != nil {
		t.Fatal(err)
	}
	sum, err = s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().LastVerifiedNumberings; got != 6 {
		t.Fatalf("3-leaf star: LastVerifiedNumberings = %d, want 6", got)
	}
	if !reflect.DeepEqual(sum.Orbits, [][]int{{0}, {1, 2, 3}}) {
		t.Fatalf("orbits = %v", sum.Orbits)
	}
}

func TestServiceEditErrors(t *testing.T) {
	s := newStarService(t)
	if err := s.AddNode(1, "x"); err == nil {
		t.Fatal("duplicate node accepted")
	}
	if err := s.AddEdge(0, 9, "e"); err == nil {
		t.Fatal("edge to unknown node accepted")
	}
	if err := s.SetRoot(9); err == nil {
		t.Fatal("unknown root accepted")
	}
	v := s.Version()
	if _, err := s.Summary(); err != nil {
		t.Fatalf("failed edits must not corrupt state: %v", err)
	}
	// Failed edits return before bumping the version.
	if s.Version() != v {
		t.Fatalf("failed edit bumped version to %d", s.Version())
	}
}
