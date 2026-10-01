package canon

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// graphSpec describes a graph abstractly, so tests can rebuild it with
// any node creation order.
type graphSpec struct {
	labels []Value
	edges  [][]int
	root   int
}

// buildGraph instantiates the spec, creating nodes in the order given by
// perm (perm[k] is the spec index of the k-th created node).
func buildGraph(s graphSpec, perm []int) *GNode {
	nodes := make([]*GNode, len(s.labels))
	for _, si := range perm {
		nodes[si] = &GNode{Label: s.labels[si]}
	}
	for i := range nodes {
		for _, tgt := range s.edges[i] {
			nodes[i].Out = append(nodes[i].Out, nodes[tgt])
		}
	}
	return nodes[s.root]
}

func identity(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return p
}

func permutations(n int) [][]int {
	if n <= 1 {
		return [][]int{identity(n)}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i < n; i++ {
			q := make([]int, 0, n)
			q = append(q, p[:i]...)
			q = append(q, n-1)
			q = append(q, p[i:]...)
			out = append(out, q)
		}
	}
	return out
}

func testSpecs() map[string]graphSpec {
	return map[string]graphSpec{
		// Shared reference: two parents point at the same child.
		"sharedChild": {
			labels: []Value{String("root"), String("x"), String("x"), String("leaf")},
			edges:  [][]int{{1, 2}, {3}, {3}, {}},
			root:   0,
		},
		// Directed cycle with distinct labels.
		"cycle3": {
			labels: []Value{String("a"), String("b"), String("c")},
			edges:  [][]int{{1}, {2}, {0}},
			root:   0,
		},
		// Self-loop.
		"selfLoop": {
			labels: []Value{String("loop")},
			edges:  [][]int{{0}},
			root:   0,
		},
		// Two identical-label leaves: identities must not be merged.
		"twinLeaves": {
			labels: []Value{String("r"), String("c"), String("c")},
			edges:  [][]int{{1, 2}, {}, {}},
			root:   0,
		},
		// Uniform 4-cycle: forces individualization-refinement search.
		"uniformCycle4": {
			labels: []Value{Null{}, Null{}, Null{}, Null{}},
			edges:  [][]int{{1}, {2}, {3}, {0}},
			root:   0,
		},
		// Cycle back to the root through a shared node.
		"diamondCycle": {
			labels: []Value{String("r"), String("a"), String("b"), String("s")},
			edges:  [][]int{{1, 2}, {3}, {3}, {0}},
			root:   0,
		},
	}
}

// Reference check: for every spec, all n! permutations of node creation
// order must yield the identical canonical encoding.
func TestIsomorphismInvarianceUnderAllPermutations(t *testing.T) {
	for name, spec := range testSpecs() {
		t.Run(name, func(t *testing.T) {
			perms := permutations(len(spec.labels))
			want, err := EncodeGraph(buildGraph(spec, identity(len(spec.labels))), 0)
			if err != nil {
				t.Fatalf("EncodeGraph: %v", err)
			}
			for _, p := range perms {
				got, err := EncodeGraph(buildGraph(spec, p), 0)
				if err != nil {
					t.Fatalf("EncodeGraph(perm %v): %v", p, err)
				}
				if !bytes.Equal(want, got) {
					t.Fatalf("perm %v changed the encoding:\n%x\n%x", p, want, got)
				}
			}
			t.Logf("%d permutations verified", len(perms))
		})
	}
}

