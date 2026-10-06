package dynconn

import (
	"math/rand"
	"testing"
)

func add(u, v int) Op       { return Op{Kind: AddEdge, U: u, V: v} }
func rem(u, v int) Op       { return Op{Kind: RemoveEdge, U: u, V: v} }
func ask(t, u, v int) Query { return Query{Time: t, U: u, V: v} }

func mustSolve(t *testing.T, n int, ops []Op, qs []Query) []bool {
	t.Helper()
	ans, err := Solve(n, ops, qs)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return ans
}

func TestBasicTimeline(t *testing.T) {
	ops := []Op{
		add(0, 1), // 1
		add(1, 2), // 2
		rem(0, 1), // 3
		add(0, 2), // 4
		rem(1, 2), // 5
	}
	qs := []Query{
		ask(0, 0, 2), // empty graph
		ask(1, 0, 2), // only 0-1
		ask(2, 0, 2), // 0-1-2
		ask(3, 0, 2), // only 1-2 left, 0 is isolated
		ask(4, 0, 2), // direct 0-2
		ask(5, 0, 2), // direct 0-2 still alive
		ask(5, 0, 1), // 0-1 removed at op 3
	}
	want := []bool{false, false, true, false, true, true, false}
	ans := mustSolve(t, 3, ops, qs)
	for i := range want {
		if ans[i] != want[i] {
			t.Errorf("query %d (%+v): got %v, want %v", i, qs[i], ans[i], want[i])
		}
	}
}

func TestReferenceCounting(t *testing.T) {
	// The edge is added twice; only the second removal disconnects it.
	ops := []Op{
		add(0, 1), // 1
		add(0, 1), // 2
		rem(0, 1), // 3: refcount 2 -> 1
		rem(0, 1), // 4: refcount 1 -> 0
	}
	qs := []Query{
		ask(2, 0, 1),
		ask(3, 0, 1),
		ask(4, 0, 1),
	}
	want := []bool{true, true, false}
	ans := mustSolve(t, 2, ops, qs)
	for i := range want {
		if ans[i] != want[i] {
			t.Errorf("query %d: got %v, want %v", i, ans[i], want[i])
		}
	}
}

func TestSelfLoopsAndParallelEdges(t *testing.T) {
	ops := []Op{
		add(0, 0), // 1: self-loop, must not break anything
		add(0, 1), // 2
		add(1, 0), // 3: parallel edge, reversed endpoint order
		rem(0, 1), // 4: drops one reference
		add(2, 2), // 5: another self-loop
	}
	qs := []Query{
		ask(1, 0, 0), // self-loop: vertex always reaches itself
		ask(3, 0, 1), // two parallel references
		ask(4, 0, 1), // one reference left
		ask(5, 0, 2), // self-loops never connect distinct vertices
		ask(5, 1, 2),
	}
	want := []bool{true, true, true, false, false}
	ans := mustSolve(t, 3, ops, qs)
	for i := range want {
		if ans[i] != want[i] {
			t.Errorf("query %d: got %v, want %v", i, ans[i], want[i])
		}
	}
}

// TestSiblingSegmentsNoLeak builds a timeline where an edge lives only
// inside one segment-tree subtree and checks that queries in sibling
// subtrees never observe it.
func TestSiblingSegmentsNoLeak(t *testing.T) {
	ops := []Op{
		add(0, 1), // 1: lives for the whole timeline
		add(2, 3), // 2
		rem(2, 3), // 3: edge 2-3 exists only after op 2, before op 3
		add(3, 4), // 4
		rem(3, 4), // 5
		add(1, 4), // 6: now 0..4 form a chain without 2-3 / 3-4
	}
	qs := []Query{
		ask(2, 2, 3), // inside the edge's lifetime
		ask(1, 2, 3), // left sibling: before the add
		ask(3, 2, 3), // right sibling: after the remove
		ask(4, 3, 4), // 3-4 alive only in [4,5)
		ask(5, 3, 4), // gone again
		ask(6, 0, 4), // connected via 0-1-4
		ask(6, 2, 4), // 2 stays isolated
		ask(0, 0, 1), // before everything
	}
	want := []bool{true, false, false, true, false, true, false, false}
	ans := mustSolve(t, 5, ops, qs)
	for i := range want {
		if ans[i] != want[i] {
			t.Errorf("query %d (%+v): got %v, want %v", i, qs[i], ans[i], want[i])
		}
	}
}

func TestUnmatchedRemove(t *testing.T) {
	ops := []Op{add(0, 1), rem(0, 1), rem(0, 1)}
	if _, err := Solve(2, ops, nil); err == nil {
		t.Fatal("Solve: expected error for unmatched remove")
	}
	if _, err := SolveBruteForce(2, ops, []Query{ask(3, 0, 1)}); err == nil {
		t.Fatal("SolveBruteForce: expected error for unmatched remove")
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := Solve(2, []Op{add(0, 5)}, nil); err == nil {
		t.Fatal("expected error for vertex out of range")
	}
	if _, err := Solve(2, nil, []Query{ask(1, 0, 1)}); err == nil {
		t.Fatal("expected error for query time out of range")
	}
}

// TestRandomizedCrossCheck compares Solve against the brute-force
// rebuild-at-each-time reference on random histories that include
// parallel edges and self-loops, querying every point in time.
func TestRandomizedCrossCheck(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(8)
		numOps := 1 + rng.Intn(60)

		ops := make([]Op, 0, numOps)
		alive := make(map[edge]int)
		var aliveList []edge
		for i := 0; i < numOps; i++ {
			if len(aliveList) > 0 && rng.Intn(100) < 35 {
				e := aliveList[rng.Intn(len(aliveList))]
				ops = append(ops, rem(e.u, e.v))
				alive[e]--
				if alive[e] == 0 {
					delete(alive, e)
					for j, x := range aliveList {
						if x == e {
							aliveList = append(aliveList[:j], aliveList[j+1:]...)
							break
						}
					}
				}
			} else {
				u, v := rng.Intn(n), rng.Intn(n) // may be a self-loop
				ops = append(ops, add(u, v))
				e := canonical(u, v)
				if alive[e] == 0 {
					aliveList = append(aliveList, e)
				}
				alive[e]++
			}
		}

		var qs []Query
		for tm := 0; tm <= len(ops); tm++ {
			for k := 0; k < 3; k++ {
				qs = append(qs, ask(tm, rng.Intn(n), rng.Intn(n)))
			}
		}

		got, err := Solve(n, ops, qs)
		if err != nil {
			t.Fatalf("seed %d: Solve: %v", seed, err)
		}
		want, err := SolveBruteForce(n, ops, qs)
		if err != nil {
			t.Fatalf("seed %d: SolveBruteForce: %v", seed, err)
		}
		for i := range qs {
			if got[i] != want[i] {
				t.Fatalf("seed %d: query %d (%+v): got %v, want %v\nops=%v",
					seed, i, qs[i], got[i], want[i], ops)
			}
		}
	}
}
