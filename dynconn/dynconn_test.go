package dynconn

import (
	"math/rand"
	"testing"
)

// bruteAnswers rebuilds the graph from scratch at every queried time and
// runs a BFS, giving an independent reference for the segment-tree solver.
// Because each time point is rebuilt in isolation, any rollback state that
// leaks into a sibling time segment shows up as a mismatch.
func bruteAnswers(ops []Op, queries []Query) []bool {
	ans := make([]bool, len(queries))
	for i, q := range queries {
		at := q.At
		if at < 0 {
			at = 0
		}
		if at > len(ops) {
			at = len(ops)
		}
		count := make(map[edgeKey]int)
		for _, op := range ops[:at] {
			k := canon(op.U, op.V)
			switch op.Kind {
			case OpAdd:
				count[k]++
			case OpDel:
				if count[k] > 0 {
					count[k]--
				}
			}
		}
		adj := make(map[int][]int)
		for k, c := range count {
			if c > 0 {
				adj[k.a] = append(adj[k.a], k.b)
				if k.a != k.b {
					adj[k.b] = append(adj[k.b], k.a)
				}
			}
		}
		ans[i] = reachable(adj, q.U, q.V)
	}
	return ans
}

func reachable(adj map[int][]int, src, dst int) bool {
	if src == dst {
		return true
	}
	seen := map[int]bool{src: true}
	queue := []int{src}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, y := range adj[x] {
			if y == dst {
				return true
			}
			if !seen[y] {
				seen[y] = true
				queue = append(queue, y)
			}
		}
	}
	return false
}

func checkAgainstBrute(t *testing.T, ops []Op, queries []Query) {
	t.Helper()
	got := Answers(ops, queries)
	want := bruteAnswers(ops, queries)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d %+v: got %v, want %v", i, queries[i], got[i], want[i])
		}
	}
}

func TestRefCountKeepsEdgeAlive(t *testing.T) {
	ops := []Op{
		{Kind: OpAdd, U: 0, V: 1},
		{Kind: OpAdd, U: 0, V: 1}, // second copy, count = 2
		{Kind: OpDel, U: 0, V: 1}, // count = 1, still alive
		{Kind: OpDel, U: 0, V: 1}, // count = 0, disconnected
	}
	queries := []Query{
		{U: 0, V: 1, At: 1}, // after first add
		{U: 0, V: 1, At: 3}, // after one delete: still connected
		{U: 0, V: 1, At: 4}, // after last delete: disconnected
	}
	want := []bool{true, true, false}
	got := Answers(ops, queries)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d: got %v, want %v", i, got[i], want[i])
		}
	}
	checkAgainstBrute(t, ops, queries)
}

func TestSelfLoop(t *testing.T) {
	ops := []Op{
		{Kind: OpAdd, U: 2, V: 2}, // self-loop
		{Kind: OpAdd, U: 2, V: 2},
		{Kind: OpDel, U: 2, V: 2},
	}
	queries := []Query{
		{U: 2, V: 2, At: 0}, // vertex always reaches itself
		{U: 2, V: 2, At: 2},
		{U: 2, V: 3, At: 2}, // self-loop must not merge components
		{U: 3, V: 3, At: 3},
	}
	want := []bool{true, true, false, true}
	got := Answers(ops, queries)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d: got %v, want %v", i, got[i], want[i])
		}
	}
	checkAgainstBrute(t, ops, queries)
}

func TestParallelEdgesBridgeComponents(t *testing.T) {
	ops := []Op{
		{Kind: OpAdd, U: 0, V: 1},
		{Kind: OpAdd, U: 2, V: 3},
		{Kind: OpAdd, U: 1, V: 2}, // bridge, copy 1
		{Kind: OpAdd, U: 1, V: 2}, // bridge, copy 2
		{Kind: OpDel, U: 1, V: 2}, // one copy left: still bridged
	}
	queries := []Query{
		{U: 0, V: 3, At: 2}, // no bridge yet
		{U: 0, V: 3, At: 4}, // bridged
		{U: 0, V: 3, At: 5}, // still bridged via remaining copy
	}
	want := []bool{false, true, true}
	got := Answers(ops, queries)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d: got %v, want %v", i, got[i], want[i])
		}
	}
	checkAgainstBrute(t, ops, queries)
}

