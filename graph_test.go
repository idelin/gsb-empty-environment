package canon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
)

// graphSpec describes a labeled directed graph with ordered adjacency,
// independent of any Go pointer identity.
type graphSpec struct {
	labels []string
	adj    [][]int
	root   int
}

// build instantiates a spec, allocating nodes in the given order.
func build(spec graphSpec, allocOrder []int) *Node {
	nodes := make([]*Node, len(spec.labels))
	for _, i := range allocOrder {
		nodes[i] = &Node{Label: String(spec.labels[i])}
	}
	for i := range nodes {
		for _, t := range spec.adj[i] {
			nodes[i].Edges = append(nodes[i].Edges, nodes[t])
		}
	}
	return nodes[spec.root]
}

func identityOrder(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return p
}

// relabel returns an isomorphic spec with vertex i renamed to perm[i].
func relabel(spec graphSpec, perm []int) graphSpec {
	n := len(spec.labels)
	out := graphSpec{
		labels: make([]string, n),
		adj:    make([][]int, n),
		root:   perm[spec.root],
	}
	for i := 0; i < n; i++ {
		out.labels[perm[i]] = spec.labels[i]
		for _, t := range spec.adj[i] {
			out.adj[perm[i]] = append(out.adj[perm[i]], perm[t])
		}
	}
	return out
}

func encodeSpec(t *testing.T, spec graphSpec) []byte {
	t.Helper()
	return mustEncode(t, GraphValue(build(spec, identityOrder(len(spec.labels)))))
}

// permutations enumerates all permutations of [0..n).
func permutations(n int, visit func(perm []int)) {
	perm := identityOrder(n)
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			visit(append([]int(nil), perm...))
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			rec(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	rec(0)
}

// encodeWithOrder renders the spec with vertex perm[c] at canonical
// position c, mirroring the library's leaf encoding format. Used as an
// independent reference: any valid canonical encoding must equal
// encodeWithOrder under some vertex ordering.
func encodeWithOrder(spec graphSpec, perm []int) []byte {
	n := len(perm)
	inv := make([]int, n)
	for c, v := range perm {
		inv[v] = c
	}
	enc := []byte{tagGraph}
	enc = binary.AppendUvarint(enc, uint64(n))
	enc = binary.AppendUvarint(enc, uint64(inv[spec.root]))
	for _, v := range perm {
		lb, _ := Encode(String(spec.labels[v]))
		enc = append(enc, lb...)
		enc = binary.AppendUvarint(enc, uint64(len(spec.adj[v])))
		for _, t := range spec.adj[v] {
			enc = binary.AppendUvarint(enc, uint64(inv[t]))
		}
	}
	return enc
}

// reachableSpec reduces a spec to the vertices reachable from its root,
// matching the encoder's rooted-graph semantics.
func reachableSpec(spec graphSpec) graphSpec {
	reach := []int{spec.root}
	seen := map[int]bool{spec.root: true}
	for i := 0; i < len(reach); i++ {
		for _, t := range spec.adj[reach[i]] {
			if !seen[t] {
				seen[t] = true
				reach = append(reach, t)
			}
		}
	}
	idx := make(map[int]int, len(reach))
	out := graphSpec{
		labels: make([]string, len(reach)),
		adj:    make([][]int, len(reach)),
	}
	for i, v := range reach {
		idx[v] = i
		out.labels[i] = spec.labels[v]
	}
	for i, v := range reach {
		for _, t := range spec.adj[v] {
			out.adj[i] = append(out.adj[i], idx[t])
		}
	}
	out.root = idx[spec.root]
	return out
}

// randomSpec generates a random spec whose root reaches every vertex
// (a spanning cycle guarantees reachability).
func randomSpec(rng *rand.Rand, n int) graphSpec {
	spec := graphSpec{
		labels: make([]string, n),
		adj:    make([][]int, n),
		root:   rng.Intn(n),
	}
	for i := 0; i < n; i++ {
		spec.labels[i] = string(rune('a' + rng.Intn(3)))
		spec.adj[i] = append(spec.adj[i], (i+1)%n) // spanning cycle
		for j := 0; j < rng.Intn(2); j++ {
			spec.adj[i] = append(spec.adj[i], rng.Intn(n))
		}
	}
	return spec
}

func testSpecs() []graphSpec {
	specs := []graphSpec{
		// 3-cycle with distinct labels.
		{[]string{"a", "b", "c"}, [][]int{{1}, {2}, {0}}, 0},
		// 4-cycle, all identical labels (highly symmetric).
		{[]string{"x", "x", "x", "x"}, [][]int{{1}, {2}, {3}, {0}}, 0},
		// Diamond DAG with shared sink.
		{[]string{"r", "l", "r2", "m"}, [][]int{{1, 2}, {3}, {3}, {}}, 0},
		// Self loop plus parallel edges.
		{[]string{"a", "b"}, [][]int{{0, 1, 1}, {0}}, 0},
		// Two same-label nodes with different structure.
		{[]string{"s", "s", "t"}, [][]int{{1, 2}, {2}, {}}, 0},
		// Non-root start with equal-label subtrees.
		{[]string{"r", "x", "x", "y"}, [][]int{{1, 2}, {3}, {3}, {}}, 1},
	}
	rng := rand.New(rand.NewSource(42))
	for n := 1; n <= 6; n++ {
		for i := 0; i < 6; i++ {
			specs = append(specs, randomSpec(rng, n))
		}
	}
	return specs
}

// TestPermutationReferenceInvariance is the full-permutation reference
// check: for every small spec, all n! isomorphic relabelings must encode
// identically (isomorphism invariance), and the encoding must equal the
// rendering of some vertex ordering (validity of the canonical form).
func TestPermutationReferenceInvariance(t *testing.T) {
	for i, raw := range testSpecs() {
		spec := reachableSpec(raw)
		base := encodeSpec(t, spec)
		valid := false
		permutations(len(spec.labels), func(perm []int) {
			if bytes.Equal(encodeWithOrder(spec, perm), base) {
				valid = true
			}
			iso := relabel(spec, perm)
			if got := encodeSpec(t, iso); !bytes.Equal(base, got) {
				t.Fatalf("spec %d perm %v: isomorphic relabeling changed encoding:\n%x\n%x",
					i, perm, base, got)
			}
		})
		if !valid {
			t.Fatalf("spec %d: canonical encoding %x matches no vertex ordering", i, base)
		}
	}
}

func TestAllocationOrderInvariance(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		n := 1 + rng.Intn(6)
		spec := randomSpec(rng, n)
		base := encodeSpec(t, spec)
		for p := 0; p < 10; p++ {
			if got := mustEncode(t, GraphValue(build(spec, rng.Perm(n)))); !bytes.Equal(base, got) {
				t.Fatalf("trial %d: allocation order changed encoding", trial)
			}
		}
	}
}

