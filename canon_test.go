package graphsum

import (
	"math/rand"
	"reflect"
	"testing"
)

type edgeSpec struct {
	from, to int
	label    string
}

func buildGraph(t *testing.T, root int, labels map[int]string, edges []edgeSpec) *Graph {
	t.Helper()
	g := NewGraph()
	ids := make([]int, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	// Insert nodes in map order on purpose: insertion order must not matter.
	for _, id := range ids {
		if err := g.AddNode(id, labels[id]); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e.from, e.to, e.label); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if _, err := g.SetRoot(root); err != nil {
		t.Fatalf("SetRoot: %v", err)
	}
	return g
}

func mustCanonical(t *testing.T, g *Graph) *Summary {
	t.Helper()
	s, err := Canonical(g)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if err := Verify(g, s); err != nil {
		t.Fatalf("Verify rejected Canonical output: %v", err)
	}
	return s
}

func TestIsomorphicGraphsSameEncoding(t *testing.T) {
	// Directed 4-cycle with a chord, under two different node numberings
	// and different edge insertion orders.
	a := buildGraph(t, 0, map[int]string{0: "x", 1: "y", 2: "x", 3: "y"},
		[]edgeSpec{{0, 1, "e"}, {1, 2, "e"}, {2, 3, "e"}, {3, 0, "e"}, {0, 2, "f"}})
	// Isomorphism: 0->10, 1->20, 2->30, 3->40.
	b := buildGraph(t, 10, map[int]string{10: "x", 20: "y", 30: "x", 40: "y"},
		[]edgeSpec{{30, 40, "e"}, {10, 30, "f"}, {40, 10, "e"}, {20, 30, "e"}, {10, 20, "e"}})

	sa, sb := mustCanonical(t, a), mustCanonical(t, b)
	if sa.Encoding != sb.Encoding {
		t.Fatalf("isomorphic graphs got different encodings:\n%s\n%s", sa.Encoding, sb.Encoding)
	}
	if !reflect.DeepEqual(sa.Orbits, sb.Orbits) && len(sa.Orbits) != len(sb.Orbits) {
		t.Fatalf("orbit structure differs: %v vs %v", sa.Orbits, sb.Orbits)
	}
}

func TestNonIsomorphicGraphsDiffer(t *testing.T) {
	cycle := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 2, "e"}, {2, 0, "e"}})
	path := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 2, "e"}})
	if mustCanonical(t, cycle).Encoding == mustCanonical(t, path).Encoding {
		t.Fatal("cycle and path share an encoding")
	}
}

func TestParallelEdgesCounted(t *testing.T) {
	single := buildGraph(t, 0, map[int]string{0: "a", 1: "a"}, []edgeSpec{{0, 1, "e"}})
	double := buildGraph(t, 0, map[int]string{0: "a", 1: "a"}, []edgeSpec{{0, 1, "e"}, {0, 1, "e"}})
	twoLabels := buildGraph(t, 0, map[int]string{0: "a", 1: "a"}, []edgeSpec{{0, 1, "e"}, {0, 1, "f"}})
	if mustCanonical(t, single).Encoding == mustCanonical(t, double).Encoding {
		t.Fatal("parallel-edge multiplicity not preserved")
	}
	if mustCanonical(t, double).Encoding == mustCanonical(t, twoLabels).Encoding {
		t.Fatal("2 parallel same-label edges conflated with 2 different-label edges")
	}
}

func TestEdgeInsertionOrderIrrelevant(t *testing.T) {
	edges := []edgeSpec{{0, 1, "e"}, {1, 2, "f"}, {2, 0, "e"}, {0, 2, "g"}, {1, 0, "e"}}
	a := buildGraph(t, 0, map[int]string{0: "a", 1: "b", 2: "c"}, edges)
	rev := make([]edgeSpec, len(edges))
	for i, e := range edges {
		rev[len(edges)-1-i] = e
	}
	b := buildGraph(t, 0, map[int]string{0: "a", 1: "b", 2: "c"}, rev)
	if mustCanonical(t, a).Encoding != mustCanonical(t, b).Encoding {
		t.Fatal("edge insertion order changed the encoding")
	}
}

