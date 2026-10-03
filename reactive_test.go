package timetravel

import (
	"fmt"
	"testing"
)

// Acceptance: 3 consecutive user mutations -> Undo(2) -> all Signals and
// Computed precisely restored to step-1 values -> Redo(1) -> no drift.
func TestAcceptance_UndoRedoNoDrift(t *testing.T) {
	ctl := NewController()

	count := NewSignal(ctl, 0)
	name := NewSignal(ctl, "init")
	price := NewSignal(ctl, 1.5)

	double := NewComputed(ctl, func() int { return count.Get() * 2 })
	label := NewComputed(ctl, func() string {
		return fmt.Sprintf("%s#%d", name.Get(), count.Get())
	})

	// Track downstream re-render notifications.
	renders := 0
	ctl.Subscribe(func() { renders++ })

	type state struct {
		count  int
		name   string
		price  float64
		double int
		label  string
	}
	snapshot := func() state {
		return state{count.Get(), name.Get(), price.Get(), double.Get(), label.Get()}
	}

	// 3 consecutive user mutations.
	count.Set(1) // step 1
	step1 := snapshot()
	name.Set("alice")  // step 2
	ctl.Batch(func() { // step 3: two writes, one commit
		count.Set(3)
		price.Set(9.99)
	})

	// Undo 2 steps -> back to step 1.
	if !ctl.Undo(2) {
		t.Fatal("Undo(2) failed")
	}
	if got := snapshot(); got != step1 {
		t.Fatalf("after Undo(2): got %+v, want %+v", got, step1)
	}

	// Redo 1 step -> forward to step 2, must match recorded history exactly.
	if !ctl.Redo(1) {
		t.Fatal("Redo(1) failed")
	}
	wantStep2 := state{1, "alice", 1.5, 2, "alice#1"}
	if got := snapshot(); got != wantStep2 {
		t.Fatalf("after Redo(1): got %+v, want %+v", got, wantStep2)
	}

	// Every commit/undo/redo notified subscribers (3 commits + undo + redo).
	if renders != 5 {
		t.Fatalf("subscriber notified %d times, want 5", renders)
	}
}

func TestSnapshotImmutability(t *testing.T) {
	ctl := NewController()
	a := NewSignal(ctl, 10)
	a.Set(20)
	snap1 := ctl.Current()
	a.Set(30)

	if v, _ := snap1.Value(0); v != 20 {
		t.Fatalf("snapshot mutated: got %v, want 20", v)
	}
	if a.Get() != 30 {
		t.Fatalf("signal: got %v, want 30", a.Get())
	}
}

func TestGoToArbitrarySnapshot(t *testing.T) {
	ctl := NewController()
	a := NewSignal(ctl, 0)
	ids := []int{ctl.Current().ID}
	for i := 1; i <= 4; i++ {
		a.Set(i * 100)
		ids = append(ids, ctl.Current().ID)
	}
	if !ctl.GoTo(ids[2]) || a.Get() != 200 {
		t.Fatalf("GoTo(%d): a=%v", ids[2], a.Get())
	}
	if !ctl.Undo(2) || a.Get() != 0 {
		t.Fatalf("Undo to root: a=%v", a.Get())
	}
	if ctl.Undo(1) {
		t.Fatal("Undo past root should fail")
	}
	if !ctl.Redo(4) || a.Get() != 400 {
		t.Fatalf("Redo(4): a=%v", a.Get())
	}
	if ctl.Redo(1) {
		t.Fatal("Redo past tip should fail")
	}
}

func TestBranchingHistoryTree(t *testing.T) {
	ctl := NewController()
	a := NewSignal(ctl, 0)
	a.Set(1)
	a.Set(2)
	ctl.Undo(1) // back to value 1
	a.Set(99)   // new branch from snapshot 1
	if a.Get() != 99 {
		t.Fatalf("branch: got %v", a.Get())
	}
	// Redo follows the most recent branch.
	if !ctl.Undo(1) || !ctl.Redo(1) || a.Get() != 99 {
		t.Fatalf("redo should follow latest branch, a=%v", a.Get())
	}
	// The old branch (value 2) is still reachable via GoTo.
	root := ctl.Root()
	oldBranch := root.Children[0].Children[0]
	if !ctl.GoTo(oldBranch.ID) || a.Get() != 2 {
		t.Fatalf("GoTo old branch: a=%v", a.Get())
	}
}

func TestComputedCaching(t *testing.T) {
	ctl := NewController()
	a := NewSignal(ctl, 2)
	calls := 0
	c := NewComputed(ctl, func() int { calls++; return a.Get() * 10 })
	if c.Get() != 20 || c.Get() != 20 || calls != 1 {
		t.Fatalf("cache: calls=%d", calls)
	}
	a.Set(5)
	if c.Get() != 50 || calls != 2 {
		t.Fatalf("recompute after set: got %v calls=%d", c.Get(), calls)
	}
	ctl.Undo(1)
	if c.Get() != 20 || calls != 3 {
		t.Fatalf("recompute after undo: got %v calls=%d", c.Get(), calls)
	}
}
