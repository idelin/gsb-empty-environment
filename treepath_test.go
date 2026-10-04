package treepath

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// randomCase builds a random tree (n nodes) and a random op stream.
// Recolors mostly reuse a small color pool but sometimes introduce a
// brand-new color, exercising online color creation.
func randomCase(r *rand.Rand, n, nOps, colorPool int) ([]Edge, []int, []Op) {
	edges := make([]Edge, 0, n-1)
	for v := 1; v < n; v++ {
		edges = append(edges, Edge{U: r.Intn(v), V: v})
	}
	colors := make([]int, n)
	for i := range colors {
		colors[i] = r.Intn(colorPool)
	}
	ops := make([]Op, 0, nOps)
	fresh := colorPool
	for i := 0; i < nOps; i++ {
		if r.Intn(2) == 0 {
			ops = append(ops, Query(r.Intn(n), r.Intn(n)))
			continue
		}
		color := r.Intn(colorPool)
		if r.Intn(5) == 0 { // 20%: online new color
			color = fresh
			fresh += 1 + r.Intn(3)
		}
		ops = append(ops, Recolor(r.Intn(n), color))
	}
	return edges, colors, ops
}

func answersEqual(a, b []int) bool { return reflect.DeepEqual(a, b) }

// Root-change independence: the Mo solver is rooted internally, but the
// answers must not depend on the chosen root, and must match the naive
// root-free reference.
func TestRootIndependence(t *testing.T) {
	r := rand.New(rand.NewSource(20261004))
	for trial := 0; trial < 30; trial++ {
		n := 2 + r.Intn(40)
		edges, colors, ops := randomCase(r, n, 120, 4)
		want := NaiveDistinctColorsOnPaths(n, edges, colors, ops)
		for _, root := range []int{0, n / 2, n - 1} {
			got := DistinctColorsOnPaths(n, edges, colors, ops, root)
			if !answersEqual(got, want) {
				t.Fatalf("trial %d root %d: got %v, want %v", trial, root, got, want)
			}
		}
	}
}

// Same-node path: path u->u contains exactly one node, hence one color,
// regardless of recolors of u or others.
func TestSameNodePath(t *testing.T) {
	edges := []Edge{{0, 1}, {1, 2}}
	colors := []int{7, 7, 7}
	ops := []Op{
		Query(1, 1),
		Recolor(1, 9),
		Query(1, 1),
		Recolor(1, 9), // recolor to the same color
		Query(1, 1),
		Recolor(0, 3),
		Query(1, 1),
	}
	want := []int{1, 1, 1, 1}
	for _, root := range []int{0, 1, 2} {
		if got := DistinctColorsOnPaths(3, edges, colors, ops, root); !answersEqual(got, want) {
			t.Fatalf("root %d: got %v, want %v", root, got, want)
		}
	}
	if got := NaiveDistinctColorsOnPaths(3, edges, colors, ops); !answersEqual(got, want) {
		t.Fatalf("naive: got %v, want %v", got, want)
	}
}

// Color reverted back and forth: recoloring a node away and back must
// restore earlier answers exactly; queries must see the colors in effect
// at their own time, never the final colors.
func TestColorRevertAndTimeTravel(t *testing.T) {
	edges := []Edge{{0, 1}, {1, 2}, {2, 3}}
	colors := []int{1, 1, 2, 3}
	ops := []Op{
		Query(0, 3),   // t0: {1,2,3} -> 3
		Recolor(2, 5), // node 2: 2 -> 5
		Query(0, 3),   // t1: {1,5,3} -> 3
		Recolor(2, 2), // revert node 2 back to 2
		Query(0, 3),   // t2: {1,2,3} -> 3 again
		Recolor(3, 1), // node 3: 3 -> 1
		Recolor(0, 8), // node 0: 1 -> 8
		Query(0, 3),   // t3: {8,1,2,1} -> 3
		Recolor(0, 1), // revert node 0
		Recolor(3, 3), // revert node 3
		Query(0, 3),   // t4: fully reverted -> 3
		Query(0, 2),   // t4: {1,1,2} -> 2
	}
	want := []int{3, 3, 3, 3, 3, 2}
	for _, root := range []int{0, 2, 3} {
		if got := DistinctColorsOnPaths(4, edges, colors, ops, root); !answersEqual(got, want) {
			t.Fatalf("root %d: got %v, want %v", root, got, want)
		}
	}
	if got := NaiveDistinctColorsOnPaths(4, edges, colors, ops); !answersEqual(got, want) {
		t.Fatalf("naive: got %v, want %v", got, want)
	}
}

// A query issued before any update must use initial colors even though
// later updates repaint everything; a query after updates must not use
// initial colors. This pins down "query time, not final colors".
func TestQueriesUseTheirOwnTime(t *testing.T) {
	edges := []Edge{{0, 1}, {1, 2}}
	colors := []int{1, 2, 3}
	ops := []Op{
		Query(0, 2), // before updates: {1,2,3} -> 3
		Recolor(0, 5),
		Recolor(1, 5),
		Recolor(2, 5),
		Query(0, 2), // after updates: {5} -> 1
	}
	want := []int{3, 1}
	if got := DistinctColorsOnPaths(3, edges, colors, ops, 0); !answersEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Determinism: identical inputs must produce identical outputs across
// repeated runs.
func TestDeterministic(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	edges, colors, ops := randomCase(r, 50, 300, 6)
	first := DistinctColorsOnPaths(50, edges, colors, ops, 0)
	for i := 0; i < 5; i++ {
		if got := DistinctColorsOnPaths(50, edges, colors, ops, 0); !answersEqual(got, first) {
			t.Fatalf("run %d differs", i)
		}
	}
}

// Random cross-check of the Mo solver against naive path enumeration,
// including single-node trees, new colors, reverts, and same-node queries.
func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 1 + r.Intn(60)
		edges, colors, ops := randomCase(r, n, 200, 5)
		root := r.Intn(n)
		got := DistinctColorsOnPaths(n, edges, colors, ops, root)
		want := NaiveDistinctColorsOnPaths(n, edges, colors, ops)
		if !answersEqual(got, want) {
			t.Fatalf("seed %d: mismatch\ngot  %v\nwant %v", seed, got, want)
		}
	}
}

func ExampleDistinctColorsOnPaths() {
	// Path 0-1-2-3 with initial colors [1 1 2 3].
	edges := []Edge{{0, 1}, {1, 2}, {2, 3}}
	colors := []int{1, 1, 2, 3}
	ops := []Op{
		Query(0, 3),   // {1,2,3} -> 3
		Recolor(2, 4), // node 2: 2 -> 4 (new color online)
		Query(0, 3),   // {1,4,3} -> 3
		Recolor(3, 1), // node 3: 3 -> 1
		Query(0, 3),   // {1,4} -> 2
		Query(2, 2),   // same-node path -> 1
		Recolor(2, 2), // revert node 2
		Query(0, 3),   // {1,2} -> 2
	}
	fmt.Println(DistinctColorsOnPaths(4, edges, colors, ops, 0))
	// Output: [3 3 2 1 2]
}
