package forest

import "testing"

func mustAdd(t *testing.T, f *Forest, u, v int) {
	t.Helper()
	if err := f.AddEdge(u, v); err != nil {
		t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
	}
}

func TestCycleRejectedWithoutStateChange(t *testing.T) {
	f := New()
	mustAdd(t, f, 1, 2)
	mustAdd(t, f, 2, 3)
	before := f.EdgeCount()
	if err := f.AddEdge(1, 3); err != ErrCycle {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
	if err := f.AddEdge(1, 1); err != ErrCycle {
		t.Fatalf("self-loop: expected ErrCycle, got %v", err)
	}
	if f.EdgeCount() != before {
		t.Fatalf("state changed after rejected adds: %d != %d", f.EdgeCount(), before)
	}
	if f.HasEdge(1, 3) || f.HasEdge(3, 1) {
		t.Fatal("rejected edge leaked into adjacency lists")
	}
}

func TestDuplicateAddRejected(t *testing.T) {
	f := New()
	mustAdd(t, f, 4, 5)
	if err := f.AddEdge(4, 5); err != ErrDuplicateEdge {
		t.Fatalf("expected ErrDuplicateEdge, got %v", err)
	}
	if err := f.AddEdge(5, 4); err != ErrDuplicateEdge {
		t.Fatalf("reversed duplicate: expected ErrDuplicateEdge, got %v", err)
	}
	if f.EdgeCount() != 1 {
		t.Fatalf("edge count = %d, want 1", f.EdgeCount())
	}
}

func TestReverseEdgeIdentityOnRemove(t *testing.T) {
	f := New()
	mustAdd(t, f, 7, 9)
	// Removing with reversed endpoints must hit the same undirected edge.
	if err := f.RemoveEdge(9, 7); err != nil {
		t.Fatalf("RemoveEdge(9,7): %v", err)
	}
	if f.HasEdge(7, 9) || f.HasEdge(9, 7) {
		t.Fatal("edge still present after reversed remove")
	}
	if err := f.RemoveEdge(7, 9); err != ErrEdgeNotFound {
		t.Fatalf("second remove: expected ErrEdgeNotFound, got %v", err)
	}
}

func TestRemoveNonexistentEdge(t *testing.T) {
	f := New()
	if err := f.RemoveEdge(1, 2); err != ErrEdgeNotFound {
		t.Fatalf("empty forest: expected ErrEdgeNotFound, got %v", err)
	}
	mustAdd(t, f, 1, 2)
	if err := f.RemoveEdge(1, 3); err != ErrEdgeNotFound {
		t.Fatalf("expected ErrEdgeNotFound, got %v", err)
	}
}

// TestCutAndReconnect exercises BFS connectivity across a cut and a
// subsequent reconnect through a different path.
func TestCutAndReconnect(t *testing.T) {
	f := New()
	// Chain 1-2-3-4 plus a tail 3-5.
	mustAdd(t, f, 1, 2)
	mustAdd(t, f, 2, 3)
	mustAdd(t, f, 3, 4)
	mustAdd(t, f, 3, 5)
	if !f.Connected(1, 5) || !f.Connected(4, 5) {
		t.Fatal("expected connectivity in initial tree")
	}
	// Cut the chain: {1,2} splits from {3,4,5}.
	if err := f.RemoveEdge(2, 3); err != nil {
		t.Fatal(err)
	}
	if f.Connected(1, 3) || f.Connected(2, 4) {
		t.Fatal("BFS still reports connectivity after cut")
	}
	if !f.Connected(1, 2) || !f.Connected(3, 5) {
		t.Fatal("BFS lost connectivity within surviving components")
	}
	// Reconnect through a new bridge 2-5.
	mustAdd(t, f, 2, 5)
	if !f.Connected(1, 4) || !f.Connected(4, 5) {
		t.Fatal("BFS does not see reconnected component")
	}
	// The old edge must now be a cycle again.
	if err := f.AddEdge(2, 3); err != ErrCycle {
		t.Fatalf("re-adding cut edge: expected ErrCycle, got %v", err)
	}
}

func TestRunReportsFailurePositions(t *testing.T) {
	ops := []Op{
		{Kind: OpAdd, U: 1, V: 2},    // 1: ok
		{Kind: OpAdd, U: 2, V: 3},    // 2: ok
		{Kind: OpAdd, U: 1, V: 3},    // 3: cycle, fail
		{Kind: OpQuery, U: 1, V: 3},  // 4: connected
		{Kind: OpRemove, U: 9, V: 9}, // 5: missing edge, fail
		{Kind: OpAdd, U: 1, V: 3},    // 6: same failure repeated, fail
		{Kind: OpRemove, U: 3, V: 2}, // 7: ok (reversed identity)
		{Kind: OpQuery, U: 1, V: 3},  // 8: disconnected
	}
	rep := Run(ops)
	if len(rep.Results) != len(ops) {
		t.Fatalf("results = %d, want %d", len(rep.Results), len(ops))
	}
	if !rep.Results[3].OK {
		t.Fatal("op 4: expected connected")
	}
	if rep.Results[7].OK {
		t.Fatal("op 8: expected disconnected after cut")
	}
	wantPos := []int{3, 5, 6}
	if len(rep.Failures) != len(wantPos) {
		t.Fatalf("failures = %v, want positions %v", rep.Failures, wantPos)
	}
	for i, f := range rep.Failures {
		if f.Pos != wantPos[i] {
			t.Fatalf("failure %d at pos %d, want %d", i, f.Pos, wantPos[i])
		}
	}
	// The repeated failure at position 6 must carry the same error as at 3.
	if rep.Failures[0].Err != rep.Failures[2].Err {
		t.Fatal("repeated failure did not preserve identical error")
	}
}