// TestCyclesNotTruncated uses graphs that fool degree-based or recursive
// hashing: a directed 6-cycle vs two disjoint directed 3-cycles. Both are
// 1-in/1-out regular with identical labels, so any hash truncated at cycle
// revisit collapses them; exact enumeration must not.
func TestCyclesNotTruncated(t *testing.T) {
	c6 := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a", 3: "a", 4: "a", 5: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 2, "e"}, {2, 3, "e"}, {3, 4, "e"}, {4, 5, "e"}, {5, 0, "e"}})
	twoC3 := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a", 3: "a", 4: "a", 5: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 2, "e"}, {2, 0, "e"}, {3, 4, "e"}, {4, 5, "e"}, {5, 3, "e"}})
	if mustCanonical(t, c6).Encoding == mustCanonical(t, twoC3).Encoding {
		t.Fatal("C6 and 2xC3 share an encoding: cycle handling is unsound")
	}

	// A directed 4-cycle vs two disjoint 2-cycles: same degree sequence too.
	c4 := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a", 3: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 2, "e"}, {2, 3, "e"}, {3, 0, "e"}})
	twoC2 := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a", 3: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 0, "e"}, {2, 3, "e"}, {3, 2, "e"}})
	if mustCanonical(t, c4).Encoding == mustCanonical(t, twoC2).Encoding {
		t.Fatal("C4 and 2xC2 share an encoding: cycle handling is unsound")
	}
}

func TestRootAndLabelsMatter(t *testing.T) {
	base := map[int]string{0: "a", 1: "a", 2: "a"}
	edges := []edgeSpec{{0, 1, "e"}, {1, 2, "e"}}
	g0 := buildGraph(t, 0, base, edges)
	g1 := buildGraph(t, 1, base, edges)
	if mustCanonical(t, g0).Encoding == mustCanonical(t, g1).Encoding {
		t.Fatal("root position ignored by encoding")
	}
	relabeled := buildGraph(t, 0, map[int]string{0: "a", 1: "b", 2: "a"}, edges)
	if mustCanonical(t, g0).Encoding == mustCanonical(t, relabeled).Encoding {
		t.Fatal("node label ignored by encoding")
	}
	edgeLabel := buildGraph(t, 0, base, []edgeSpec{{0, 1, "X"}, {1, 2, "e"}})
	if mustCanonical(t, g0).Encoding == mustCanonical(t, edgeLabel).Encoding {
		t.Fatal("edge label ignored by encoding")
	}
}

