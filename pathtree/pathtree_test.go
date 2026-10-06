package pathtree

import (
	"math/rand"
	"testing"
)

// brute is a reference implementation that walks the path point by point.
type brute struct {
	adj [][]int
	val []int64
}

func newBrute(n int, edges [][2]int) *brute {
	b := &brute{adj: make([][]int, n), val: make([]int64, n)}
	for _, e := range edges {
		b.adj[e[0]] = append(b.adj[e[0]], e[1])
		b.adj[e[1]] = append(b.adj[e[1]], e[0])
	}
	return b
}

// path returns every node on the undirected u-v path, found via BFS.
func (b *brute) path(u, v int) []int {
	prev := make([]int, len(b.adj))
	for i := range prev {
		prev[i] = -2
	}
	prev[u] = -1
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		if x == v {
			break
		}
		for _, w := range b.adj[x] {
			if prev[w] == -2 {
				prev[w] = x
				queue = append(queue, w)
			}
		}
	}
	var p []int
	for x := v; x != -1; x = prev[x] {
		p = append(p, x)
	}
	return p
}

func (b *brute) addPath(u, v int, delta int64) {
	for _, x := range b.path(u, v) {
		b.val[x] += delta
	}
}

func (b *brute) queryPath(u, v int) (int64, int) {
	var max int64
	node := -1
	for _, x := range b.path(u, v) {
		if node == -1 || b.val[x] > max || (b.val[x] == max && x < node) {
			max, node = b.val[x], x
		}
	}
	return max, node
}

func randomEdges(rng *rand.Rand, n int) [][2]int {
	edges := make([][2]int, 0, n-1)
	for i := 1; i < n; i++ {
		edges = append(edges, [2]int{rng.Intn(i), i})
	}
	return edges
}

func buildTree(n int, edges [][2]int) *Tree {
	tr := New(n)
	for _, e := range edges {
		tr.AddEdge(e[0], e[1])
	}
	tr.Build()
	return tr
}

// TestRandomAgainstBrute cross-checks random mixed operations (including
// negative deltas and re-rooting) against point-by-point traversal.
func TestRandomAgainstBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(120)
		edges := randomEdges(rng, n)
		tr := buildTree(n, edges)
		bf := newBrute(n, edges)

		for op := 0; op < 600; op++ {
			u, v := rng.Intn(n), rng.Intn(n)
			switch rng.Intn(10) {
			case 0, 1, 2, 3: // add, often negative
				delta := int64(rng.Intn(121) - 60)
				tr.AddPath(u, v, delta)
				bf.addPath(u, v, delta)
			case 4: // re-root: undirected paths must be unaffected
				tr.SetRoot(rng.Intn(n))
			default: // query
				gm, gn := tr.QueryPath(u, v)
				wm, wn := bf.queryPath(u, v)
				if gm != wm || gn != wn {
					t.Fatalf("trial %d op %d query(%d,%d): got (%d,%d), want (%d,%d)",
						trial, op, u, v, gm, gn, wm, wn)
				}
			}
		}
	}
}

// TestSingleNode exercises the degenerate one-node tree.
func TestSingleNode(t *testing.T) {
	tr := New(1)
	tr.Build()

	if m, id := tr.QueryPath(0, 0); m != 0 || id != 0 {
		t.Fatalf("initial: got (%d,%d), want (0,0)", m, id)
	}
	tr.AddPath(0, 0, -5)
	if m, id := tr.QueryPath(0, 0); m != -5 || id != 0 {
		t.Fatalf("after -5: got (%d,%d), want (-5,0)", m, id)
	}
	tr.SetRoot(0)
	tr.AddPath(0, 0, 8)
	if m, id := tr.QueryPath(0, 0); m != 3 || id != 0 {
		t.Fatalf("after +8: got (%d,%d), want (3,0)", m, id)
	}
}

