package forest

import "testing"

func mustLink(t *testing.T, f *Forest, u, v int) {
	t.Helper()
	if err := f.Link(u, v); err != nil {
		t.Fatalf("Link(%d, %d) = %v, want nil", u, v, err)
	}
}

func mustCut(t *testing.T, f *Forest, u, v int) {
	t.Helper()
	if err := f.Cut(u, v); err != nil {
		t.Fatalf("Cut(%d, %d) = %v, want nil", u, v, err)
	}
}

func TestLinkAndConnected(t *testing.T) {
	f := New()
	mustLink(t, f, 1, 2)
	mustLink(t, f, 2, 3)
	mustLink(t, f, 4, 5)
	if !f.Connected(1, 3) {
		t.Error("Connected(1, 3) = false, want true (BFS through 2)")
	}
	if !f.Connected(3, 1) {
		t.Error("Connected(3, 1) = false, want true (symmetric)")
	}
	if f.Connected(1, 4) {
		t.Error("Connected(1, 4) = true, want false (different trees)")
	}
	if !f.Connected(7, 7) {
		t.Error("Connected(7, 7) = false, want true (reflexive)")
	}
}

func TestLinkCycleRejectedUnchanged(t *testing.T) {
	f := New()
	mustLink(t, f, 1, 2)
	mustLink(t, f, 2, 3)
	if err := f.Link(3, 1); err != ErrCycle {
		t.Fatalf("Link(3, 1) = %v, want ErrCycle", err)
	}
	if f.HasEdge(3, 1) || f.HasEdge(1, 3) {
		t.Error("rejected link mutated the forest")
	}
	if err := f.Cut(1, 3); err != ErrNoEdge {
		t.Errorf("Cut(1, 3) = %v, want ErrNoEdge (rejected edge absent)", err)
	}
	// Existing structure intact.
	if !f.Connected(1, 3) {
		t.Error("Connected(1, 3) = false after rejected link, want true")
	}
}

func TestDuplicateAndReverseEdge(t *testing.T) {
	f := New()
	mustLink(t, f, 1, 2)
	if err := f.Link(1, 2); err != ErrCycle {
		t.Errorf("duplicate Link(1, 2) = %v, want ErrCycle", err)
	}
	if err := f.Link(2, 1); err != ErrCycle {
		t.Errorf("reverse duplicate Link(2, 1) = %v, want ErrCycle", err)
	}
	// Cut with reversed endpoints removes the same undirected edge.
	mustCut(t, f, 2, 1)
	if f.HasEdge(1, 2) || f.HasEdge(2, 1) {
		t.Error("edge still present after reversed Cut")
	}
	if f.Connected(1, 2) {
		t.Error("Connected(1, 2) = true after cut, want false")
	}
}

func TestSelfLoopRejected(t *testing.T) {
	f := New()
	if err := f.Link(1, 1); err != ErrCycle {
		t.Errorf("Link(1, 1) = %v, want ErrCycle", err)
	}
	if f.HasEdge(1, 1) {
		t.Error("self loop stored after rejection")
	}
}

func TestCutMissingEdge(t *testing.T) {
	f := New()
	if err := f.Cut(1, 2); err != ErrNoEdge {
		t.Errorf("Cut(1, 2) on empty forest = %v, want ErrNoEdge", err)
	}
	mustLink(t, f, 1, 2)
	mustCut(t, f, 1, 2)
	if err := f.Cut(1, 2); err != ErrNoEdge {
		t.Errorf("second Cut(1, 2) = %v, want ErrNoEdge", err)
	}
}

func TestCutSplitsAndRelinkReconnects(t *testing.T) {
	f := New()
	// Build a path 1-2-3-4 plus a branch 2-5.
	for _, e := range [][2]int{{1, 2}, {2, 3}, {3, 4}, {2, 5}} {
		mustLink(t, f, e[0], e[1])
	}
	mustCut(t, f, 2, 3)
	if f.Connected(1, 4) {
		t.Error("Connected(1, 4) = true after cut, want false")
	}
	if !f.Connected(1, 5) || !f.Connected(3, 4) {
		t.Error("cut split the wrong components")
	}
	// Reconnect the two components with a different edge.
	mustLink(t, f, 4, 5)
	if !f.Connected(1, 4) || !f.Connected(3, 5) {
		t.Error("relink did not restore connectivity")
	}
	// The new edge is a bridge now: removing it disconnects again.
	mustCut(t, f, 5, 4)
	if f.Connected(1, 4) {
		t.Error("Connected(1, 4) = true after cutting bridge, want false")
	}
}

func TestZeroValueUsable(t *testing.T) {
	var f Forest
	if err := f.Link(1, 2); err != nil {
		t.Fatalf("Link on zero value = %v", err)
	}
	if !f.Connected(1, 2) {
		t.Error("zero-value Forest not connected after Link")
	}
}

func TestProcessKeepsFailurePositions(t *testing.T) {
	ops := []Op{
		LinkOp(1, 2), // 0: ok
		LinkOp(2, 3), // 1: ok
		LinkOp(1, 3), // 2: ErrCycle
		ConnOp(1, 3), // 3: true
		LinkOp(1, 3), // 4: ErrCycle again, own position
		CutOp(9, 9),  // 5: ErrNoEdge
		CutOp(9, 9),  // 6: same failure repeated, own position
		CutOp(2, 1),  // 7: ok, reversed endpoints
		ConnOp(1, 2), // 8: false
		LinkOp(3, 1), // 9: ok now (path 1-2 was cut)
	}
	got := Process(New(), ops)
	if len(got) != len(ops) {
		t.Fatalf("Process returned %d results for %d ops", len(got), len(ops))
	}
	wantErr := []error{nil, nil, ErrCycle, nil, ErrCycle, ErrNoEdge, ErrNoEdge, nil, nil, nil}
	for i, r := range got {
		if r.Err != wantErr[i] {
			t.Errorf("result %d (%v): Err = %v, want %v", i, r.Op, r.Err, wantErr[i])
		}
	}
	if !got[3].Connected {
		t.Error("result 3: Connected = false, want true")
	}
	if got[8].Connected {
		t.Error("result 8: Connected = true, want false")
	}
	// Every result carries its own op, so duplicates stay distinguishable.
	if got[2].Op != ops[2] || got[4].Op != ops[4] || got[5].Op != ops[5] || got[6].Op != ops[6] {
		t.Error("results do not echo their input ops")
	}
}