// TestNoLeakAcrossSiblingSegments builds a timeline where an edge lives only
// in an early segment and another only in a late segment; queries at times
// dominated by the sibling subtree must not see the other segment's edges.
func TestNoLeakAcrossSiblingSegments(t *testing.T) {
	ops := []Op{
		{Kind: OpAdd, U: 0, V: 1}, // alive for times 1..2
		{Kind: OpDel, U: 0, V: 1},
		{Kind: OpAdd, U: 2, V: 3}, // alive for times 3..4
		{Kind: OpAdd, U: 3, V: 4},
	}
	queries := []Query{
		{U: 0, V: 1, At: 1}, // inside first segment
		{U: 0, V: 1, At: 4}, // long gone
		{U: 2, V: 3, At: 1}, // not yet added
		{U: 2, V: 4, At: 4}, // chain 2-3-4 alive
		{U: 0, V: 4, At: 4}, // components never merged
	}
	want := []bool{true, false, false, true, false}
	got := Answers(ops, queries)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d: got %v, want %v", i, got[i], want[i])
		}
	}
	checkAgainstBrute(t, ops, queries)
}

func TestHistoricalQueries(t *testing.T) {
	ops := []Op{
		{Kind: OpAdd, U: 0, V: 1},
		{Kind: OpAdd, U: 1, V: 2},
		{Kind: OpDel, U: 0, V: 1},
	}
	queries := []Query{
		{U: 0, V: 2, At: 0}, // empty graph
		{U: 0, V: 2, At: 1},
		{U: 0, V: 2, At: 2}, // path 0-1-2 complete
		{U: 0, V: 2, At: 3}, // 0-1 removed
		{U: 1, V: 2, At: 3}, // still alive
	}
	want := []bool{false, false, true, false, true}
	got := Answers(ops, queries)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d: got %v, want %v", i, got[i], want[i])
		}
	}
	checkAgainstBrute(t, ops, queries)
}

func TestUnmatchedDeleteIsNoOp(t *testing.T) {
	ops := []Op{
		{Kind: OpDel, U: 0, V: 1}, // deletes nothing
		{Kind: OpAdd, U: 0, V: 1},
		{Kind: OpDel, U: 0, V: 1},
		{Kind: OpDel, U: 0, V: 1}, // deletes nothing again
	}
	queries := []Query{
		{U: 0, V: 1, At: 1},
		{U: 0, V: 1, At: 2},
		{U: 0, V: 1, At: 4},
	}
	want := []bool{false, true, false}
	got := Answers(ops, queries)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d: got %v, want %v", i, got[i], want[i])
		}
	}
	checkAgainstBrute(t, ops, queries)
}

func TestAliveIntervalsRefCount(t *testing.T) {
	ops := []Op{
		{Kind: OpAdd, U: 1, V: 0}, // reversed order canonicalized
		{Kind: OpAdd, U: 0, V: 1},
		{Kind: OpDel, U: 0, V: 1}, // count 2 -> 1, still alive
		{Kind: OpAdd, U: 5, V: 5}, // self-loop, never deleted
		{Kind: OpDel, U: 0, V: 1}, // count 1 -> 0, disconnects
	}
	ivs := AliveIntervals(ops)
	byEdge := make(map[edgeKey][]Interval)
	for _, iv := range ivs {
		byEdge[canon(iv.U, iv.V)] = append(byEdge[canon(iv.U, iv.V)], iv)
	}
	ab := byEdge[edgeKey{0, 1}]
	if len(ab) != 1 || ab[0].L != 1 || ab[0].R != 5 {
		t.Fatalf("edge (0,1) intervals: %+v, want single [1,5)", ab)
	}
	self := byEdge[edgeKey{5, 5}]
	if len(self) != 1 || self[0].L != 4 || self[0].R != len(ops)+1 {
		t.Fatalf("self-loop intervals: %+v, want single [4,%d)", self, len(ops)+1)
	}
}

func TestRandomizedCrossCheck(t *testing.T) {
	for seed := int64(0); seed < 60; seed++ {
		r := rand.New(rand.NewSource(seed))
		verts := 1 + r.Intn(8)
		nOps := 1 + r.Intn(120)
		ops := make([]Op, nOps)
		for i := range ops {
			kind := OpAdd
			if r.Intn(2) == 0 {
				kind = OpDel
			}
			u := r.Intn(verts)
			v := r.Intn(verts)
			if r.Intn(6) == 0 {
				v = u // sprinkle self-loops
			}
			ops[i] = Op{Kind: kind, U: u, V: v}
		}
		nQueries := 1 + r.Intn(120)
		queries := make([]Query, nQueries)
		for i := range queries {
			queries[i] = Query{
				U:  r.Intn(verts),
				V:  r.Intn(verts),
				At: r.Intn(nOps + 1),
			}
		}
		got := Answers(ops, queries)
		want := bruteAnswers(ops, queries)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("seed %d query %d %+v: got %v, want %v", seed, i, queries[i], got[i], want[i])
			}
		}
	}
}

func TestEmpty(t *testing.T) {
	if got := Answers(nil, nil); len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
	got := Answers(nil, []Query{{U: 0, V: 0, At: 0}, {U: 0, V: 1, At: 0}})
	want := []bool{true, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d: got %v, want %v", i, got[i], want[i])
		}
	}
}