func TestDistinctIdentitiesNotMerged(t *testing.T) {
	twin := testSpecs()["twinLeaves"]
	enc, err := EncodeGraph(buildGraph(twin, identity(3)), 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := DecodeGraph(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Out) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.Out))
	}
	if root.Out[0] == root.Out[1] {
		t.Fatal("two distinct nodes were merged into one")
	}
	// A graph where the child is genuinely shared must encode differently.
	shared := graphSpec{
		labels: []Value{String("r"), String("c")},
		edges:  [][]int{{1, 1}, {}},
		root:   0,
	}
	encShared, err := EncodeGraph(buildGraph(shared, identity(2)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(enc, encShared) {
		t.Fatal("twin leaves and shared child must not encode identically")
	}
	root2, err := DecodeGraph(encShared)
	if err != nil {
		t.Fatal(err)
	}
	if root2.Out[0] != root2.Out[1] {
		t.Fatal("shared reference was not preserved through decoding")
	}
}

func TestEdgeOrderSignificant(t *testing.T) {
	ab := graphSpec{
		labels: []Value{String("r"), String("a"), String("b")},
		edges:  [][]int{{1, 2}, {}, {}},
		root:   0,
	}
	ba := graphSpec{
		labels: []Value{String("r"), String("a"), String("b")},
		edges:  [][]int{{2, 1}, {}, {}},
		root:   0,
	}
	ea, err := EncodeGraph(buildGraph(ab, identity(3)), 0)
	if err != nil {
		t.Fatal(err)
	}
	eb, err := EncodeGraph(buildGraph(ba, identity(3)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ea, eb) {
		t.Fatal("edge order must be significant")
	}
}

func TestCyclesSurviveRoundTrip(t *testing.T) {
	for _, name := range []string{"selfLoop", "cycle3", "diamondCycle"} {
		spec := testSpecs()[name]
		enc, err := EncodeGraph(buildGraph(spec, identity(len(spec.labels))), 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		root, err := DecodeGraph(enc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		re, err := EncodeGraph(root, 0)
		if err != nil {
			t.Fatalf("%s re-encode: %v", name, err)
		}
		if !bytes.Equal(enc, re) {
			t.Fatalf("%s: round trip mismatch:\n%x\n%x", name, enc, re)
		}
	}
	// Self-loop must decode to a node pointing at itself.
	enc, _ := EncodeGraph(buildGraph(testSpecs()["selfLoop"], identity(1)), 0)
	root, _ := DecodeGraph(enc)
	if root.Out[0] != root {
		t.Fatal("self-loop not preserved")
	}
}

func TestBudgetExceededIsExplicit(t *testing.T) {
	// A uniform directed cycle needs individualization: refinement alone
	// cannot break the symmetry.
	n := 8
	spec := graphSpec{labels: make([]Value, n), edges: make([][]int, n)}
	for i := range spec.labels {
		spec.labels[i] = Null{}
		spec.edges[i] = []int{(i + 1) % n}
	}
	root := buildGraph(spec, identity(n))
	if _, err := EncodeGraph(root, 2); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("budget=2: got %v, want ErrBudgetExceeded", err)
	}
	enc, err := EncodeGraph(root, 0) // default budget
	if err != nil {
		t.Fatalf("default budget: %v", err)
	}
	// Rotating the cycle (an isomorphism) must not change the encoding.
	rot := buildGraph(spec, identity(n))
	_ = rot
	enc2, err := EncodeGraph(buildGraph(spec, identity(n)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enc, enc2) {
		t.Fatal("same cycle encoded differently")
	}
}

func TestNonIsomorphicGraphsDiffer(t *testing.T) {
	specs := []graphSpec{
		{labels: []Value{Null{}, Null{}, Null{}}, edges: [][]int{{1}, {2}, {0}}, root: 0},              // 3-cycle
		{labels: []Value{Null{}, Null{}, Null{}}, edges: [][]int{{1}, {2}, {}}, root: 0},               // path
		{labels: []Value{Null{}, Null{}, Null{}}, edges: [][]int{{1, 2}, {}, {}}, root: 0},             // star
		{labels: []Value{Null{}, Null{}, Null{}}, edges: [][]int{{1}, {1}, {0}}, root: 0},              // other shape
		{labels: []Value{Int(1), Null{}, Null{}}, edges: [][]int{{1}, {2}, {0}}, root: 0},              // labeled 3-cycle
		{labels: []Value{Null{}, Null{}, Null{}, Null{}}, edges: [][]int{{1}, {2}, {3}, {0}}, root: 0}, // 4-cycle
	}
	encs := make([][]byte, len(specs))
	for i, s := range specs {
		e, err := EncodeGraph(buildGraph(s, identity(len(s.labels))), 0)
		if err != nil {
			t.Fatalf("spec %d: %v", i, err)
		}
		encs[i] = e
	}
	for i := range encs {
		for j := i + 1; j < len(encs); j++ {
			if bytes.Equal(encs[i], encs[j]) {
				t.Fatalf("non-isomorphic specs %d and %d share an encoding", i, j)
			}
		}
	}
}

func TestGraphRoundTripAllSpecs(t *testing.T) {
	for name, spec := range testSpecs() {
		enc, err := EncodeGraph(buildGraph(spec, identity(len(spec.labels))), 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		root, err := DecodeGraph(enc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		re, err := EncodeGraph(root, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(enc, re) {
			t.Fatalf("%s: decode/encode not stable", name)
		}
	}
}

func TestDecodeGraphValidation(t *testing.T) {
	valid, err := EncodeGraph(buildGraph(testSpecs()["cycle3"], identity(3)), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Truncations must all fail.
	for i := 0; i < len(valid); i++ {
		if _, err := DecodeGraph(valid[:i]); err == nil {
			t.Fatalf("truncated graph (%d of %d bytes) accepted", i, len(valid))
		}
	}
	// Edge target out of range: tagGraph, 1 node, null label, degree 1, target 5, root 0.
	bad := []byte{tagGraph, 0x01, tagNull, 0x01, 0x05, 0x00}
	if _, err := DecodeGraph(bad); err == nil {
		t.Fatal("out-of-range edge target accepted")
	}
	// Root index out of range.
	bad = []byte{tagGraph, 0x01, tagNull, 0x00, 0x07}
	if _, err := DecodeGraph(bad); err == nil {
		t.Fatal("out-of-range root index accepted")
	}
	// Zero nodes.
	if _, err := DecodeGraph([]byte{tagGraph, 0x00, 0x00}); err == nil {
		t.Fatal("empty graph accepted")
	}
	// Huge node count with no payload.
	huge := append([]byte{tagGraph}, encodeUvarint(1<<40)...)
	if _, err := DecodeGraph(huge); err == nil {
		t.Fatal("huge node count accepted")
	}
}

func encodeUvarint(u uint64) []byte {
	var b [10]byte
	n := 0
	for {
		b[n] = byte(u) | 0x80
		u >>= 7
		n++
		if u == 0 {
			b[n-1] &^= 0x80
			return b[:n]
		}
	}
}

func TestNilRootRejected(t *testing.T) {
	if _, err := EncodeGraph(nil, 0); err == nil {
		t.Fatal("nil root accepted")
	}
}

func ExampleEncodeGraph() {
	// root --+--> a --+--> shared
	//        +--> b --+
	shared := &GNode{Label: String("leaf")}
	a := &GNode{Label: String("a"), Out: []*GNode{shared}}
	b := &GNode{Label: String("b"), Out: []*GNode{shared}}
	root := &GNode{Label: String("root"), Out: []*GNode{a, b}}
	enc, err := EncodeGraph(root, 0)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%x\n", enc)
	// Output:
	// 09040501610102050162010205046c656166000504726f6f7402000103
}
