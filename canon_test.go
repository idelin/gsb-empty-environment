package graphcanon

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func randomGraph(rng *rand.Rand, n int, alphabet []string) *Graph {
	labels := make([]string, n)
	for i := range labels {
		labels[i] = alphabet[rng.Intn(len(alphabet))]
	}
	g, err := NewGraph(n, rng.Intn(n), labels)
	if err != nil {
		panic(err)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			for k := rng.Intn(3); k > 0; k-- {
				if err := g.AddEdge(i, j); err != nil {
					panic(err)
				}
			}
		}
	}
	return g
}

// Isomorphic graphs (random relabelings) must get identical canonical
// codes, and their automorphism orbits must correspond under the
// relabeling permutation.
func TestPermutationInvariance(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []string{"a", "b", "c"}
	for n := 1; n <= MaxNodes; n++ {
		for trial := 0; trial < 30; trial++ {
			g := randomGraph(rng, n, alphabet)
			perm := rng.Perm(n)
			g2, err := Permute(g, perm)
			if err != nil {
				t.Fatal(err)
			}
			if !VerifyPermutation(g, g2, perm) {
				t.Fatalf("n=%d trial=%d: Permute output rejected by independent checker", n, trial)
			}
			r1, r2 := Canonicalize(g), Canonicalize(g2)
			if r1.Code != r2.Code {
				t.Fatalf("n=%d trial=%d: isomorphic graphs have different codes\n%s\n%s",
					n, trial, r1.Code, r2.Code)
			}
			mapped := make([][]int, len(r1.Orbits))
			for i, orb := range r1.Orbits {
				for _, id := range orb {
					mapped[i] = append(mapped[i], perm[id])
				}
				sort.Ints(mapped[i])
			}
			sort.Slice(mapped, func(a, b int) bool { return mapped[a][0] < mapped[b][0] })
			if !reflect.DeepEqual(mapped, r2.Orbits) {
				t.Fatalf("n=%d trial=%d: orbits do not correspond: %v vs %v",
					n, trial, mapped, r2.Orbits)
			}
		}
	}
}

// The canonical code agrees with the independent exhaustive isomorphism
// checker on random pairs: equal codes iff isomorphic.
func TestCanonicalCodeMatchesExhaustiveIsomorphism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alphabet := []string{"x", "y"}
	iso, nonIso := 0, 0
	for n := 1; n <= MaxNodes; n++ {
		for trial := 0; trial < 40; trial++ {
			g1 := randomGraph(rng, n, alphabet)
			var g2 *Graph
			if trial%2 == 0 {
				var err error
				g2, err = Permute(g1, rng.Perm(n))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				g2 = randomGraph(rng, n, alphabet)
			}
			codeEq := Canonicalize(g1).Code == Canonicalize(g2).Code
			isoEq := AreIsomorphic(g1, g2)
			if codeEq != isoEq {
				t.Fatalf("n=%d trial=%d: code equality %v disagrees with exhaustive isomorphism %v",
					n, trial, codeEq, isoEq)
			}
			if isoEq {
				iso++
			} else {
				nonIso++
			}
		}
	}
	if iso == 0 || nonIso == 0 {
		t.Fatalf("uninformative sample: iso=%d nonIso=%d", iso, nonIso)
	}
}

// Edge insertion order must not affect the canonical form.
func TestEdgeOrderIndependence(t *testing.T) {
	edges := [][2]int{{0, 1}, {0, 1}, {1, 2}, {2, 3}, {3, 0}, {0, 1}, {2, 2}}
	reversed := make([][2]int, len(edges))
	for i, e := range edges {
		reversed[len(edges)-1-i] = e
	}
	build := func(list [][2]int) *Graph {
		g, err := NewGraph(4, 0, []string{"a", "b", "a", "c"})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range list {
			if err := g.AddEdge(e[0], e[1]); err != nil {
				t.Fatal(err)
			}
		}
		return g
	}
	g1, g2 := build(edges), build(reversed)
	if Canonicalize(g1).Code != Canonicalize(g2).Code {
		t.Fatal("edge insertion order changed the canonical code")
	}
	if !AreIsomorphic(g1, g2) {
		t.Fatal("edge insertion order changed isomorphism")
	}
}