// TestNegativeDeltas checks that maxima can go and stay negative.
func TestNegativeDeltas(t *testing.T) {
	tr := NewWithValues([]int64{-10, -3, -7})
	tr.AddEdge(0, 1)
	tr.AddEdge(1, 2)
	tr.Build()

	if m, id := tr.QueryPath(0, 2); m != -3 || id != 1 {
		t.Fatalf("got (%d,%d), want (-3,1)", m, id)
	}
	tr.AddPath(0, 2, -4) // -14, -7, -11
	if m, id := tr.QueryPath(0, 2); m != -7 || id != 1 {
		t.Fatalf("got (%d,%d), want (-7,1)", m, id)
	}
	tr.AddPath(2, 2, 20) // -14, -7, 9
	if m, id := tr.QueryPath(0, 2); m != 9 || id != 2 {
		t.Fatalf("got (%d,%d), want (9,2)", m, id)
	}
}

// TestTieBreakMinNode verifies the smallest-numbered maximum node is returned.
func TestTieBreakMinNode(t *testing.T) {
	tr := NewWithValues([]int64{5, 5, 5, 5})
	tr.AddEdge(3, 2)
	tr.AddEdge(2, 1)
	tr.AddEdge(1, 0)
	tr.Build()
	tr.SetRoot(3) // root choice must not matter

	if m, id := tr.QueryPath(0, 3); m != 5 || id != 0 {
		t.Fatalf("got (%d,%d), want (5,0)", m, id)
	}
	tr.AddPath(2, 3, 1) // 5,5,6,6
	if m, id := tr.QueryPath(0, 3); m != 6 || id != 2 {
		t.Fatalf("got (%d,%d), want (6,2)", m, id)
	}
}

// TestLongChainNoRecursionStack runs a chain long enough that any
// implementation recursing along the tree would blow the goroutine stack
// growth budget; correctness is checked against a plain array.
func TestLongChainNoRecursionStack(t *testing.T) {
	const n = 1 << 20 // 1,048,576 nodes
	tr := New(n)
	for i := 0; i+1 < n; i++ {
		tr.AddEdge(i, i+1)
	}
	tr.Build()

	rng := rand.New(rand.NewSource(99))
	diff := make([]int64, n+1)
	for op := 0; op < 200; op++ {
		a, b := rng.Intn(n), rng.Intn(n)
		if a > b {
			a, b = b, a
		}
		delta := int64(rng.Intn(2001) - 1000)
		tr.AddPath(a, b, delta)
		diff[a] += delta
		diff[b+1] -= delta
	}
	val := make([]int64, n)
	var acc int64
	for i := 0; i < n; i++ {
		acc += diff[i]
		val[i] = acc
	}

	for q := 0; q < 300; q++ {
		a, b := rng.Intn(n), rng.Intn(n)
		if a > b {
			a, b = b, a
		}
		gm, gn := tr.QueryPath(a, b)
		wm, wn := val[a], a
		for i := a + 1; i <= b; i++ {
			if val[i] > wm {
				wm, wn = val[i], i
			}
		}
		if gm != wm || gn != wn {
			t.Fatalf("query(%d,%d): got (%d,%d), want (%d,%d)", a, b, gm, gn, wm, wn)
		}
	}

	// Full-path query with a known analytic answer.
	tr2 := New(n)
	for i := 0; i+1 < n; i++ {
		tr2.AddEdge(i, i+1)
	}
	tr2.Build()
	tr2.SetRoot(n - 1)
	tr2.AddPath(0, n-1, 7)
	tr2.AddPath(n/4, 3*n/4, -3)
	if m, id := tr2.QueryPath(0, n-1); m != 7 || id != 0 {
		t.Fatalf("full path: got (%d,%d), want (7,0)", m, id)
	}
	if m, id := tr2.QueryPath(n/4, 3*n/4); m != 4 || id != n/4 {
		t.Fatalf("middle: got (%d,%d), want (4,%d)", m, id, n/4)
	}
	if m, id := tr2.QueryPath(n-1, n-1); m != 7 || id != n-1 {
		t.Fatalf("point: got (%d,%d), want (7,%d)", m, id, n-1)
	}
}
