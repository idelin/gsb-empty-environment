package treecolor

import (
	"math/rand"
	"reflect"
	"testing"
)

// randomTree returns n-1 edges of a random tree on nodes 0..n-1.
func randomTree(rng *rand.Rand, n int) [][2]int {
	edges := make([][2]int, 0, n-1)
	for v := 1; v < n; v++ {
		edges = append(edges, [2]int{v, rng.Intn(v)})
	}
	return edges
}

// randomOps builds a random operation sequence with a small color pool
// (forcing frequent recolors back to previously used values) plus
// occasional brand-new colors added "online".
func randomOps(rng *rand.Rand, n, count int) []Op {
	ops := make([]Op, 0, count)
	nextNew := int64(1000)
	for i := 0; i < count; i++ {
		if rng.Intn(2) == 0 {
			color := int64(rng.Intn(5))
			if rng.Intn(10) == 0 { // introduce a fresh color value
				color = nextNew
				nextNew++
			}
			ops = append(ops, Op{Kind: OpUpdate, Node: rng.Intn(n), Color: color})
		} else {
			u, v := rng.Intn(n), rng.Intn(n)
			if rng.Intn(8) == 0 {
				v = u // same-node path
			}
			ops = append(ops, Op{Kind: OpQuery, U: u, V: v})
		}
	}
	return ops
}

func randomInstance(rng *rand.Rand, n, q int) ([][2]int, []int64, []Op) {
	edges := randomTree(rng, n)
	init := make([]int64, n)
	for i := range init {
		init[i] = int64(rng.Intn(4))
	}
	return edges, init, randomOps(rng, n, q)
}

func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(30)
		edges, init, ops := randomInstance(rng, n, 60)
		got := Solve(n, edges, init, ops, 0)
		want := SolveNaive(n, edges, init, ops)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d n=%d\n got: %v\nwant: %v", seed, n, got, want)
		}
	}
}

func TestRootIndependence(t *testing.T) {
	for seed := int64(1000); seed < 1050; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2 + rng.Intn(25)
		edges, init, ops := randomInstance(rng, n, 50)
		base := Solve(n, edges, init, ops, 0)
		for root := 0; root < n; root++ {
			got := Solve(n, edges, init, ops, root)
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("seed=%d root=%d differs from root=0\n got: %v\nbase: %v", seed, root, got, base)
			}
		}
	}
}

func TestSameNodePath(t *testing.T) {
	edges := [][2]int{{0, 1}, {1, 2}}
	init := []int64{7, 8, 9}
	ops := []Op{
		{Kind: OpQuery, U: 1, V: 1}, // single node -> exactly 1 color
		{Kind: OpUpdate, Node: 1, Color: 42},
		{Kind: OpQuery, U: 1, V: 1}, // still 1 after recolor
		{Kind: OpQuery, U: 0, V: 0},
	}
	got := Solve(3, edges, init, ops, 0)
	want := []int{1, 1, 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestColorRevert(t *testing.T) {
	// Path 0-1-2. Recolor node 1 twice, then revert to its original color;
	// the answer must return to the initial value, and intermediate queries
	// must see the colors of their own moment, not the final ones.
	edges := [][2]int{{0, 1}, {1, 2}}
	init := []int64{1, 2, 3}
	ops := []Op{
		{Kind: OpQuery, U: 0, V: 2}, // {1,2,3} -> 3
		{Kind: OpUpdate, Node: 1, Color: 1},
		{Kind: OpQuery, U: 0, V: 2}, // {1,1,3} -> 2
		{Kind: OpUpdate, Node: 1, Color: 3},
		{Kind: OpQuery, U: 0, V: 2},         // {1,3,3} -> 2
		{Kind: OpUpdate, Node: 1, Color: 2}, // revert to original
		{Kind: OpQuery, U: 0, V: 2},         // {1,2,3} -> 3 again
	}
	got := Solve(3, edges, init, ops, 0)
	want := []int{3, 2, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if naive := SolveNaive(3, edges, init, ops); !reflect.DeepEqual(naive, want) {
		t.Fatalf("naive got %v, want %v", naive, want)
	}
}

func TestQuerySeesMomentNotFinal(t *testing.T) {
	// A query issued before an update must not observe the final color.
	edges := [][2]int{{0, 1}}
	init := []int64{5, 5}
	ops := []Op{
		{Kind: OpQuery, U: 0, V: 1}, // both 5 -> 1
		{Kind: OpUpdate, Node: 1, Color: 9},
		{Kind: OpQuery, U: 0, V: 1}, // 5 and 9 -> 2
	}
	got := Solve(2, edges, init, ops, 0)
	want := []int{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	n := 40
	edges, init, ops := randomInstance(rng, n, 200)
	first := Solve(n, edges, init, ops, 0)
	for rep := 0; rep < 5; rep++ {
		got := Solve(n, edges, init, ops, 0)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs from first run", rep)
		}
	}
}

func TestSingleNodeTree(t *testing.T) {
	ops := []Op{
		{Kind: OpQuery, U: 0, V: 0},
		{Kind: OpUpdate, Node: 0, Color: 123},
		{Kind: OpQuery, U: 0, V: 0},
	}
	got := Solve(1, nil, []int64{4}, ops, 0)
	want := []int{1, 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
