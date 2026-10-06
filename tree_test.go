package pathtree

import (
	"math/rand"
	"testing"
)

// naive is a reference implementation that walks paths point by point.
type naive struct {
	n      int
	vals   []int64
	parent []int
	depth  []int
}

func newNaive(n int, edges [][2]int, init []int64) *naive {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		adj[e[1]] = append(adj[e[1]], e[0])
	}
	nv := &naive{
		n:      n,
		vals:   make([]int64, n),
		parent: make([]int, n),
		depth:  make([]int, n),
	}
	copy(nv.vals, init)
	for i := range nv.parent {
		nv.parent[i] = -2
	}
	nv.parent[0] = -1
	queue := []int{0}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, to := range adj[v] {
			if nv.parent[to] == -2 {
				nv.parent[to] = v
				nv.depth[to] = nv.depth[v] + 1
				queue = append(queue, to)
			}
		}
	}
	return nv
}

func (nv *naive) pathNodes(u, v int) []int {
	var res []int
	a, b := u, v
	for nv.depth[a] > nv.depth[b] {
		res = append(res, a)
		a = nv.parent[a]
	}
	for nv.depth[b] > nv.depth[a] {
		res = append(res, b)
		b = nv.parent[b]
	}
	for a != b {
		res = append(res, a, b)
		a = nv.parent[a]
		b = nv.parent[b]
	}
	return append(res, a)
}

func (nv *naive) pathAdd(u, v int, d int64) {
	for _, x := range nv.pathNodes(u, v) {
		nv.vals[x] += d
	}
}

func (nv *naive) pathMax(u, v int) (int64, int) {
	nodes := nv.pathNodes(u, v)
	best, idx := nv.vals[nodes[0]], nodes[0]
	for _, x := range nodes[1:] {
		if nv.vals[x] > best || (nv.vals[x] == best && x < idx) {
			best, idx = nv.vals[x], x
		}
	}
	return best, idx
}

func randomTreeEdges(rng *rand.Rand, n int) [][2]int {
	edges := make([][2]int, 0, n-1)
	for v := 1; v < n; v++ {
		edges = append(edges, [2]int{rng.Intn(v), v})
	}
	return edges
}

func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(60)
		edges := randomTreeEdges(rng, n)
		init := make([]int64, n)
		for i := range init {
			init[i] = int64(rng.Intn(21) - 10)
		}
		tr := NewTree(n, edges, rng.Intn(n), init)
		nv := newNaive(n, edges, init)
		for op := 0; op < 3000; op++ {
			u, v := rng.Intn(n), rng.Intn(n)
			if rng.Intn(2) == 0 {
				d := int64(rng.Intn(41) - 20) // includes negative deltas
				tr.PathAdd(u, v, d)
				nv.pathAdd(u, v, d)
			} else {
				gm, gi := tr.PathMax(u, v)
				wm, wi := nv.pathMax(u, v)
				if gm != wm || gi != wi {
					t.Fatalf("seed=%d op=%d path(%d,%d): got (%d,%d), want (%d,%d)",
						seed, op, u, v, gm, gi, wm, wi)
				}
			}
		}
	}
}

func TestSingleNode(t *testing.T) {
	tr := NewTree(1, nil, 0, []int64{7})
	if m, i := tr.PathMax(0, 0); m != 7 || i != 0 {
		t.Fatalf("got (%d,%d), want (7,0)", m, i)
	}
	tr.PathAdd(0, 0, -15)
	if m, i := tr.PathMax(0, 0); m != -8 || i != 0 {
		t.Fatalf("got (%d,%d), want (-8,0)", m, i)
	}
	tr.PathAdd(0, 0, 3)
	if m, i := tr.PathMax(0, 0); m != -5 || i != 0 {
		t.Fatalf("got (%d,%d), want (-5,0)", m, i)
	}
}

// TestLongChain uses a chain of 300k nodes. All decomposition passes are
// iterative, so this must not depend on recursion stack depth. On a chain
// numbered 0..n-1 the path a..b (a<=b) is exactly the interval [a,b].
func TestLongChain(t *testing.T) {
	n := 300000
	edges := make([][2]int, n-1)
	for i := 0; i+1 < n; i++ {
		edges[i] = [2]int{i, i + 1}
	}
	tr := NewTree(n, edges, n/2, nil)
	vals := make([]int64, n)
	rng := rand.New(rand.NewSource(42))
	for op := 0; op < 300; op++ {
		a, b := rng.Intn(n), rng.Intn(n)
		if a > b {
			a, b = b, a
		}
		if rng.Intn(2) == 0 {
			d := int64(rng.Intn(2001) - 1000)
			tr.PathAdd(a, b, d)
			for i := a; i <= b; i++ {
				vals[i] += d
			}
		} else {
			gm, gi := tr.PathMax(a, b)
			wm, wi := vals[a], a
			for i := a + 1; i <= b; i++ {
				if vals[i] > wm {
					wm, wi = vals[i], i
				}
			}
			if gm != wm || gi != wi {
				t.Fatalf("op=%d path(%d,%d): got (%d,%d), want (%d,%d)",
					op, a, b, gm, gi, wm, wi)
			}
		}
	}
}

// TestRootInvariance verifies that the chosen root never affects results:
// undirected paths are identical regardless of where the tree is rooted.
func TestRootInvariance(t *testing.T) {
	for seed := int64(100); seed < 110; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2 + rng.Intn(80)
		edges := randomTreeEdges(rng, n)
		init := make([]int64, n)
		for i := range init {
			init[i] = int64(rng.Intn(11) - 5)
		}
		t1 := NewTree(n, edges, 0, init)
		t2 := NewTree(n, edges, n-1, init)
		t3 := NewTree(n, edges, rng.Intn(n), init)
		for op := 0; op < 1000; op++ {
			u, v := rng.Intn(n), rng.Intn(n)
			if rng.Intn(2) == 0 {
				d := int64(rng.Intn(31) - 15)
				t1.PathAdd(u, v, d)
				t2.PathAdd(u, v, d)
				t3.PathAdd(u, v, d)
			} else {
				m1, i1 := t1.PathMax(u, v)
				m2, i2 := t2.PathMax(u, v)
				m3, i3 := t3.PathMax(u, v)
				if m1 != m2 || i1 != i2 || m1 != m3 || i1 != i3 {
					t.Fatalf("seed=%d op=%d path(%d,%d): (%d,%d) (%d,%d) (%d,%d)",
						seed, op, u, v, m1, i1, m2, i2, m3, i3)
				}
			}
		}
	}
}