func TestAutomorphismOrbits(t *testing.T) {
	// Directed 3-cycle, uniform labels, root at 0: nodes 1 and 2 are not
	// symmetric (direction distinguishes them: 0->1 but 2->0), so all
	// orbits are singletons.
	cycle := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 2, "e"}, {2, 0, "e"}})
	s := mustCanonical(t, cycle)
	if len(s.Orbits) != 3 {
		t.Fatalf("directed rooted 3-cycle: want 3 singleton orbits, got %v", s.Orbits)
	}

	// Bidirectional pair hanging off the root: nodes 1 and 2 swap.
	star := buildGraph(t, 0, map[int]string{0: "r", 1: "a", 2: "a"},
		[]edgeSpec{{0, 1, "e"}, {0, 2, "e"}})
	s = mustCanonical(t, star)
	want := [][]int{{0}, {1, 2}}
	if !reflect.DeepEqual(s.Orbits, want) {
		t.Fatalf("star orbits: want %v, got %v", want, s.Orbits)
	}

	// Unrooted-symmetric triangle with all edges both directions and the
	// root the only distinguisher: full orbit on the other two nodes.
	full := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a"},
		[]edgeSpec{{0, 1, "e"}, {1, 0, "e"}, {1, 2, "e"}, {2, 1, "e"}, {0, 2, "e"}, {2, 0, "e"}})
	s = mustCanonical(t, full)
	if !reflect.DeepEqual(s.Orbits, [][]int{{0}, {1, 2}}) {
		t.Fatalf("bidirectional triangle orbits: got %v", s.Orbits)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	g := buildGraph(t, 0, map[int]string{0: "a", 1: "a", 2: "a"},
		[]edgeSpec{{0, 1, "e"}, {0, 2, "e"}})
	good := mustCanonical(t, g)

	badEnc := *good
	badEnc.Encoding = good.Encoding + "x"
	if err := Verify(g, &badEnc); err == nil {
		t.Fatal("Verify accepted tampered encoding")
	}

	badNum := *good
	badNum.Numbering = []int{good.Numbering[1], good.Numbering[0], good.Numbering[2]}
	if enc := g.encode(badNum.Numbering); enc != good.Encoding {
		if err := Verify(g, &badNum); err == nil {
			t.Fatal("Verify accepted numbering that does not attain the encoding")
		}
	}

	badNum2 := *good
	badNum2.Numbering = []int{0, 0, 1}
	if err := Verify(g, &badNum2); err == nil {
		t.Fatal("Verify accepted non-permutation numbering")
	}

	badOrb := *good
	badOrb.Orbits = [][]int{{0, 1}, {2}}
	if err := Verify(g, &badOrb); err == nil {
		t.Fatal("Verify accepted wrong orbit partition")
	}
}

// TestRandomIsomorphicCopies is a property test: for random graphs, a
// randomly renumbered copy with shuffled edge insertion must produce the
// identical encoding, and Verify must accept every summary.
func TestRandomIsomorphicCopies(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(8)
		labels := map[int]string{}
		for i := 0; i < n; i++ {
			labels[i] = string(rune('a' + rng.Intn(3)))
		}
		var edges []edgeSpec
		for m := 0; m < 2*n; m++ {
			edges = append(edges, edgeSpec{rng.Intn(n), rng.Intn(n), string(rune('e' + rng.Intn(2)))})
		}
		g := buildGraph(t, rng.Intn(n), labels, edges)

		// Random permutation p: node i in g becomes node p[i] in h.
		p := rng.Perm(n)
		hlabels := map[int]string{}
		for i := 0; i < n; i++ {
			hlabels[p[i]] = labels[i]
		}
		hedges := make([]edgeSpec, len(edges))
		for i, e := range edges {
			hedges[i] = edgeSpec{p[e.from], p[e.to], e.label}
		}
		// Shuffle edge insertion order.
		rng.Shuffle(len(hedges), func(i, j int) { hedges[i], hedges[j] = hedges[j], hedges[i] })
		groot, _ := g.Root()
		h := buildGraph(t, p[groot], hlabels, hedges)

		sg, sh := mustCanonical(t, g), mustCanonical(t, h)
		if sg.Encoding != sh.Encoding {
			t.Fatalf("trial %d: isomorphic copies differ:\n%s\n%s", trial, sg.Encoding, sh.Encoding)
		}
	}
}

func TestEightNodeEnumeration(t *testing.T) {
	labels := map[int]string{}
	for i := 0; i < 8; i++ {
		labels[i] = "a"
	}
	var edges []edgeSpec
	for i := 0; i < 8; i++ {
		edges = append(edges, edgeSpec{i, (i + 1) % 8, "e"})
	}
	g := buildGraph(t, 0, labels, edges)
	s := mustCanonical(t, g)
	// Rooted directed 8-cycle, uniform labels: root breaks all symmetry.
	if len(s.Orbits) != 8 {
		t.Fatalf("rooted 8-cycle: want 8 singleton orbits, got %v", s.Orbits)
	}
}
