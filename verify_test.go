package graphcanon

import "testing"

func TestVerifyPermutation(t *testing.T) {
	g1, _ := NewGraph(3, 0, []string{"a", "b", "a"})
	g1.AddEdge(0, 1)
	g1.AddEdge(1, 2)
	g1.AddEdge(1, 2) // parallel edge

	perm := []int{2, 0, 1}
	g2, err := Permute(g1, perm)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPermutation(g1, g2, perm) {
		t.Fatal("valid isomorphism rejected")
	}

	bad := []struct {
		name string
		perm []int
	}{
		{"not a bijection", []int{0, 0, 1}},
		{"out of range", []int{0, 1, 3}},
		{"wrong length", []int{0, 1}},
		{"root not preserved", []int{1, 2, 0}},
		{"label mismatch", []int{0, 2, 1}},
	}
	for _, tc := range bad {
		if VerifyPermutation(g1, g2, tc.perm) {
			t.Fatalf("%s: invalid permutation accepted", tc.name)
		}
	}

	// Edge mismatch: drop one parallel edge from the copy.
	g3, err := Permute(g1, perm)
	if err != nil {
		t.Fatal(err)
	}
	if err := g3.RemoveEdge(perm[1], perm[2]); err != nil {
		t.Fatal(err)
	}
	if VerifyPermutation(g1, g3, perm) {
		t.Fatal("multiplicity mismatch accepted")
	}
	if AreIsomorphic(g1, g3) {
		t.Fatal("exhaustive checker ignores multiplicity mismatch")
	}
}

func TestAreIsomorphicKnownCases(t *testing.T) {
	// Two triangles with opposite orientation are not isomorphic as
	// directed graphs when rooted consistently... but a directed 3-cycle
	// and its reverse ARE isomorphic via relabeling (root can map).
	fwd, _ := NewGraph(3, 0, []string{"a", "a", "a"})
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}} {
		fwd.AddEdge(e[0], e[1])
	}
	rev, _ := NewGraph(3, 0, []string{"a", "a", "a"})
	for _, e := range [][2]int{{0, 2}, {2, 1}, {1, 0}} {
		rev.AddEdge(e[0], e[1])
	}
	if !AreIsomorphic(fwd, rev) {
		t.Fatal("reversed directed 3-cycle should be isomorphic")
	}
	if Canonicalize(fwd).Code != Canonicalize(rev).Code {
		t.Fatal("reversed directed 3-cycle should share the canonical code")
	}

	// A path and a star on 4 nodes are not isomorphic.
	path, _ := NewGraph(4, 0, []string{"a", "a", "a", "a"})
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 3}} {
		path.AddEdge(e[0], e[1])
	}
	star, _ := NewGraph(4, 0, []string{"a", "a", "a", "a"})
	for _, e := range [][2]int{{0, 1}, {0, 2}, {0, 3}} {
		star.AddEdge(e[0], e[1])
	}
	if AreIsomorphic(path, star) {
		t.Fatal("path and star reported isomorphic")
	}
}