// Parallel-edge multiplicities are preserved and distinguish graphs.
func TestParallelEdgesCounted(t *testing.T) {
	one, _ := NewGraph(2, 0, []string{"a", "a"})
	one.AddEdge(0, 1)
	two, _ := NewGraph(2, 0, []string{"a", "a"})
	two.AddEdge(0, 1)
	two.AddEdge(0, 1)
	if Canonicalize(one).Code == Canonicalize(two).Code {
		t.Fatal("parallel-edge multiplicity not reflected in canonical code")
	}
	if AreIsomorphic(one, two) {
		t.Fatal("parallel-edge multiplicity ignored by isomorphism check")
	}
	relabeled, err := Permute(two, []int{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	if Canonicalize(two).Code != Canonicalize(relabeled).Code {
		t.Fatal("relabeled multigraph should keep the same code")
	}
	if !AreIsomorphic(two, relabeled) {
		t.Fatal("relabeled multigraph should be isomorphic")
	}
}

// Direction, labels, and root choice all distinguish canonical forms.
func TestNonIsomorphicGraphsDiffer(t *testing.T) {
	mk := func(root int, labels []string, edges [][2]int) *Graph {
		g, err := NewGraph(len(labels), root, labels)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if err := g.AddEdge(e[0], e[1]); err != nil {
				t.Fatal(err)
			}
		}
		return g
	}
	cases := []struct {
		name string
		a, b *Graph
	}{
		{"direction", mk(0, []string{"a", "a"}, [][2]int{{0, 1}}),
			mk(0, []string{"a", "a"}, [][2]int{{1, 0}})},
		{"labels", mk(0, []string{"a", "a"}, [][2]int{{0, 1}}),
			mk(0, []string{"a", "b"}, [][2]int{{0, 1}})},
		{"root", mk(0, []string{"a", "a", "a"}, [][2]int{{0, 1}, {1, 2}}),
			mk(1, []string{"a", "a", "a"}, [][2]int{{0, 1}, {1, 2}})},
		{"self-loop", mk(0, []string{"a", "a"}, [][2]int{{0, 0}}),
			mk(0, []string{"a", "a"}, [][2]int{{1, 1}})},
	}
	for _, tc := range cases {
		if Canonicalize(tc.a).Code == Canonicalize(tc.b).Code {
			t.Fatalf("%s: non-isomorphic graphs share a canonical code", tc.name)
		}
		if AreIsomorphic(tc.a, tc.b) {
			t.Fatalf("%s: exhaustive checker claims isomorphism", tc.name)
		}
	}
}

// Automorphism orbits and group sizes on hand-built symmetric graphs.
func TestAutomorphismOrbits(t *testing.T) {
	// Empty graph, uniform labels: root is fixed, all others one orbit.
	empty, _ := NewGraph(4, 0, []string{"a", "a", "a", "a"})
	r := Canonicalize(empty)
	if want := [][]int{{0}, {1, 2, 3}}; !reflect.DeepEqual(r.Orbits, want) {
		t.Fatalf("empty graph orbits = %v, want %v", r.Orbits, want)
	}
	if len(r.Automorphisms) != 6 { // 3! permutations of the non-root nodes
		t.Fatalf("empty graph automorphisms = %d, want 6", len(r.Automorphisms))
	}

	// Diamond 0->1, 0->2, 1->3, 2->3: only the swap of 1 and 2 is symmetric.
	diamond, _ := NewGraph(4, 0, []string{"a", "a", "a", "a"})
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}} {
		diamond.AddEdge(e[0], e[1])
	}
	rd := Canonicalize(diamond)
	if want := [][]int{{0}, {1, 2}, {3}}; !reflect.DeepEqual(rd.Orbits, want) {
		t.Fatalf("diamond orbits = %v, want %v", rd.Orbits, want)
	}
	if len(rd.Automorphisms) != 2 {
		t.Fatalf("diamond automorphisms = %d, want 2", len(rd.Automorphisms))
	}

	// Directed 3-cycle: fixing the root breaks all rotational symmetry.
	cycle, _ := NewGraph(3, 0, []string{"a", "a", "a"})
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}} {
		cycle.AddEdge(e[0], e[1])
	}
	rc := Canonicalize(cycle)
	if want := [][]int{{0}, {1}, {2}}; !reflect.DeepEqual(rc.Orbits, want) {
		t.Fatalf("cycle orbits = %v, want %v", rc.Orbits, want)
	}
	if len(rc.Automorphisms) != 1 {
		t.Fatalf("cycle automorphisms = %d, want 1", len(rc.Automorphisms))
	}
}

// Every reported automorphism must pass the independent permutation
// checker when conjugated back to stable node identities.
func TestAutomorphismsVerifyIndependently(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for n := 1; n <= MaxNodes; n++ {
		for trial := 0; trial < 10; trial++ {
			g := randomGraph(rng, n, []string{"a", "b"})
			r := Canonicalize(g)
			if encode(g, r.Numbering) != r.Code {
				t.Fatalf("n=%d trial=%d: numbering does not reproduce the code", n, trial)
			}
			inv := invertPerm(r.Numbering)
			for _, sigma := range r.Automorphisms {
				tau := make([]int, n)
				for orig := 0; orig < n; orig++ {
					tau[orig] = inv[sigma[r.Numbering[orig]]]
				}
				if !VerifyPermutation(g, g, tau) {
					t.Fatalf("n=%d trial=%d: automorphism %v fails independent verification",
						n, trial, sigma)
				}
			}
		}
	}
}

// The canonical numbering must be deterministic across repeated runs.
func TestCanonicalizationDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for n := 1; n <= MaxNodes; n++ {
		g := randomGraph(rng, n, []string{"a", "b"})
		r1 := Canonicalize(g)
		r2 := Canonicalize(g)
		if !reflect.DeepEqual(r1, r2) {
			t.Fatalf("n=%d: canonicalization is not deterministic", n)
		}
	}
}