func TestSharedVsDistinctIdentity(t *testing.T) {
	// root -> [u, u] (one shared leaf) vs root -> [u1, u2] (two distinct
	// identical-label leaves): different node counts, must not merge.
	shared := graphSpec{[]string{"r", "u"}, [][]int{{1, 1}, {}}, 0}
	distinct := graphSpec{[]string{"r", "u", "u"}, [][]int{{1, 2}, {}, {}}, 0}
	if bytes.Equal(encodeSpec(t, shared), encodeSpec(t, distinct)) {
		t.Fatal("shared reference and distinct identities must not be merged")
	}
}

func TestEdgeOrderSignificant(t *testing.T) {
	ab := graphSpec{[]string{"r", "a", "b"}, [][]int{{1, 2}, {}, {}}, 0}
	ba := graphSpec{[]string{"r", "a", "b"}, [][]int{{2, 1}, {}, {}}, 0}
	if bytes.Equal(encodeSpec(t, ab), encodeSpec(t, ba)) {
		t.Fatal("edge order must be significant, like an array")
	}
}

func TestCycleCreationOrderInvariance(t *testing.T) {
	// Build a -> b -> c -> a in two different creation orders.
	a1 := &Node{Label: String("a")}
	b1 := &Node{Label: String("b")}
	c1 := &Node{Label: String("c")}
	a1.Edges = []*Node{b1}
	b1.Edges = []*Node{c1}
	c1.Edges = []*Node{a1}

	c2 := &Node{Label: String("c")}
	b2 := &Node{Label: String("b")}
	a2 := &Node{Label: String("a")}
	c2.Edges = []*Node{a2}
	b2.Edges = []*Node{c2}
	a2.Edges = []*Node{b2}

	e1 := mustEncode(t, GraphValue(a1))
	e2 := mustEncode(t, GraphValue(a2))
	if !bytes.Equal(e1, e2) {
		t.Fatalf("creation order changed encoding:\n%x\n%x", e1, e2)
	}
}

func TestBudgetExhaustedIsExplicit(t *testing.T) {
	// A 64-cycle of identical labels defeats pure refinement; the
	// individualization search needs ~n^2 refinement rounds.
	n := 64
	spec := graphSpec{labels: make([]string, n), adj: make([][]int, n), root: 0}
	for i := 0; i < n; i++ {
		spec.labels[i] = "x"
		spec.adj[i] = []int{(i + 1) % n}
	}
	v := GraphValue(build(spec, identityOrder(n)))

	if _, err := EncodeWith(v, Options{SearchBudget: 200}); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("small budget: got %v, want ErrBudgetExhausted", err)
	}
	if _, err := EncodeWith(v, Options{SearchBudget: 1 << 20}); err != nil {
		t.Fatalf("generous budget: %v", err)
	}
}

func TestGraphRoundTrip(t *testing.T) {
	spec := graphSpec{[]string{"a", "b", "c"}, [][]int{{1, 2}, {2, 0}, {0}}, 2}
	enc := encodeSpec(t, spec)
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if re := mustEncode(t, dec); !bytes.Equal(enc, re) {
		t.Fatalf("graph round trip mismatch:\n%x\n%x", enc, re)
	}
}

func TestNestedGraphLabelRejected(t *testing.T) {
	inner := &Node{Label: String("inner")}
	outer := &Node{Label: GraphValue(inner)}
	if _, err := Encode(GraphValue(outer)); err == nil {
		t.Fatal("graph nested inside a node label must be rejected")
	}
}

func TestDecodeGraphValidation(t *testing.T) {
	// Root index out of range.
	data := []byte{tagGraph, 1, 5, tagNull, 0}
	if _, err := Decode(data); err == nil {
		t.Fatal("root index out of range must be rejected")
	}
	// Edge target out of range.
	data = []byte{tagGraph, 1, 0, tagNull, 1, 3}
	if _, err := Decode(data); err == nil {
		t.Fatal("edge target out of range must be rejected")
	}
	// Zero nodes.
	data = []byte{tagGraph, 0, 0}
	if _, err := Decode(data); err == nil {
		t.Fatal("zero-node graph must be rejected")
	}
}
